package srvr_test

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// recordingObserver records every Begin and end it sees.
type recordingObserver struct {
	mu      sync.Mutex
	begins  []srvr.RequestInfo
	results []srvr.ResultInfo
	// traces are the trace contexts seen at Begin.
	traces []srvr.Trace
}

func (o *recordingObserver) Begin(ctx context.Context, info srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
	tr, _ := srvr.TraceFromContext(ctx)

	o.mu.Lock()
	o.begins = append(o.begins, info)
	o.traces = append(o.traces, tr)
	o.mu.Unlock()

	return ctx, func(res srvr.ResultInfo) {
		o.mu.Lock()
		defer o.mu.Unlock()

		o.results = append(o.results, res)
	}
}

// seen returns copies of what the observer recorded.
func (o *recordingObserver) seen() ([]srvr.RequestInfo, []srvr.ResultInfo, []srvr.Trace) {
	o.mu.Lock()
	defer o.mu.Unlock()

	return append([]srvr.RequestInfo(nil), o.begins...), append([]srvr.ResultInfo(nil), o.results...), append([]srvr.Trace(nil), o.traces...)
}

// failing returns a handler that returns err.
func failing(err error) srvr.HandlerFunc {
	return func(http.ResponseWriter, *http.Request) error { return err }
}

// panicking is a handler that panics with "boom".
func panicking(http.ResponseWriter, *http.Request) error { panic("boom") }

// observed starts a server with a recording observer and conf and returns
// the observer and the server's base URL.
func observed(t *testing.T, conf configFunc) (*recordingObserver, string) {
	t.Helper()

	obs := &recordingObserver{}
	srv := newServer(t, srvr.WithObserver(obs), srvr.WithLogOutput(&logBuffer{}))
	mustNil(t, srv.Register(conf))

	return obs, startServer(t, srv)
}

// TestObserver_NetHTTP covers request-observer "Begin and end per engine",
// net/http rows, including the unmatched row.
func TestObserver_NetHTTP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		behaviour srvr.HandlerFunc
		path      string
		route     string
		status    int
	}{
		{"writes 200", status(http.StatusOK), "/users/42", "/users/{id}", 200},
		{"returns ErrNotFound", failing(prblm.ErrNotFound), "/users/42", "/users/{id}", 404},
		{"returns boom", failing(errors.New("boom")), "/users/42", "/users/{id}", 500},
		{"panics", panicking, "/users/42", "/users/{id}", 500},
		{"unmatched", status(http.StatusOK), "/nope", "", 404},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs, base := observed(t, on("", "/users/{id}", tt.behaviour))

			if got := get(t, base+tt.path).status; got != tt.status {
				t.Fatalf("client status = %d, want %d", got, tt.status)
			}

			begins, results, _ := obs.seen()
			if len(begins) != 1 || len(results) != 1 {
				t.Fatalf("Begin calls = %d, end calls = %d, want 1 and 1", len(begins), len(results))
			}

			if begins[0].Method != http.MethodGet {
				t.Errorf("Begin method = %q, want GET", begins[0].Method)
			}

			if res := results[0]; res.Route != tt.route || res.Status != tt.status {
				t.Errorf("end route, status = %q, %d, want %q, %d", res.Route, res.Status, tt.route, tt.status)
			}
		})
	}
}

// TestObserver_Placement covers request-observer "Observer view of the
// request", net/http rows: the observer sees srvr's trace at Begin and the
// final rendered status at end.
func TestObserver_Placement(t *testing.T) {
	t.Parallel()

	const incoming = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	tests := []struct {
		name        string
		traceparent string
		behaviour   srvr.HandlerFunc
		path        string
		traceID     string // empty: a generated id
		status      int
		kind        srvr.ErrorKind
	}{
		{"continued trace", incoming, status(http.StatusOK), "/users/42", "4bf92f3577b34da6a3ce929d0e0e4736", 200, srvr.ErrorKindNone},
		{"returned error", "", failing(errors.New("boom")), "/users/42", "", 500, srvr.ErrorKindError},
		{"panic", "", panicking, "/users/42", "", 500, srvr.ErrorKindPanic},
		{"unmatched", "", status(http.StatusOK), "/nope", "", 404, srvr.ErrorKindError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs, base := observed(t, on("", "/users/{id}", tt.behaviour))

			header := map[string]string{}
			if tt.traceparent != "" {
				header["traceparent"] = tt.traceparent
			}

			res := do(t, http.MethodGet, base+tt.path, nil, header)

			_, results, traces := obs.seen()
			if len(results) != 1 {
				t.Fatalf("end calls = %d, want 1", len(results))
			}

			tr := traces[0]
			switch {
			case tt.traceID != "" && tr.TraceID != tt.traceID:
				t.Errorf("trace id at Begin = %q, want %q", tr.TraceID, tt.traceID)
			case tt.traceID == "" && len(tr.TraceID) != 32:
				t.Errorf("trace id at Begin = %q, want a generated id", tr.TraceID)
			}

			if res.problem != nil {
				if id, _ := res.problem.Ext("trace_id"); id != tr.TraceID {
					t.Errorf("problem trace_id = %v, Begin saw %q", id, tr.TraceID)
				}
			}

			if got := results[0]; got.Status != tt.status || got.ErrorKind != tt.kind {
				t.Errorf("end status, kind = %d, %q, want %d, %q", got.Status, got.ErrorKind, tt.status, tt.kind)
			}
		})
	}
}

// TestObserver_Result covers request-observer "Result fields", all rows.
func TestObserver_Result(t *testing.T) {
	t.Parallel()

	itemsRoute := configFunc(func(g *srvr.Group) {
		g.POST("/items", func(w http.ResponseWriter, r *http.Request) error {
			if r.Header.Get("X-Late") != "" {
				_, _ = w.Write([]byte("abc"))

				return errors.New("late")
			}

			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("twelve bytes"))

			return nil
		})
	})

	tests := []struct {
		name      string
		method    string
		path      string
		body      string
		late      bool
		route     string
		status    int
		size      int // -1: the problem body length
		committed bool
	}{
		{"created", http.MethodPost, "/items", "hello", false, "/items", 201, 12, false},
		{"unmatched", http.MethodGet, "/nope", "", false, "", 404, -1, false},
		{"method not allowed", http.MethodDelete, "/items", "", false, "", 405, -1, false},
		{"late error", http.MethodPost, "/items", "", true, "/items", 200, 3, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs, base := observed(t, itemsRoute)

			header := map[string]string{}
			if tt.late {
				header["X-Late"] = "1"
			}

			res := do(t, tt.method, base+tt.path, strings.NewReader(tt.body), header)

			_, results, _ := obs.seen()
			if len(results) != 1 {
				t.Fatalf("end calls = %d, want 1", len(results))
			}

			want := tt.size
			if want < 0 {
				want = len(res.body)
			}

			got := results[0]
			if got.Route != tt.route || got.Status != tt.status || got.ResponseBodySize != int64(want) {
				t.Errorf("route, status, size = %q, %d, %d, want %q, %d, %d", got.Route, got.Status, got.ResponseBodySize, tt.route, tt.status, want)
			}

			if got.RequestBodySize != int64(len(tt.body)) {
				t.Errorf("request body size = %d, want %d", got.RequestBodySize, len(tt.body))
			}

			if got.Committed != tt.committed {
				t.Errorf("committed = %v, want %v", got.Committed, tt.committed)
			}

			if got.Duration <= 0 {
				t.Errorf("duration = %v, want > 0", got.Duration)
			}
		})
	}
}

// TestObserver_Abort pins ResultInfo for a handler that panics with
// http.ErrAbortHandler: the end function still runs, before the access
// record, and reports the abort (otel-metrics and otel-tracing abort rows
// build on it).
func TestObserver_Abort(t *testing.T) {
	t.Parallel()

	obs, base := observed(t, on("", "/users/{id}", func(http.ResponseWriter, *http.Request) error { panic(http.ErrAbortHandler) }))

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/users/42", nil)
	if res, err := clientFor(req.URL.String()).Do(req); err == nil {
		_ = res.Body.Close()
		t.Fatalf("status %d, want an aborted connection", res.StatusCode)
	}

	eventually(t, func() bool { _, results, _ := obs.seen(); return len(results) == 1 }, "the end function did not run for an aborted request")

	_, results, _ := obs.seen()
	if got := results[0]; !got.Aborted || got.ErrorKind != srvr.ErrorKindPanic || got.Status != 0 || got.Route != "/users/{id}" {
		t.Errorf("result = %+v, want aborted, kind panic, status 0, route /users/{id}", got)
	}
}

// TestObserver_RequestInfo pins what Begin learns: the header reader, the
// path only with IncludePath, the ignore-incoming flag and the scheme.
func TestObserver_RequestInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opts   []srvr.Option
		path   string
		ignore bool
		tls    bool
	}{
		{"defaults", nil, "", false, false},
		{"include path", []srvr.Option{srvr.WithAccessLogIncludePath()}, "/ping", false, false},
		{"ignore incoming", []srvr.Option{srvr.WithIgnoreIncomingTraceContext()}, "", true, false},
		{"TLS", nil, "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				mu   sync.Mutex
				got  srvr.RequestInfo
				hdr  string
				keys []string
			)

			obs := observerFunc(func(ctx context.Context, info srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
				mu.Lock()
				defer mu.Unlock()

				got, hdr, keys = info, info.Header.Get("X-Probe"), info.Header.Keys()

				return ctx, nil
			})

			opts := append(slices.Clone(tt.opts), srvr.WithObserver(obs))
			scheme := "http"

			if tt.tls {
				scheme = "https"
				getTLS(t, okRoute("/ping"), "/ping", map[string]string{"X-Probe": "yes"}, opts...)
			} else {
				base, _ := loggedServer(t, okRoute("/ping"), opts...)
				do(t, http.MethodGet, base+"/ping", nil, map[string]string{"X-Probe": "yes"})
			}

			mu.Lock()
			defer mu.Unlock()

			if got.Path != tt.path || got.IgnoreIncomingTrace != tt.ignore || got.Method != http.MethodGet || got.Scheme != scheme {
				t.Errorf("RequestInfo = %+v", got)
			}

			if hdr != "yes" || !slices.Contains(keys, "X-Probe") {
				t.Errorf("header X-Probe = %q, keys %v", hdr, keys)
			}
		})
	}
}

// getTLS serves conf over TLS (a test certificate on a WithListener
// listener) and sends GET path with header through a client trusting it.
func getTLS(t *testing.T, conf configFunc, path string, header map[string]string, opts ...srvr.Option) {
	t.Helper()

	cert := httptest.NewUnstartedServer(nil) // only for its certificate and client
	cert.StartTLS()
	client := cert.Client()
	cfg := cert.TLS.Clone()
	cert.Close()

	srv, err := srvr.New(append([]srvr.Option{srvr.WithListener(tls.NewListener(listen(t), cfg)), srvr.WithLogOutput(io.Discard)}, opts...)...)
	mustNil(t, err)
	mustNil(t, srv.Register(conf))
	startServer(t, srv)
	t.Cleanup(client.CloseIdleConnections) // before the server stops

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://"+srv.Addr()+path, http.NoBody)
	mustNil(t, err)

	for k, v := range header {
		req.Header.Set(k, v)
	}

	res, err := client.Do(req)
	mustNil(t, err)

	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
}

// observerFunc adapts a function to srvr.Observer.
type observerFunc func(ctx context.Context, info srvr.RequestInfo) (context.Context, func(srvr.ResultInfo))

func (f observerFunc) Begin(ctx context.Context, info srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
	return f(ctx, info)
}

// callLog records calls in order across observers.
type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.calls = append(l.calls, s)
}

func (l *callLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.calls)
}

// named returns an observer that logs name.Begin and name.end.
func named(log *callLog, name string) srvr.Observer {
	return observerFunc(func(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
		log.add(name + ".Begin")

		return ctx, func(srvr.ResultInfo) { log.add(name + ".end") }
	})
}

// TestObserver_Compose covers request-observer "Composition", all rows.
func TestObserver_Compose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		names []string
		order []string
	}{
		{[]string{"A", "B"}, []string{"A.Begin", "B.Begin", "B.end", "A.end"}},
		{[]string{"A"}, []string{"A.Begin", "A.end"}},
	}

	for _, tt := range tests {
		t.Run(tt.order[0], func(t *testing.T) {
			t.Parallel()

			log := &callLog{}
			var opts []srvr.Option
			for _, n := range tt.names {
				opts = append(opts, srvr.WithObserver(named(log, n)))
			}

			base, _ := loggedServer(t, okRoute("/ping"), opts...)
			get(t, base+"/ping")

			if got := log.get(); !slices.Equal(got, tt.order) {
				t.Errorf("call order = %v, want %v", got, tt.order)
			}
		})
	}
}

// TestObserver_ComposeContext checks that each Begin receives the context
// the previous one returned.
func TestObserver_ComposeContext(t *testing.T) {
	t.Parallel()

	type key struct{}

	var seen atomic.Value

	first := observerFunc(func(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
		return context.WithValue(ctx, key{}, "from A"), nil
	})
	second := observerFunc(func(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
		seen.Store(ctx.Value(key{}))

		return ctx, nil
	})

	base, _ := loggedServer(t, okRoute("/ping"), srvr.WithObserver(first), srvr.WithObserver(second))
	get(t, base+"/ping")

	if got := seen.Load(); got != "from A" {
		t.Errorf("B saw %v, want the context A returned", got)
	}
}

// panicky is a pointer observer type, for the typed-nil row.
type panicky struct{}

func (*panicky) Begin(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
	return ctx, nil
}

// TestObserver_FaultsConstruction covers request-observer "Observer
// faults", the nil and typed-nil rows.
func TestObserver_FaultsConstruction(t *testing.T) {
	t.Parallel()

	for name, o := range map[string]srvr.Observer{"nil": nil, "typed-nil": (*panicky)(nil)} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := srvr.New(srvr.WithObserver(o)); !errors.Is(err, srvr.ErrConfig) {
				t.Fatalf("New = %v, want an error wrapping ErrConfig", err)
			}
		})
	}
}

// panickingError is an error whose Error method panics.
type panickingError struct{}

func (panickingError) Error() string { panic("Error() panicked") }

// valuePanics is a context whose Value panics.
type valuePanics struct{ context.Context }

func (valuePanics) Value(any) any { panic("Value panicked") }

// TestObserver_Faults covers request-observer "Observer faults", the
// request rows, plus a returned context whose Value panics and an end
// panic whose value's Error panics.
func TestObserver_Faults(t *testing.T) {
	t.Parallel()

	type ctxKey struct{}

	tests := []struct {
		name     string
		begin    func(ctx context.Context) (context.Context, func(srvr.ResultInfo))
		requests int
		minTime  time.Duration
		kind     string // the expected fault kind, empty for none
	}{
		{"panics in Begin", func(context.Context) (context.Context, func(srvr.ResultInfo)) { panic("boom") }, 1, 0, "begin_panic"},
		{"nil context", func(context.Context) (context.Context, func(srvr.ResultInfo)) { return nil, func(srvr.ResultInfo) {} }, 1, 0, ""},
		{"foreign context", func(context.Context) (context.Context, func(srvr.ResultInfo)) {
			return context.WithValue(context.Background(), ctxKey{}, 1), nil //nolint:usetesting // a context unrelated to the request is the row under test
		}, 1, 0, ""},
		{"nil end", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) { return ctx, nil }, 1, 0, ""},
		{"panics in end", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) {
			return ctx, func(srvr.ResultInfo) { panic("boom") }
		}, 1, 0, "end_panic"},
		{"panics in Begin on 1000 requests", func(context.Context) (context.Context, func(srvr.ResultInfo)) { panic("boom") }, 1000, 0, "begin_panic"},
		{"blocks in Begin", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) {
			time.Sleep(50 * time.Millisecond)

			return ctx, nil
		}, 1, 50 * time.Millisecond, ""},
		{"blocks in end", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) {
			return ctx, func(srvr.ResultInfo) { time.Sleep(50 * time.Millisecond) }
		}, 1, 50 * time.Millisecond, ""},
		{"panics in end with ErrAbortHandler", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) {
			return ctx, func(srvr.ResultInfo) { panic(http.ErrAbortHandler) }
		}, 1, 0, "end_panic"},
		{"returns a context whose Value panics", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) {
			return valuePanics{ctx}, nil
		}, 1, 0, "begin_panic"},
		{"panics in end with an error whose Error panics", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) {
			return ctx, func(srvr.ResultInfo) { panic(panickingError{}) }
		}, 1, 0, "end_panic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs := observerFunc(func(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
				return tt.begin(ctx)
			})
			out := &logBuffer{}
			srv := newServer(t, srvr.WithObserver(obs), srvr.WithLogOutput(out))

			var handlerHasTrace atomic.Bool

			mustNil(t, srv.Register(on("", "/ping", func(w http.ResponseWriter, r *http.Request) error {
				_, ok := srvr.TraceFromContext(r.Context())
				handlerHasTrace.Store(ok)
				w.WriteHeader(http.StatusOK)

				return nil
			})))
			base := startServer(t, srv)

			for range tt.requests {
				start := time.Now()
				if got := get(t, base+"/ping").status; got != http.StatusOK {
					t.Fatalf("status = %d, want 200", got)
				}

				if d := time.Since(start); d < tt.minTime {
					t.Fatalf("response after %v, want at least %v", d, tt.minTime)
				}
			}

			if !handlerHasTrace.Load() {
				t.Error("the handler's context lost srvr's request state")
			}

			eventually(t, func() bool { return len(out.find(t, "http.request.completed")) == tt.requests }, "missing access records")

			faults := out.find(t, "srvr.observer.fault")
			switch {
			case tt.kind == "" && len(faults) != 0:
				t.Fatalf("fault records = %v, want none", faults)
			case tt.kind != "" && len(faults) != 1:
				t.Fatalf("fault records = %d, want 1:\n%s", len(faults), out.String())
			case tt.kind != "":
				if f := faults[0]; f["srvr.observer.fault.kind"] != tt.kind || f["level"] != "ERROR" {
					t.Errorf("fault record = %v, want kind %s at ERROR", f, tt.kind)
				}
			}
		})
	}
}

// orderedOutput records whether the access record was written after the
// end function returned.
type orderedOutput struct {
	logBuffer

	endReturned atomic.Bool
	afterEnd    atomic.Bool
}

func (o *orderedOutput) Write(p []byte) (int, error) {
	if o.endReturned.Load() {
		o.afterEnd.Store(true)
	}

	return o.logBuffer.Write(p)
}

// TestObserver_FaultsBlockingEnd covers the blocking end-function row:
// the access record is written after the end function returns.
func TestObserver_FaultsBlockingEnd(t *testing.T) {
	t.Parallel()

	out := &orderedOutput{}
	obs := observerFunc(func(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
		return ctx, func(srvr.ResultInfo) {
			time.Sleep(50 * time.Millisecond)
			out.endReturned.Store(true)
		}
	})

	srv := newServer(t, srvr.WithObserver(obs), srvr.WithLogOutput(out))
	mustNil(t, srv.Register(okRoute("/ping")))

	start := time.Now()
	if got := get(t, startServer(t, srv)+"/ping").status; got != http.StatusOK || time.Since(start) < 50*time.Millisecond {
		t.Fatalf("status %d after %v", got, time.Since(start))
	}

	eventually(t, func() bool { return len(out.find(t, "http.request.completed")) == 1 }, "no access record")

	if !out.afterEnd.Load() {
		t.Error("the access record was written before the end function returned")
	}
}
