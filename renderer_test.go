package srvr_test

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

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// testRenderer is the spec's R: it answers status with
// application/vnd.test+json and {"code":"<code>"}.
type testRenderer struct {
	status int
	code   string
}

func (r *testRenderer) Render(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse {
	return srvr.ErrorResponse{
		Status: r.status,
		Header: map[string][]string{"Content-Type": {"application/vnd.test+json"}},
		Body:   []byte(`{"code":"` + r.code + `"}`),
	}
}

// TestWithErrorRenderer_Rejected covers error-rendering / Scenario:
// Rejected renderer configuration, net/http rows.
func TestWithErrorRenderer_Rejected(t *testing.T) {
	t.Parallel()

	r := &testRenderer{status: 429, code: "quota"}

	tests := []struct {
		name    string
		opts    []srvr.Option
		fields  []string
		message string
	}{
		{"nil", []srvr.Option{srvr.WithErrorRenderer(nil)}, []string{"ErrorRenderer"}, "is nil"},
		{"typed nil", []srvr.Option{srvr.WithErrorRenderer((*testRenderer)(nil))}, []string{"ErrorRenderer"}, "is nil"},
		{
			"renderer then problem options",
			[]srvr.Option{srvr.WithErrorRenderer(r), srvr.WithProblemOptions(prblm.WithNamespace("https://errors.example/p"))},
			[]string{"ErrorRenderer"},
			"cannot be combined with ProblemOptions",
		},
		{
			"problem options then renderer",
			[]srvr.Option{srvr.WithProblemOptions(prblm.WithNamespace("https://errors.example/p")), srvr.WithErrorRenderer(r)},
			[]string{"ErrorRenderer"},
			"cannot be combined with ProblemOptions",
		},
		{
			"empty problem options",
			[]srvr.Option{srvr.WithErrorRenderer(r), srvr.WithProblemOptions()},
			[]string{"ErrorRenderer"},
			"cannot be combined with ProblemOptions",
		},
		{
			"joined with Addr",
			[]srvr.Option{srvr.WithErrorRenderer(nil), srvr.WithAddr("bad")},
			[]string{"ErrorRenderer", "Addr"},
			"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, err := newRecovered(tt.opts...)
			if srv != nil || !errors.Is(err, srvr.ErrConfig) {
				t.Fatalf("New = %v, %v; want an error wrapping ErrConfig", srv, err)
			}

			assertConfigFields(t, err, tt.fields, tt.message)
		})
	}
}

// assertConfigFields checks that err holds a *srvr.ConfigError for every
// field, the first with message unless message is empty.
func assertConfigFields(t *testing.T, err error, fields []string, message string) {
	t.Helper()

	var all []error
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		all = j.Unwrap()
	} else {
		all = []error{err}
	}

	for _, field := range fields {
		found := false

		for _, e := range all {
			if ce, ok := errors.AsType[*srvr.ConfigError](e); ok && ce.Field == field {
				found = true

				if message != "" && ce.Reason != message {
					t.Errorf("%s reason %q, want %q", field, ce.Reason, message)
				}
			}
		}

		if !found {
			t.Errorf("no ConfigError for %s in %v", field, err)
		}
	}
}

// recordingRenderer is the spec's R for the HTTP rows: it answers srvr's
// own errors and errQuota with their status, sends every call's error on
// errs and its request on reqs.
type recordingRenderer struct {
	errs chan error
	reqs chan srvr.ErrorRequest
	ctxs chan context.Context
}

func newRecordingRenderer() *recordingRenderer {
	return &recordingRenderer{errs: make(chan error, 64), reqs: make(chan srvr.ErrorRequest, 64), ctxs: make(chan context.Context, 64)}
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

// renderedServer starts a logged net/http server with routes and
// WithErrorRenderer(r).
func renderedServer(t *testing.T, r srvr.ErrorRenderer, routes configFunc, opts ...srvr.Option) (string, *logBuffer) {
	t.Helper()

	return loggedServer(t, routes, append([]srvr.Option{srvr.WithErrorRenderer(r)}, opts...)...)
}

// TestErrorRenderer_EveryAnswer covers error-rendering / Scenario: Custom
// renderer on every engine, net/http rows.
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

			r := newRecordingRenderer()
			entered := make(chan struct{})
			base, out := renderedServer(t, r, func(g *srvr.Group) {
				g.GET("/quota", returning(errQuota))
				g.GET("/panic", func(http.ResponseWriter, *http.Request) error { panic("boom") })
				g.POST("/post", func(_ http.ResponseWriter, req *http.Request) error {
					_, err := io.ReadAll(req.Body)
					return err
				})
				g.GET("/gone", func(_ http.ResponseWriter, req *http.Request) error {
					close(entered)
					<-req.Context().Done()

					return fmt.Errorf("q: %w", context.Canceled)
				})
				srvr.Static("/assets", site).Configure(g)
			}, srvr.WithMaxRequestBodyBytes(10))

			if tt.gone {
				disconnectWhile(t, base+tt.path, entered)
			} else {
				res := do(t, tt.method, base+tt.path, strings.NewReader(tt.body), nil)
				if res.status != tt.status || res.header.Get("Content-Type") != "application/vnd.test+json" ||
					res.body != `{"code":"`+strconv.Itoa(tt.status)+`"}` {
					t.Fatalf("response %d %v %s, want R's %d", res.status, res.header, res.body, tt.status)
				}
			}

			if err := receive(t, r.errs, "renderer call"); !tt.match(err) {
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

// TestErrorRenderer_Request covers error-rendering / Scenario: What the
// renderer receives.
func TestErrorRenderer_Request(t *testing.T) {
	t.Parallel()

	r := newRecordingRenderer()
	base, _ := renderedServer(t, r, func(g *srvr.Group) { g.GET("/items/{id}", returning(errQuota)) })
	do(t, "GET", base+"/items/7?q=secret", nil, map[string]string{
		"Accept": "application/json", "traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	})

	_ = receive(t, r.errs, "renderer call")
	req, ctx := <-r.reqs, <-r.ctxs
	tr, traced := srvr.TraceFromContext(ctx)

	tests := []struct {
		field string
		got   any
		want  any
	}{
		{"Method", req.Method, "GET"},
		{"Path", req.Path, "/items/7"},
		{"Route", req.Route, "/items/{id}"}, // the engine's pattern, as http.route logs it
		{"Header.Get(Accept)", req.Header.Get("Accept"), "application/json"},
		{"TraceID", req.TraceID, "4bf92f3577b34da6a3ce929d0e0e4736"},
		{"the context", traced && tr.TraceID == "4bf92f3577b34da6a3ce929d0e0e4736", true},
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
// renderer, net/http rows; the deadline row is TestRender's (the 5 s
// deadline is shortened only through internal/engine).
func TestErrorRenderer_Faulty(t *testing.T) {
	t.Parallel()

	const mib = 1 << 20

	ct := http.Header{"Content-Type": {"application/vnd.test+json"}}
	answer := func(res srvr.ErrorResponse) srvr.ErrorRendererFunc {
		return func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse { return res }
	}

	tests := []struct {
		name     string
		renderer srvr.ErrorRendererFunc
		failure  string // "": sent as returned
		status   int
		check    func(t *testing.T, res response)
	}{
		{name: "panics", renderer: func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse { panic("render boom") }, failure: "panic"},
		{name: "panics with nil", renderer: func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse { panic(nilPanicValue) }, failure: "panic"},
		{name: "status 0", renderer: answer(srvr.ErrorResponse{}), failure: "invalid_status"},
		{name: "status 200", renderer: answer(srvr.ErrorResponse{Status: 200}), failure: "invalid_status"},
		{name: "status 399", renderer: answer(srvr.ErrorResponse{Status: 399}), failure: "invalid_status"},
		{name: "status 600", renderer: answer(srvr.ErrorResponse{Status: 600}), failure: "invalid_status"},
		{name: "status -1", renderer: answer(srvr.ErrorResponse{Status: -1}), failure: "invalid_status"},
		{name: "status 100", renderer: answer(srvr.ErrorResponse{Status: 100}), failure: "invalid_status"},
		{name: "body of exactly 1 MiB", renderer: answer(srvr.ErrorResponse{Status: 400, Header: ct, Body: bytes.Repeat([]byte("a"), mib)}), status: 400, check: func(t *testing.T, res response) {
			t.Helper()

			if len(res.body) != mib {
				t.Errorf("body %d bytes, want %d", len(res.body), mib)
			}
		}},
		{name: "body of 1 MiB + 1", renderer: answer(srvr.ErrorResponse{Status: 400, Header: ct, Body: make([]byte, mib+1)}), failure: "oversized_body"},
		{name: "body without Content-Type", renderer: answer(srvr.ErrorResponse{Status: 400, Body: make([]byte, 10)}), failure: "missing_content_type"},
		{name: "bare 404", renderer: answer(srvr.ErrorResponse{Status: 404}), status: 404, check: func(t *testing.T, res response) {
			t.Helper()

			if res.body != "" || res.header.Get("Content-Length") != "0" {
				t.Errorf("response %v %q, want a bare 404", res.header, res.body)
			}
		}},
		{name: "nil header", renderer: answer(srvr.ErrorResponse{Status: 404, Header: nil}), status: 404},
		{
			name: "Content-Length and Transfer-Encoding dropped",
			renderer: answer(srvr.ErrorResponse{Status: 429, Header: http.Header{
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
			renderer: answer(srvr.ErrorResponse{Status: 429, Header: http.Header{
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
			renderer: answer(srvr.ErrorResponse{Status: 401, Header: http.Header{
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

			base, out := renderedServer(t, tt.renderer, func(g *srvr.Group) {
				g.GET("/x", returning(errors.New("db down")))
				g.GET("/ok", status(http.StatusOK))
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
// renderer, net/http rows on how a valid answer is sent: a HEAD request
// gets the renderer's status and headers without a body, and srvr writes
// a copy of the renderer's header map, never the map itself.
func TestErrorRenderer_Answer(t *testing.T) {
	t.Parallel()

	t.Run("HEAD request", func(t *testing.T) {
		t.Parallel()

		r := srvr.ErrorRendererFunc(func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse {
			return srvr.ErrorResponse{Status: 429, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"code":"quota"}`)}
		})
		base, _ := renderedServer(t, r, func(g *srvr.Group) { g.GET("/x", returning(errQuota)) })

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
		base, _ := renderedServer(t, r, func(g *srvr.Group) {
			g.GET("/x", func(w http.ResponseWriter, _ *http.Request) error {
				w.Header().Set("X-Handler", "1")
				return errQuota
			})
		})

		for range 2 {
			if res := get(t, base+"/x"); res.status != 429 || res.header.Get("X-N") != "1" {
				t.Fatalf("response %d %v, want 429 with X-N", res.status, res.header)
			}
		}

		want := http.Header{"Content-Type": {"application/json"}, "Connection": {"close"}, "X-N": {"1"}}
		if !maps.EqualFunc(shared, want, slices.Equal) {
			t.Fatalf("renderer's header map changed to %v, want %v", shared, want)
		}
	})
}

// TestErrorRenderer_Hostile covers error-rendering / Scenario: Hostile
// errors and committed responses, net/http rows.
func TestErrorRenderer_Hostile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		handler srvr.HandlerFunc
		calls   int
		check   func(t *testing.T, res response, rec map[string]any, err error)
	}{
		{
			name: "cyclic chain", handler: returning(&cycleError{}), calls: 1,
			check: func(t *testing.T, res response, _ map[string]any, err error) {
				t.Helper()

				_ = errors.Is(err, errQuota) // terminates
				if res.status != 500 || res.header.Get("Content-Type") != "application/vnd.test+json" {
					t.Errorf("response %d %v, want R's answer", res.status, res.header)
				}
			},
		},
		{
			name: "Unwrap panics", handler: returning(unwrapPanicError{}), calls: 1,
			check: func(t *testing.T, res response, _ map[string]any, err error) {
				t.Helper()

				_ = errors.Is(err, errQuota) // does not panic
				if res.header.Get("Content-Type") != "application/vnd.test+json" {
					t.Errorf("response %v, want R's answer", res.header)
				}
			},
		},
		{
			name: "typed-nil error", handler: returning((*typedNilError)(nil)), calls: 1,
			check: func(t *testing.T, _ response, _ map[string]any, err error) {
				t.Helper()

				if _, ok := err.(*typedNilError); !ok { //nolint:errorlint // the very value.
					t.Errorf("renderer got %T, want *typedNilError", err)
				}
			},
		},
		{
			name: "late error", handler: late(http.StatusOK, func() error { return errors.New("late") }), calls: 0,
			check: func(t *testing.T, res response, rec map[string]any, _ error) {
				t.Helper()

				if res.status != 200 || rec["srvr.response.committed"] != true {
					t.Errorf("response %d record %v, want 200 and committed", res.status, rec)
				}
			},
		},
		{
			name: "late panic", handler: late(http.StatusOK, func() error { panic("late") }), calls: 0,
			check: func(t *testing.T, _ response, rec map[string]any, _ error) {
				t.Helper()

				if rec["error.kind"] != "panic" {
					t.Errorf("record %v, want error.kind panic", rec)
				}
			},
		},
		{
			name: "Cache-Control replaced",
			handler: func(w http.ResponseWriter, _ *http.Request) error {
				w.Header().Set("Cache-Control", "max-age=60")
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
			handler: func(w http.ResponseWriter, _ *http.Request) error {
				w.Header().Set("Vary", "Origin")
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
			handler: func(w http.ResponseWriter, _ *http.Request) error {
				w.Header().Set("Vary", "Origin, accept")
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

			base, out := renderedServer(t, r, func(g *srvr.Group) { g.GET("/x", tt.handler) })
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

		r := newRecordingRenderer()
		r.errs, r.reqs, r.ctxs = make(chan error, 50), make(chan srvr.ErrorRequest, 50), make(chan context.Context, 50)
		base, _ := renderedServer(t, r, func(g *srvr.Group) { g.GET("/x", returning(errQuota)) })

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

// cycleError unwraps to itself.
type cycleError struct{}

func (*cycleError) Error() string   { return "cyclic" }
func (e *cycleError) Unwrap() error { return e }

// unwrapPanicError panics when unwrapped.
type unwrapPanicError struct{}

func (unwrapPanicError) Error() string { return "unwrap panics" }
func (unwrapPanicError) Unwrap() error { panic("unwrap") }

// typedNilError is returned as a typed nil.
type typedNilError struct{}

func (*typedNilError) Error() string { return "typed nil" }

// TestErrorRenderer_Lifecycle covers error-rendering / Scenario: Renderer
// and lifecycle, net/http rows.
func TestErrorRenderer_Lifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		deadline time.Duration
		wantErr  error
		outcome  int // the in-flight status; -1: connection closed
	}{
		{"answer reaches the client before the drain ends", time.Second, nil, http.StatusTooManyRequests},
		{"renderer blocks past the drain deadline", 50 * time.Millisecond, context.DeadlineExceeded, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			entered, release := make(chan struct{}), make(chan struct{})
			r := srvr.ErrorRendererFunc(func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse {
				close(entered)
				<-release

				return srvr.ErrorResponse{Status: http.StatusTooManyRequests}
			})

			srv := newServer(t, srvr.WithErrorRenderer(r))
			mustNil(t, srv.Register(configFunc(func(g *srvr.Group) { g.GET("/x", returning(errQuota)) })))
			done := startBackground(t, srv)

			status := inFlight("http://" + srv.Addr() + "/x")
			receive(t, entered, "the renderer did not start")

			ctx, cancel := context.WithTimeout(t.Context(), tt.deadline)
			defer cancel()

			shutdown := make(chan error, 1)
			go func() { shutdown <- srv.Shutdown(ctx) }()

			if tt.wantErr == nil {
				close(release)
			}

			if err := receive(t, shutdown, "Shutdown did not return"); !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Shutdown = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr != nil {
				close(release) // the handler goroutine is net/http's; it ends here
			}

			if got := receive(t, status, "the in-flight request did not finish"); got != tt.outcome {
				t.Fatalf("in-flight = %d, want %d", got, tt.outcome)
			}

			if err := receive(t, done, "Start did not return"); err != nil {
				t.Fatalf("Start = %v", err)
			}
		})
	}
}

// TestWithErrorRenderer_Accepted covers error-rendering / Scenario:
// Rejected renderer configuration, accepted rows.
func TestWithErrorRenderer_Accepted(t *testing.T) {
	t.Parallel()

	first, second := &testRenderer{status: 418, code: "first"}, &testRenderer{status: 429, code: "second"}

	t.Run("twice: the later renderer is used", func(t *testing.T) {
		t.Parallel()

		base, _ := loggedServer(t, func(g *srvr.Group) { g.GET("/x", returning(errQuota)) }, srvr.WithErrorRenderer(first), srvr.WithErrorRenderer(second))
		if res := get(t, base+"/x"); res.status != 429 || res.body != `{"code":"second"}` {
			t.Fatalf("response %d %s, want the second renderer's", res.status, res.body)
		}
	})

	t.Run("with WithErrorHook: the hook runs for every answered error", func(t *testing.T) {
		t.Parallel()

		hooked := make(chan int, 4)
		base, _ := loggedServer(t, func(g *srvr.Group) { g.GET("/x", returning(errQuota)) }, srvr.WithErrorRenderer(second),
			srvr.WithErrorHook(func(_ context.Context, err error, sent srvr.ErrorReport) {
				if errors.Is(err, errQuota) {
					hooked <- sent.Status
				}
			}))
		get(t, base+"/x")
		get(t, base+"/missing")

		if s := receive(t, hooked, "hook"); s != 429 {
			t.Fatalf("hook status %d, want 429", s)
		}
	})

	t.Run("WithConfig then WithErrorRenderer", func(t *testing.T) {
		t.Parallel()

		cfg := srvr.Config{
			Addr: "127.0.0.1:0", ReadHeaderTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
			IdleTimeout: time.Second, MaxHeaderBytes: 1 << 16, MaxRequestBodyBytes: 1 << 10,
			ShutdownTimeout: time.Second, ShutdownHookTimeout: time.Second,
		}
		srv, err := srvr.New(srvr.WithConfig(cfg), srvr.WithErrorRenderer(second))
		mustNil(t, err)
		mustNil(t, srv.Register(configFunc(func(g *srvr.Group) { g.GET("/x", returning(errQuota)) })))

		if res := get(t, startServer(t, srv)+"/x"); res.status != 429 {
			t.Fatalf("status %d, want R's 429", res.status)
		}
	})
}

// TestProblemGroup_CustomRenderer covers triage-error-records / Scenario:
// Problem group, the custom-renderer rows.
func TestProblemGroup_CustomRenderer(t *testing.T) {
	t.Parallel()

	hostile := strings.Repeat("t", 10000) + "\n\x1b[31m"
	answer := func(typ, title string) srvr.ErrorRendererFunc {
		return func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse {
			return srvr.ErrorResponse{Status: 429, Type: typ, Title: title}
		}
	}

	tests := []struct {
		name     string
		renderer srvr.ErrorRendererFunc
		err      error
		check    func(t *testing.T, rec map[string]any, problem map[string]any)
	}{
		{"no type and title", answer("", ""), errQuota, func(t *testing.T, rec, p map[string]any) {
			t.Helper()

			if p["status"] != float64(429) || p["type"] != nil || p["title"] != nil || rec["srvr.renderer.failure"] != nil {
				t.Errorf("record %v, want status 429 only", rec)
			}
		}},
		{"type and title", answer("https://errors.example/quota", "Quota exceeded"), errQuota, func(t *testing.T, _, p map[string]any) {
			t.Helper()

			if p["status"] != float64(429) || p["type"] != "https://errors.example/quota" || p["title"] != "Quota exceeded" {
				t.Errorf("problem %v", p)
			}
		}},
		{"hostile title", answer("", hostile), errQuota, func(t *testing.T, _, p map[string]any) {
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

			rec := failedRecord(t, returning(tt.err), srvr.WithErrorRenderer(tt.renderer))
			problem, _ := rec["problem"].(map[string]any)
			tt.check(t, rec, problem)
		})
	}
}

// TestErrorRenderer_AbortHandler covers a handler panicking with
// http.ErrAbortHandler under a custom renderer: as with prblm, net/http
// aborts the connection, the renderer is not called and the record says
// aborted.
func TestErrorRenderer_AbortHandler(t *testing.T) {
	t.Parallel()

	r := newRecordingRenderer()
	base, out := renderedServer(t, r, func(g *srvr.Group) {
		g.GET("/x", func(http.ResponseWriter, *http.Request) error { panic(http.ErrAbortHandler) })
	})

	if res, err := clientFor(base).Get(base + "/x"); err == nil {
		_ = res.Body.Close()
		t.Fatalf("status %d, want the connection aborted", res.StatusCode)
	}

	if rec := out.waitRecord(t, "http.request.failed"); rec["srvr.response.aborted"] != true || rec["error.kind"] != "panic" {
		t.Fatalf("record %v, want an aborted panic", rec)
	}

	if len(r.errs) != 0 {
		t.Fatalf("renderer called for an aborted request")
	}
}

// nilPanicValue is panicked with for the spec's panic(nil) rows (a
// variable, so vet does not flag the deliberate nil panic).
var nilPanicValue any
