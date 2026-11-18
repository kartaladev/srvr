package srvr_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/kartaladev/srvr"
)

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
