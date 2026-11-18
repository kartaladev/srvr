package srvr_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sync"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

const (
	incoming      = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	incomingTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
)

var hexTraceID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// traceEcho registers GET /trace writing the request's trace id.
func traceEcho(g *srvr.Group) {
	g.GET("/trace", func(w http.ResponseWriter, r *http.Request) error {
		tr, _ := srvr.TraceFromContext(r.Context())
		_, err := io.WriteString(w, tr.TraceID)

		return err
	})
}

// TestTrace_Ignore covers trace-context / Scenario: Ignoring incoming context.
func TestTrace_Ignore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		opts      []srvr.Option
		continued bool
	}{
		{"none", nil, true},
		{"WithIgnoreIncomingTraceContext", []srvr.Option{srvr.WithIgnoreIncomingTraceContext()}, false},
		{"WithTraceConfig", []srvr.Option{srvr.WithTraceConfig(srvr.TraceConfig{IgnoreIncoming: true})}, false},
		{"WithIgnoreIncomingTraceContext then empty WithTraceConfig", []srvr.Option{srvr.WithIgnoreIncomingTraceContext(), srvr.WithTraceConfig(srvr.TraceConfig{})}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t, tt.opts...)
			mustNil(t, srv.Register(configFunc(traceEcho)))
			base := startServer(t, srv)

			got := do(t, http.MethodGet, base+"/trace", nil, map[string]string{"traceparent": incoming}).body
			if (got == incomingTrace) != tt.continued || !hexTraceID.MatchString(got) {
				t.Fatalf("trace id = %q, continued want %v", got, tt.continued)
			}
		})
	}
}

// TestTraceFromContext covers trace-context / Scenario: Reading the trace context.
func TestTraceFromContext(t *testing.T) {
	t.Parallel()

	t.Run("a request context handled by the server", func(t *testing.T) {
		t.Parallel()

		var (
			got Trace
			ok  bool
		)
		srv := newServer(t)
		mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
			g.GET("/x", func(http.ResponseWriter, *http.Request) error { return nil })
			g.GET("/read", func(_ http.ResponseWriter, r *http.Request) error {
				got, ok = srvr.TraceFromContext(r.Context())

				return nil
			})
		})))
		base := startServer(t, srv)
		do(t, http.MethodGet, base+"/read", nil, map[string]string{"traceparent": incoming})

		if !ok || got.TraceID != incomingTrace || len(got.SpanID) != 16 || got.ParentSpanID != "00f067aa0ba902b7" || !got.Sampled {
			t.Fatalf("TraceFromContext = %+v, %v", got, ok)
		}
	})

	for name, ctx := range map[string]context.Context{"background": context.Background(), "nil": nil} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tr, ok := srvr.TraceFromContext(ctx); ok || tr != (Trace{}) {
				t.Fatalf("TraceFromContext = %+v, %v; want zero, false", tr, ok)
			}
		})
	}
}

// Trace aliases srvr.Trace for brevity.
type Trace = srvr.Trace

// TestTrace_Unique covers trace-context / Scenario: Concurrent generation.
func TestTrace_Unique(t *testing.T) {
	t.Parallel()

	srv := newServer(t)
	mustNil(t, srv.Register(configFunc(traceEcho)))
	base := startServer(t, srv)

	const n = 1000
	var (
		mu  sync.Mutex
		ids = map[string]bool{}
		wg  sync.WaitGroup
	)
	const workers = 50
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: workers}}
	defer client.CloseIdleConnections()

	sem := make(chan struct{}, workers)
	for range n {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/trace", nil)
			res, err := client.Do(req)
			if err != nil {
				t.Error(err)

				return
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			id := string(b)
			mu.Lock()
			ids[id] = true
			mu.Unlock()
		})
	}
	wg.Wait()

	if len(ids) != n {
		t.Fatalf("distinct trace ids = %d, want %d", len(ids), n)
	}
}

// TestProblemTraceID covers error-responses / Scenario: Trace id in the problem.
func TestProblemTraceID(t *testing.T) {
	t.Parallel()

	tests := []struct{ traceparent, want string }{
		{incoming, incomingTrace},
		{"", ""}, // a generated id
	}

	for _, tt := range tests {
		t.Run(tt.traceparent, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
				g.GET("/items/{id}", func(http.ResponseWriter, *http.Request) error { return prblm.ErrNotFound })
			})))
			base := startServer(t, srv)

			hdr := map[string]string{}
			if tt.traceparent != "" {
				hdr["traceparent"] = tt.traceparent
			}
			res := do(t, http.MethodGet, base+"/items/7?token=x", nil, hdr)
			if res.problem == nil {
				t.Fatalf("no problem in %q", res.body)
			}
			id, _ := res.problem.Ext("trace_id")
			s, _ := id.(string)
			if (tt.want != "" && s != tt.want) || !hexTraceID.MatchString(s) {
				t.Fatalf("trace_id = %v, want %q", id, tt.want)
			}
			if res.problem.Instance != "/items/7" {
				t.Fatalf("instance = %q, want /items/7", res.problem.Instance)
			}
		})
	}
}

// sourcing is an observer that supplies trace from TraceFromContext, or
// panics there when panics is set.
type sourcing struct {
	trace  srvr.Trace
	panics bool
}

func (*sourcing) Begin(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
	return ctx, nil
}

func (s *sourcing) TraceFromContext(context.Context) (srvr.Trace, bool) {
	if s.panics {
		panic("source panicked")
	}

	return s.trace, true
}

var _ srvr.TraceSource = (*sourcing)(nil)

// TestTraceSource covers request-observer "Trace source", all rows, plus a
// source that panics.
func TestTraceSource(t *testing.T) {
	t.Parallel()

	const (
		incoming = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		supplied = "5bf92f3577b34da6a3ce929d0e0e4736"
	)

	tests := []struct {
		name     string
		observer srvr.Observer
		spanID   string // empty: srvr's generated span id
		traceID  string // empty: the incoming trace id
		fault    bool
	}{
		{"none", nil, "", "", false},
		{"supplied", &sourcing{trace: srvr.Trace{TraceID: supplied, SpanID: "1111111111111111"}}, "1111111111111111", supplied, false},
		{"all-zero span id", &sourcing{trace: srvr.Trace{TraceID: supplied, SpanID: "0000000000000000"}}, "", "", false},
		{"panics", &sourcing{panics: true}, "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var opts []srvr.Option
			if tt.observer != nil {
				opts = append(opts, srvr.WithObserver(tt.observer))
			}

			var handlerTrace srvr.Trace

			base, out := loggedServer(t, on("", "/ping", func(_ http.ResponseWriter, r *http.Request) error {
				handlerTrace, _ = srvr.TraceFromContext(r.Context())
				srvr.LoggerFromContext(r.Context()).Info("handler.own")

				return nil
			}), opts...)

			res := do(t, http.MethodGet, base+"/ping", nil, map[string]string{"traceparent": incoming})
			if res.status != http.StatusOK {
				t.Fatalf("status = %d", res.status)
			}

			wantTrace := tt.traceID
			if wantTrace == "" {
				wantTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
			}

			access := out.waitRecord(t, "http.request.completed")
			own := out.waitRecord(t, "handler.own")

			for name, rec := range map[string]map[string]any{"access": access, "handler": own} {
				if rec["trace_id"] != wantTrace {
					t.Errorf("%s trace_id = %v, want %s", name, rec["trace_id"], wantTrace)
				}

				span, _ := rec["span_id"].(string)
				if (tt.spanID != "" && span != tt.spanID) || (tt.spanID == "" && (len(span) != 16 || span == "1111111111111111" || span == "0000000000000000")) {
					t.Errorf("%s span_id = %q, want %q (empty: srvr's generated one)", name, span, tt.spanID)
				}
			}

			if handlerTrace.TraceID != wantTrace || handlerTrace.SpanID != access["span_id"] {
				t.Errorf("TraceFromContext = %+v, access record %v/%v", handlerTrace, access["trace_id"], access["span_id"])
			}

			faults := out.find(t, "srvr.observer.fault")
			if tt.fault && (len(faults) != 1 || faults[0]["srvr.observer.fault.kind"] != "trace_source_panic") {
				t.Errorf("fault records = %v, want one trace_source_panic", faults)
			}

			if !tt.fault && len(faults) != 0 {
				t.Errorf("fault records = %v, want none", faults)
			}
		})
	}
}

// TestTraceSourceProblem checks that the problem trace_id follows the
// source.
func TestTraceSourceProblem(t *testing.T) {
	t.Parallel()

	const supplied = "5bf92f3577b34da6a3ce929d0e0e4736"

	src := &sourcing{trace: srvr.Trace{TraceID: supplied, SpanID: "1111111111111111"}}
	base, _ := loggedServer(t, on("", "/fail", func(http.ResponseWriter, *http.Request) error { return errors.New("boom") }), srvr.WithObserver(src))

	res := get(t, base+"/fail")
	if id, _ := res.problem.Ext("trace_id"); id != supplied {
		t.Errorf("problem trace_id = %v, want %s", id, supplied)
	}
}

// TestSampledFlag covers trace-context / Scenario: Sampled flag, both
// tables: the sampled flag is bit 0 of the flags byte read as hex, and a
// malformed flags field starts a new, unsampled trace.
func TestSampledFlag(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		got = map[string]Trace{}
	)
	srv := newServer(t)
	mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
		g.GET("/read", func(_ http.ResponseWriter, r *http.Request) error {
			tr, _ := srvr.TraceFromContext(r.Context())
			mu.Lock()
			got[r.Header.Get("traceparent")] = tr
			mu.Unlock()

			return nil
		})
	})))
	base := startServer(t, srv)

	tests := []struct {
		flags    string
		sampled  bool
		newTrace bool
	}{
		{flags: "00"},
		{flags: "01", sampled: true},
		{flags: "02"},
		{flags: "09", sampled: true},
		{flags: "0a"},
		{flags: "0b", sampled: true},
		{flags: "0e"},
		{flags: "0f", sampled: true},
		{flags: "fe"},
		{flags: "ff", sampled: true},
		// rejected flags start a new trace
		{flags: "0B", newTrace: true},
		{flags: "0g", newTrace: true},
		{flags: "1", newTrace: true},
	}

	for _, tt := range tests {
		t.Run("flags "+tt.flags, func(t *testing.T) {
			t.Parallel()

			header := "00-" + incomingTrace + "-00f067aa0ba902b7-" + tt.flags
			do(t, http.MethodGet, base+"/read", nil, map[string]string{"traceparent": header})

			mu.Lock()
			tr := got[header]
			mu.Unlock()

			if continued := tr.TraceID == incomingTrace; continued == tt.newTrace {
				t.Fatalf("trace id = %q, new trace want %v", tr.TraceID, tt.newTrace)
			}
			if tr.Sampled != tt.sampled {
				t.Fatalf("Sampled = %v, want %v", tr.Sampled, tt.sampled)
			}
		})
	}
}
