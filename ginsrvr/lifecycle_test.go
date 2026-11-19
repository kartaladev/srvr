package ginsrvr_test

import (
	"context"
	"errors"
	"io"
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
		call  func(context.Context, *ginsrvr.Server) error
		want  error
	}{
		{"Start while running", "running", func(ctx context.Context, s *ginsrvr.Server) error { return s.Start(ctx) }, srvr.ErrServerStarted},
		{"Shutdown never started", "new", func(ctx context.Context, s *ginsrvr.Server) error { return s.Shutdown(ctx) }, nil},
		{"Start after stop", "stopped", func(ctx context.Context, s *ginsrvr.Server) error { return s.Start(ctx) }, srvr.ErrServerClosed},
		{"Register while running", "running", func(_ context.Context, s *ginsrvr.Server) error { return s.Register(routes(func(*gin.RouterGroup) {})) }, srvr.ErrServerStarted},
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
		{"Shutdown while serving, then again", func(t *testing.T, srv *ginsrvr.Server) {
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
		{"Shutdown never started", func(t *testing.T, srv *ginsrvr.Server) {
			t.Helper()

			if err := srv.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
		}, after},
		{"Start(nil), then Shutdown", func(t *testing.T, srv *ginsrvr.Server) {
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

	t.Run("Start fails because the address is in use", func(t *testing.T) {
		t.Parallel()

		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()

		rec := &hookRecorder{}
		srv, err := ginsrvr.New(append(rec.options(), srvr.WithAddr(ln.Addr().String()), srvr.WithLogOutput(&logBuffer{}))...)
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

	for _, tt := range tests { //nolint:paralleltest // gin's mode is process-global.
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
// gin_debug_mode warning, counted in LogRecordsLost, and construction
// still succeeds.
func TestGinDebugWarning_PanickingHandler(t *testing.T) { //nolint:paralleltest // gin's mode is process-global.
	checkGoroutines(t)
	gin.SetMode(gin.DebugMode)
	t.Cleanup(func() { gin.SetMode(gin.TestMode) })

	before := srvr.LogRecordsLost()

	srv, err := ginsrvr.New(srvr.WithLogHandler(panicHandler{}))
	if err != nil || srv == nil {
		t.Fatalf("New = %v, %v", srv, err)
	}

	if srvr.LogRecordsLost() <= before {
		t.Fatal("LogRecordsLost did not increase")
	}
}

// TestShutdown_StuckListener covers server-lifecycle / Scenario: Stuck
// listener on every engine, the gin row.
func TestShutdown_StuckListener(t *testing.T) {
	t.Parallel()

	tests := []struct{ engine string }{{"gin"}}

	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			t.Parallel()

			stuck := &stuckListener{Listener: listen(t), accepting: make(chan struct{}), release: make(chan struct{})}
			release := sync.OnceFunc(func() { close(stuck.release) })
			t.Cleanup(release)

			srv, err := ginsrvr.New(srvr.WithListener(stuck), srvr.WithShutdownTimeout(200*time.Millisecond), srvr.WithLogOutput(io.Discard))
			if err != nil {
				t.Fatal(err)
			}
			if err := srv.Register(routes(func(g *gin.RouterGroup) { g.GET("/ok", ok) })); err != nil {
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

// listen opens a loopback listener on an ephemeral port, closed when the
// test ends.
func listen(t *testing.T) net.Listener {
	t.Helper()

	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	return ln
}
