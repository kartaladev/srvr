package fibersrvr_test

import (
	"context"
	"errors"
	"fmt"
	"io"
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
		call  func(context.Context, *fibersrvr.Server) error
		want  error
	}{
		{"Start while running", "running", func(ctx context.Context, s *fibersrvr.Server) error { return s.Start(ctx) }, srvr.ErrServerStarted},
		{"Shutdown never started", "new", func(ctx context.Context, s *fibersrvr.Server) error { return s.Shutdown(ctx) }, nil},
		{"Start after stop", "stopped", func(ctx context.Context, s *fibersrvr.Server) error { return s.Start(ctx) }, srvr.ErrServerClosed},
		{"Register while running", "running", func(_ context.Context, s *fibersrvr.Server) error { return s.Register(routes(func(fiber.Router) {})) }, srvr.ErrServerStarted},
		{"Register after stop", "stopped", func(_ context.Context, s *fibersrvr.Server) error { return s.Register(routes(func(fiber.Router) {})) }, srvr.ErrServerClosed},
		{"Shutdown(nil) while running", "running", func(_ context.Context, s *fibersrvr.Server) error {
			err := s.Shutdown(nil) //nolint:contextcheck,staticcheck // a nil context is the row under test.
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
				if err := srv.Shutdown(t.Context()); err != nil {
					t.Fatal(err)
				}
			}

			if err := tt.call(t.Context(), srv); !errors.Is(err, tt.want) || (tt.want == nil && err != nil) {
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
			t.Helper()

			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- srv.Start(ctx) }()
			<-srv.Ready()
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}, both},
		{"Shutdown while serving, then again", func(t *testing.T, srv *fibersrvr.Server) {
			t.Helper()

			done := make(chan error, 1)
			go func() { done <- srv.Start(t.Context()) }()
			<-srv.Ready()
			if err := srv.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := srv.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}, both},
		{"Shutdown never started", func(t *testing.T, srv *fibersrvr.Server) {
			t.Helper()

			if err := srv.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
		}, after},
		{"Start(nil), then Shutdown", func(t *testing.T, srv *fibersrvr.Server) {
			t.Helper()

			if err := srv.Start(nil); !errors.Is(err, srvr.ErrConfig) { //nolint:staticcheck // a nil context is the row under test.
				t.Fatalf("Start(nil) = %v", err)
			}
			if err := srv.Shutdown(t.Context()); err != nil {
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
		go func() { done <- srv.Start(t.Context()) }()
		<-srv.Ready()

		go func() {
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+srv.Addr()+"/slow", nil) //nolint:usetesting // the request goroutine may outlive the test body.
			if res, err := clientFor(req.URL.String()).Do(req); err == nil {
				_ = res.Body.Close()
			}
		}()
		<-started

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
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
		go func() { done <- srv.Start(t.Context()) }()
		<-srv.Ready()

		if err := srv.Start(t.Context()); !errors.Is(err, srvr.ErrServerStarted) {
			t.Fatalf("second Start = %v", err)
		}
		if err := srv.Shutdown(t.Context()); err != nil {
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

		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()

		rec := &hookRecorder{}
		srv, err := fibersrvr.New(append(rec.options(), srvr.WithAddr(ln.Addr().String()), srvr.WithLogOutput(&logBuffer{}))...)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		if err := srv.Start(ctx); err == nil {
			t.Fatal("Start succeeded on a used address")
		}

		if got := rec.recorded(); !slices.Equal(got, after) {
			t.Fatalf("hooks = %v, want %v", got, after)
		}
	})
}

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
			go func() { done <- srv.Start(t.Context()) }()
			<-srv.Ready()

			// One client connection of its own, kept alive.
			client := &http.Client{Transport: &http.Transport{DialContext: (&net.Dialer{}).DialContext, MaxIdleConnsPerHost: 1}}
			defer client.CloseIdleConnections()

			request := func() {
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+srv.Addr()+"/w", nil) //nolint:usetesting // run in a goroutine that may outlive the test body.
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

			ctx, cancel := context.WithTimeout(t.Context(), tt.deadline)
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

// TestFiberShutdownNeverServed pins the fiber fact design D3 relies on:
// ShutdownWithContext on an app that never served returns nil (fiber's
// ErrNotRunning is only for an app without a fasthttp server, which
// fiber.New always creates).
func TestFiberShutdownNeverServed(t *testing.T) {
	t.Parallel()

	if err := fiber.New().ShutdownWithContext(t.Context()); err != nil {
		t.Fatalf("ShutdownWithContext = %v, want nil", err)
	}
}

// TestFiberContextReuse covers fiber-engine / Scenario: Context reuse: with
// fiber reusing its pooled contexts and buffers, every access record keeps
// its own request's route and trace id.
func TestFiberContextReuse(t *testing.T) {
	t.Parallel()

	const (
		requests = 1000
		routeN   = 10
	)

	for _, immutable := range []bool{false, true} {
		t.Run(fmt.Sprint("immutable ", immutable), func(t *testing.T) {
			t.Parallel()

			var opts []srvr.Option
			if immutable {
				opts = append(opts, fibersrvr.WithFiberConfig(func(c *fiber.Config) { c.Immutable = true }))
			}

			srv, logs := newServer(t, opts...)
			conf := routes(func(r fiber.Router) {
				for i := range routeN {
					r.Get(fmt.Sprintf("/r%d/:id", i), ok)
				}
			})

			// Configure once before the concurrent requests.
			serve(t, srv, conf, http.MethodGet, "/r0/warmup", nil, nil)

			want := map[string]string{} // trace id -> route
			var wg sync.WaitGroup

			for i := range requests {
				traceID := fmt.Sprintf("%032x", i+1)
				route := fmt.Sprintf("/r%d/:id", i%routeN)
				want[traceID] = route

				wg.Go(func() {
					serve(t, srv, conf, http.MethodGet, fmt.Sprintf("/r%d/%d", i%routeN, i), nil,
						map[string]string{"traceparent": "00-" + traceID + "-00f067aa0ba902b7-01"})
				})
			}

			wg.Wait()

			got := 0
			for _, r := range logs.access(t) {
				id, _ := r["trace_id"].(string)
				route, known := want[id]
				if !known {
					continue // the warm-up request
				}

				got++
				if r["http.route"] != route {
					t.Fatalf("record %v: route %v, want %s", id, r["http.route"], route)
				}
			}

			if got != requests {
				t.Fatalf("%d matching records, want %d", got, requests)
			}
		})
	}
}

// TestShutdown_StuckListener covers server-lifecycle / Scenario: Stuck
// listener on every engine, the fiber row.
func TestShutdown_StuckListener(t *testing.T) {
	t.Parallel()

	tests := []struct{ engine string }{{"fiber"}}

	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			t.Parallel()

			ln := loopback(t)
			t.Cleanup(func() { _ = ln.Close() })
			stuck := &stuckListener{Listener: ln, accepting: make(chan struct{}), release: make(chan struct{})}
			release := sync.OnceFunc(func() { close(stuck.release) })
			t.Cleanup(release)

			srv, err := fibersrvr.New(srvr.WithListener(stuck), srvr.WithShutdownTimeout(200*time.Millisecond), srvr.WithLogOutput(io.Discard))
			if err != nil {
				t.Fatal(err)
			}
			if err := srv.Register(routes(func(r fiber.Router) { r.Get("/ok", ok) })); err != nil {
				t.Fatal(err)
			}

			started := make(chan error, 1)
			go func() { started <- srv.Start(context.Background()) }() //nolint:usetesting // Start must outlive the Shutdown calls under test

			select {
			case <-stuck.accepting:
			case err := <-started:
				t.Fatalf("Start returned before Accept: %v", err)
			case <-time.After(10 * time.Second):
				t.Fatal("Serve never called Accept")
			}

			stopped := make(chan error, 1)
			go func() { stopped <- srv.Shutdown(context.Background()) }() //nolint:usetesting // the scenario's context has no deadline

			select {
			case err := <-stopped:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("Shutdown = %v, want the forced drain's context.DeadlineExceeded", err)
				}
			case <-time.After(time.Second):
				t.Fatal("Shutdown did not return within 1s")
			}

			later := make(chan error, 1)
			go func() { later <- srv.Shutdown(context.Background()) }() //nolint:usetesting // as above

			select {
			case err := <-later:
				if err != nil {
					t.Fatalf("later Shutdown = %v, want nil", err)
				}
			case <-time.After(time.Second):
				t.Fatal("later Shutdown did not return within 1s")
			}

			release()

			select {
			case err := <-started:
				if err != nil {
					t.Fatalf("Start = %v after the stop, want nil", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Start did not return after Accept was released")
			}
		})
	}
}

// stuckListener is a listener whose Close does not unblock Accept: Accept
// signals accepting once, then blocks until release is closed.
type stuckListener struct {
	net.Listener
	once      sync.Once
	accepting chan struct{}
	release   chan struct{}
}

func (l *stuckListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.accepting) })
	<-l.release

	return nil, net.ErrClosed
}

func (l *stuckListener) Close() error { return nil }
