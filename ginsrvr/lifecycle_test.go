package ginsrvr_test

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// TestGinLifecycle covers engine-support / Scenario: Lifecycle misuse on
// every engine, gin rows.
func TestGinLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state string
		call  func(*ginsrvr.Server) error
		want  error
	}{
		{"Start while running", "running", func(s *ginsrvr.Server) error { return s.Start(context.Background()) }, srvr.ErrServerStarted},
		{"Shutdown never started", "new", func(s *ginsrvr.Server) error { return s.Shutdown(context.Background()) }, nil},
		{"Start after stop", "stopped", func(s *ginsrvr.Server) error { return s.Start(context.Background()) }, srvr.ErrServerClosed},
		{"Register while running", "running", func(s *ginsrvr.Server) error { return s.Register(routes(func(*gin.RouterGroup) {})) }, srvr.ErrServerStarted},
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

// TestGinShutdownHooks covers engine-support / Scenario: Hooks per engine
// and stop path, gin rows.
func TestGinShutdownHooks(t *testing.T) {
	t.Parallel()

	both := []string{"BeforeDrain", "AfterDrain"}
	after := []string{"AfterDrain"}

	tests := []struct {
		name string
		stop func(t *testing.T, srv *ginsrvr.Server)
		want []string
	}{
		{"Start context cancelled while serving", func(t *testing.T, srv *ginsrvr.Server) {
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- srv.Start(ctx) }()
			<-srv.Ready()
			cancel()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		}, both},
		{"Shutdown while serving, then again", func(t *testing.T, srv *ginsrvr.Server) {
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
		{"Shutdown never started", func(t *testing.T, srv *ginsrvr.Server) {
			if err := srv.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		}, after},
		{"Start(nil), then Shutdown", func(t *testing.T, srv *ginsrvr.Server) {
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

	t.Run("Start fails because the address is in use", func(t *testing.T) {
		t.Parallel()

		ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()

		rec := &hookRecorder{}
		srv, err := ginsrvr.New(append(rec.options(), srvr.WithAddr(ln.Addr().String()), srvr.WithLogOutput(&logBuffer{}))...)
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

// TestGinDebugWarning covers gin-engine / Scenario: Gin mode at
// construction and the gin rows of engine-support / Scenario: Configuration
// warning record. Gin's mode is process-global, so the rows run in order,
// not in parallel, each restoring the test mode.
func TestGinDebugWarning(t *testing.T) { //nolint:paralleltest // gin's mode is process-global.
	tests := []struct {
		mode     string
		warnings int
	}{
		{gin.DebugMode, 1},
		{gin.ReleaseMode, 0},
		{gin.TestMode, 0},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			checkGoroutines(t)
			gin.SetMode(tt.mode)
			t.Cleanup(func() { gin.SetMode(gin.TestMode) })

			srv, logs := newServer(t)
			start(t, srv)

			warnings := logs.withMsg(t, "server.config.warning")
			if len(warnings) != tt.warnings {
				t.Fatalf("%d warnings, want %d", len(warnings), tt.warnings)
			}
			if tt.warnings == 1 && warnings[0]["srvr.warning.code"] != "gin_debug_mode" {
				t.Fatalf("warning %v", warnings[0])
			}
			if gin.Mode() != tt.mode {
				t.Fatalf("gin mode %q after construction, want %q", gin.Mode(), tt.mode)
			}
		})
	}
}

// TestGinDebugWarning_PanickingHandler covers the engine-support consumer
// fault row: in debug mode, a log handler whose Handle panics loses the
// gin_debug_mode warning, counted in LogWriteErrors, and construction
// still succeeds.
func TestGinDebugWarning_PanickingHandler(t *testing.T) { //nolint:paralleltest // gin's mode is process-global.
	checkGoroutines(t)
	gin.SetMode(gin.DebugMode)
	t.Cleanup(func() { gin.SetMode(gin.TestMode) })

	before := srvr.LogWriteErrors()

	srv, err := ginsrvr.New(srvr.WithLogHandler(panicHandler{}))
	if err != nil || srv == nil {
		t.Fatalf("New = %v, %v", srv, err)
	}

	if srvr.LogWriteErrors() <= before {
		t.Fatal("LogWriteErrors did not increase")
	}
}
