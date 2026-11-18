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

// TestStart_Twice covers server-lifecycle / Scenario: Start twice.
func TestStart_Twice(t *testing.T) {
	t.Parallel()

	t.Run("is running", func(t *testing.T) {
		t.Parallel()

		srv := newServer(t)
		if err := srv.Register(okRoute("/ping")); err != nil {
			t.Fatal(err)
		}
		base := startServer(t, srv)

		if err := srv.Start(context.Background()); !errors.Is(err, srvr.ErrServerStarted) {
			t.Fatalf("second Start = %v, want ErrServerStarted", err)
		}
		if got := get(t, base+"/ping").status; got != http.StatusOK {
			t.Fatalf("GET /ping = %d, want 200: the first server must keep serving", got)
		}
	})

	t.Run("is starting in another goroutine at the same time", func(t *testing.T) {
		t.Parallel()

		srv := newServer(t)
		if err := srv.Register(okRoute("/ping")); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		const n = 8
		results := make(chan error, n)
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() { results <- srv.Start(ctx) })
		}

		// All but one Start return at once.
		for range n - 1 {
			select {
			case err := <-results:
				if !errors.Is(err, srvr.ErrServerStarted) {
					t.Fatalf("concurrent Start = %v, want ErrServerStarted", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("concurrent Start did not return")
			}
		}

		<-srv.Ready()
		if got := get(t, "http://"+srv.Addr()+"/ping").status; got != http.StatusOK {
			t.Fatalf("GET /ping = %d, want 200", got)
		}

		cancel()
		wg.Wait()
		if err := <-results; err != nil {
			t.Fatalf("serving Start = %v, want nil", err)
		}
	})

	t.Run("has stopped", func(t *testing.T) {
		t.Parallel()

		srv := newServer(t)
		ctx, cancel := context.WithCancel(context.Background())
		done := startWith(ctx, t, srv)
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		addr := srv.Addr()

		if err := srv.Start(context.Background()); !errors.Is(err, srvr.ErrServerClosed) {
			t.Fatalf("Start after stop = %v, want ErrServerClosed", err)
		}

		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/", nil)
		if res, err := clientFor(req.URL.String()).Do(req); err == nil {
			res.Body.Close()
			t.Fatal("a request after stop was served: the server must stay stopped")
		}
	})
}
