package fibersrvr_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
)

// TestFiberDrain covers fiber-engine / Scenario: Fiber drain.
func TestFiberDrain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		work     time.Duration // 0: one idle keep-alive connection
		deadline time.Duration
		want     error
	}{
		{"idle keep-alive", 0, time.Second, nil},
		{"short request", 10 * time.Millisecond, time.Second, nil},
		{"long request", 5 * time.Second, 50 * time.Millisecond, context.DeadlineExceeded},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t)
			started := make(chan struct{}, 1)
			if err := srv.Register(routes(func(r fiber.Router) {
				r.Get("/w", func(c fiber.Ctx) error {
					started <- struct{}{}
					select {
					case <-time.After(tt.work):
					case <-c.Context().Done():
					}

					return c.SendStatus(http.StatusOK)
				})
			})); err != nil {
				t.Fatal(err)
			}

			done := make(chan error, 1)
			go func() { done <- srv.Start(context.Background()) }()
			<-srv.Ready()

			// One client connection of its own, kept alive.
			client := &http.Client{Transport: &http.Transport{DialContext: (&net.Dialer{}).DialContext, MaxIdleConnsPerHost: 1}}
			defer client.CloseIdleConnections()

			request := func() {
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+srv.Addr()+"/w", nil)
				if res, err := client.Do(req); err == nil {
					_ = res.Body.Close()
				}
			}

			if tt.work == 0 {
				request() // leaves the connection idle
				<-started
			} else {
				go request()
				<-started
			}

			ctx, cancel := context.WithTimeout(context.Background(), tt.deadline)
			defer cancel()

			if err := srv.Shutdown(ctx); !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Fatalf("Shutdown = %v, want %v", err, tt.want)
			}
			if n := srv.OpenConns(); n != 0 {
				t.Fatalf("%d server connections open after Shutdown", n)
			}

			if err := <-done; err != nil {
				t.Errorf("Start = %v", err)
			}
		})
	}
}
