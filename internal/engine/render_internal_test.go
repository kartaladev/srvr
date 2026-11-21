package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/internal/errchain"
)

// goldenTraceparent is the traceparent the golden responses of
// internal/paritytest were recorded with.
const goldenTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// renderGolden is one net/http golden response of internal/paritytest's
// TestParity_DefaultRendering.
type renderGolden struct {
	Status int         `json:"status"`
	Header http.Header `json:"header"`
	Body   string      `json:"body"`
}

// netHTTPGoldens reads the net/http golden responses, recorded at v0.3.0.
func netHTTPGoldens(t *testing.T) map[string]renderGolden {
	t.Helper()

	b, err := os.ReadFile(filepath.FromSlash("../paritytest/testdata/default_rendering.json"))
	if err != nil {
		t.Fatal(err)
	}

	var all map[string]map[string]renderGolden
	if err := json.Unmarshal(b, &all); err != nil {
		t.Fatal(err)
	}

	return all["net/http"]
}

// renderCycleError unwraps to itself.
type renderCycleError struct{}

func (*renderCycleError) Error() string   { return "cyclic" }
func (e *renderCycleError) Unwrap() error { return e }

// TestDefaultRenderer covers error-rendering / Scenario: Default rendering
// is unchanged, for the default renderer value (design D7): for every
// row, given the error srvr hands a renderer, it answers the net/http
// golden response, headers included, with the
// problem's type and title for logs and hooks.
func TestDefaultRenderer(t *testing.T) {
	t.Parallel()

	errQuota := errors.New("quota exceeded")
	goldens := netHTTPGoldens(t)

	tests := []struct {
		row          string
		method, path string
		err          error
		opts         []Option
		clientClosed bool
		title        string
	}{
		{"handler returns prblm.ErrConflict", "GET", "/x", prblm.ErrConflict, nil, false, "Conflict"},
		{"handler returns boom", "GET", "/x", errors.New("boom"), nil, false, "Internal Server Error"},
		{"handler panics", "GET", "/x", &prblm.PanicError{Value: "boom"}, nil, false, "Internal Server Error"},
		{"no route", "GET", "/missing", errchain.ErrNotFound, nil, false, "Not Found"},
		{"method not allowed", "GET", "/x", MethodNotAllowed("POST"), nil, false, "Method Not Allowed"},
		{"body over limit", "POST", "/x", errchain.RequestTooLarge(&http.MaxBytesError{Limit: 10}), nil, false, "Request Entity Too Large"},
		{"static miss", "GET", "/assets/missing.js", errchain.ErrNotFound, nil, false, "Not Found"},
		{"client closed", "GET", "/x", errchain.ClientClosed(fmt.Errorf("q: %w", context.Canceled)), nil, true, "Client Closed Request"},
		{"cyclic chain", "GET", "/x", GuardChain(&renderCycleError{}), nil, false, "Internal Server Error"},
		{
			"consumer mapper", "GET", "/x", errQuota,
			[]Option{func(s *Settings) {
				s.ProblemOptions = []prblm.Option{prblm.WithMappers(prblm.Status(errQuota, http.StatusTooManyRequests))}
			}},
			false, "Too Many Requests",
		},
	}

	for _, tt := range tests {
		t.Run(tt.row, func(t *testing.T) {
			t.Parallel()

			want, ok := goldens[tt.row]
			if !ok {
				t.Fatalf("no golden response for %q", tt.row)
			}

			c, err := ResolveConfig(tt.opts)
			if err != nil {
				t.Fatal(err)
			}

			ctx := c.BeginRequest(t.Context(), []string{goldenTraceparent})
			if tt.clientClosed {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			req := ErrorRequest{Method: tt.method, Path: tt.path, Header: httpHeader{}}
			got := DefaultRenderer.Render(ctx, req, tt.err)

			if got.Status != want.Status || string(got.Body) != want.Body || !maps.EqualFunc(got.Header, want.Header, slices.Equal) {
				t.Fatalf("got %d %v %s\nwant %d %v %s", got.Status, got.Header, got.Body, want.Status, want.Header, want.Body)
			}

			if got.Type != "about:blank" || got.Title != tt.title {
				t.Fatalf("type %q title %q, want about:blank %q", got.Type, got.Title, tt.title)
			}
		})
	}
}

// TestDefaultRenderer_Edges covers the default renderer outside the rows:
// a nil error answers nothing, a context without a request uses srvr's
// built-in problems, and a nil header reader is an empty one.
func TestDefaultRenderer_Edges(t *testing.T) {
	t.Parallel()

	t.Run("nil error", func(t *testing.T) {
		t.Parallel()

		if got := DefaultRenderer.Render(t.Context(), ErrorRequest{}, nil); got.Status != 0 || got.Body != nil {
			t.Fatalf("got %+v, want the zero response", got)
		}
	})

	t.Run("no request in context, nil header", func(t *testing.T) {
		t.Parallel()

		got := DefaultRenderer.Render(t.Context(), ErrorRequest{Method: "GET", Path: "/y"}, MethodNotAllowed("GET, HEAD"))
		if got.Status != http.StatusMethodNotAllowed || got.Header.Get("Allow") != "GET, HEAD" || got.Title != "Method Not Allowed" {
			t.Fatalf("got %d %v %q", got.Status, got.Header, got.Title)
		}

		if want := `{"title":"Method Not Allowed","status":405,"instance":"/y"}`; string(got.Body) != want {
			t.Fatalf("body %s, want %s", got.Body, want)
		}
	})

	t.Run("Accept selects XML", func(t *testing.T) {
		t.Parallel()

		got := DefaultRenderer.Render(t.Context(), ErrorRequest{Header: httpHeader{"Accept": {"application/xml"}}}, errchain.ErrNotFound)
		if ct := got.Header.Get("Content-Type"); ct != "application/problem+xml" {
			t.Fatalf("Content-Type %q, want application/problem+xml", ct)
		}
	})
}

// fakeTarget records what Render writes.
type fakeTarget struct {
	committed bool
	writes    []ErrorResponse
}

func (f *fakeTarget) Committed() bool            { return f.committed }
func (f *fakeTarget) WriteError(r ErrorResponse) { f.writes = append(f.writes, r) }

// typedNilError is an error type returned as a typed nil.
type typedNilError struct{}

func (*typedNilError) Error() string { return "typed nil" }

// renderCall is one call of a test renderer.
type renderCall struct {
	ctx    context.Context
	ctxErr error // ctx.Err() during the call
	req    ErrorRequest
	err    error
}

// TestRender covers error-rendering / Scenario: Faulty renderer and
// Scenario: Hostile errors and committed responses at the decision level,
// without HTTP: what Render hands the renderer, what it writes, what it
// records and what the error hook receives.
func TestRender(t *testing.T) {
	t.Parallel()

	const mib = 1 << 20

	testCT := http.Header{"Content-Type": {"application/vnd.test+json"}}
	ok := func(context.Context, ErrorRequest, error) ErrorResponse {
		return ErrorResponse{Status: 429, Header: testCT, Body: []byte(`{"code":"quota"}`)}
	}
	answer := func(res ErrorResponse) ErrorRendererFunc {
		return func(context.Context, ErrorRequest, error) ErrorResponse { return res }
	}
	cyclic := &renderCycleError{}

	tests := []struct {
		name      string
		renderer  ErrorRendererFunc
		err       error
		committed bool
		cancelled bool // the request context is cancelled (client gone)
		// want: renderer calls, written status (0: nothing written),
		// recorded status and renderer failure, hook problem status.
		calls      int
		status     int
		failure    string
		check      func(t *testing.T, call renderCall, res ErrorResponse)
		hookStatus int
	}{
		{name: "valid answer", renderer: ok, err: errors.New("db down"), calls: 1, status: 429, hookStatus: 429},
		{name: "panics", renderer: func(context.Context, ErrorRequest, error) ErrorResponse { panic("render boom") }, err: errors.New("db down"), calls: 1, status: 500, failure: "panic", hookStatus: 500},
		{name: "panics with nil", renderer: func(context.Context, ErrorRequest, error) ErrorResponse { panic(nilPanicValue) }, err: errors.New("db down"), calls: 1, status: 500, failure: "panic", hookStatus: 500},
		{name: "status 0", renderer: answer(ErrorResponse{}), err: errors.New("db down"), calls: 1, status: 500, failure: "invalid_status", hookStatus: 500},
		{name: "status 200", renderer: answer(ErrorResponse{Status: 200}), err: errors.New("x"), calls: 1, status: 500, failure: "invalid_status", hookStatus: 500},
		{name: "status 399", renderer: answer(ErrorResponse{Status: 399}), err: errors.New("x"), calls: 1, status: 500, failure: "invalid_status", hookStatus: 500},
		{name: "status 600", renderer: answer(ErrorResponse{Status: 600}), err: errors.New("x"), calls: 1, status: 500, failure: "invalid_status", hookStatus: 500},
		{name: "status -1", renderer: answer(ErrorResponse{Status: -1}), err: errors.New("x"), calls: 1, status: 500, failure: "invalid_status", hookStatus: 500},
		{name: "body of exactly 1 MiB", renderer: answer(ErrorResponse{Status: 400, Header: testCT, Body: make([]byte, mib)}), err: errors.New("x"), calls: 1, status: 400, hookStatus: 400},
		{name: "body of 1 MiB + 1", renderer: answer(ErrorResponse{Status: 400, Header: testCT, Body: make([]byte, mib+1)}), err: errors.New("x"), calls: 1, status: 500, failure: "oversized_body", hookStatus: 500},
		{name: "body without Content-Type", renderer: answer(ErrorResponse{Status: 400, Body: make([]byte, 10)}), err: errors.New("x"), calls: 1, status: 500, failure: "missing_content_type", hookStatus: 500},
		{name: "lower-case content-type", renderer: answer(ErrorResponse{Status: 400, Header: http.Header{"content-type": {"text/plain"}}, Body: []byte("x")}), err: errors.New("x"), calls: 1, status: 400, hookStatus: 400},
		{name: "bare 404", renderer: answer(ErrorResponse{Status: 404}), err: errors.New("x"), calls: 1, status: 404, hookStatus: 404},
		{
			name: "returns after the deadline",
			renderer: func(ctx context.Context, _ ErrorRequest, _ error) ErrorResponse {
				<-ctx.Done()
				return ErrorResponse{Status: 429}
			},
			err: errors.New("db down"), calls: 1, status: 500, failure: "deadline", hookStatus: 500,
		},
		{
			name: "client gone, returns before the deadline", renderer: ok, err: errors.New("db down"), cancelled: true, calls: 1, status: 429, hookStatus: 429,
			check: func(t *testing.T, call renderCall, _ ErrorResponse) {
				t.Helper()

				if call.ctxErr != nil {
					t.Errorf("renderer context ended: %v", call.ctxErr)
				}
			},
		},
		{name: "nil header", renderer: answer(ErrorResponse{Status: 404, Header: nil}), err: errors.New("x"), calls: 1, status: 404, hookStatus: 404},
		{
			name: "Content-Length and hop-by-hop headers dropped",
			renderer: answer(ErrorResponse{Status: 429, Header: http.Header{
				"Content-Type": {"application/json"}, "Content-Length": {"999"}, "Transfer-Encoding": {"chunked"},
				"Connection": {"close"}, "X-Kept": {"1"},
			}, Body: []byte("{}")}),
			err: errors.New("x"), calls: 1, status: 429, hookStatus: 429,
			check: func(t *testing.T, _ renderCall, res ErrorResponse) {
				t.Helper()

				for _, k := range []string{"Content-Length", "Transfer-Encoding", "Connection"} {
					if _, ok := res.Header[k]; ok {
						t.Errorf("header %s kept: %v", k, res.Header)
					}
				}
				if res.Header.Get("X-Kept") != "1" || res.Header.Get("Content-Type") != "application/json" {
					t.Errorf("headers %v, want X-Kept and Content-Type", res.Header)
				}
			},
		},
		{name: "committed", renderer: ok, err: errors.New("late"), committed: true, calls: 0, status: 0},
		{
			name: "cyclic chain", renderer: ok, err: cyclic, calls: 1, status: 429, hookStatus: 429,
			check: func(t *testing.T, call renderCall, _ ErrorResponse) {
				t.Helper()

				if errchain.HasCycle(call.err) {
					t.Errorf("renderer received a cyclic chain")
				}
			},
		},
		{
			name: "Unwrap panics", renderer: ok, err: panicUnwrapError{}, calls: 1, status: 429, hookStatus: 429,
			check: func(t *testing.T, call renderCall, _ ErrorResponse) {
				t.Helper()

				_ = errors.Is(call.err, context.Canceled) // must not panic
			},
		},
		{
			name: "typed nil error", renderer: ok, err: (*typedNilError)(nil), calls: 1, status: 429, hookStatus: 429,
			check: func(t *testing.T, call renderCall, _ ErrorResponse) {
				t.Helper()

				if _, ok := call.err.(*typedNilError); !ok { //nolint:errorlint // the very value.
					t.Errorf("renderer received %T, want *typedNilError", call.err)
				}
			},
		},
		{
			name: "client closed", renderer: ok, err: fmt.Errorf("q: %w", context.Canceled), cancelled: true, calls: 1, status: 429, hookStatus: 429,
			check: func(t *testing.T, call renderCall, _ ErrorResponse) {
				t.Helper()

				if !errors.Is(call.err, errchain.ErrClientClosed) || !errors.Is(call.err, context.Canceled) {
					t.Errorf("renderer received %v, want ErrClientClosed and context.Canceled", call.err)
				}
			},
		},
		{
			name: "server-side cancellation is not client closed", renderer: ok, err: fmt.Errorf("q: %w", context.Canceled), calls: 1, status: 429, hookStatus: 429,
			check: func(t *testing.T, call renderCall, _ ErrorResponse) {
				t.Helper()

				if errors.Is(call.err, errchain.ErrClientClosed) {
					t.Errorf("renderer received ErrClientClosed for a live request")
				}
			},
		},
		{
			name: "body over the limit", renderer: ok, err: fmt.Errorf("read: %w", &http.MaxBytesError{Limit: 10}), calls: 1, status: 429, hookStatus: 429,
			check: func(t *testing.T, call renderCall, _ ErrorResponse) {
				t.Helper()

				if !errors.Is(call.err, errchain.ErrRequestTooLarge) || !errors.As(call.err, new(*http.MaxBytesError)) {
					t.Errorf("renderer received %v, want ErrRequestTooLarge and *http.MaxBytesError", call.err)
				}
			},
		},
		{
			name: "already ErrRequestTooLarge is not wrapped again", renderer: ok, err: errchain.RequestTooLarge(&http.MaxBytesError{Limit: 10}), calls: 1, status: 429, hookStatus: 429,
			check: func(t *testing.T, call renderCall, _ ErrorResponse) {
				t.Helper()

				if call.err.Error() != errchain.RequestTooLarge(&http.MaxBytesError{Limit: 10}).Error() {
					t.Errorf("renderer received %q", call.err)
				}
			},
		},
		{
			name: "request fields and context", renderer: ok, err: errors.New("x"), calls: 1, status: 429, hookStatus: 429,
			check: func(t *testing.T, call renderCall, _ ErrorResponse) {
				t.Helper()

				if call.req.Method != http.MethodGet || call.req.Path != "/items/7" || call.req.Header.Get("Accept") != "application/json" {
					t.Errorf("request %+v", call.req)
				}
				if tr, ok := TraceFromContext(call.ctx); !ok || tr.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
					t.Errorf("trace %v %v", tr, ok)
				}
				if _, ok := call.ctx.Deadline(); !ok {
					t.Errorf("renderer context has no deadline")
				}
			},
		},
		{
			name: "delegating to DefaultRenderer records and hooks once", err: prblm.ErrConflict, calls: 1, status: 409, hookStatus: 409,
			renderer: func(ctx context.Context, req ErrorRequest, err error) ErrorResponse {
				return DefaultRenderer.Render(ctx, req, err)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				calls []renderCall
				hooks []ErrorReport
			)

			renderer := ErrorRendererFunc(func(ctx context.Context, req ErrorRequest, err error) ErrorResponse {
				calls = append(calls, renderCall{ctx, ctx.Err(), req, err})
				return tt.renderer(ctx, req, err)
			})

			c, err := ResolveConfig([]Option{func(s *Settings) {
				s.ErrorRenderer = renderer
				s.ErrorHook = func(_ context.Context, _ error, sent ErrorReport) { hooks = append(hooks, sent) }
			}})
			if err != nil {
				t.Fatal(err)
			}

			c.renderDeadline = 20 * time.Millisecond

			ctx := c.BeginRequest(t.Context(), []string{goldenTraceparent})
			if tt.cancelled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			target := &fakeTarget{committed: tt.committed}
			req := ErrorRequest{Method: "GET", Path: "/items/7", Header: httpHeader{"Accept": {"application/json"}}}
			c.Render(ctx, target, req, tt.err)

			if len(calls) != tt.calls {
				t.Fatalf("renderer called %d times, want %d", len(calls), tt.calls)
			}

			state, _ := StateFrom(ctx)
			fail := state.failure

			if tt.status == 0 {
				if len(target.writes) != 0 || fail == nil || !fail.committed {
					t.Fatalf("writes %v failure %+v, want nothing written and a committed failure", target.writes, fail)
				}
				if len(hooks) != 1 || hooks[0] != (ErrorReport{}) {
					t.Fatalf("hook got %v, want one call with the zero report", hooks)
				}

				return
			}

			if len(target.writes) != 1 || target.writes[0].Status != tt.status {
				t.Fatalf("writes %+v, want one with status %d", target.writes, tt.status)
			}

			res := target.writes[0]
			if fail == nil || fail.sent.Status != tt.status || fail.rendererFailure != tt.failure {
				t.Fatalf("failure %+v, want status %d renderer failure %q", fail, tt.status, tt.failure)
			}

			if len(hooks) != 1 || hooks[0].Status != tt.hookStatus {
				t.Fatalf("hook got %v, want one call with status %d", hooks, tt.hookStatus)
			}

			if tt.failure != "" {
				if res.Header.Get("Content-Type") != "application/problem+json" || bytes.Contains(res.Body, []byte("db down")) ||
					!bytes.Contains(res.Body, []byte(`"title":"Internal Server Error"`)) {
					t.Fatalf("safe 500 %v %s", res.Header, res.Body)
				}
				if fail.sent.Type != "about:blank" || fail.sent.Title != "Internal Server Error" {
					t.Fatalf("recorded answer %+v", fail.sent)
				}
			}

			if tt.check != nil {
				tt.check(t, calls[0], res)
			}
		})
	}
}

// TestRender_HookPanics covers the error hook panicking on the custom
// path: the panic is contained and the answer is written.
func TestRender_HookPanics(t *testing.T) {
	t.Parallel()

	c, err := ResolveConfig([]Option{func(s *Settings) {
		s.ErrorRenderer = ErrorRendererFunc(func(context.Context, ErrorRequest, error) ErrorResponse { return ErrorResponse{Status: 404} })
		s.ErrorHook = func(context.Context, error, ErrorReport) { panic("hook") }
	}})
	if err != nil {
		t.Fatal(err)
	}

	target := &fakeTarget{}
	c.Render(c.BeginRequest(t.Context(), nil), target, ErrorRequest{}, errors.New("x"))

	if len(target.writes) != 1 || target.writes[0].Status != 404 {
		t.Fatalf("writes %+v, want a 404", target.writes)
	}
}

// nilPanicValue is panicked with for the spec's panic(nil) row.
var nilPanicValue any

// TestRender_DeadlineEndToEnd covers error-rendering / Scenario: Faulty
// renderer, net/http rows "returns after the deadline" and "returns
// before the deadline while the client already disconnected", end to end
// through the net/http stage chain (trace, access log, renderer) with the
// render deadline shortened through the unexported seam (owner decision
// 2026-11-21): the bytes sent and the access record.
func TestRender_DeadlineEndToEnd(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		disconnect  bool
		wantStatus  int
		wantFailure string // "" when no srvr.renderer.failure key is logged
	}{
		{name: "returns after the deadline", wantStatus: 500, wantFailure: "deadline"},
		{name: "returns before the deadline while the client already disconnected", disconnect: true, wantStatus: 429},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			renderer := ErrorRendererFunc(func(ctx context.Context, _ ErrorRequest, _ error) ErrorResponse {
				if !tt.disconnect {
					<-ctx.Done()
				}

				return ErrorResponse{Status: 429, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"code":"quota"}`)}
			})

			var logs syncBuffer

			c, err := ResolveConfig([]Option{func(s *Settings) {
				s.ErrorRenderer = renderer
				s.Log.Output = &logs
			}})
			if err != nil {
				t.Fatal(err)
			}

			c.renderDeadline = 20 * time.Millisecond

			h := c.StartTrace(c.LogAccess(c.HTTPErrors().Wrap(func(http.ResponseWriter, *http.Request) error {
				return errors.New("db down")
			})))

			ctx := t.Context()
			if tt.disconnect {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/items/7", nil)
			r.Header.Set("Traceparent", goldenTraceparent)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			if w.Code != tt.wantStatus {
				t.Fatalf("status %d, want %d (body %s)", w.Code, tt.wantStatus, w.Body)
			}

			if tt.wantFailure != "" {
				const safe = `{"title":"Internal Server Error","status":500,"instance":"/items/7","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}`
				if w.Body.String() != safe || w.Header().Get("Content-Type") != "application/problem+json" {
					t.Fatalf("response %v %s, want the safe 500 %s", w.Header(), w.Body, safe)
				}
			} else if w.Body.String() != `{"code":"quota"}` {
				t.Fatalf("body %s, want the renderer's", w.Body)
			}

			failure := ""
			found := false
			for _, rec := range logs.records(t) {
				if st, ok := rec["http.response.status_code"].(float64); ok && int(st) == tt.wantStatus {
					found = true
					failure, _ = rec["srvr.renderer.failure"].(string)
				}
			}
			if !found || failure != tt.wantFailure {
				t.Fatalf("record found=%v srvr.renderer.failure=%q, want %q", found, failure, tt.wantFailure)
			}
		})
	}
}

// TestErrorHook_NoAllocation proves design D8's claim: passing the
// ErrorReport value to a hook adds no allocation on either path, prblm's
// (onError) and the renderer's (Render). Each case counts the allocations
// with and without a hook, and requires them equal.
// It does not run in parallel: testing.AllocsPerRun counts every
// goroutine's allocations.
//
//nolint:paralleltest // AllocsPerRun counts the allocations of parallel tests too.
func TestErrorHook_NoAllocation(t *testing.T) {
	hook := func(context.Context, error, ErrorReport) {}
	problem := &prblm.Problem{Status: 404, Type: "about:blank", Title: "Not Found"}
	renderer := ErrorRendererFunc(func(context.Context, ErrorRequest, error) ErrorResponse { return ErrorResponse{Status: 404} })

	tests := []struct {
		name string
		run  func(c *Core, ctx context.Context)
	}{
		{"prblm hook", func(c *Core, ctx context.Context) { c.onError(ctx, prblm.ErrNotFound, problem) }},
		{"renderer", func(c *Core, ctx context.Context) { c.Render(ctx, &fakeTarget{}, ErrorRequest{}, prblm.ErrNotFound) }},
	}

	allocs := func(t *testing.T, run func(*Core, context.Context), withHook bool) float64 {
		t.Helper()

		c, err := ResolveConfig([]Option{func(s *Settings) {
			s.ErrorRenderer = renderer
			if withHook {
				s.ErrorHook = hook
			}
		}})
		if err != nil {
			t.Fatal(err)
		}
		ctx := c.BeginRequest(t.Context(), nil)

		return testing.AllocsPerRun(100, func() { run(c, ctx) })
	}

	for _, tt := range tests { //nolint:paralleltest // see the function comment
		t.Run(tt.name, func(t *testing.T) {
			without, with := allocs(t, tt.run, false), allocs(t, tt.run, true)
			if with != without {
				t.Fatalf("allocs/op with a hook %v, without %v: the hook call allocates", with, without)
			}
		})
	}

	// The comparison above misses an allocation made whether or not a
	// hook is set (an escaping report); the hook call itself must make
	// none.
	t.Run("hook call", func(t *testing.T) {
		c, err := ResolveConfig([]Option{func(s *Settings) { s.ErrorHook = hook }})
		if err != nil {
			t.Fatal(err)
		}
		ctx, sent := t.Context(), ErrorReport{Status: 404, Type: "about:blank", Title: "Not Found"}

		if n := testing.AllocsPerRun(100, func() { c.CallErrorHook(ctx, prblm.ErrNotFound, sent) }); n != 0 {
			t.Fatalf("allocs/op of a hook call %v, want 0", n)
		}
	})
}
