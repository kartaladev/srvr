package otelsrvr_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// syncBuffer is a concurrency-safe log output.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

// record returns the first log record with msg.
func (b *syncBuffer) record(t *testing.T, msg string) map[string]any {
	t.Helper()

	b.mu.Lock()
	defer b.mu.Unlock()

	for line := range strings.Lines(b.buf.String()) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err == nil && m["msg"] == msg {
			return m
		}
	}

	t.Fatalf("no %s record in:\n%s", msg, b.buf.String())

	return nil
}

// TestSpanCorrelation covers otel-tracing "Correlation equality", all rows.
func TestSpanCorrelation(t *testing.T) {
	t.Parallel()

	tp, rec := recorder()
	logs := &syncBuffer{}

	var child trace.SpanContext

	base := traced(t, configFunc(func(g *srvr.Group) {
		g.GET("/fail", func(_ http.ResponseWriter, r *http.Request) error {
			_, span := tp.Tracer("handler").Start(r.Context(), "child")
			child = span.SpanContext()
			span.End()

			return errors.New("boom")
		})
	}), []srvr.Option{srvr.WithLogOutput(logs)}, otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp))

	res, err := clientFor(base).Get(base + "/fail")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	problem, err := prblm.Parse(res)
	if err != nil {
		t.Fatal(err)
	}

	var server sdktrace.ReadOnlySpan

	for _, s := range rec.Ended() {
		if s.SpanKind() == trace.SpanKindServer {
			server = s
		}
	}

	if server == nil {
		t.Fatal("no server span")
	}

	sc := server.SpanContext()
	failed := logs.record(t, "http.request.failed")

	if failed["trace_id"] != sc.TraceID().String() || failed["span_id"] != sc.SpanID().String() {
		t.Errorf("failed record %v/%v, server span %s/%s", failed["trace_id"], failed["span_id"], sc.TraceID(), sc.SpanID())
	}

	if id, _ := problem.Ext("trace_id"); id != sc.TraceID().String() {
		t.Errorf("problem trace_id = %v, want %s", id, sc.TraceID())
	}

	for _, s := range rec.Ended() {
		if s.SpanContext().SpanID() == child.SpanID() && s.Parent().SpanID() != sc.SpanID() {
			t.Errorf("child parent = %s, want the server span %s", s.Parent().SpanID(), sc.SpanID())
		}
	}
}

// TestSpanCorrelationUnsampled covers otel-tracing "Unsampled spans", all
// rows.
func TestSpanCorrelationUnsampled(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name        string
		traceparent string
	}{
		{"incoming unsampled", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00"},
		{"none", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample()), sdktrace.WithSpanProcessor(tracetest.NewSpanRecorder()))
			logs := &syncBuffer{}

			var seen trace.SpanContext

			base := traced(t, configFunc(func(g *srvr.Group) {
				g.GET("/ping", func(w http.ResponseWriter, r *http.Request) error {
					seen = trace.SpanContextFromContext(r.Context())
					w.WriteHeader(http.StatusOK)

					return nil
				})
			}), []srvr.Option{srvr.WithLogOutput(logs)}, otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp))

			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/ping", http.NoBody)
			if tt.traceparent != "" {
				req.Header.Set("traceparent", tt.traceparent)
			}

			res, err := clientFor(req.URL.String()).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()

			access := logs.record(t, "http.request.completed")

			if tt.traceparent != "" && access["trace_id"] != incomingTrace {
				t.Errorf("trace_id = %v, want %s", access["trace_id"], incomingTrace)
			}

			if !seen.IsValid() || access["trace_id"] != seen.TraceID().String() || access["span_id"] != seen.SpanID().String() {
				t.Errorf("access %v/%v, non-recording span %s/%s", access["trace_id"], access["span_id"], seen.TraceID(), seen.SpanID())
			}
		})
	}
}
