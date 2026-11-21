package fibersrvr_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
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

			srv, err := fibersrvr.New(tt.opts...)
			ce, ok := errors.AsType[*srvr.ConfigError](err)
			if srv != nil || !ok || ce.Field != "ErrorRenderer" || ce.Reason != tt.reason {
				t.Fatalf("New = %v, %v; want a ConfigError for ErrorRenderer: %s", srv, err, tt.reason)
			}
		})
	}

	t.Run("valid renderer accepted", func(t *testing.T) {
		t.Parallel()

		srv, err := fibersrvr.New(srvr.WithLogOutput(io.Discard), srvr.WithErrorRenderer(renderer))
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
	case errors.Is(err, srvr.ErrHeadersTooLarge):
		status = http.StatusRequestHeaderFieldsTooLarge
	case errors.Is(err, srvr.ErrRequestTimeout):
		status = http.StatusRequestTimeout
	case errors.Is(err, srvr.ErrClientClosed):
		status = 499
	case errors.As(err, new(*fiber.Error)):
		fe, _ := errors.AsType[*fiber.Error](err)
		status = fe.Code
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

// renderedServer starts a logged fiber server with conf and
// WithErrorRenderer(r) and returns its base URL and log output.
func renderedServer(t *testing.T, r srvr.ErrorRenderer, conf routes, opts ...srvr.Option) (string, *logBuffer) {
	t.Helper()

	srv, logs := newServer(t, append([]srvr.Option{srvr.WithErrorRenderer(r)}, opts...)...)
	mustNil(t, srv.Register(conf))

	return start(t, srv), logs
}

// get sends GET url and reads the whole response.
func get(t *testing.T, url string) response {
	t.Helper()

	return do(t, http.MethodGet, url, nil, nil)
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

// rawSend writes raw, a request as is, to addr over its own TCP
// connection and reads the server's answer.
func rawSend(t *testing.T, addr, raw string) response {
	t.Helper()

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, _ = io.WriteString(conn, raw)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer res.Body.Close()

	return parseResponse(t, res)
}

// TestErrorRenderer_EveryAnswer covers error-rendering / Scenario: Custom
// renderer on every engine, fiber rows, the pre-routing rejections (413,
// 431, 408) over a real listener: they come from fasthttp's error path,
// which app.Test does not reach. The client-closed row is
// TestErrorRenderer_Deadline's (fiber's request context never cancels on
// a disconnect).
func TestErrorRenderer_EveryAnswer(t *testing.T) {
	t.Parallel()

	site := fstest.MapFS{"app.js": &fstest.MapFile{Data: []byte("1")}}

	tests := []struct {
		name   string
		opts   []srvr.Option
		send   func(t *testing.T, base, addr string) response
		match  func(err error) bool
		status int
	}{
		{name: "handler error", send: getPath("/quota"), status: 429, match: func(err error) bool { return errors.Is(err, errQuota) }},
		{name: "panic", send: getPath("/panic"), status: 500, match: func(err error) bool {
			pe, ok := errors.AsType[*srvr.PanicError](err)
			return ok && pe.Value == "boom"
		}},
		{name: "router 404", send: getPath("/missing"), status: 404, match: func(err error) bool {
			return errors.Is(err, srvr.ErrNotFound) && errors.Is(err, prblm.ErrNotFound)
		}},
		{name: "router 405", send: getPath("/post"), status: 405, match: func(err error) bool {
			mna, ok := errors.AsType[*srvr.MethodNotAllowedError](err)
			return ok && slices.Equal(mna.Allow, []string{"POST"})
		}},
		{name: "static miss", send: getPath("/assets/missing.js"), status: 404, match: func(err error) bool { return errors.Is(err, srvr.ErrNotFound) }},
		{name: "static 405", send: func(t *testing.T, base, _ string) response {
			t.Helper()

			return do(t, http.MethodDelete, base+"/assets/app.js", nil, nil)
		}, status: 405, match: func(err error) bool {
			mna, ok := errors.AsType[*srvr.MethodNotAllowedError](err)
			return ok && slices.Equal(mna.Allow, []string{"GET", "HEAD"})
		}},
		{name: "body over the limit", opts: []srvr.Option{srvr.WithMaxRequestBodyBytes(10)}, send: func(t *testing.T, _, addr string) response {
			t.Helper()

			return rawRequest(t, addr, http.MethodPost, "/post", "", []byte("12345678901"))
		}, status: 413, match: func(err error) bool { return errors.Is(err, srvr.ErrRequestTooLarge) }},
		{name: "headers over the read buffer", opts: []srvr.Option{srvr.WithMaxHeaderBytes(8192)}, send: func(t *testing.T, _, addr string) response {
			t.Helper()

			return rawRequest(t, addr, http.MethodGet, "/x", "X-Pad: "+strings.Repeat("a", 16<<10)+"\r\n", nil)
		}, status: 431, match: func(err error) bool { return errors.Is(err, srvr.ErrHeadersTooLarge) }},
		{name: "read timeout", opts: []srvr.Option{srvr.WithReadTimeout(50 * time.Millisecond)}, send: func(t *testing.T, _, addr string) response {
			t.Helper()

			return rawSend(t, addr, "GET /x HTTP/1.1\r\nHost: x\r\n") // headers never end
		}, status: 408, match: func(err error) bool { return errors.Is(err, srvr.ErrRequestTimeout) }},
		{name: "another rejection", send: func(t *testing.T, _, addr string) response {
			t.Helper()

			return rawSend(t, addr, "GET /x HTTP/1.1\r\nHost: x\r\nContent-Length: abc\r\n\r\n")
		}, status: 400, match: func(err error) bool {
			fe, ok := errors.AsType[*fiber.Error](err)
			return ok && fe.Code == 400
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRecordingRenderer(64)
			conf := routes(func(g fiber.Router) {
				g.Get("/quota", func(fiber.Ctx) error { return errQuota })
				g.Get("/panic", func(fiber.Ctx) error { panic("boom") })
				g.Post("/post", ok)
			})

			srv, out := newServer(t, append([]srvr.Option{srvr.WithErrorRenderer(r)}, tt.opts...)...)
			mustNil(t, srv.Register(conf))
			mustNil(t, srv.Register(fibersrvr.Static("/assets", site)))
			base := start(t, srv)

			res := tt.send(t, base, srv.Addr())
			if res.status != tt.status || res.header.Get("Content-Type") != "application/vnd.test+json" ||
				res.body != `{"code":"`+strconv.Itoa(tt.status)+`"}` {
				t.Fatalf("response %d %v %s, want R's %d", res.status, res.header, res.body, tt.status)
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
}

// getPath returns a send function for GET path.
func getPath(path string) func(t *testing.T, base, addr string) response {
	return func(t *testing.T, base, _ string) response {
		t.Helper()

		return get(t, base+path)
	}
}

// TestErrorRenderer_Unreplayed covers a request fasthttp rejects whose
// method fiber does not know, so fiber does not replay the Use chain and
// srvr's request stage never runs: the renderer still answers it, and no
// access record is written, as without a renderer.
func TestErrorRenderer_Unreplayed(t *testing.T) {
	t.Parallel()

	r := newRecordingRenderer(4)
	srv, out := newServer(t, srvr.WithErrorRenderer(r), srvr.WithMaxRequestBodyBytes(10))
	mustNil(t, srv.Register(routes(func(g fiber.Router) { g.Get("/x", ok) })))
	start(t, srv)

	res := rawRequest(t, srv.Addr(), "BREW", "/x", "", []byte("12345678901"))
	if res.status != 413 || res.body != `{"code":"413"}` {
		t.Fatalf("response %d %v %s, want R's 413", res.status, res.header, res.body)
	}

	if err := receive(t, r.errs, "renderer not called"); !errors.Is(err, srvr.ErrRequestTooLarge) {
		t.Fatalf("renderer got %T %v, want ErrRequestTooLarge", err, err)
	}

	if recs := out.access(t); len(recs) != 0 {
		t.Fatalf("access records %v, want none", recs)
	}
}

// TestErrorRenderer_Request covers error-rendering / Scenario: What the
// renderer receives, on fiber: Route is fiber's registered path, the
// renderer's context is bounded by srvr's render deadline, and Path is a
// copy that stays valid after fiber reuses its buffers.
func TestErrorRenderer_Request(t *testing.T) {
	t.Parallel()

	r := newRecordingRenderer(4)
	base, _ := renderedServer(t, r, func(g fiber.Router) {
		g.Get("/items/:id", func(fiber.Ctx) error { return errQuota })
		g.Get("/o/:id", ok)
	})
	do(t, "GET", base+"/items/7?q=secret", nil, map[string]string{
		"Accept": "application/json", "traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	})

	_ = receive(t, r.errs, "renderer not called")
	req, ctx := <-r.reqs, <-r.ctxs
	tr, traced := srvr.TraceFromContext(ctx)
	deadline, bounded := ctx.Deadline()

	// fiber reuses its pooled contexts' path buffers for later requests
	// (a path of the same length, so the buffer is not reallocated).
	for range 20 {
		do(t, "GET", base+"/o/zzzzz", nil, nil)
	}

	tests := []struct {
		field string
		got   any
		want  any
	}{
		{"Method", req.Method, "GET"},
		{"Path", req.Path, "/items/7"},
		{"Route", req.Route, "/items/:id"}, // fiber's registered path, as http.route logs it
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

	t.Run("Header.Get(Accept) during Render", func(t *testing.T) {
		t.Parallel()

		accept := make(chan string, 1)
		base, _ := renderedServer(t, srvr.ErrorRendererFunc(func(_ context.Context, req srvr.ErrorRequest, _ error) srvr.ErrorResponse {
			accept <- req.Header.Get("Accept")
			return srvr.ErrorResponse{Status: 429}
		}), func(g fiber.Router) { g.Get("/x", func(fiber.Ctx) error { return errQuota }) })
		do(t, "GET", base+"/x", nil, map[string]string{"Accept": "application/json"})

		if got := receive(t, accept, "renderer not called"); got != "application/json" {
			t.Fatalf("Accept %q, want application/json", got)
		}
	})
}

// TestErrorRenderer_Faulty covers error-rendering / Scenario: Faulty
// renderer, fiber rows. The deadline rows are TestErrorRenderer_Deadline.
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

				got := res.header.Values("Set-Cookie")
				slices.Sort(got)
				if !slices.Equal(got, []string{"csrf=; Max-Age=0", "session=; Max-Age=0"}) {
					t.Errorf("Set-Cookie %q, want both cookies", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := renderedServer(t, tt.renderer, func(g fiber.Router) {
				g.Get("/x", func(fiber.Ctx) error { return errors.New("db down") })
				g.Get("/ok", ok)
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
// renderer, fiber rows on how a valid answer is sent: a HEAD request gets
// the renderer's status and headers without a body, and srvr writes a
// copy of the renderer's header map, never the map itself.
func TestErrorRenderer_Answer(t *testing.T) {
	t.Parallel()

	t.Run("HEAD request", func(t *testing.T) {
		t.Parallel()

		base, _ := renderedServer(t, fixedAnswer(srvr.ErrorResponse{
			Status: 429, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"code":"quota"}`),
		}), func(g fiber.Router) {
			g.Head("/x", func(fiber.Ctx) error { return errQuota })
		})

		res := do(t, http.MethodHead, base+"/x", nil, nil)
		if res.status != 429 || res.body != "" || res.header.Get("Content-Type") != "application/json" {
			t.Fatalf("response %d %v %q, want 429 with the headers and no body", res.status, res.header, res.body)
		}
	})

	t.Run("header map not aliased", func(t *testing.T) {
		t.Parallel()

		shared := http.Header{"Content-Type": {"application/json"}, "Connection": {"close"}, "X-N": {"1"}}
		base, _ := renderedServer(t, fixedAnswer(srvr.ErrorResponse{Status: 429, Header: shared, Body: []byte(`{}`)}), func(g fiber.Router) {
			g.Get("/x", func(c fiber.Ctx) error {
				c.Set("X-Handler", "1")
				return errQuota
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

// unwrapPanicError panics in Unwrap.
type unwrapPanicError struct{}

func (unwrapPanicError) Error() string { return "unwrap panics" }
func (unwrapPanicError) Unwrap() error { panic("unwrap") }

// streamBody starts a body stream with status 200, then calls fail.
func streamBody(fail func() error) fiber.Handler {
	return func(c fiber.Ctx) error {
		c.Status(http.StatusOK)
		c.Response().SetBodyStream(strings.NewReader("partial"), len("partial"))

		return fail()
	}
}

// TestErrorRenderer_Hostile covers error-rendering / Scenario: Hostile
// errors and committed responses, fiber rows. fasthttp sends nothing
// before the chain returns, so on fiber only a body stream commits the
// response; a buffered body is replaced by the renderer's answer.
func TestErrorRenderer_Hostile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler fiber.Handler
		calls   int
		check   func(t *testing.T, res response, rec map[string]any, err error)
	}{
		{
			name: "cyclic chain", handler: func(fiber.Ctx) error { return &cyclicError{} }, calls: 1,
			check: func(t *testing.T, res response, _ map[string]any, err error) {
				t.Helper()

				_ = errors.Is(err, errQuota) // terminates
				if res.status != 500 || res.header.Get("Content-Type") != "application/vnd.test+json" {
					t.Errorf("response %d %v, want R's answer", res.status, res.header)
				}
			},
		},
		{
			name: "Unwrap panics", handler: func(fiber.Ctx) error { return unwrapPanicError{} }, calls: 1,
			check: func(t *testing.T, res response, _ map[string]any, err error) {
				t.Helper()

				_ = errors.Is(err, errQuota) // does not panic
				if res.header.Get("Content-Type") != "application/vnd.test+json" {
					t.Errorf("response %v, want R's answer", res.header)
				}
			},
		},
		{
			name: "typed-nil error", handler: func(fiber.Ctx) error { return (*MyError)(nil) }, calls: 1,
			check: func(t *testing.T, _ response, _ map[string]any, err error) {
				t.Helper()

				if _, ok := err.(*MyError); !ok { //nolint:errorlint // the very value.
					t.Errorf("renderer got %T, want *MyError", err)
				}
			},
		},
		{
			name: "buffered body then error",
			handler: func(c fiber.Ctx) error {
				_ = c.Status(http.StatusOK).SendString("partial")
				return errors.New("late")
			},
			calls: 1,
			check: func(t *testing.T, res response, rec map[string]any, _ error) {
				t.Helper()

				if res.status != 500 || res.body != "{}" || rec["srvr.response.committed"] != nil {
					t.Errorf("response %d %q record %v, want R's answer, not committed", res.status, res.body, rec)
				}
			},
		},
		{
			name: "buffered body then panic",
			handler: func(c fiber.Ctx) error {
				_ = c.Status(http.StatusOK).SendString("partial")
				panic("late")
			},
			calls: 1,
			check: func(t *testing.T, res response, rec map[string]any, _ error) {
				t.Helper()

				if res.status != 500 || res.body != "{}" || rec["srvr.response.committed"] != nil {
					t.Errorf("response %d %q record %v, want R's answer, not committed", res.status, res.body, rec)
				}
			},
		},
		{
			name: "body stream then error", handler: streamBody(func() error { return errors.New("late") }), calls: 0,
			check: func(t *testing.T, res response, rec map[string]any, _ error) {
				t.Helper()

				if res.status != 200 || res.body != "partial" || rec["srvr.response.committed"] != true {
					t.Errorf("response %d %q record %v, want 200 and committed", res.status, res.body, rec)
				}
			},
		},
		{
			name: "body stream then panic", handler: streamBody(func() error { panic("late") }), calls: 0,
			check: func(t *testing.T, res response, rec map[string]any, _ error) {
				t.Helper()

				if res.status != 200 || rec["error.kind"] != "panic" {
					t.Errorf("response %d record %v, want 200 and error.kind panic", res.status, rec)
				}
			},
		},
		{
			name: "Cache-Control replaced",
			handler: func(c fiber.Ctx) error {
				c.Set("Cache-Control", "max-age=60")
				return errQuota
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
			handler: func(c fiber.Ctx) error {
				c.Set("Vary", "Origin")
				return errQuota
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
			handler: func(c fiber.Ctx) error {
				c.Set("Vary", "Origin, accept")
				return errQuota
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
			name: "Transfer-Encoding set by the handler dropped",
			handler: func(c fiber.Ctx) error {
				c.Set("Transfer-Encoding", "chunked")
				return errQuota
			},
			calls: 1,
			check: func(t *testing.T, res response, _ map[string]any, _ error) {
				t.Helper()

				if res.body != "{}" || res.header.Get("Content-Length") != "2" {
					t.Errorf("response %v %q, want R's body with Content-Length 2", res.header, res.body)
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

			base, out := renderedServer(t, r, func(g fiber.Router) { g.Get("/x", tt.handler) })
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
		base, _ := renderedServer(t, r, func(g fiber.Router) { g.Get("/x", func(fiber.Ctx) error { return errQuota }) })

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
// and lifecycle on fiber: a second Start leaves the renderer working. The
// fiber row (R past the render deadline) is TestErrorRenderer_Deadline.
func TestErrorRenderer_Lifecycle(t *testing.T) {
	t.Parallel()

	srv, _ := newServer(t, srvr.WithErrorRenderer(newRecordingRenderer(4)))
	mustNil(t, srv.Register(routes(func(g fiber.Router) { g.Get("/x", func(fiber.Ctx) error { return errQuota }) })))
	base := start(t, srv)

	if err := srv.Start(t.Context()); !errors.Is(err, srvr.ErrServerStarted) {
		t.Fatalf("second Start = %v, want ErrServerStarted", err)
	}

	if res := get(t, base+"/x"); res.status != http.StatusTooManyRequests {
		t.Fatalf("status %d, want R's 429", res.status)
	}
}

// TestErrorHook covers error-responses / Scenario: Consumer mapper and
// hook on fiber: the hook receives every answered error, a pre-routing
// rejection included, with an ErrorReport of the answer sent, on the
// default path and with a renderer.
func TestErrorHook(t *testing.T) {
	t.Parallel()

	type call struct {
		err    error
		report srvr.ErrorReport
	}

	tests := []struct {
		name     string
		renderer bool
		method   string
		path     string
		body     []byte // past the 10-byte body limit for the rejection
		status   int
		want     call
	}{
		{"default: error", false, http.MethodGet, "/404", nil, 404, call{prblm.ErrNotFound, srvr.ErrorReport{Status: 404, Type: "about:blank", Title: "Not Found"}}},
		{"renderer: error", true, http.MethodGet, "/x", nil, 429, call{errQuota, srvr.ErrorReport{Status: 429}}},
		{"renderer: router 404", true, http.MethodGet, "/missing", nil, 404, call{srvr.ErrNotFound, srvr.ErrorReport{Status: 404}}},
		{"default: rejection", false, http.MethodPost, "/post", []byte("12345678901"), 413, call{fiber.ErrRequestEntityTooLarge, srvr.ErrorReport{Status: 413, Type: "about:blank", Title: "Request Entity Too Large"}}},
		{"default: error after the body stream started", false, http.MethodGet, "/stream", nil, 200, call{errQuota, srvr.ErrorReport{}}},
		{"renderer: error after the body stream started", true, http.MethodGet, "/stream", nil, 200, call{errQuota, srvr.ErrorReport{}}},
		{"renderer: rejection", true, http.MethodPost, "/post", []byte("12345678901"), 413, call{srvr.ErrRequestTooLarge, srvr.ErrorReport{Status: 413}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			calls := make(chan call, 4)
			opts := []srvr.Option{
				srvr.WithMaxRequestBodyBytes(10),
				srvr.WithErrorHook(func(_ context.Context, err error, sent srvr.ErrorReport) { calls <- call{err, sent} }),
			}
			if tt.renderer {
				opts = append(opts, srvr.WithErrorRenderer(newRecordingRenderer(4)))
			}
			srv, out := newServer(t, opts...)
			mustNil(t, srv.Register(routes(func(g fiber.Router) {
				g.Get("/x", func(fiber.Ctx) error { return errQuota })
				g.Get("/404", func(fiber.Ctx) error { return prblm.ErrNotFound })
				g.Post("/post", ok)
				g.Get("/stream", func(c fiber.Ctx) error {
					c.Response().SetBodyStream(strings.NewReader("x"), 1)
					return errQuota
				})
			})))
			start(t, srv)

			rawRequest(t, srv.Addr(), tt.method, tt.path, "", tt.body)
			eventually(t, func() bool { return len(out.access(t)) == 1 }, "1 access record")

			if rec := out.access(t)[0]; rec["http.response.status_code"] != float64(tt.status) {
				t.Fatalf("access record %v, want status %d", rec, tt.status)
			}
			if len(calls) != 1 {
				t.Fatalf("hook called %d times, want once", len(calls))
			}
			if c := <-calls; !errors.Is(c.err, tt.want.err) || c.report != tt.want.report {
				t.Fatalf("hook call %+v, want %+v", c, tt.want)
			}
		})
	}
}

// TestProblemGroup_CustomRenderer covers triage-error-records / Scenario:
// Problem group, the custom-renderer rows on fiber.
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

			base, out := renderedServer(t, tt.renderer, func(g fiber.Router) {
				g.Get("/x", func(fiber.Ctx) error { return tt.err })
			})
			get(t, base+"/x")

			rec := out.waitRecord(t, "http.request.failed")
			problem, _ := rec["problem"].(map[string]any)
			tt.check(t, rec, problem)
		})
	}
}

// TestErrorRenderer_AbortHandler covers a handler panicking with
// http.ErrAbortHandler under a custom renderer: as with fiberprblm (the
// existing fiber parity exception), fasthttp has no connection abort, so
// the panic is answered like any other, through the renderer.
func TestErrorRenderer_AbortHandler(t *testing.T) {
	t.Parallel()

	r := newRecordingRenderer(4)
	base, out := renderedServer(t, r, func(g fiber.Router) {
		g.Get("/x", func(fiber.Ctx) error { panic(http.ErrAbortHandler) })
	})

	if res := get(t, base+"/x"); res.status != 500 || res.header.Get("Content-Type") != "application/vnd.test+json" {
		t.Fatalf("response %d %v, want R's 500", res.status, res.header)
	}

	if err := receive(t, r.errs, "renderer not called"); !errors.As(err, new(*srvr.PanicError)) {
		t.Fatalf("renderer got %T %v, want a PanicError", err, err)
	}

	if rec := out.waitRecord(t, "http.request.failed"); rec["error.kind"] != "panic" {
		t.Fatalf("record %v, want a panic", rec)
	}
}

// TestErrorRenderer_RejectionRecord covers the access record and the
// observers' result of a request fasthttp rejected before routing: both
// report the answer the client got, the renderer's or srvr's safe 500
// when the renderer panics, not the status fiber set.
func TestErrorRenderer_RejectionRecord(t *testing.T) {
	t.Parallel()

	answer := []byte("bad request!")
	tests := []struct {
		name     string
		renderer srvr.ErrorRendererFunc
		opt      srvr.Option
		send     func(t *testing.T, addr string) response
		status   int
	}{
		{
			name: "413 answered 400",
			renderer: func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse {
				return srvr.ErrorResponse{Status: 400, Header: http.Header{"Content-Type": {"text/plain"}}, Body: answer}
			},
			opt: srvr.WithMaxRequestBodyBytes(10),
			send: func(t *testing.T, addr string) response {
				t.Helper()

				return rawRequest(t, addr, http.MethodPost, "/x", "", []byte("12345678901"))
			},
			status: 400,
		},
		{
			name:     "431 renderer panics",
			renderer: func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse { panic("boom") },
			opt:      srvr.WithMaxHeaderBytes(8192),
			send: func(t *testing.T, addr string) response {
				t.Helper()

				return rawRequest(t, addr, http.MethodGet, "/x", "X-Pad: "+strings.Repeat("a", 16<<10)+"\r\n", nil)
			},
			status: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs := &recordingObserver{}
			srv, out := newServer(t, tt.opt, srvr.WithErrorRenderer(tt.renderer), srvr.WithObserver(obs))
			mustNil(t, srv.Register(routes(func(g fiber.Router) {
				g.Get("/x", ok)
				g.Post("/x", ok)
			})))
			start(t, srv)

			res := tt.send(t, srv.Addr())
			rec := out.waitRecord(t, "http.request.failed")
			_, results := obs.seen() // WriteAccess ends the observers before it logs
			if len(results) != 1 {
				t.Fatalf("observer ended %d times, want 1", len(results))
			}
			result := results[0]

			if res.status != tt.status || rec["http.response.status_code"] != float64(res.status) ||
				rec["http.response.body.size"] != float64(len(res.body)) || result.Status != res.status {
				t.Errorf("client got %d (%d bytes); access record status_code=%v body.size=%v; observer status %d",
					res.status, len(res.body), rec["http.response.status_code"], rec["http.response.body.size"], result.Status)
			}
		})
	}
}

// TestErrorRenderer_RequestRetained covers a renderer keeping its
// ErrorRequest: later failing requests with a path of the same length,
// which reuse fiber's pooled buffers, leave it unchanged.
func TestErrorRenderer_RequestRetained(t *testing.T) {
	t.Parallel()

	reqs := make(chan srvr.ErrorRequest, 1)
	r := srvr.ErrorRendererFunc(func(_ context.Context, req srvr.ErrorRequest, _ error) srvr.ErrorResponse {
		reqs <- req
		return srvr.ErrorResponse{Status: 429}
	})
	base, _ := renderedServer(t, r, func(g fiber.Router) {
		g.Get("/items/:id", func(fiber.Ctx) error { return errQuota })
		g.Post("/items/:id", func(fiber.Ctx) error { return errQuota })
	})

	do(t, http.MethodGet, base+"/items/aaaa", nil, map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"})
	first := <-reqs

	for range 30 {
		do(t, http.MethodPost, base+"/items/bbbb", nil, map[string]string{"traceparent": "00-11111111111111111111111111111111-00f067aa0ba902b7-01"})
		<-reqs
	}

	want := srvr.ErrorRequest{Method: http.MethodGet, Path: "/items/aaaa", Route: "/items/:id", TraceID: "4bf92f3577b34da6a3ce929d0e0e4736"}
	if first.Method != want.Method || first.Path != want.Path || first.Route != want.Route || first.TraceID != want.TraceID {
		t.Errorf("retained request %+v, want %+v", first, want)
	}
}
