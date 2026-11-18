package otelsetup_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"github.com/kartaladev/srvr/otelsetup"
)

// listening reports whether the setup's metrics listener accepts
// connections.
func listening(addr string) bool {
	conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return false
	}

	_ = conn.Close()

	return true
}

// TestShutdown covers telemetry-setup "Flush outcomes", the Prometheus
// row, and "Lifecycle misuse", all rows.
func TestShutdown(t *testing.T) {
	t.Parallel()

	t.Run("Prometheus only", func(t *testing.T) {
		t.Parallel()

		s, err := otelsetup.New(context.Background(), otelsetup.WithPrometheusConfig(local))
		if err != nil {
			t.Fatal(err)
		}

		addr := s.MetricsAddr()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		start := time.Now()
		if err := s.Shutdown(ctx); err != nil || time.Since(start) > time.Second {
			t.Fatalf("Shutdown = %v after %v, want nil within 1s", err, time.Since(start))
		}

		if listening(addr) {
			t.Error("the metrics listener is still open")
		}
	})

	t.Run("already shut down", func(t *testing.T) {
		t.Parallel()

		s, err := otelsetup.New(context.Background(), otelsetup.WithPrometheusConfig(local))
		if err != nil {
			t.Fatal(err)
		}

		_ = s.Shutdown(context.Background())
		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatalf("second Shutdown = %v, want nil", err)
		}
	})

	t.Run("concurrent", func(t *testing.T) {
		t.Parallel()

		s, err := otelsetup.New(context.Background(), otelsetup.WithPrometheusConfig(local))
		if err != nil {
			t.Fatal(err)
		}

		addr := s.MetricsAddr()

		var wg sync.WaitGroup

		errs := make([]error, 8)
		for i := range errs {
			wg.Go(func() { errs[i] = s.Shutdown(context.Background()) })
		}

		wg.Wait()

		if err := errors.Join(errs...); err != nil {
			t.Fatalf("Shutdown errors: %v", err)
		}

		if listening(addr) {
			t.Error("a Shutdown returned before the listener closed")
		}
	})
}

// panicking is a configurer that panics.
type panicking struct{}

func (panicking) Configure(*srvr.Group) { panic("boom") }

// TestShutdownHook_NeverServed covers telemetry-setup "Server that never
// served", all rows.
func TestShutdownHook_NeverServed(t *testing.T) {
	t.Parallel()

	taken, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = taken.Close() })

	ok := configFunc(func(g *srvr.Group) {
		g.GET("/x", func(w http.ResponseWriter, _ *http.Request) error { w.WriteHeader(http.StatusOK); return nil })
	})

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		opts []srvr.Option
		conf srvr.RouteConfigurer[*srvr.Group]
		// act runs the event; afterEvent is whether the listener is still
		// open right after it; shutdownRan whether the server's Shutdown
		// then closes it (false: the setup must stay up).
		act                     func(t *testing.T, srv *srvr.Server)
		afterEvent, shutdownRan bool
	}{
		{"never started", nil, ok, func(*testing.T, *srvr.Server) {}, true, true},
		{"bind failure", []srvr.Option{srvr.WithAddr(taken.Addr().String())}, ok, startFails, false, true},
		{"configurer panics", nil, panicking{}, startFails, false, true},
		{"route check fails", []srvr.Option{srvr.WithRouteCheck(func(context.Context, engine.Routes) error { return errors.New("no") })}, ok, startFails, false, true},
		{"cancelled context", nil, ok, func(t *testing.T, srv *srvr.Server) {
			t.Helper()

			if err := srv.Start(cancelled); err == nil {
				t.Fatal("Start with a cancelled context returned nil")
			}
		}, false, true},
		{"Start(nil)", nil, ok, func(t *testing.T, srv *srvr.Server) {
			t.Helper()

			if err := srv.Start(nil); !errors.Is(err, srvr.ErrConfig) { //nolint:staticcheck // the nil context is the case under test.
				t.Fatalf("Start(nil) = %v", err)
			}
		}, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, err := otelsetup.New(context.Background(), otelsetup.WithPrometheusConfig(local))
			if err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

			addr := s.MetricsAddr()
			srv := hooked(t, s, tt.opts, tt.conf)

			tt.act(t, srv)

			if got := listening(addr); got != tt.afterEvent {
				t.Fatalf("listener open after the event = %v, want %v", got, tt.afterEvent)
			}

			if tt.shutdownRan {
				if err := srv.Shutdown(context.Background()); err != nil {
					t.Fatalf("Shutdown = %v", err)
				}

				if listening(addr) {
					t.Error("the metrics listener is open after the server's Shutdown")
				}
			}
		})
	}

	t.Run("second Start while serving", func(t *testing.T) {
		t.Parallel()

		s := setUp(t, otelsetup.WithPrometheusConfig(local))
		srv := hooked(t, s, nil, ok)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)

		go func() { done <- srv.Start(ctx) }()
		<-srv.Ready()

		if err := srv.Start(context.Background()); !errors.Is(err, srvr.ErrServerStarted) {
			t.Fatalf("second Start = %v", err)
		}

		if !listening(s.MetricsAddr()) {
			t.Error("a misused Start shut the setup down")
		}

		cancel()

		if err := <-done; err != nil {
			t.Errorf("Start = %v", err)
		}
	})

	t.Run("Start after Shutdown", func(t *testing.T) {
		t.Parallel()

		s, err := otelsetup.New(context.Background(), otelsetup.WithPrometheusConfig(local))
		if err != nil {
			t.Fatal(err)
		}

		addr := s.MetricsAddr()
		srv := hooked(t, s, nil, ok)

		if err := srv.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}

		if err := srv.Start(context.Background()); !errors.Is(err, srvr.ErrServerClosed) {
			t.Fatalf("Start after Shutdown = %v", err)
		}

		if listening(addr) {
			t.Error("the earlier Shutdown did not shut the setup down")
		}
	})
}

// hooked returns a server with s's shutdown hook, conf and opts.
func hooked(t *testing.T, s *otelsetup.Setup, opts []srvr.Option, conf srvr.RouteConfigurer[*srvr.Group]) *srvr.Server {
	t.Helper()

	srv, err := srvr.New(append([]srvr.Option{srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard), s.ShutdownHook()}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}

	if err := srv.Register(conf); err != nil {
		t.Fatal(err)
	}

	return srv
}

// startFails starts srv and requires an error.
func startFails(t *testing.T, srv *srvr.Server) {
	t.Helper()

	if err := srv.Start(context.Background()); err == nil {
		t.Fatal("Start returned nil")
	}
}
