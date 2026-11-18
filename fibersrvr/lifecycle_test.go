package fibersrvr_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
)

// TestFiberLifecycle covers engine-support / Scenario: Lifecycle misuse on
// every engine, fiber rows.
func TestFiberLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state string
		call  func(*fibersrvr.Server) error
		want  error
	}{
		{"Start while running", "running", func(s *fibersrvr.Server) error { return s.Start(context.Background()) }, srvr.ErrServerStarted},
		{"Shutdown never started", "new", func(s *fibersrvr.Server) error { return s.Shutdown(context.Background()) }, nil},
		{"Start after stop", "stopped", func(s *fibersrvr.Server) error { return s.Start(context.Background()) }, srvr.ErrServerClosed},
		{"Register while running", "running", func(s *fibersrvr.Server) error { return s.Register(routes(func(fiber.Router) {})) }, srvr.ErrServerStarted},
		{"Register after stop", "stopped", func(s *fibersrvr.Server) error { return s.Register(routes(func(fiber.Router) {})) }, srvr.ErrServerClosed},
		{"Shutdown(nil) while running", "running", func(s *fibersrvr.Server) error {
			err := s.Shutdown(nil) //nolint:staticcheck // a nil context is the row under test.
			if !errors.Is(err, srvr.ErrConfig) {
				return err
			}
			select {
			case <-s.Ready():
				return nil
			default:
				return errors.New("not serving")
			}
		}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t)

			switch tt.state {
			case "running":
				start(t, srv)
			case "stopped":
				if err := srv.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
			}

			if err := tt.call(srv); !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
				t.Fatalf("call = %v, want %v", err, tt.want)
			}
		})
	}
}

// hookRecorder records the shutdown hooks that ran, in order.
type hookRecorder struct {
	mu    sync.Mutex
	calls []string
}

func (h *hookRecorder) options() []srvr.Option {
	record := func(name string) func(context.Context) error {
		return func(context.Context) error {
			h.mu.Lock()
			defer h.mu.Unlock()

			h.calls = append(h.calls, name)

			return nil
		}
	}

	return []srvr.Option{
		srvr.WithShutdownHook(srvr.BeforeDrain, record("BeforeDrain")),
		srvr.WithShutdownHook(srvr.AfterDrain, record("AfterDrain")),
	}
}

func (h *hookRecorder) recorded() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	return slices.Clone(h.calls)
}

// TestFiberShutdownHooks covers engine-support / Scenario: Hooks per engine
// and stop path, fiber rows.
func TestFiberShutdownHooks(t *testing.T) {
	t.Parallel()

	both := []string{"BeforeDrain", "AfterDrain"}
	after := []string{"AfterDrain"}

	tests := []struct {
		name string
		stop func(t *testing.T, srv *fibersrvr.Server)
		want []string
	}{
		{"Start context cancelled while serving", func(t *testing.T, srv *fibersrvr.Server) {
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- srv.Start(ctx) }()
			<-srv.Ready()
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}, both},
		{"Shutdown while serving, then again", func(t *testing.T, srv *fibersrvr.Server) {
			done := make(chan error, 1)
			go func() { done <- srv.Start(context.Background()) }()
			<-srv.Ready()
			if err := srv.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := srv.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}, both},
		{"Shutdown never started", func(t *testing.T, srv *fibersrvr.Server) {
			if err := srv.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		}, after},
		{"Start(nil), then Shutdown", func(t *testing.T, srv *fibersrvr.Server) {
			if err := srv.Start(nil); !errors.Is(err, srvr.ErrConfig) { //nolint:staticcheck // a nil context is the row under test.
				t.Fatalf("Start(nil) = %v", err)
			}
			if err := srv.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		}, after},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &hookRecorder{}
			srv, _ := newServer(t, rec.options()...)

			tt.stop(t, srv)

			if got := rec.recorded(); !slices.Equal(got, tt.want) {
				t.Fatalf("hooks = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("forced drain", func(t *testing.T) {
		t.Parallel()

		rec := &hookRecorder{}
		srv, _ := newServer(t, rec.options()...)
		started := make(chan struct{})
		if err := srv.Register(routes(func(r fiber.Router) {
			r.Get("/slow", func(c fiber.Ctx) error {
				close(started)
				select {
				case <-time.After(5 * time.Second):
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

		go func() {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+srv.Addr()+"/slow", nil)
			if res, err := clientFor(req.URL.String()).Do(req); err == nil {
				_ = res.Body.Close()
			}
		}()
		<-started

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Shutdown = %v, want DeadlineExceeded", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Start = %v", err)
		}

		if got := rec.recorded(); !slices.Equal(got, both) {
			t.Fatalf("hooks = %v, want %v", got, both)
		}
	})

	t.Run("second Start, then Shutdown", func(t *testing.T) {
		t.Parallel()

		rec := &hookRecorder{}
		srv, _ := newServer(t, rec.options()...)
		done := make(chan error, 1)
		go func() { done <- srv.Start(context.Background()) }()
		<-srv.Ready()

		if err := srv.Start(context.Background()); !errors.Is(err, srvr.ErrServerStarted) {
			t.Fatalf("second Start = %v", err)
		}
		if err := srv.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Errorf("Start = %v", err)
		}

		if got := rec.recorded(); !slices.Equal(got, both) {
			t.Fatalf("hooks = %v, want %v", got, both)
		}
	})

	t.Run("Start fails because the address is in use", func(t *testing.T) {
		t.Parallel()

		ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()

		rec := &hookRecorder{}
		srv, err := fibersrvr.New(append(rec.options(), srvr.WithAddr(ln.Addr().String()), srvr.WithLogOutput(&logBuffer{}))...)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := srv.Start(ctx); err == nil {
			t.Fatal("Start succeeded on a used address")
		}

		if got := rec.recorded(); !slices.Equal(got, after) {
			t.Fatalf("hooks = %v, want %v", got, after)
		}
	})
}
