package otelsetup_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsetup"
	"github.com/kartaladev/srvr/otelsrvr"
)

// TestShutdownOrdering covers telemetry-setup "Ordering with the server",
// all rows: the in-flight request's duration reaches the final export
// because the setup flushes after the drain.
func TestShutdownOrdering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		consumer bool // the consumer calls setup.Shutdown after the server stopped
		viaCtx   bool // the Start context is cancelled instead of calling Shutdown
	}{
		{"Start context cancelled", false, true},
		{"Shutdown", false, false},
		{"Shutdown then the consumer's setup.Shutdown", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector := newHTTPCollector(t, false)

			s, err := otelsetup.New(context.Background(), otelsetup.WithOTLP(otelsetup.OTLPConfig{Endpoint: collector.url, Metrics: true}))
			if err != nil {
				t.Fatal(err)
			}

			obs, err := otelsrvr.New(otelsrvr.WithMeterProvider(s.MeterProvider()))
			if err != nil {
				t.Fatal(err)
			}

			started, release := make(chan struct{}), make(chan struct{})

			srv := hooked(t, s, []srvr.Option{srvr.WithObserver(obs)}, configFunc(func(g *srvr.Group) {
				g.GET("/slow", func(w http.ResponseWriter, _ *http.Request) error {
					close(started)
					<-release
					w.WriteHeader(http.StatusOK)

					return nil
				})
			}))

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan error, 1)

			go func() { done <- srv.Start(ctx) }()
			<-srv.Ready()

			answered := make(chan struct{})

			go func() {
				defer close(answered)

				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+srv.Addr()+"/slow", http.NoBody)
				if res, err := (&http.Client{Transport: &http.Transport{}}).Do(req); err == nil {
					_ = res.Body.Close()
				}
			}()

			<-started

			stopped := make(chan error, 1)
			if tt.viaCtx {
				cancel()
			} else {
				go func() { stopped <- srv.Shutdown(context.Background()) }()
			}

			time.Sleep(20 * time.Millisecond) // the stop is under way, the request still in flight
			close(release)
			<-answered

			if err := <-done; err != nil {
				t.Fatalf("Start = %v", err)
			}

			if !tt.viaCtx {
				if err := <-stopped; err != nil {
					t.Fatalf("Shutdown = %v", err)
				}
			}

			got := collector.received("metrics")
			if !bytes.Contains(got, []byte("http.server.request.duration")) || !bytes.Contains(got, []byte("/slow")) {
				t.Error("the final export lacks the in-flight request's duration")
			}

			if tt.consumer {
				if err := s.Shutdown(context.Background()); err != nil {
					t.Errorf("the consumer's Shutdown = %v, want nil", err)
				}
			}
		})
	}
}
