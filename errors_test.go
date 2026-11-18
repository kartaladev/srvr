package srvr_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// TestHandlerResult covers route-registration / Scenario: Handler result.
func TestHandlerResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		behaviour   string
		handler     srvr.HandlerFunc
		status      int
		contentType string
	}{
		{
			behaviour: "writes 200 JSON and returns nil",
			handler: func(w http.ResponseWriter, _ *http.Request) error {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))

				return nil
			},
			status:      http.StatusOK,
			contentType: "application/json",
		},
		{
			behaviour:   "returns nil without writing",
			handler:     func(http.ResponseWriter, *http.Request) error { return nil },
			status:      http.StatusOK,
			contentType: "",
		},
		{
			behaviour:   "returns prblm.ErrNotFound",
			handler:     func(http.ResponseWriter, *http.Request) error { return prblm.ErrNotFound },
			status:      http.StatusNotFound,
			contentType: "application/problem+json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.behaviour, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			if err := srv.Register(configFunc(func(g *srvr.Group) { g.GET("/items", tt.handler) })); err != nil {
				t.Fatal(err)
			}
			base := startServer(t, srv)

			res := get(t, base+"/items")
			if res.status != tt.status {
				t.Fatalf("status = %d, want %d", res.status, tt.status)
			}
			if got := res.header.Get("Content-Type"); got != tt.contentType {
				t.Fatalf("Content-Type = %q, want %q", got, tt.contentType)
			}
		})
	}
}

// TestErrorToProblem covers error-responses / Scenario: Error to problem.
func TestErrorToProblem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		accept      string
		status      int
		contentType string
		title       string
		hidden      string
	}{
		// mapped
		{"not found", prblm.ErrNotFound, "application/json", 404, "application/problem+json", "Not Found", ""},
		{"wrapped conflict", fmt.Errorf("item 7: %w", prblm.ErrConflict), "*/*", 409, "application/problem+json", "Conflict", "item 7"},
		{"status hint", prblm.WithStatus(errors.New("gone"), 410), "", 410, "application/problem+json", "Gone", "gone"},
		{"deadline", context.DeadlineExceeded, "application/json", 504, "application/problem+json", "Gateway Timeout", "deadline"},
		// unmapped and negotiated
		{"unmapped", errors.New("pq: password authentication failed for user app"), "application/json", 500, "application/problem+json", "Internal Server Error", "pq:"},
		{"xml", errors.New("x"), "application/xml", 500, "application/problem+xml", "Internal Server Error", "x</"},
		{"html", errors.New("x"), "text/html", 500, "application/problem+json", "Internal Server Error", `x"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			err := srv.Register(configFunc(func(g *srvr.Group) {
				g.GET("/items/{id}", func(http.ResponseWriter, *http.Request) error { return tt.err })
			}))
			if err != nil {
				t.Fatal(err)
			}
			base := startServer(t, srv)

			hdr := map[string]string{}
			if tt.accept != "" {
				hdr["Accept"] = tt.accept
			}
			res := do(t, http.MethodGet, base+"/items/7", nil, hdr)

			if res.status != tt.status {
				t.Fatalf("status = %d, want %d", res.status, tt.status)
			}
			if got := res.header.Get("Content-Type"); got != tt.contentType {
				t.Fatalf("Content-Type = %q, want %q", got, tt.contentType)
			}
			if res.problem == nil || res.problem.Title != tt.title || res.problem.Status != tt.status {
				t.Fatalf("problem = %+v, want title %q and status %d", res.problem, tt.title, tt.status)
			}
			if tt.hidden != "" && strings.Contains(res.body, tt.hidden) {
				t.Fatalf("body %q leaks %q", res.body, tt.hidden)
			}
		})
	}
}

var errQuota = errors.New("quota exceeded")

// TestErrorExtension covers error-responses / Scenario: Consumer mapper and hook.
func TestErrorExtension(t *testing.T) {
	t.Parallel()

	type seen struct {
		err    error
		status int
	}

	tests := []struct {
		name   string
		opts   func(got *atomic.Pointer[seen]) []srvr.Option
		err    error
		status int
		hook   bool // the hook must have seen err with the response status
	}{
		{
			name: "consumer mapper",
			opts: func(*atomic.Pointer[seen]) []srvr.Option {
				return []srvr.Option{srvr.WithProblemOptions(prblm.WithMappers(prblm.Status(errQuota, http.StatusTooManyRequests)))}
			},
			err: errQuota, status: http.StatusTooManyRequests,
		},
		{
			name: "consumer hook",
			opts: func(got *atomic.Pointer[seen]) []srvr.Option {
				return []srvr.Option{srvr.WithErrorHook(func(_ context.Context, err error, p *prblm.Problem) {
					got.Store(&seen{err: err, status: p.Status})
				})}
			},
			err: prblm.ErrNotFound, status: http.StatusNotFound, hook: true,
		},
		{
			name: "panicking consumer hook",
			opts: func(*atomic.Pointer[seen]) []srvr.Option {
				return []srvr.Option{srvr.WithErrorHook(func(context.Context, error, *prblm.Problem) { panic("hook") })}
			},
			err: prblm.ErrNotFound, status: http.StatusNotFound,
		},
		{
			name: "consumer prblm.OnError does not replace srvr's hook",
			opts: func(got *atomic.Pointer[seen]) []srvr.Option {
				return []srvr.Option{
					srvr.WithProblemOptions(prblm.OnError(func(context.Context, error, *prblm.Problem) { panic("replaced") })),
					srvr.WithErrorHook(func(_ context.Context, err error, p *prblm.Problem) {
						got.Store(&seen{err: err, status: p.Status})
					}),
				}
			},
			err: prblm.ErrNotFound, status: http.StatusNotFound, hook: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got atomic.Pointer[seen]
			srv := newServer(t, tt.opts(&got)...)
			mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
				g.GET("/x", func(http.ResponseWriter, *http.Request) error { return tt.err })
			})))
			base := startServer(t, srv)

			if res := get(t, base+"/x"); res.status != tt.status {
				t.Fatalf("status = %d, want %d", res.status, tt.status)
			}
			if tt.hook {
				s := got.Load()
				if s == nil || !errors.Is(s.err, tt.err) || s.status != tt.status {
					t.Fatalf("hook saw %+v, want %v with status %d", s, tt.err, tt.status)
				}
			}
		})
	}
}

// TestErrorExtension_Config: invalid error options fail construction.
func TestErrorExtension_Config(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, message string
		opt           srvr.Option
	}{
		{"nil hook", "ErrorHook", srvr.WithErrorHook(nil)},
		{"invalid prblm option", "ProblemOptions", srvr.WithProblemOptions(prblm.WithNamespace("not a uri"))},
		{"prblm option panicking with a value whose Error panics", "ProblemOptions", srvr.WithProblemOptions(func(*prblm.Handler) { panic(selfPanicError{}) })},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, err := newRecovered(tt.opt)
			if srv != nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("New = %v, %v; want ErrConfig naming %s", srv, err, tt.message)
			}
		})
	}
}

// TestPanicRecovery covers error-responses / Scenario: Recovered panic.
func TestPanicRecovery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   any
		aborted bool
		hidden  string
	}{
		{name: "string", value: "boom", hidden: "boom"},
		{name: "error", value: errors.New("db down"), hidden: "db down"},
		{name: "nil", value: nil},
		{name: "http.ErrAbortHandler", value: http.ErrAbortHandler, aborted: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
				g.GET("/panic", func(http.ResponseWriter, *http.Request) error { panic(tt.value) })
				g.GET("/ok", status(http.StatusOK))
			})))
			base := startServer(t, srv)

			if tt.aborted {
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/panic", nil)
				if res, err := clientFor(req.URL.String()).Do(req); err == nil {
					res.Body.Close()
					t.Fatalf("got a response %d, want an aborted connection", res.StatusCode)
				}
			} else {
				got := do(t, http.MethodGet, base+"/panic", nil, nil)
				if got.status != http.StatusInternalServerError || got.problem == nil || got.problem.Title != "Internal Server Error" {
					t.Fatalf("got %d %q, want a 500 problem", got.status, got.body)
				}
				if tt.hidden != "" && strings.Contains(got.body, tt.hidden) {
					t.Fatalf("body %q leaks %q", got.body, tt.hidden)
				}
				if strings.Contains(got.body, "goroutine") || strings.Contains(got.body, ".go:") {
					t.Fatalf("body %q leaks a stack trace", got.body)
				}
			}

			if got := get(t, base+"/ok").status; got != http.StatusOK {
				t.Fatalf("next request = %d, want 200", got)
			}
		})
	}
}

// TestLateError covers error-responses / Scenario: Late error, the client
// response and the access record.
func TestLateError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		written int
		fail    func() error
		level   string
	}{
		{"200 then error", http.StatusOK, func() error { return errors.New("late") }, "ERROR"},
		{"200 then panic", http.StatusOK, func() error { panic("late") }, "ERROR"},
		{"201 then conflict", http.StatusCreated, func() error { return prblm.ErrConflict }, "WARN"},
		{"500 then conflict", http.StatusInternalServerError, func() error { return prblm.ErrConflict }, "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) {
				g.GET("/late", late(tt.written, tt.fail))
			})

			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/late", nil)
			res, err := clientFor(req.URL.String()).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body := make([]byte, 7)
			n, _ := res.Body.Read(body)
			if res.StatusCode != tt.written || !strings.HasPrefix(string(body[:n]), "partial") {
				t.Fatalf("got %d %q, want %d starting with partial", res.StatusCode, body[:n], tt.written)
			}

			rec := out.waitRecord(t, "http.request.failed")
			if _, hasProblem := rec["problem"]; hasProblem || rec["srvr.response.committed"] != true || rec["level"] != tt.level {
				t.Fatalf("record = %v, want level %s, srvr.response.committed and no problem", rec, tt.level)
			}
		})
	}
}

// TestUnmatched covers error-responses / Scenario: No route or wrong method.
func TestUnmatched(t *testing.T) {
	t.Parallel()

	srv := newServer(t)
	mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
		g.GET("/users", status(http.StatusOK))
		g.POST("/users", status(http.StatusOK))
	})))
	base := startServer(t, srv)

	tests := []struct {
		method, path string
		status       int
		allow        string
	}{
		{http.MethodGet, "/nope", 404, ""},
		{http.MethodDelete, "/users", 405, "GET, HEAD, POST"},
		{http.MethodGet, "/users/", 404, ""},
		{http.MethodPatch, "/nope", 404, ""},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			res := do(t, tt.method, base+tt.path, nil, nil)
			if res.status != tt.status || res.header.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("got %d %q, want %d application/problem+json", res.status, res.header.Get("Content-Type"), tt.status)
			}
			if res.problem == nil || res.problem.Status != tt.status {
				t.Fatalf("problem = %+v", res.problem)
			}
			if got := res.header.Get("Allow"); got != tt.allow {
				t.Fatalf("Allow = %q, want %q", got, tt.allow)
			}
		})
	}
}

// TestUnmatched_Redirect: ServeMux redirects for non-canonical paths pass
// through unchanged; only 404 and 405 become problems.
func TestUnmatched_Redirect(t *testing.T) {
	t.Parallel()

	srv := newServer(t)
	mustNil(t, srv.Register(okRoute("/dir/")))
	base := startServer(t, srv)

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for p, want := range map[string]string{"/dir": "/dir/", "/a/../dir/": "/dir/", "//nope": "/nope"} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+p, nil)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode < 300 || res.StatusCode > 399 || !strings.HasSuffix(res.Header.Get("Location"), want) {
			t.Fatalf("GET %s = %d Location %q, want a redirect to %s", p, res.StatusCode, res.Header.Get("Location"), want)
		}
	}
}

// TestUpstream_PrblmOnErrorComposes reproduces the prblm limitation srvr
// works around with WithErrorHook: prblm.OnError keeps a single hook, so of
// two OnError options only the last one runs. It is the RED step of the
// upstream fix (openspec/changes/core-server/notes.md, "Ask prblm for a
// composable OnError ...") and runs only with SRVR_UPSTREAM_TESTS=1, since
// it fails against prblm v0.1.0 by design.
func TestUpstream_PrblmOnErrorComposes(t *testing.T) {
	if os.Getenv("SRVR_UPSTREAM_TESTS") != "1" {
		t.Skip("upstream reproduction; set SRVR_UPSTREAM_TESTS=1 to run")
	}

	var first, second bool
	h := prblm.NewHandler(
		prblm.OnError(func(context.Context, error, *prblm.Problem) { first = true }),
		prblm.OnError(func(context.Context, error, *prblm.Problem) { second = true }),
	)

	h.Write(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), errors.New("boom"))

	if !first || !second {
		t.Fatalf("OnError hooks called: first=%v second=%v, want both", first, second)
	}
}

// TestConfigError covers engine-support / Scenario: Configuration errors
// name their field.
func TestConfigError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		opts  []srvr.Option
		found bool
		field string
		texts []string // also named in err.Error()
	}{
		{"invalid level", []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "loud"})}, true, "Level", nil},
		{"nil output", []srvr.Option{srvr.WithLogOutput(nil)}, true, "Output", nil},
		{"nil route check", []srvr.Option{srvr.WithRouteCheck(nil)}, true, "RouteCheck", nil},
		{"two invalid fields", []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "loud", Format: "xml"})}, true, "Level", []string{"Level", "Format"}},
		{"none", nil, false, "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := srvr.New(tt.opts...)

			var ce *srvr.ConfigError
			if found := errors.As(err, &ce); found != tt.found {
				t.Fatalf("errors.As(%v) = %v, want %v", err, found, tt.found)
			}
			if tt.found && ce.Field != tt.field {
				t.Fatalf("Field = %q, want %q (error %v)", ce.Field, tt.field, err)
			}
			for _, text := range tt.texts {
				if !strings.Contains(err.Error(), text) {
					t.Fatalf("error %q does not name %s", err, text)
				}
			}
		})
	}
}
