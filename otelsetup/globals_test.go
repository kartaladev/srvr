package otelsetup_test

import (
	"context"
	"testing"

	"github.com/kartaladev/srvr/otelsetup"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// TestSetGlobal covers telemetry-setup "Globals", all rows, plus a failed
// New that leaves the globals unchanged. It replaces the globals, so it is
// not parallel and resets them.
func TestSetGlobal(t *testing.T) {
	resetGlobals(t)

	injects := func() bool {
		sc := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
		})
		carrier := propagation.MapCarrier{}
		otel.GetTextMapPropagator().Inject(trace.ContextWithSpanContext(context.Background(), sc), carrier)

		return carrier.Get("traceparent") != ""
	}

	t.Run("defaults", func(t *testing.T) {
		before := otel.GetMeterProvider()
		setUp(t, otelsetup.WithPrometheusConfig(local))

		if otel.GetMeterProvider() != before || injects() {
			t.Error("globals changed without SetGlobal")
		}
	})

	t.Run("failed New", func(t *testing.T) {
		before := otel.GetMeterProvider()

		_, err := otelsetup.New(context.Background(), otelsetup.WithSetGlobal(), otelsetup.WithPrometheusConfig(otelsetup.PrometheusConfig{Enabled: true, Addr: "127.0.0.1:1"}))
		if err == nil {
			t.Skip("port 1 was bindable")
		}

		if otel.GetMeterProvider() != before || injects() {
			t.Error("a failed New changed the globals")
		}
	})

	t.Run("SetGlobal", func(t *testing.T) {
		s := setUp(t, otelsetup.WithSetGlobal(), otelsetup.WithPrometheusConfig(local))

		if otel.GetMeterProvider() != s.MeterProvider() || !injects() {
			t.Error("SetGlobal did not install the meter provider and a TraceContext propagator")
		}
	})
}
