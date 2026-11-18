package otelsrvr_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/goleak"
)

// leakOptions are the goleak options of the otelsrvr test binary. Nothing in
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

// TestLifecycle_Goroutines covers test-quality / Scenario: Lifecycle leaves
// no goroutine behind (otelsrvr row). Not parallel: the check sees every
// goroutine of the process.
func TestLifecycle_Goroutines(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"WithTracing and an SDK tracer provider, Start, GET /ok, Shutdown, provider Shutdown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkGoroutines(t)

			// A batching provider: its span processor runs a goroutine of
			// its own until the provider shuts down.
			exp := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))

			obs, err := otelsrvr.New(otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp))
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			okRoute := configFunc(func(g *srvr.Group) {
				g.GET("/ok", func(w http.ResponseWriter, _ *http.Request) error {
					w.WriteHeader(http.StatusOK)

					return nil
				})
			})
			srv, done := startServing(context.Background(), t, okRoute, srvr.WithObserver(obs))
			registerClient(t, srv.Addr())

			if got := send(t, http.MethodGet, "http://"+srv.Addr()+"/ok", ""); got != http.StatusOK {
				t.Fatalf("GET /ok = %d", got)
			}

			if err := srv.Shutdown(context.Background()); err != nil {
				t.Fatalf("Shutdown = %v", err)
			}

			if err := <-done; err != nil {
				t.Fatalf("Start = %v", err)
			}

			// The exporter forgets its spans when the provider shuts it down.
			if err := tp.ForceFlush(context.Background()); err != nil {
				t.Fatalf("ForceFlush = %v", err)
			}

			if n := len(exp.GetSpans()); n != 1 {
				t.Fatalf("%d spans exported, want 1", n)
			}

			if err := tp.Shutdown(context.Background()); err != nil {
				t.Fatalf("provider Shutdown = %v", err)
			}
		})
	}
}
