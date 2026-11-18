package srvr_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

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

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/users/42", nil)
	if res, err := http.DefaultClient.Do(req); err == nil {
		_ = res.Body.Close()
		t.Fatalf("status %d, want an aborted connection", res.StatusCode)
	}

	eventually(t, func() bool { _, results, _ := obs.seen(); return len(results) == 1 }, "the end function did not run for an aborted request")

	_, results, _ := obs.seen()
	if got := results[0]; !got.Aborted || got.ErrorKind != srvr.ErrorKindPanic || got.Status != 0 || got.Route != "/users/{id}" {
		t.Errorf("result = %+v, want aborted, kind panic, status 0, route /users/{id}", got)
	}
}
