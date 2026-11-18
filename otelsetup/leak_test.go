package otelsetup_test

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kartaladev/srvr/otelsetup"
	"go.uber.org/goleak"
)

// leakOptions are the goleak options of the otelsetup test binary. Nothing in
// it is a deliberate long-lived goroutine, so it ignores nothing.
var leakOptions []goleak.Option

// TestMain fails the test binary when a goroutine is still running after
// its tests passed.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m, leakOptions...) }

// findLeaks runs goleak.Find with leakOptions plus extra, without
// appending to the shared leakOptions slice.
func findLeaks(extra ...goleak.Option) error {
	return goleak.Find(slices.Concat(leakOptions, extra)...)
}

// checkGoroutines fails t when a goroutine started since this call is
// still running after t and all its cleanups end. Call it first in the
// test: the check is registered as t's first cleanup, so it runs last,
// after the cleanups that stop servers and close clients. Only for tests
// that are not parallel: a parallel test would see the goroutines of the
// tests running alongside it.
func checkGoroutines(t *testing.T) {
	t.Helper()

	current := goleak.IgnoreCurrent()
	t.Cleanup(func() {
		if err := findLeaks(current); err != nil {
			t.Error(err)
		}
	})
}

// blockUntil blocks until ch is closed. It is a named function so a leak
// report names it.
func blockUntil(ch <-chan struct{}) { <-ch }

// startBlocked starts a goroutine running blockUntil and returns the
// function that ends it.
func startBlocked() (release func()) {
	ch := make(chan struct{})
	go blockUntil(ch)

	return func() { close(ch) }
}

// TestLeakCheck proves the leak check this module's test binary runs
// reports a goroutine left running. Not parallel: goleak.Find sees every
// goroutine of the process.
func TestLeakCheck(t *testing.T) {
	tests := []struct {
		name  string
		start func(t *testing.T) (release func())
		want  string // a function the report names; "" for no report
	}{
		{"blocked on a channel that is never closed", func(*testing.T) func() { return startBlocked() }, "blockUntil"},
		{"blocked on a channel the test closes at cleanup", func(t *testing.T) func() {
			t.Cleanup(startBlocked())
			return nil
		}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := goleak.IgnoreCurrent()

			var release func()
			t.Run("run", func(t *testing.T) { release = tt.start(t) })

			err := findLeaks(before)
			if release != nil {
				release()
			}

			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("leak reported: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("leak report = %v, want one naming %s", err, tt.want)
			}
		})
	}
}

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
