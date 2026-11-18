package otelsetup_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/kartaladev/srvr/otelsetup"
)

// metricsServeGoroutine is the function of the goroutine serving the
// Prometheus listener, which a leak report of a setup never shut down
// names.
const metricsServeGoroutine = "github.com/kartaladev/srvr/otelsetup.(*Setup).listen.func1"

// TestLifecycle_Goroutines covers test-quality / Scenario: Lifecycle leaves
// no goroutine behind (otelsetup rows). Not parallel: the checks see every
// goroutine of the process.
func TestLifecycle_Goroutines(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
		leak string // the function a "yes" row's report names; "" for "no"
	}{
		{"Prometheus on 127.0.0.1:0, New, GET /metrics, Shutdown", func(t *testing.T) {
			s := newSetup(t, otelsetup.WithPrometheusConfig(local))
			registerClient(t, s.MetricsAddr())

			if code, _ := scrape(t, s); code != http.StatusOK {
				t.Fatalf("GET /metrics = %d", code)
			}

			shutdown(t, s)
		}, ""},
		{"OTLP metrics and traces over HTTP to a test collector, New, one export, Shutdown", func(t *testing.T) {
			c := newHTTPCollector(t, false)
			s := newSetup(t, otelsetup.WithOTLP(otelsetup.OTLPConfig{
				Endpoint: c.url, Protocol: "http/protobuf", Metrics: true, Traces: true,
			}))

			emit(t, s)
			shutdown(t, s)

			if !bytes.Contains(c.received("traces"), []byte("otelsetup.test.span")) {
				t.Error("the collector got no span")
			}
		}, ""},
		{"OTLP metrics and traces over gRPC to an unreachable endpoint, New, Shutdown", func(t *testing.T) {
			s := newSetup(t, otelsetup.WithOTLP(otelsetup.OTLPConfig{
				Endpoint: unreachable(t), Protocol: "grpc", Metrics: true, Traces: true,
			}))

			// The final export cannot succeed; a short deadline ends it.
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()

			_ = s.Shutdown(ctx) // the failed final export is not under test
		}, ""},
		// OTLP export as well, so the providers' goroutines are running
		// when binding the listener, New's last step, fails: New must
		// stop them.
		{"Prometheus on an address already in use, New fails", func(t *testing.T) {
			ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = ln.Close() })

			if _, err := otelsetup.New(context.Background(),
				otelsetup.WithPrometheusConfig(otelsetup.PrometheusConfig{Enabled: true, Addr: ln.Addr().String()}),
				otelsetup.WithOTLP(otelsetup.OTLPConfig{
					Endpoint: newHTTPCollector(t, false).url, Protocol: "http/protobuf", Metrics: true, Traces: true,
				}),
			); err == nil {
				t.Fatal("New = nil on an address in use")
			}
		}, ""},
		{"Prometheus on 127.0.0.1:0, New and never Shutdown", func(t *testing.T) {
			s := newSetup(t, otelsetup.WithPrometheusConfig(local))
			t.Cleanup(func() { shutdown(t, s) }) // after the check, so the binary stays green
		}, metricsServeGoroutine},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.leak == "" {
				checkGoroutines(t)
				tt.run(t)

				return
			}

			before := goleak.IgnoreCurrent()
			tt.run(t)

			if err := findLeaks(before); err == nil || !strings.Contains(err.Error(), tt.leak) {
				t.Fatalf("leak report = %v, want one naming %s", err, tt.leak)
			}
		})
	}
}
