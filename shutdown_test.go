package srvr_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

// TestShutdown_Drain covers server-lifecycle / Scenario: Drain within and beyond the deadline.
func TestShutdown_Drain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		work     time.Duration // 0: no request in flight
		deadline time.Duration
		wantErr  error
		outcome  int // in-flight status; -1: connection closed
	}{
		{"work 10ms deadline 1s", 10 * time.Millisecond, time.Second, nil, http.StatusOK},
		{"work 5s deadline 50ms", 5 * time.Second, 50 * time.Millisecond, context.DeadlineExceeded, -1},
		{"no request deadline 1s", 0, time.Second, nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			started, release := make(chan struct{}, 1), make(chan struct{})
			t.Cleanup(func() { close(release) })

			srv := newServer(t)
			if err := srv.Register(workRoute(tt.work, started, release)); err != nil {
				t.Fatal(err)
			}
			done := startBackground(t, srv)

			var status <-chan int
			if tt.work > 0 {
				status = inFlight("http://" + srv.Addr() + "/work")
				<-started
			}

			ctx, cancel := context.WithTimeout(context.Background(), tt.deadline)
			defer cancel()

			if err := srv.Shutdown(ctx); !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Shutdown = %v, want %v", err, tt.wantErr)
			}
			if status != nil {
				if got := <-status; got != tt.outcome {
					t.Fatalf("in-flight = %d, want %d", got, tt.outcome)
				}
			}
			if err := <-done; err != nil {
				t.Fatalf("Start = %v, want nil", err)
			}
		})
	}
}

// TestShutdown_Misuse covers server-lifecycle / Scenario: Shutdown misuse.
func TestShutdown_Misuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state string
		// shutdown brings the server into state and calls Shutdown, returning its result.
		shutdown func(t *testing.T, srv *srvr.Server) error
	}{
		{
			state: "was never started",
			shutdown: func(_ *testing.T, srv *srvr.Server) error {
				return srv.Shutdown(context.Background())
			},
		},
		{
			state: "was already shut down",
			shutdown: func(t *testing.T, srv *srvr.Server) error {
				t.Helper()
				done := startBackground(t, srv)
				if err := srv.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
				mustNil(t, <-done)

				return srv.Shutdown(context.Background())
			},
		},
		{
			state: "is being shut down by another goroutine",
			shutdown: func(t *testing.T, srv *srvr.Server) error {
				t.Helper()
				started, release := make(chan struct{}, 1), make(chan struct{})
				if err := srv.Register(workRoute(time.Hour, started, release)); err != nil {
					t.Fatal(err)
				}
				done := startBackground(t, srv)
				status := inFlight("http://" + srv.Addr() + "/work")
				<-started

				var wg sync.WaitGroup
				first := make(chan error, 1)
				wg.Go(func() { first <- srv.Shutdown(context.Background()) })

				// Wait until the first Shutdown is draining: new connections are refused.
				waitRefused(t, srv.Addr())

				second := make(chan error, 1)
				wg.Go(func() { second <- srv.Shutdown(context.Background()) })

				select {
				case err := <-second:
					t.Fatalf("second Shutdown returned %v before the first finished", err)
				case <-time.After(50 * time.Millisecond):
				}

				close(release)
				wg.Wait()
				<-status
				mustNil(t, <-done)
				if err := <-first; err != nil {
					t.Fatalf("first Shutdown = %v", err)
				}

				return <-second
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			if err := tt.shutdown(t, srv); err != nil {
				t.Fatalf("Shutdown = %v, want nil", err)
			}
			if err := srv.Start(context.Background()); !errors.Is(err, srvr.ErrServerClosed) {
				t.Fatalf("later Start = %v, want ErrServerClosed", err)
			}
		})
	}
}

// TestShutdown_NilContext covers server-lifecycle / Scenario: Shutdown with a nil context.
func TestShutdown_NilContext(t *testing.T) {
	t.Parallel()

	srv := newServer(t)
	if err := srv.Register(okRoute("/ping")); err != nil {
		t.Fatal(err)
	}
	base := startServer(t, srv)

	//nolint:staticcheck // a nil context is the row under test.
	if err := srv.Shutdown(nil); !errors.Is(err, srvr.ErrConfig) {
		t.Fatalf("Shutdown(nil) = %v, want ErrConfig", err)
	}
	if got := get(t, base+"/ping").status; got != http.StatusOK {
		t.Fatalf("GET /ping = %d, want 200: the server must keep serving", got)
	}
}
