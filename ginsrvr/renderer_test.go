package ginsrvr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/prblm/adapters/ginprblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// renderer answers 429 with a test body.
var renderer = srvr.ErrorRendererFunc(func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse {
	return srvr.ErrorResponse{Status: 429, Header: map[string][]string{"Content-Type": {"application/vnd.test+json"}}, Body: []byte(`{"code":"quota"}`)}
})

// TestWithErrorRenderer covers error-rendering / Scenario: Rejected
// renderer configuration, rows of this engine.
func TestWithErrorRenderer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, reason string
		opts         []srvr.Option
	}{
		{"nil", "is nil", []srvr.Option{srvr.WithErrorRenderer(nil)}},
		{"problem options then renderer", "cannot be combined with ProblemOptions", []srvr.Option{srvr.WithProblemOptions(prblm.WithNamespace("https://errors.example/p")), srvr.WithErrorRenderer(renderer)}},
		{"renderer then empty problem options", "cannot be combined with ProblemOptions", []srvr.Option{srvr.WithErrorRenderer(renderer), srvr.WithProblemOptions()}},
		{"typed nil", "is nil", []srvr.Option{srvr.WithErrorRenderer((*typedNilRenderer)(nil))}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, err := ginsrvr.New(tt.opts...)
			ce, ok := errors.AsType[*srvr.ConfigError](err)
			if srv != nil || !ok || ce.Field != "ErrorRenderer" || ce.Reason != tt.reason {
				t.Fatalf("New = %v, %v; want a ConfigError for ErrorRenderer: %s", srv, err, tt.reason)
			}
		})
	}

	t.Run("valid renderer accepted", func(t *testing.T) {
		t.Parallel()

		srv, err := ginsrvr.New(srvr.WithLogOutput(io.Discard), srvr.WithErrorRenderer(renderer))
		if srv == nil || err != nil {
			t.Fatalf("New = %v, %v; want a server", srv, err)
		}
	})
}

// typedNilRenderer is passed as a typed-nil renderer.
type typedNilRenderer struct{}

func (*typedNilRenderer) Render(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse {
	return srvr.ErrorResponse{}
}

// errQuota is the spec's ErrQuota.
var errQuota = errors.New("quota exceeded")

// recordingRenderer is the spec's R for the HTTP rows: it answers srvr's
// own errors and errQuota with their status, sends every call's error on
// errs and its request and context on reqs and ctxs.
type recordingRenderer struct {
	errs chan error
	reqs chan srvr.ErrorRequest
	ctxs chan context.Context
}

func newRecordingRenderer(n int) *recordingRenderer {
	return &recordingRenderer{errs: make(chan error, n), reqs: make(chan srvr.ErrorRequest, n), ctxs: make(chan context.Context, n)}
}

func (r *recordingRenderer) Render(ctx context.Context, req srvr.ErrorRequest, err error) srvr.ErrorResponse {
	r.errs <- err
	r.reqs <- req
	r.ctxs <- ctx

	status := http.StatusInternalServerError

	switch {
	case errors.Is(err, errQuota):
		status = http.StatusTooManyRequests
	case errors.Is(err, srvr.ErrNotFound):
		status = http.StatusNotFound
	case errors.As(err, new(*srvr.MethodNotAllowedError)):
		status = http.StatusMethodNotAllowed
	case errors.Is(err, srvr.ErrRequestTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, srvr.ErrClientClosed):
		status = 499
	}

	return srvr.ErrorResponse{
		Status: status,
		Header: http.Header{"Content-Type": {"application/vnd.test+json"}},
		Body:   []byte(`{"code":"` + strconv.Itoa(status) + `"}`),
	}
}

// fixedAnswer returns a renderer always answering res.
func fixedAnswer(res srvr.ErrorResponse) srvr.ErrorRendererFunc {
	return func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse { return res }
}

// renderedServer starts a logged gin server with conf and
// WithErrorRenderer(r) and returns its base URL and log output.
func renderedServer(t *testing.T, r srvr.ErrorRenderer, conf routes, opts ...srvr.Option) (string, *logBuffer) {
	t.Helper()

	srv, logs := newServer(t, append([]srvr.Option{srvr.WithErrorRenderer(r)}, opts...)...)
	mustNil(t, srv.Register(conf))

	return start(t, srv), logs
}

// receive returns the next value from ch, failing with msg after 10s.
func receive[T any](t *testing.T, ch <-chan T, msg string) T {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(10 * time.Second):
		t.Fatal(msg)

		var zero T

		return zero
	}
}

// disconnectWhile sends GET url and cancels it once entered is closed
// (the handler runs), then waits for the client to return.
func disconnectWhile(t *testing.T, url string, entered <-chan struct{}) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})

	go func() {
		defer close(done)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return
		}

		if res, err := clientFor(url).Do(req); err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}
	}()

	receive(t, entered, "the handler did not start")
	cancel()
	receive(t, done, "the client did not return")
}

// TestErrorRenderer_EveryAnswer covers error-rendering / Scenario: Custom
// renderer on every engine, gin rows, plus gin's early-committed headers:
// c.AbortWithError writes its status before the error middleware runs.
func TestErrorRenderer_EveryAnswer(t *testing.T) {
	t.Parallel()

	site := fstest.MapFS{"app.js": &fstest.MapFile{Data: []byte("1")}}

	tests := []struct {
		name         string
		method, path string
		body         string
		gone         bool // the client disconnects while the handler runs
		match        func(err error) bool
		status       int
	}{
		{name: "handler error", method: "GET", path: "/quota", status: 429, match: func(err error) bool { return errors.Is(err, errQuota) }},
		{name: "c.AbortWithError", method: "GET", path: "/abort", status: 429, match: func(err error) bool { return errors.Is(err, errQuota) }},
		{name: "panic", method: "GET", path: "/panic", status: 500, match: func(err error) bool {
			pe, ok := errors.AsType[*srvr.PanicError](err)
			return ok && pe.Value == "boom"
		}},
		{name: "router 404", method: "GET", path: "/missing", status: 404, match: func(err error) bool {
			return errors.Is(err, srvr.ErrNotFound) && errors.Is(err, prblm.ErrNotFound)
		}},
		{name: "router 405", method: "GET", path: "/post", status: 405, match: func(err error) bool {
			mna, ok := errors.AsType[*srvr.MethodNotAllowedError](err)
			return ok && slices.Equal(mna.Allow, []string{"POST"})
		}},
		{name: "static miss", method: "GET", path: "/assets/missing.js", status: 404, match: func(err error) bool { return errors.Is(err, srvr.ErrNotFound) }},
		{name: "static 405", method: "DELETE", path: "/assets/app.js", status: 405, match: func(err error) bool {
			mna, ok := errors.AsType[*srvr.MethodNotAllowedError](err)
			return ok && slices.Equal(mna.Allow, []string{"GET", "HEAD"})
		}},
		{name: "body over the limit", method: "POST", path: "/post", body: "12345678901", status: 413, match: func(err error) bool { return errors.Is(err, srvr.ErrRequestTooLarge) }},
		{name: "client closed", method: "GET", path: "/gone", gone: true, status: 499, match: func(err error) bool {
			return errors.Is(err, srvr.ErrClientClosed) && errors.Is(err, context.Canceled)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRecordingRenderer(64)
			entered := make(chan struct{})
			conf := routes(func(g *gin.RouterGroup) {
				g.GET("/quota", func(c *gin.Context) { _ = c.Error(errQuota) })
				g.GET("/abort", func(c *gin.Context) { _ = c.AbortWithError(http.StatusTeapot, errQuota) })
				g.GET("/panic", func(*gin.Context) { panic("boom") })
				g.POST("/post", func(c *gin.Context) {
					if _, err := io.ReadAll(c.Request.Body); err != nil {
						_ = c.Error(err)
					}
				})
				g.GET("/gone", func(c *gin.Context) {
					close(entered)
					<-c.Request.Context().Done()
					_ = c.Error(fmt.Errorf("q: %w", context.Canceled))
				})
			})

			srv, out := newServer(t, srvr.WithErrorRenderer(r), srvr.WithMaxRequestBodyBytes(10))
			mustNil(t, srv.Register(conf))
			mustNil(t, srv.Register(ginsrvr.Static("/assets", site)))
			base := start(t, srv)

			if tt.gone {
				disconnectWhile(t, base+tt.path, entered)
			} else {
				res := do(t, tt.method, base+tt.path, strings.NewReader(tt.body), nil)
				if res.status != tt.status || res.header.Get("Content-Type") != "application/vnd.test+json" ||
					res.body != `{"code":"`+strconv.Itoa(tt.status)+`"}` {
					t.Fatalf("response %d %v %s, want R's %d", res.status, res.header, res.body, tt.status)
				}
			}

			if err := receive(t, r.errs, "renderer not called"); !tt.match(err) {
				t.Fatalf("renderer got %T %v, want a match", err, err)
			}

			rec := out.waitRecord(t, "http.request.failed")
			problem, _ := rec["problem"].(map[string]any)
			if problem["status"] != float64(tt.status) || problem["type"] != nil {
				t.Fatalf("record problem %v, want status %d and no type", rec["problem"], tt.status)
			}

			if len(r.errs) != 0 {
				t.Fatalf("renderer called %d more times", len(r.errs))
			}
		})
	}

	t.Run("c.AbortWithStatus(401)", func(t *testing.T) {
		t.Parallel()

		r := newRecordingRenderer(4)
		base, out := renderedServer(t, r, func(g *gin.RouterGroup) {
			g.GET("/x", func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
		})

		res := get(t, base+"/x")
		if res.status != http.StatusUnauthorized || res.body != "" {
			t.Fatalf("response %d %q, want the bare 401 gin wrote", res.status, res.body)
		}

		if rec := out.waitRecord(t, "http.request.completed"); rec["http.response.status_code"] != float64(401) {
			t.Fatalf("record %v, want a completed 401", rec)
		}

		if len(r.errs) != 0 {
			t.Fatal("renderer called for a status-only abort")
		}
	})
}

// TestErrorRenderer_Request covers error-rendering / Scenario: What the
// renderer receives, on gin: Route is gin's FullPath form, and the
// renderer's context is bounded by srvr's render deadline.
func TestErrorRenderer_Request(t *testing.T) {
	t.Parallel()

	r := newRecordingRenderer(4)
	base, _ := renderedServer(t, r, func(g *gin.RouterGroup) {
		g.GET("/items/:id", func(c *gin.Context) { _ = c.Error(errQuota) })
	})
	do(t, "GET", base+"/items/7?q=secret", nil, map[string]string{
		"Accept": "application/json", "traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	})

	_ = receive(t, r.errs, "renderer not called")
	req, ctx := <-r.reqs, <-r.ctxs
	tr, traced := srvr.TraceFromContext(ctx)
	deadline, bounded := ctx.Deadline()

	tests := []struct {
		field string
		got   any
		want  any
	}{
		{"Method", req.Method, "GET"},
		{"Path", req.Path, "/items/7"},
		{"Route", req.Route, "/items/:id"}, // gin's FullPath, as http.route logs it
		{"Header.Get(Accept)", req.Header.Get("Accept"), "application/json"},
		{"TraceID", req.TraceID, "4bf92f3577b34da6a3ce929d0e0e4736"},
		{"the context", traced && tr.TraceID == "4bf92f3577b34da6a3ce929d0e0e4736", true},
		{"the render deadline", bounded && time.Until(deadline) <= 5*time.Second, true},
		{"Query", strings.Contains(fmt.Sprintf("%+v", req), "secret"), false},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.want {
				t.Fatalf("%s = %v, want %v", tt.field, tt.got, tt.want)
			}
		})
	}
}

// TestErrorRenderer_Faulty covers error-rendering / Scenario: Faulty
// renderer, gin rows. The deadline row is TestRender_DeadlineEndToEnd in
// internal/engine: the 5 s deadline is shortened only through
// Core.renderDeadline, which this package cannot reach.
func TestErrorRenderer_Faulty(t *testing.T) {
	t.Parallel()

	const mib = 1 << 20

	ct := http.Header{"Content-Type": {"application/vnd.test+json"}}

	tests := []struct {
		name     string
		renderer srvr.ErrorRendererFunc
		failure  string // "": sent as returned
		status   int
		check    func(t *testing.T, res response)
	}{
		{name: "panics", renderer: func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse { panic("render boom") }, failure: "panic"},
		{name: "panics with nil", renderer: func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse { panic(nilPanicValue) }, failure: "panic"},
		{name: "status 0", renderer: fixedAnswer(srvr.ErrorResponse{}), failure: "invalid_status"},
		{name: "status 200", renderer: fixedAnswer(srvr.ErrorResponse{Status: 200}), failure: "invalid_status"},
		{name: "status 399", renderer: fixedAnswer(srvr.ErrorResponse{Status: 399}), failure: "invalid_status"},
		{name: "status 600", renderer: fixedAnswer(srvr.ErrorResponse{Status: 600}), failure: "invalid_status"},
		{name: "status -1", renderer: fixedAnswer(srvr.ErrorResponse{Status: -1}), failure: "invalid_status"},
		{name: "status 100", renderer: fixedAnswer(srvr.ErrorResponse{Status: 100}), failure: "invalid_status"},
		{name: "body of exactly 1 MiB", renderer: fixedAnswer(srvr.ErrorResponse{Status: 400, Header: ct, Body: bytes.Repeat([]byte("a"), mib)}), status: 400, check: func(t *testing.T, res response) {
			t.Helper()

			if len(res.body) != mib {
				t.Errorf("body %d bytes, want %d", len(res.body), mib)
			}
		}},
		{name: "body of 1 MiB + 1", renderer: fixedAnswer(srvr.ErrorResponse{Status: 400, Header: ct, Body: make([]byte, mib+1)}), failure: "oversized_body"},
		{name: "body without Content-Type", renderer: fixedAnswer(srvr.ErrorResponse{Status: 400, Body: make([]byte, 10)}), failure: "missing_content_type"},
		{name: "bare 404", renderer: fixedAnswer(srvr.ErrorResponse{Status: 404}), status: 404, check: func(t *testing.T, res response) {
			t.Helper()

			if res.body != "" || res.header.Get("Content-Length") != "0" {
				t.Errorf("response %v %q, want a bare 404", res.header, res.body)
			}
		}},
		{name: "nil header", renderer: fixedAnswer(srvr.ErrorResponse{Status: 404, Header: nil}), status: 404},
		{
			name: "Content-Length and Transfer-Encoding dropped",
			renderer: fixedAnswer(srvr.ErrorResponse{Status: 429, Header: http.Header{
				"Content-Type": {"application/json"}, "Content-Length": {"999"}, "Transfer-Encoding": {"chunked"},
			}, Body: []byte(`{"a":1}`)}),
			status: 429,
			check: func(t *testing.T, res response) {
				t.Helper()

				if res.body != `{"a":1}` || res.header.Get("Content-Length") != "7" || res.header.Get("Transfer-Encoding") != "" {
					t.Errorf("response %v %q, want Content-Length 7 and the body", res.header, res.body)
				}
			},
		},
		{
			name: "hop-by-hop headers dropped",
			renderer: fixedAnswer(srvr.ErrorResponse{Status: 429, Header: http.Header{
				"Content-Type": {"application/json"}, "Connection": {"close"}, "Keep-Alive": {"timeout=5"},
				"Proxy-Connection": {"keep-alive"}, "Proxy-Authenticate": {"Basic"}, "Proxy-Authorization": {"Basic eA=="},
				"Te": {"trailers"}, "Trailer": {"X-T"}, "Upgrade": {"websocket"}, "X-Kept": {"1"},
			}, Body: []byte(`{}`)}),
			status: 429,
			check: func(t *testing.T, res response) {
				t.Helper()

				for _, k := range []string{"Connection", "Keep-Alive", "Proxy-Connection", "Proxy-Authenticate", "Proxy-Authorization", "Te", "Trailer", "Upgrade"} {
					if v, ok := res.header[k]; ok {
						t.Errorf("header %s sent: %q", k, v)
					}
				}
				if res.header.Get("X-Kept") != "1" {
					t.Errorf("headers %v, want X-Kept", res.header)
				}
			},
		},
		{
			name: "Set-Cookie passes through",
			renderer: fixedAnswer(srvr.ErrorResponse{Status: 401, Header: http.Header{
				"Content-Type": {"application/json"}, "Set-Cookie": {"session=; Max-Age=0", "csrf=; Max-Age=0"},
			}, Body: []byte(`{}`)}),
			status: 401,
			check: func(t *testing.T, res response) {
				t.Helper()

				if got := res.header.Values("Set-Cookie"); !slices.Equal(got, []string{"session=; Max-Age=0", "csrf=; Max-Age=0"}) {
					t.Errorf("Set-Cookie %q, want both cookies", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := renderedServer(t, tt.renderer, func(g *gin.RouterGroup) {
				g.GET("/x", func(c *gin.Context) { _ = c.Error(errors.New("db down")) })
				g.GET("/ok", ok)
			})

			res := get(t, base+"/x")
			rec := out.waitRecord(t, "http.request.failed")
			problem, _ := rec["problem"].(map[string]any)

			if tt.failure == "" {
				if res.status != tt.status || problem["status"] != float64(tt.status) || rec["srvr.renderer.failure"] != nil {
					t.Fatalf("response %d record %v, want %d sent as returned", res.status, rec, tt.status)
				}
			} else {
				if res.status != 500 || res.header.Get("Content-Type") != "application/problem+json" || strings.Contains(res.body, "db down") ||
					res.problem == nil || res.problem.Title != "Internal Server Error" {
					t.Fatalf("response %d %v %s, want the safe 500", res.status, res.header, res.body)
				}
				if rec["level"] != "ERROR" || problem["status"] != float64(500) || rec["srvr.renderer.failure"] != tt.failure ||
					!slices.Contains(chainOf(rec), "*errors.errorString") {
					t.Fatalf("record %v, want ERROR, status 500, failure %s and the original chain", rec, tt.failure)
				}
			}

			if tt.check != nil {
				tt.check(t, res)
			}

			if res := get(t, base+"/ok"); res.status != http.StatusOK {
				t.Fatalf("next request %d, want 200", res.status)
			}
		})
	}
}

// TestErrorRenderer_Answer covers error-rendering / Scenario: Faulty
// renderer, gin rows on how a valid answer is sent: a HEAD request gets
// the renderer's status and headers without a body, and srvr writes a
// copy of the renderer's header map, never the map itself.
func TestErrorRenderer_Answer(t *testing.T) {
	t.Parallel()

	t.Run("HEAD request", func(t *testing.T) {
		t.Parallel()

		r := srvr.ErrorRendererFunc(func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse {
			return srvr.ErrorResponse{Status: 429, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"code":"quota"}`)}
		})
		base, _ := renderedServer(t, r, func(g *gin.RouterGroup) {
			g.HEAD("/x", func(c *gin.Context) { _ = c.Error(errQuota) })
		})

		res := do(t, http.MethodHead, base+"/x", nil, nil)
		if res.status != 429 || res.body != "" || res.header.Get("Content-Type") != "application/json" {
			t.Fatalf("response %d %v %q, want 429 with the headers and no body", res.status, res.header, res.body)
		}
	})

	t.Run("header map not aliased", func(t *testing.T) {
		t.Parallel()

		shared := http.Header{"Content-Type": {"application/json"}, "Connection": {"close"}, "X-N": {"1"}}
		r := srvr.ErrorRendererFunc(func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse {
			return srvr.ErrorResponse{Status: 429, Header: shared, Body: []byte(`{}`)}
		})
		base, _ := renderedServer(t, r, func(g *gin.RouterGroup) {
			g.GET("/x", func(c *gin.Context) {
				c.Header("X-Handler", "1")
				_ = c.Error(errQuota)
			})
		})

		for range 2 {
			if res := get(t, base+"/x"); res.status != 429 || res.header.Get("X-N") != "1" || res.header.Get("X-Handler") != "1" {
				t.Fatalf("response %d %v, want 429 with X-N and X-Handler", res.status, res.header)
			}
		}

		want := http.Header{"Content-Type": {"application/json"}, "Connection": {"close"}, "X-N": {"1"}}
		if !maps.EqualFunc(shared, want, slices.Equal) {
			t.Fatalf("renderer's header map changed to %v, want %v", shared, want)
		}
	})
}

// cycleError unwraps to itself.
type cycleError struct{}

func (*cycleError) Error() string   { return "cyclic" }
func (e *cycleError) Unwrap() error { return e }

// lateBody writes 200 and a body, then calls fail.
func lateBody(fail func(c *gin.Context)) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.String(http.StatusOK, "partial")
		fail(c)
	}
}

// TestErrorRenderer_Hostile covers error-rendering / Scenario: Hostile
// errors and committed responses, gin rows. A cyclic chain reaches
// c.Errors by assignment: gin's c.Error loops on it first (package doc).
func TestErrorRenderer_Hostile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler gin.HandlerFunc
		calls   int
		check   func(t *testing.T, res response, rec map[string]any, err error)
	}{
		{
			name: "cyclic chain",
			handler: func(c *gin.Context) {
				c.Errors = append(c.Errors, &gin.Error{Err: &cycleError{}, Type: gin.ErrorTypePrivate})
			},
			calls: 1,
			check: func(t *testing.T, res response, _ map[string]any, err error) {
				t.Helper()

				_ = errors.Is(err, errQuota) // terminates
				if res.status != 500 || res.header.Get("Content-Type") != "application/vnd.test+json" {
					t.Errorf("response %d %v, want R's answer", res.status, res.header)
				}
			},
		},
		{
			name: "Unwrap panics", handler: func(c *gin.Context) { c.Errors = append(c.Errors, &gin.Error{Err: unwrapPanicError{}}) }, calls: 1,
			check: func(t *testing.T, res response, _ map[string]any, err error) {
				t.Helper()

				_ = errors.Is(err, errQuota) // does not panic
				if res.header.Get("Content-Type") != "application/vnd.test+json" {
					t.Errorf("response %v, want R's answer", res.header)
				}
			},
		},
		{
			name: "typed-nil error", handler: func(c *gin.Context) { _ = c.Error((*MyError)(nil)) }, calls: 1,
			check: func(t *testing.T, _ response, _ map[string]any, err error) {
				t.Helper()

				if _, ok := err.(*MyError); !ok { //nolint:errorlint // the very value.
					t.Errorf("renderer got %T, want *MyError", err)
				}
			},
		},
		{
			name: "late error", handler: lateBody(func(c *gin.Context) { _ = c.Error(errors.New("late")) }), calls: 0,
			check: func(t *testing.T, res response, rec map[string]any, _ error) {
				t.Helper()

				if res.status != 200 || res.body != "partial" || rec["srvr.response.committed"] != true {
					t.Errorf("response %d %q record %v, want 200 and committed", res.status, res.body, rec)
				}
			},
		},
		{
			name: "late panic", handler: lateBody(func(*gin.Context) { panic("late") }), calls: 0,
			check: func(t *testing.T, _ response, rec map[string]any, _ error) {
				t.Helper()

				if rec["error.kind"] != "panic" {
					t.Errorf("record %v, want error.kind panic", rec)
				}
			},
		},
		{
			name: "Cache-Control replaced",
			handler: func(c *gin.Context) {
				c.Header("Cache-Control", "max-age=60")
				_ = c.Error(errQuota)
			},
			calls: 1,
			check: func(t *testing.T, res response, _ map[string]any, _ error) {
				t.Helper()

				if got := res.header.Values("Cache-Control"); !slices.Equal(got, []string{"no-store"}) {
					t.Errorf("Cache-Control %q, want [no-store]", got)
				}
			},
		},
		{
			name: "Vary merged",
			handler: func(c *gin.Context) {
				c.Header("Vary", "Origin")
				_ = c.Error(errQuota)
			},
			calls: 1,
			check: func(t *testing.T, res response, _ map[string]any, _ error) {
				t.Helper()

				if got := res.header.Values("Vary"); !slices.Equal(got, []string{"Origin", "Accept"}) {
					t.Errorf("Vary %q, want [Origin Accept]", got)
				}
			},
		},
		{
			name: "Vary already listed",
			handler: func(c *gin.Context) {
				c.Header("Vary", "Origin, accept")
				_ = c.Error(errQuota)
			},
			calls: 1,
			check: func(t *testing.T, res response, _ map[string]any, _ error) {
				t.Helper()

				if got := res.header.Values("Vary"); !slices.Equal(got, []string{"Origin, accept"}) {
					t.Errorf("Vary %q, want [Origin, accept] unchanged", got)
				}
			},
		},
		{
			name: "last of several errors",
			handler: func(c *gin.Context) {
				_ = c.Error(errors.New("first"))
				_ = c.Error(errQuota)
			},
			calls: 1,
			check: func(t *testing.T, _ response, _ map[string]any, err error) {
				t.Helper()

				if !errors.Is(err, errQuota) {
					t.Errorf("renderer got %v, want the last error", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			errs := make(chan error, 4)
			r := srvr.ErrorRendererFunc(func(_ context.Context, _ srvr.ErrorRequest, err error) srvr.ErrorResponse {
				errs <- err
				return srvr.ErrorResponse{Status: 500, Header: http.Header{
					"Content-Type": {"application/vnd.test+json"}, "Cache-Control": {"no-store"}, "Vary": {"Accept"},
				}, Body: []byte(`{}`)}
			})

			base, out := renderedServer(t, r, func(g *gin.RouterGroup) { g.GET("/x", tt.handler) })
			res := get(t, base+"/x")
			rec := out.waitRecord(t, "http.request.failed")

			if len(errs) != tt.calls {
				t.Fatalf("renderer called %d times, want %d", len(errs), tt.calls)
			}

			var err error
			if tt.calls > 0 {
				err = <-errs
			}

			tt.check(t, res, rec, err)
		})
	}

	t.Run("50 concurrent requests", func(t *testing.T) {
		t.Parallel()

		r := newRecordingRenderer(50)
		base, _ := renderedServer(t, r, func(g *gin.RouterGroup) { g.GET("/x", func(c *gin.Context) { _ = c.Error(errQuota) }) })

		var wg sync.WaitGroup

		statuses := make(chan int, 50)

		for range 50 {
			wg.Go(func() {
				res, err := clientFor(base).Get(base + "/x")
				if err != nil {
					statuses <- -1
					return
				}
				_ = res.Body.Close()
				statuses <- res.StatusCode
			})
		}

		wg.Wait()
		close(statuses)

		for s := range statuses {
			if s != http.StatusTooManyRequests {
				t.Fatalf("status %d, want R's 429", s)
			}
		}
	})
}

// nilPanicValue is panicked with for the spec's panic(nil) rows (a
// variable, so vet does not flag the deliberate nil panic).
var nilPanicValue any

// TestErrorRenderer_Lifecycle covers error-rendering / Scenario: Renderer
// and lifecycle, gin row.
func TestErrorRenderer_Lifecycle(t *testing.T) {
	t.Parallel()

	r := newRecordingRenderer(4)
	srv, _ := newServer(t, srvr.WithErrorRenderer(r))
	mustNil(t, srv.Register(routes(func(g *gin.RouterGroup) { g.GET("/x", func(c *gin.Context) { _ = c.Error(errQuota) }) })))
	base := start(t, srv)

	if err := srv.Start(t.Context()); !errors.Is(err, srvr.ErrServerStarted) {
		t.Fatalf("second Start = %v, want ErrServerStarted", err)
	}

	if res := get(t, base+"/x"); res.status != http.StatusTooManyRequests {
		t.Fatalf("status %d, want R's 429", res.status)
	}
}

// TestErrorHook covers error-responses / Scenario: Consumer mapper and
// hook on gin: the hook receives every answered error with an
// ErrorReport of the answer sent, on the default path and with a
// renderer, and a status-only abort (ginprblm.ErrStatusOnly) on both
// paths (owner decision 2026-11-21); with a renderer its bare status
// stays the answer and the report carries no type or title.
func TestErrorHook(t *testing.T) {
	t.Parallel()

	type call struct {
		err    error
		report srvr.ErrorReport
	}

	conf := func(g *gin.RouterGroup) {
		g.GET("/x", func(c *gin.Context) { _ = c.Error(errQuota) })
		g.GET("/404", func(c *gin.Context) { _ = c.Error(prblm.ErrNotFound) })
		g.GET("/401", func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) })
		g.GET("/401body", func(c *gin.Context) { c.String(http.StatusUnauthorized, "no") })
		g.GET("/204", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	}

	tests := []struct {
		name     string
		renderer bool
		path     string
		status   int
		want     []call // nil: the hook is not called
	}{
		{"default: error", false, "/404", 404, []call{{prblm.ErrNotFound, srvr.ErrorReport{Status: 404, Type: "about:blank", Title: "Not Found"}}}},
		{"default: status-only abort", false, "/401", 401, []call{{ginprblm.ErrStatusOnly, srvr.ErrorReport{Status: 401, Type: "about:blank", Title: "Unauthorized"}}}},
		{"renderer: error", true, "/x", 429, []call{{errQuota, srvr.ErrorReport{Status: 429}}}},
		{"renderer: router 404", true, "/missing", 404, []call{{srvr.ErrNotFound, srvr.ErrorReport{Status: 404}}}},
		{"renderer: status-only abort", true, "/401", 401, []call{{ginprblm.ErrStatusOnly, srvr.ErrorReport{Status: 401}}}},
		{"renderer: written 401 is no abort", true, "/401body", 401, nil},
		{"renderer: bare success status", true, "/204", 204, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			calls := make(chan call, 4)
			opts := []srvr.Option{srvr.WithErrorHook(func(_ context.Context, err error, sent srvr.ErrorReport) { calls <- call{err, sent} })}
			if tt.renderer {
				opts = append(opts, srvr.WithErrorRenderer(newRecordingRenderer(4)))
			}
			srv, out := newServer(t, opts...)
			mustNil(t, srv.Register(routes(conf)))
			base := start(t, srv)

			if res := get(t, base+tt.path); res.status != tt.status {
				t.Fatalf("status %d, want %d", res.status, tt.status)
			}
			eventually(t, func() bool { return len(out.access(t)) == 1 }, "1 access record")

			var got []call
			for len(calls) > 0 {
				got = append(got, <-calls)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("hook calls %+v, want %+v", got, tt.want)
			}
			for i, c := range got {
				if !errors.Is(c.err, tt.want[i].err) || c.report != tt.want[i].report {
					t.Fatalf("hook call %+v, want %+v", c, tt.want[i])
				}
			}
		})
	}

	t.Run("renderer: panicking hook on a status-only abort", func(t *testing.T) {
		t.Parallel()

		srv, out := newServer(t, srvr.WithErrorRenderer(newRecordingRenderer(4)),
			srvr.WithErrorHook(func(context.Context, error, srvr.ErrorReport) { panic("hook") }))
		mustNil(t, srv.Register(routes(conf)))

		if res := get(t, start(t, srv)+"/401"); res.status != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", res.status)
		}
		eventually(t, func() bool { return len(out.access(t)) == 1 }, "1 access record")
	})
}

// TestErrorHook_AfterResponseStarted covers error-responses / Scenario:
// Consumer mapper and hook, the late rows on gin: an error or panic
// arriving after the response started reaches the hook with the zero
// report (nothing sent), on the default path and with a renderer.
func TestErrorHook_AfterResponseStarted(t *testing.T) {
	t.Parallel()

	conf := func(g *gin.RouterGroup) {
		g.GET("/error", func(c *gin.Context) {
			c.String(http.StatusOK, "x")
			_ = c.Error(errQuota)
		})
		g.GET("/panic", func(c *gin.Context) {
			c.String(http.StatusOK, "x")
			c.Writer.Flush()
			panic("late")
		})
	}

	tests := []struct {
		name     string
		renderer bool
		path     string
	}{
		{"default: error after write", false, "/error"},
		{"renderer: error after write", true, "/error"},
		{"default: panic after flush", false, "/panic"},
		{"renderer: panic after flush", true, "/panic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			reports := make(chan srvr.ErrorReport, 4)
			opts := []srvr.Option{srvr.WithErrorHook(func(_ context.Context, _ error, sent srvr.ErrorReport) { reports <- sent })}
			if tt.renderer {
				opts = append(opts, srvr.WithErrorRenderer(newRecordingRenderer(4)))
			}
			srv, out := newServer(t, opts...)
			mustNil(t, srv.Register(routes(conf)))
			base := start(t, srv)

			if res := get(t, base+tt.path); res.status != http.StatusOK {
				t.Fatalf("status %d, want 200", res.status)
			}
			eventually(t, func() bool { return len(out.access(t)) == 1 }, "1 access record")
			if len(reports) != 1 {
				t.Fatalf("hook called %d times, want once", len(reports))
			}
			if got := <-reports; got != (srvr.ErrorReport{}) {
				t.Fatalf("hook report %+v, want zero (nothing sent)", got)
			}
		})
	}
}

// TestProblemGroup_CustomRenderer covers triage-error-records / Scenario:
// Problem group, the custom-renderer rows on gin.
func TestProblemGroup_CustomRenderer(t *testing.T) {
	t.Parallel()

	hostile := strings.Repeat("t", 10000) + "\n\x1b[31m"
	quota := func(typ, title string) srvr.ErrorRendererFunc {
		return fixedAnswer(srvr.ErrorResponse{Status: 429, Type: typ, Title: title})
	}

	tests := []struct {
		name     string
		renderer srvr.ErrorRendererFunc
		err      error
		check    func(t *testing.T, rec, problem map[string]any)
	}{
		{"no type and title", quota("", ""), errQuota, func(t *testing.T, rec, p map[string]any) {
			t.Helper()

			if p["status"] != float64(429) || p["type"] != nil || p["title"] != nil || rec["srvr.renderer.failure"] != nil {
				t.Errorf("record %v, want status 429 only", rec)
			}
		}},
		{"type and title", quota("https://errors.example/quota", "Quota exceeded"), errQuota, func(t *testing.T, _, p map[string]any) {
			t.Helper()

			if p["status"] != float64(429) || p["type"] != "https://errors.example/quota" || p["title"] != "Quota exceeded" {
				t.Errorf("problem %v", p)
			}
		}},
		{"hostile title", quota("", hostile), errQuota, func(t *testing.T, _, p map[string]any) {
			t.Helper()

			title, _ := p["title"].(string)
			if !strings.HasSuffix(title, "…[truncated]") || strings.ContainsAny(title, "\n\x1b") || len(title) > 1100 {
				t.Errorf("title %q, want capped and escaped", title)
			}
		}},
		{
			"renderer panics", func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse { panic("secret token") }, errors.New("boom"),
			func(t *testing.T, rec, p map[string]any) {
				t.Helper()

				if p["status"] != float64(500) || p["type"] != "about:blank" || p["title"] != "Internal Server Error" ||
					rec["srvr.renderer.failure"] != "panic" || !slices.Contains(chainOf(rec), "*errors.errorString") {
					t.Errorf("record %v", rec)
				}
				if b, _ := json.Marshal(rec); strings.Contains(string(b), "secret token") {
					t.Errorf("record logs the panic value: %s", b)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := renderedServer(t, tt.renderer, func(g *gin.RouterGroup) {
				g.GET("/x", func(c *gin.Context) { _ = c.Error(tt.err) })
			})
			get(t, base+"/x")

			rec := out.waitRecord(t, "http.request.failed")
			problem, _ := rec["problem"].(map[string]any)
			tt.check(t, rec, problem)
		})
	}
}

// TestErrorRenderer_AbortHandler covers a handler panicking with
// http.ErrAbortHandler under a custom renderer: as with ginprblm, the
// connection is aborted, the renderer is not called and the record says
// aborted.
func TestErrorRenderer_AbortHandler(t *testing.T) {
	t.Parallel()

	r := newRecordingRenderer(4)
	base, out := renderedServer(t, r, func(g *gin.RouterGroup) {
		g.GET("/x", func(*gin.Context) { panic(http.ErrAbortHandler) })
	})

	if res, err := clientFor(base).Get(base + "/x"); err == nil {
		_ = res.Body.Close()
		t.Fatalf("status %d, want the connection aborted", res.StatusCode)
	}

	if rec := out.waitRecord(t, "http.request.failed"); rec["srvr.response.aborted"] != true || rec["error.kind"] != "panic" {
		t.Fatalf("record %v, want an aborted panic", rec)
	}

	if len(r.errs) != 0 {
		t.Fatal("renderer called for an aborted request")
	}
}

// TestErrorRenderer_Writer covers how srvr's deferred response writer
// keeps gin's writer contract under a custom renderer: a handler's status
// survives WriteString and Flush, a flushed, hijacked or written response
// is committed so the renderer is not called, and http.ResponseController
// reaches net/http's writer.
func TestErrorRenderer_Writer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler gin.HandlerFunc
		status  int // -1: the client reads no HTTP response from srvr
		body    string
	}{
		{"WriteString keeps the status", func(c *gin.Context) {
			c.Status(http.StatusAccepted)
			_, _ = c.Writer.WriteString("s")
			_ = c.Error(errors.New("late"))
		}, http.StatusAccepted, "s"},
		{"Flush commits the status", func(c *gin.Context) {
			c.Status(http.StatusCreated)
			c.Writer.Flush()
			_ = c.Error(errors.New("late"))
		}, http.StatusCreated, ""},
		{"Write without a status sends 200", func(c *gin.Context) {
			_, _ = c.Writer.Write([]byte("w"))
		}, http.StatusOK, "w"},
		{"a status after the body changes nothing", func(c *gin.Context) {
			c.String(http.StatusOK, "b")
			c.AbortWithStatus(http.StatusUnauthorized)
		}, http.StatusOK, "b"},
		{"ResponseController reaches net/http", func(c *gin.Context) {
			if err := http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(time.Minute)); err != nil {
				_ = c.Error(err)
				return
			}
			c.Status(http.StatusNoContent)
		}, http.StatusNoContent, ""},
		{"Hijack commits", func(c *gin.Context) {
			conn, rw, err := c.Writer.Hijack()
			if err != nil {
				_ = c.Error(err)
				return
			}
			_, _ = rw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 1\r\nConnection: close\r\n\r\nh")
			_ = rw.Flush()
			_ = conn.Close()
			_ = c.Error(errors.New("late"))
		}, http.StatusOK, "h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRecordingRenderer(4)
			base, out := renderedServer(t, r, func(g *gin.RouterGroup) { g.GET("/x", tt.handler) })

			if res := get(t, base+"/x"); res.status != tt.status || res.body != tt.body {
				t.Fatalf("response %d %q, want %d %q", res.status, res.body, tt.status, tt.body)
			}

			eventually(t, func() bool { return len(out.access(t)) == 1 }, "no access record")

			if len(r.errs) != 0 {
				t.Fatalf("renderer called with %v", <-r.errs)
			}
		})
	}
}

// TestErrorRenderer_UnmatchedAbort covers a consumer root middleware
// aborting, with its own status, a request no route or mount matched
// (it runs in gin's no-route chain once the server has a mount): that is
// the middleware's status-only abort, not the router's answer, so its
// bare status is sent and the renderer is not called.
func TestErrorRenderer_UnmatchedAbort(t *testing.T) {
	t.Parallel()

	r := newRecordingRenderer(4)
	srv, out := newServer(t, srvr.WithErrorRenderer(r))
	mustNil(t, srv.Register(ginsrvr.Static("/assets", fstest.MapFS{"app.js": &fstest.MapFile{Data: []byte("1")}})))
	mustNil(t, srv.Register(routes(func(g *gin.RouterGroup) {
		g.Use(func(c *gin.Context) { c.AbortWithStatus(http.StatusForbidden) })
	})))

	if res := get(t, start(t, srv)+"/other"); res.status != http.StatusForbidden || res.body != "" {
		t.Fatalf("response %d %q, want the bare 403", res.status, res.body)
	}

	eventually(t, func() bool { return len(out.access(t)) == 1 }, "no access record")

	if len(r.errs) != 0 {
		t.Fatalf("renderer called with %v", <-r.errs)
	}
}
