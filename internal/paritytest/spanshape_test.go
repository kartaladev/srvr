package paritytest

import (
	"net/http"
	"testing"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// TestSpanShape covers otel-tracing "Span shape per engine", the gin and
// fiber rows, on every engine (the harness route is /items/{id} in each
// engine's syntax), and that the span continues an incoming traceparent
// and supplies the access record's ids.
func TestSpanShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outcome outcome
		path    string
		route   bool
		status  codes.Code
	}{
		{"writes 200", succeeds, "/items/42", true, codes.Unset},
		{"returns boom", failsBoom, "/items/42", true, codes.Error},
		{"unmatched", succeeds, "/nope", false, codes.Unset},
	}

	for _, e := range engines {
		for _, tt := range tests {
			t.Run(e.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				rec := tracetest.NewSpanRecorder()
				obs, err := otelsrvr.New(otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))))
				must(t, err)

				base, logs := e.start(t, tt.outcome, srvr.WithObserver(obs))
				header := map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
				if _, _, err := send(t, http.MethodGet, base+tt.path, header); err != nil {
					t.Fatal(err)
				}

				access := logs.access(t)

				want := "GET"
				if tt.route {
					want += " " + e.route
				}

				spans := rec.Ended()
				if len(spans) != 1 || spans[0].Name() != want || spans[0].Status().Code != tt.status {
					for _, s := range spans {
						t.Logf("span %q status %v", s.Name(), s.Status().Code)
					}

					t.Fatalf("want one span %q with status %v", want, tt.status)
				}

				// The span continues the incoming trace, and the access
				// record carries the span's ids, on every engine.
				sc := spans[0].SpanContext()
				if sc.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || spans[0].Parent().SpanID().String() != "00f067aa0ba902b7" {
					t.Errorf("span trace %s parent %s, want the incoming ones", sc.TraceID(), spans[0].Parent().SpanID())
				}

				if access[0]["trace_id"] != sc.TraceID().String() || access[0]["span_id"] != sc.SpanID().String() {
					t.Errorf("access record %v/%v, span %s/%s", access[0]["trace_id"], access[0]["span_id"], sc.TraceID(), sc.SpanID())
				}
			})
		}
	}
}
