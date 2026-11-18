package otelsetup_test

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/kartaladev/srvr/otelsetup"
	"go.opentelemetry.io/otel"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// local is a Prometheus configuration on an ephemeral loopback port.
var local = otelsetup.PrometheusConfig{Enabled: true, Addr: "127.0.0.1:0"}

// setUp builds a setup and shuts it down when the test ends.
func setUp(t *testing.T, opts ...otelsetup.Option) *otelsetup.Setup {
	t.Helper()

	s, err := otelsetup.New(context.Background(), opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := s.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})

	return s
}

// fetch GETs url and returns the status and body.
func fetch(t *testing.T, url string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	return res.StatusCode, string(b)
}

// scrape fetches s's metrics from its own listener.
func scrape(t *testing.T, s *otelsetup.Setup) (int, string) {
	t.Helper()

	return fetch(t, "http://"+s.MetricsAddr()+"/metrics")
}

// resetGlobals resets the OpenTelemetry globals to no-op ones when the
// test ends. The originals cannot be restored: the global getters return
// delegates that refuse to be set as their own targets.
func resetGlobals(t *testing.T) {
	t.Helper()

	t.Cleanup(func() {
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {}))
	})
}
