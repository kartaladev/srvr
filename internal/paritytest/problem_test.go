package paritytest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

const (
	traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	traceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	// internalBody is the problem every unrecognised failure gets.
	internalBody = `{"title":"Internal Server Error","status":500,"instance":"/items/7","trace_id":"` + traceID + `"}`
)

// TestParity_Problem covers engine-parity / Scenario: Problem parity: the
// decoded bodies are compared exactly, and the Content-Type, Vary and
// Cache-Control headers must equal net/http's.
func TestParity_Problem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outcome outcome
		status  int
		body    string
	}{
		{"prblm.ErrNotFound", failsNotFound, 404, `{"title":"Not Found","status":404,"detail":"The requested resource was not found.","instance":"/items/7","trace_id":"` + traceID + `"}`},
		{"errors.New(secret)", failsSecret, 500, internalBody},
		{"panic boom", panicsBoom, 500, internalBody},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var want map[string]any
			must(t, json.Unmarshal([]byte(tt.body), &want))

			var reference http.Header

			for _, e := range engines {
				base, _ := e.start(t, tt.outcome)

				res, body, err := send(t, http.MethodGet, base+"/items/7", map[string]string{"traceparent": traceparent})
				must(t, err)

				var got map[string]any
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("%s: body %q: %v", e.name, body, err)
				}

				if res.StatusCode != tt.status || !reflect.DeepEqual(got, want) {
					t.Errorf("%s: %d %v, want %d %v", e.name, res.StatusCode, got, tt.status, want)
				}

				if reference == nil {
					reference = res.Header

					continue
				}

				for _, h := range []string{"Content-Type", "Vary", "Cache-Control"} {
					if got, want := res.Header.Values(h), reference.Values(h); !reflect.DeepEqual(got, want) {
						t.Errorf("%s: %s %q, net/http sends %q", e.name, h, got, want)
					}
				}
			}
		})
	}
}

// TestUnmatchedProblemParity reproduces the defect recorded in
// openspec/changes/otel-observability/notes.md "Unmatched 404 problems
// differ between net/http and gin/fiber": engine-parity "Identical
// problems across engines" requires the same problem members for the same
// failure, and a request matching no route is the same failure on every
// engine. It is skipped until the fix, whose RED step it is.
func TestUnmatchedProblemParity(t *testing.T) {
	t.Skip("known defect, see notes.md \"Unmatched 404 problems differ between net/http and gin/fiber\"")
	t.Parallel()

	traceID := regexp.MustCompile(`"trace_id":"[0-9a-f]{32}"`)

	var want string

	for _, e := range engines {
		base, _ := e.start(t, succeeds)

		res, body, err := send(t, http.MethodGet, base+"/nope", nil)
		must(t, err)

		got := traceID.ReplaceAllString(string(body), `"trace_id":"…"`)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", e.name, res.StatusCode)
		}

		if want == "" {
			want = got

			continue
		}

		if got != want {
			t.Errorf("%s problem = %s, net/http %s", e.name, got, want)
		}
	}
}

// one returns the single access record, failing otherwise.
func one(t *testing.T, logs *logBuffer) map[string]any {
	t.Helper()

	recs := logs.access(t)
	if len(recs) != 1 {
		t.Fatalf("%d access records, want 1: %v", len(recs), recs)
	}

	return recs[0]
}

// raw writes request text over its own connection (in the background:
// the server may answer before it is all written) and reads the answer.
func raw(t *testing.T, base, text string, body []byte) (*http.Response, []byte) {
	t.Helper()

	conn := dial(t, base)

	go func() {
		_, _ = io.WriteString(conn, text)
		_, _ = conn.Write(body)
	}()

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	must(t, err)
	defer res.Body.Close()

	b, _ := io.ReadAll(res.Body)

	return res, b
}

// disconnecting sends GET path and abandons it once the handler closed
// entered, so the disconnect always happens mid-request.
func disconnecting(t *testing.T, base, path string, entered <-chan struct{}) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if res, err := clientFor(req.URL.String()).Do(req); err == nil {
			_ = res.Body.Close()
		}
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not start")
	}

	cancel()
	<-done
}

// TestParity_Exceptions covers engine-parity / Scenario: Documented
// differences: one subtest per row, pinning the engine's actual behaviour.
func TestParity_Exceptions(t *testing.T) {
	t.Parallel()

	t.Run("gin keeps a success body written before c.Error", func(t *testing.T) {
		t.Parallel()

		base, logs := ginServer(t, func(g *gin.RouterGroup) {
			g.GET("/x", func(c *gin.Context) {
				c.String(http.StatusOK, "done")
				_ = c.Error(errors.New("late"))
			})
		})

		res, body, err := send(t, http.MethodGet, base+"/x", nil)
		must(t, err)
		if r := one(t, logs); res.StatusCode != http.StatusOK || string(body) != "done" || r["srvr.response.committed"] != true {
			t.Fatalf("response %d %q, record %v", res.StatusCode, body, r)
		}
	})

	t.Run("fiber replaces a buffered body with the problem", func(t *testing.T) {
		t.Parallel()

		base, logs := fiberServer(t, func(r fiber.Router) {
			r.Get("/x", func(c fiber.Ctx) error {
				_ = c.SendString("partial")

				return errors.New("late")
			})
		})

		res, body, err := send(t, http.MethodGet, base+"/x", nil)
		must(t, err)
		if r := one(t, logs); res.StatusCode != http.StatusInternalServerError || bytes.Contains(body, []byte("partial")) || r["srvr.response.committed"] != nil {
			t.Fatalf("response %d %q, record %v", res.StatusCode, body, r)
		}
	})

	t.Run("fiber keeps a started stream", func(t *testing.T) {
		t.Parallel()

		base, logs := fiberServer(t, func(r fiber.Router) {
			r.Get("/x", func(c fiber.Ctx) error {
				_ = c.SendStream(strings.NewReader("streamed"))

				return errors.New("late")
			})
		})

		res, body, err := send(t, http.MethodGet, base+"/x", nil)
		must(t, err)
		if r := one(t, logs); string(body) != "streamed" || r["srvr.response.committed"] != true {
			t.Fatalf("response %d %q, record %v", res.StatusCode, body, r)
		}
	})

	t.Run("fiber logs a stream's declared length as the body size", func(t *testing.T) {
		t.Parallel()

		base, logs := fiberServer(t, func(r fiber.Router) {
			r.Get("/x", func(c fiber.Ctx) error { return c.SendStream(bytes.NewReader([]byte("hello"))) })
		})

		res, body, err := send(t, http.MethodGet, base+"/x", nil)
		must(t, err)
		if r := one(t, logs); res.StatusCode != http.StatusOK || string(body) != "hello" || r["http.response.body.size"] != float64(0) {
			t.Fatalf("response %d %q, record %v", res.StatusCode, body, r)
		}
	})

	t.Run("net/http attempts the problem for a gone client", func(t *testing.T) {
		t.Parallel()

		entered := make(chan struct{})
		base, logs := netServer(t, func(g *srvr.Group) {
			g.GET("/x", func(_ http.ResponseWriter, r *http.Request) error {
				close(entered)
				<-r.Context().Done()

				return errors.New("after disconnect")
			})
		})

		disconnecting(t, base, "/x", entered)
		r := one(t, logs)
		if size, ok := r["http.response.body.size"].(float64); r["msg"] != "http.request.failed" || !ok || size == 0 {
			t.Fatalf("record %v, want a failed record with the problem written", r)
		}
	})

	t.Run("gin writes nothing for a gone client", func(t *testing.T) {
		t.Parallel()

		entered := make(chan struct{})
		base, logs := ginServer(t, func(g *gin.RouterGroup) {
			g.GET("/x", func(c *gin.Context) {
				close(entered)
				<-c.Request.Context().Done()
				_ = c.Error(c.Request.Context().Err())
			})
		})

		disconnecting(t, base, "/x", entered)
		if r := one(t, logs); r["http.response.body.size"] != float64(0) {
			t.Fatalf("record %v, want nothing written", r)
		}
	})

	t.Run("fiber does not notify a handler of a gone client", func(t *testing.T) {
		t.Parallel()

		notified := make(chan bool, 1)
		entered := make(chan struct{})
		base, _ := fiberServer(t, func(r fiber.Router) {
			r.Get("/x", func(c fiber.Ctx) error {
				close(entered)
				// A margin, not a proof: fasthttp never cancels, so the
				// timer is expected to win.
				select {
				case <-c.Context().Done():
					notified <- true
				case <-time.After(300 * time.Millisecond):
					notified <- false
				}

				return nil
			})
		})

		disconnecting(t, base, "/x", entered)
		select {
		case n := <-notified:
			if n {
				t.Fatal("the handler was notified")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the handler did not finish")
		}
	})

	t.Run("gin answers HEAD to a GET-only route with 405", func(t *testing.T) {
		t.Parallel()

		base, _ := ginServer(t, func(g *gin.RouterGroup) { g.GET("/users", func(c *gin.Context) { c.Status(http.StatusOK) }) })

		res, _, err := send(t, http.MethodHead, base+"/users", nil)
		must(t, err)
		if res.StatusCode != http.StatusMethodNotAllowed || res.Header.Get("Allow") != "GET" {
			t.Fatalf("HEAD %d Allow %q", res.StatusCode, res.Header.Get("Allow"))
		}
	})

	t.Run("Allow lists HEAD on net/http and fiber, not gin", func(t *testing.T) {
		t.Parallel()

		ok := func(w http.ResponseWriter, _ *http.Request) error { w.WriteHeader(http.StatusOK); return nil }
		bases := map[string]string{}
		bases["net/http"], _ = netServer(t, func(g *srvr.Group) { g.GET("/users", ok); g.POST("/users", ok) })
		bases["gin"], _ = ginServer(t, func(g *gin.RouterGroup) {
			g.GET("/users", func(*gin.Context) {})
			g.POST("/users", func(*gin.Context) {})
		})
		bases["fiber"], _ = fiberServer(t, func(r fiber.Router) {
			r.Get("/users", func(fiber.Ctx) error { return nil })
			r.Post("/users", func(fiber.Ctx) error { return nil })
		})

		for name, want := range map[string]string{"net/http": "GET, HEAD, POST", "gin": "GET, POST", "fiber": "GET, HEAD, POST"} {
			res, _, err := send(t, http.MethodPatch, bases[name]+"/users", nil)
			must(t, err)
			if res.StatusCode != http.StatusMethodNotAllowed || res.Header.Get("Allow") != want {
				t.Errorf("%s: %d Allow %q, want %q", name, res.StatusCode, res.Header.Get("Allow"), want)
			}
		}
	})

	t.Run("fiber rejects an oversized body before the handler", func(t *testing.T) {
		t.Parallel()

		base, logs := fiberServer(t, func(r fiber.Router) {
			r.Post("/upload", func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
		})

		res, _ := raw(t, base, "POST /upload HTTP/1.1\r\nHost: x\r\nContent-Length: 4194305\r\n\r\n", make([]byte, 4194305))
		if r := one(t, logs); res.StatusCode != http.StatusRequestEntityTooLarge || r["http.route"] != nil {
			t.Fatalf("status %d, record %v", res.StatusCode, r)
		}
	})

	t.Run("fiber answers an unknown method with a plain 501", func(t *testing.T) {
		t.Parallel()

		base, logs := fiberServer(t, func(r fiber.Router) { r.Get("/users", func(fiber.Ctx) error { return nil }) })

		res, _, err := send(t, "FOO", base+"/users", nil)
		must(t, err)
		if res.StatusCode != http.StatusNotImplemented || strings.HasPrefix(res.Header.Get("Content-Type"), "application/problem+json") || len(logs.accessWithin(t, 200*time.Millisecond)) != 0 {
			t.Fatalf("FOO %d %q", res.StatusCode, res.Header.Get("Content-Type"))
		}
	})

	t.Run("fiber caps the header limit at 64 KiB, net/http does not", func(t *testing.T) {
		t.Parallel()

		big := srvr.WithMaxHeaderBytes(2 << 20)
		request := "GET /h HTTP/1.1\r\nHost: x\r\nX-Pad: " + strings.Repeat("a", 70000) + "\r\n\r\n"

		fiberBase, _ := fiberServer(t, func(r fiber.Router) { r.Get("/h", func(fiber.Ctx) error { return nil }) }, big)
		netBase, _ := netServer(t, func(g *srvr.Group) {
			g.GET("/h", func(w http.ResponseWriter, _ *http.Request) error { w.WriteHeader(http.StatusOK); return nil })
		}, big)

		if res, _ := raw(t, fiberBase, request, nil); res.StatusCode != http.StatusRequestHeaderFieldsTooLarge {
			t.Errorf("fiber %d, want 431", res.StatusCode)
		}
		if res, _ := raw(t, netBase, request, nil); res.StatusCode != http.StatusOK {
			t.Errorf("net/http %d, want 200", res.StatusCode)
		}
	})

	t.Run("fiber recovers http.ErrAbortHandler as a panic", func(t *testing.T) {
		t.Parallel()

		base, logs := startFiber(t, panicsAbort)

		res, _, err := send(t, http.MethodGet, base+"/items/7", nil)
		must(t, err)
		r := one(t, logs)
		if _, aborted := r["srvr.response.aborted"]; res.StatusCode != http.StatusInternalServerError || r["level"] != "ERROR" || r["error.kind"] != "panic" || aborted {
			t.Fatalf("status %d, record %v", res.StatusCode, r)
		}
	})

	t.Run("fiber does not apply ReadHeaderTimeout", func(t *testing.T) {
		t.Parallel()

		base, _ := fiberServer(t, func(r fiber.Router) { r.Get("/h", func(fiber.Ctx) error { return nil }) },
			srvr.WithReadHeaderTimeout(100*time.Millisecond))

		conn := dial(t, base)

		_, _ = fmt.Fprint(conn, "GET /h HTTP/1.1\r\nHost: x\r\n")
		_ = conn.SetReadDeadline(time.Now().Add(400 * time.Millisecond))

		var ne net.Error
		if _, err := conn.Read(make([]byte, 1)); !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatalf("read = %v, want no answer within 400ms", err)
		}
	})

	t.Run("fiber refuses a consumer catch-all beside Static(\"/\"), net/http and gin let it win", func(t *testing.T) {
		t.Parallel()

		site := mapSite()
		catchAll := "catch-all"

		netSrv := newNetServer(t, &logBuffer{}, func(g *srvr.Group) {
			g.GET("/{path...}", func(w http.ResponseWriter, _ *http.Request) error { _, err := io.WriteString(w, catchAll); return err })
		})
		must(t, netSrv.Register(srvr.Static("/", site)))

		ginSrv := newGinServer(t, &logBuffer{}, func(g *gin.RouterGroup) {
			g.GET("/*any", func(c *gin.Context) { c.String(http.StatusOK, catchAll) })
		})
		must(t, ginSrv.Register(ginsrvr.Static("/", site)))

		for _, s := range []struct {
			name string
			srv  server
		}{{"net/http", netSrv}, {"gin", ginSrv}} {
			base := run(t, s.srv)
			for _, m := range []struct{ method, body string }{{http.MethodGet, catchAll}, {http.MethodHead, ""}} {
				res, body, err := send(t, m.method, base+"/app.js", nil)
				must(t, err)
				if res.StatusCode != http.StatusOK || string(body) != m.body {
					t.Errorf("%s: %s /app.js = %d %q, want 200 %q (the consumer's catch-all)", s.name, m.method, res.StatusCode, body, m.body)
				}
			}
		}

		fiberSrv := newFiberServer(t, &logBuffer{}, idleWorkerDuration, func(r fiber.Router) {
			r.Get("/*", func(c fiber.Ctx) error { return c.SendString(catchAll) })
		})
		must(t, fiberSrv.Register(fibersrvr.Static("/", site)))

		if err := startFailure(t, fiberSrv); !strings.Contains(err.Error(), `"GET /*"`) {
			t.Errorf("fiber Start = %v, want it to name the duplicate route GET /*", err)
		}
	})

	t.Run("net/http redirects a raw dot segment or empty element in a static path, gin and fiber answer 404", func(t *testing.T) {
		t.Parallel()

		plan := mount("/assets", os.DirFS(dirSite(t)))
		targets := []struct {
			target string
			// netStatus and netLocation are net/http's answer; gin and
			// fiber answer 404 without a Location.
			netStatus   int
			netLocation string
		}{
			{"/assets/../secret.txt", http.StatusTemporaryRedirect, "/secret.txt"},
			{"/assets//app.js", http.StatusTemporaryRedirect, "/assets/app.js"},
		}

		for _, e := range engines {
			base := run(t, staticServer(t, e.name, &logBuffer{}, plan))

			for _, tt := range targets {
				status, location := http.StatusNotFound, ""
				if e.name == "net/http" {
					status, location = tt.netStatus, tt.netLocation
				}

				res, body := raw(t, base, "GET "+tt.target+" HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n", nil)
				if res.StatusCode != status || res.Header.Get("Location") != location || strings.Contains(string(body), "SECRET") {
					t.Errorf("%s: GET %s = %d Location %q %.80q, want %d %q", e.name, tt.target, res.StatusCode, res.Header.Get("Location"), body, status, location)
				}
			}
		}
	})

	t.Run("fiber's file cache is a fiber opt-in, net/http and gin refuse FiberSendFileCache", func(t *testing.T) {
		t.Parallel()

		// The cache's staleness and its cleaner outliving Shutdown are
		// pinned by fibersrvr's TestStatic_SendFileCache; this binary
		// serves no file from a cached mount, so the cleaner never starts.
		plan := mount("/", mapSite(), srvr.WithStaticConfig(srvr.StaticConfig{FiberSendFileCache: time.Hour}))
		refuses := map[string]bool{"net/http": true, "gin": true, "fiber": false}

		for _, e := range engines {
			srv := staticServer(t, e.name, &logBuffer{}, plan)
			if !refuses[e.name] {
				run(t, srv)

				continue
			}

			err := startFailure(t, srv)
			if ce, ok := errors.AsType[*srvr.ConfigError](err); !ok || ce.Field != "Static.FiberSendFileCache" || ce.Value != time.Hour.String() {
				t.Errorf("%s: Start = %v, want a *srvr.ConfigError for Static.FiberSendFileCache 1h0m0s", e.name, err)
			}
		}
	})

	t.Run("an encoded slash at the end of a static prefix is routed as each router does", func(t *testing.T) {
		t.Parallel()

		unescape := fibersrvr.WithFiberConfig(func(c *fiber.Config) { c.UnescapePath = true })
		plan := mount("/assets", mapSite())
		tests := []struct {
			engine string
			opts   []srvr.Option
			status int
			// body is the file served, "" for the 404 problem (fiber's is
			// its router's, without detail: otel-observability notes.md
			// "Unmatched 404 problems differ between net/http and
			// gin/fiber").
			body string
		}{
			{"net/http", nil, http.StatusNotFound, ""},
			{"fiber", nil, http.StatusNotFound, ""},
			{"gin", nil, http.StatusOK, "console.log(1)"},
			{"fiber", []srvr.Option{unescape}, http.StatusOK, "console.log(1)"},
		}

		for _, tt := range tests {
			base := run(t, staticServer(t, tt.engine, &logBuffer{}, plan, tt.opts...))

			res, body, err := send(t, http.MethodGet, base+"/assets%2Fapp.js", nil)
			must(t, err)

			if res.StatusCode != tt.status || (tt.body != "" && string(body) != tt.body) {
				t.Errorf("%s (with %d options): GET /assets%%2Fapp.js = %d %.200q, want %d %q", tt.engine, len(tt.opts), res.StatusCode, body, tt.status, tt.body)
			}

			if tt.body == "" && !isNotFoundProblem(res, body) {
				t.Errorf("%s: GET /assets%%2Fapp.js = %q %.200q, want the 404 problem", tt.engine, res.Header.Get("Content-Type"), body)
			}
		}
	})

	t.Run("fiber opens a served file again by name, net/http and gin serve the file they opened", func(t *testing.T) {
		t.Parallel()

		type answer struct {
			status int
			level  any
			route  bool
		}

		served := answer{http.StatusOK, "INFO", true}
		tests := []struct {
			name string
			n    int64
			err  error
			// fiber is fiber's answer; every other engine serves the file.
			fiber answer
		}{
			{"unreadable before fiber's open", 1, fs.ErrPermission, answer{http.StatusNotFound, "WARN", false}},
			{"removed before fiber's open", 1, fs.ErrNotExist, answer{http.StatusNotFound, "WARN", false}},
			{"removed before fasthttp's second open", 2, fs.ErrNotExist, answer{http.StatusInternalServerError, "ERROR", true}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				for _, e := range engines {
					logs := &logBuffer{}
					base := run(t, staticServer(t, e.name, logs, mount("/assets", opensAfter(tt.n, tt.err))))

					res, _, err := send(t, http.MethodGet, base+"/assets/app.js", nil)
					must(t, err)

					want := served
					if e.name == "fiber" {
						want = tt.fiber
					}

					r := one(t, logs)
					if got := (answer{res.StatusCode, r["level"], r["http.route"] != nil}); got != want {
						t.Errorf("%s: got %+v, want %+v", e.name, got, want)
					}
				}
			})
		}
	})

	t.Run("fiber answers a lasting permission error at its reopen as an unmatched miss", func(t *testing.T) {
		t.Parallel()

		// Evidence for notes.md "fiber: a lasting fs.ErrPermission on
		// fasthttp's reopen is answered 404 and loses its cause": every
		// request's second open of app.js, fasthttp's, fails with
		// fs.ErrPermission while srvr's first succeeds. The record names
		// no permission error, has no route and is counted by the
		// unmatched-record limit like a scanner's miss.
		site := mapSite()

		var opened atomic.Int64

		reopenDenied := faultFS{base: site, open: map[string]func() (fs.File, error){"app.js": func() (fs.File, error) {
			if opened.Add(1)%2 == 0 {
				return nil, &fs.PathError{Op: "open", Path: "app.js", Err: fs.ErrPermission}
			}

			return site.Open("app.js")
		}}}

		logs := &logBuffer{}
		base := run(t, staticServer(t, "fiber", logs, mount("/assets", reopenDenied), srvr.WithUnmatchedRecordLimit(1, time.Minute)))

		for range 3 {
			res, _, err := send(t, http.MethodGet, base+"/assets/app.js", nil)
			must(t, err)
			if res.StatusCode != http.StatusNotFound {
				t.Fatalf("status %d, want 404", res.StatusCode)
			}
		}

		recs := logs.accessWithin(t, 300*time.Millisecond)
		if len(recs) != 1 {
			t.Fatalf("%d access records, want 1 (the other 2 suppressed by the limit): %v", len(recs), recs)
		}

		if r := recs[0]; r["exception.message"] != "prblm: not found" || r["http.route"] != nil || r["level"] != "WARN" {
			t.Errorf("record %v, want exception.message \"prblm: not found\", no http.route, WARN", r)
		}
	})

	t.Run("fiber removes encoded dot segments before routing, net/http and gin route the raw path", func(t *testing.T) {
		t.Parallel()

		type answer struct {
			status int
			// instance is the 404 problem's instance, "" for the 200.
			instance string
			// route is the access record's http.route, nil for a miss.
			route any
		}

		tests := []struct {
			target string
			// raw is the answer of net/http and gin, which route the raw
			// path; fiber is fiber's, which routes the normalised path.
			raw, fiber answer
		}{
			{"/%2e%2e/nope", answer{http.StatusNotFound, "/../nope", nil}, answer{http.StatusNotFound, "/nope", nil}},
			{"/x/%2e%2e/items/1", answer{http.StatusNotFound, "/x/../items/1", nil}, answer{http.StatusOK, "", "/items/:id"}},
		}

		for _, tt := range tests {
			t.Run(tt.target, func(t *testing.T) {
				t.Parallel()

				for _, e := range engines {
					base, logs := e.start(t, succeeds)

					res, body := raw(t, base, "GET "+tt.target+" HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n", nil)

					var p struct {
						Instance string `json:"instance"`
					}
					if res.StatusCode == http.StatusNotFound {
						if !isNotFoundProblem(res, body) {
							t.Errorf("%s: GET %s = %q %.200q, want the 404 problem", e.name, tt.target, res.Header.Get("Content-Type"), body)
						}
						_ = json.Unmarshal(body, &p) // a bad body leaves Instance "", failing the comparison below
					}

					want := tt.raw
					if e.name == "fiber" {
						want = tt.fiber
					}

					if got := (answer{res.StatusCode, p.Instance, one(t, logs)["http.route"]}); got != want {
						t.Errorf("%s: GET %s = %+v, want %+v", e.name, tt.target, got, want)
					}
				}
			})
		}
	})
}

// opensAfter returns a site whose app.js opens normally n times, then
// fails with err: the file changed after srvr's resolution, while fiber's
// file server opens it again by name.
func opensAfter(n int64, err error) fs.FS {
	site := mapSite()

	var opened atomic.Int64

	return faultFS{base: site, open: map[string]func() (fs.File, error){"app.js": func() (fs.File, error) {
		if opened.Add(1) > n {
			return nil, err
		}

		return site.Open("app.js")
	}}}
}

// isNotFoundProblem reports whether res is a 404 problem.
func isNotFoundProblem(res *http.Response, body []byte) bool {
	var p struct {
		Title  string `json:"title"`
		Status int    `json:"status"`
	}

	return res.Header.Get("Content-Type") == "application/problem+json" &&
		json.Unmarshal(body, &p) == nil && p.Title == "Not Found" && p.Status == http.StatusNotFound
}
