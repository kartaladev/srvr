package otelsrvr_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

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
