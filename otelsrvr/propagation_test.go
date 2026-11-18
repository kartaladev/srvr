package otelsrvr_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

const (
	incomingParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	incomingTrace  = "4bf92f3577b34da6a3ce929d0e0e4736"
	incomingSpan   = "00f067aa0ba902b7"
)

// extractPanics is a propagator whose Extract panics.
type extractPanics struct{ propagation.TraceContext }

func (extractPanics) Extract(context.Context, propagation.TextMapCarrier) context.Context {
	panic("Extract panicked")
}

// fieldsPanics is a propagator whose Fields panics.
type fieldsPanics struct{ propagation.TraceContext }

func (fieldsPanics) Fields() []string { panic("Fields panicked") }

// TestPropagation covers otel-tracing "Parent extraction", all rows, plus
// faulty propagators (design D11). Rows using the global propagator
// replace it, so the test is not parallel and restores it.
func TestPropagation(t *testing.T) {
	saved := otel.GetTextMapPropagator()
	t.Cleanup(func() { otel.SetTextMapPropagator(saved) })

	tests := []struct {
		name        string
		propagator  propagation.TextMapPropagator // nil: the global one
		global      propagation.TextMapPropagator
		srvOpts     []srvr.Option
		traceparent string
		continued   bool
	}{
		{"W3C valid", propagation.TraceContext{}, nil, nil, incomingParent, true},
		{"W3C garbage", propagation.TraceContext{}, nil, nil, "garbage", false},
		{"W3C none", propagation.TraceContext{}, nil, nil, "", false},
		{"no-op global falls back to W3C", nil, propagation.NewCompositeTextMapPropagator(), nil, incomingParent, true},
		{"Baggage-only global", nil, propagation.Baggage{}, nil, incomingParent, false},
		{"ignored by option", propagation.TraceContext{}, nil, []srvr.Option{srvr.WithIgnoreIncomingTraceContext()}, incomingParent, false},
		{"ignored by config", propagation.TraceContext{}, nil, []srvr.Option{srvr.WithTraceConfig(srvr.TraceConfig{IgnoreIncoming: true})}, incomingParent, false},
		{"Extract panics", extractPanics{}, nil, nil, incomingParent, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.global != nil {
				otel.SetTextMapPropagator(tt.global)
			}

			tp, rec := recorder()

			opts := []otelsrvr.Option{otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp)}
			if tt.propagator != nil {
				opts = append(opts, otelsrvr.WithPropagator(tt.propagator))
			}

			base := traced(t, usersRoute, tt.srvOpts, opts...)

			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/users/1", http.NoBody)
			if tt.traceparent != "" {
				req.Header.Set("traceparent", tt.traceparent)
			}

			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()

			if res.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", res.StatusCode)
			}

			span := onlySpan(t, rec)
			sc, parent := span.SpanContext(), span.Parent()

			if tt.continued {
				if sc.TraceID().String() != incomingTrace || parent.SpanID().String() != incomingSpan || !parent.IsRemote() {
					t.Errorf("trace %s parent %s remote %v, want %s, %s remote", sc.TraceID(), parent.SpanID(), parent.IsRemote(), incomingTrace, incomingSpan)
				}

				return
			}

			if sc.TraceID().String() == incomingTrace || parent.IsValid() {
				t.Errorf("trace %s parent %v, want a new root", sc.TraceID(), parent.SpanID())
			}
		})
	}

	for name, p := range map[string]propagation.TextMapPropagator{
		"nil":           nil,
		"typed-nil":     (*propagation.TraceContext)(nil),
		"Fields panics": fieldsPanics{},
	} {
		t.Run(name+" propagator", func(t *testing.T) {
			if _, err := otelsrvr.New(otelsrvr.WithTracing(), otelsrvr.WithPropagator(p)); !errors.Is(err, otelsrvr.ErrConfig) {
				t.Fatalf("New = %v, want an error wrapping ErrConfig", err)
			}
		})
	}
}
