package otelsrvr_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// recorder returns a tracer provider recording ended spans synchronously.
func recorder() (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	rec := tracetest.NewSpanRecorder()

	return sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)), rec
}

// traced starts a net/http server with otelsrvr built from opts and conf,
// and returns its base URL.
func traced(t *testing.T, conf configFunc, srvOpts []srvr.Option, opts ...otelsrvr.Option) string {
	t.Helper()

	obs, err := otelsrvr.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return serve(t, conf, append(srvOpts, srvr.WithObserver(obs))...)
}

// TestTracingSwitch covers otel-tracing "Tracing switch", all rows. The
// global-provider row replaces the global TracerProvider, so the test is
// not parallel and restores it.
func TestTracingSwitch(t *testing.T) {
	saved := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(saved) })

	tests := []struct {
		name   string
		global bool
		opts   func(tp *sdktrace.TracerProvider) []otelsrvr.Option
		spans  int
	}{
		{"off", false, func(tp *sdktrace.TracerProvider) []otelsrvr.Option {
			return []otelsrvr.Option{otelsrvr.WithTracerProvider(tp)}
		}, 0},
		{"WithTracing", false, func(tp *sdktrace.TracerProvider) []otelsrvr.Option {
			return []otelsrvr.Option{otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp)}
		}, 1},
		{"Config.Tracing with the global provider", true, func(*sdktrace.TracerProvider) []otelsrvr.Option {
			return []otelsrvr.Option{otelsrvr.WithConfig(otelsrvr.Config{Tracing: true})}
		}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tp, rec := recorder()
			if tt.global {
				otel.SetTracerProvider(tp)
			}

			base := traced(t, configFunc(func(g *srvr.Group) {
				g.GET("/ping", func(w http.ResponseWriter, _ *http.Request) error { w.WriteHeader(http.StatusOK); return nil })
			}), nil, tt.opts(tp)...)
			send(t, http.MethodGet, base+"/ping", "")

			if got := len(rec.Ended()); got != tt.spans {
				t.Errorf("spans = %d, want %d", got, tt.spans)
			}
		})
	}

	for name, tp := range map[string]trace.TracerProvider{"nil": nil, "typed-nil": (*sdktrace.TracerProvider)(nil)} {
		t.Run(name+" provider", func(t *testing.T) {
			if _, err := otelsrvr.New(otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp)); !errors.Is(err, otelsrvr.ErrConfig) {
				t.Fatalf("New = %v, want an error wrapping ErrConfig", err)
			}
		})
	}
}

// panickingTracerProvider panics in Tracer.
type panickingTracerProvider struct{ tracenoop.TracerProvider }

func (panickingTracerProvider) Tracer(string, ...trace.TracerOption) trace.Tracer {
	panic("Tracer panicked")
}

// TestTracerProviderPanics checks that a provider panicking in Tracer
// fails New (design D11).
func TestTracerProviderPanics(t *testing.T) {
	t.Parallel()

	if _, err := otelsrvr.New(otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(panickingTracerProvider{})); !errors.Is(err, otelsrvr.ErrConfig) {
		t.Fatalf("New = %v, want an error wrapping ErrConfig", err)
	}
}
