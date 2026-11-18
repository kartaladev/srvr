package srvr_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

// inFlight sends GET url in the background and returns a channel with the
// status, or -1 when the request failed.
func inFlight(url string) <-chan int {
	out := make(chan int, 1)

	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		res, err := clientFor(req.URL.String()).Do(req)
		if err != nil {
			out <- -1

			return
		}
		res.Body.Close()
		out <- res.StatusCode
	}()

	return out
}

// TestStart_Stop covers server-lifecycle / Scenario: Start and stop by context.
func TestStart_Stop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		trigger string
		// run returns the Start context and a function that triggers the stop.
		run func(t *testing.T, srv *srvr.Server) (context.Context, func())
	}{
		{
			trigger: "the Start context is cancelled",
			run: func(t *testing.T, _ *srvr.Server) (context.Context, func()) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())

				return ctx, cancel
			},
		},
		{
			trigger: "Shutdown(ctx) is called from another goroutine",
			run: func(_ *testing.T, srv *srvr.Server) (context.Context, func()) {
				return context.Background(), func() {
					go func() { _ = srv.Shutdown(context.Background()) }()
				}
			},
		},
		{
			trigger: "the Start context deadline passes",
			run: func(t *testing.T, _ *srvr.Server) (context.Context, func()) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				t.Cleanup(cancel)

				return ctx, func() { <-ctx.Done() }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.trigger, func(t *testing.T) {
			t.Parallel()

			started, release := make(chan struct{}, 1), make(chan struct{})
			srv := newServer(t)
			if err := srv.Register(workRoute(time.Hour, started, release)); err != nil {
				t.Fatal(err)
			}

			ctx, trigger := tt.run(t, srv)
			done := startWith(ctx, t, srv)

			status := inFlight("http://" + srv.Addr() + "/work")
			<-started
			trigger()
			// Start must still be draining: the request is in flight.
			select {
			case err := <-done:
				t.Fatalf("Start = %v before the in-flight request finished", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(release)

			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Start = %v, want nil", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Start did not return")
			}

			if got := <-status; got != http.StatusOK {
				t.Fatalf("in-flight status = %d, want 200", got)
			}
		})
	}
}
