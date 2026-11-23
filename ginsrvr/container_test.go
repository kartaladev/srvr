package ginsrvr_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// containerWait bounds every wait on the server under test.
const containerWait = 10 * time.Second

// pingServer is newServer with the route GET /ping answering 200.
func pingServer(t *testing.T, opts ...srvr.Option) *ginsrvr.Server {
	t.Helper()

	srv, _ := newServer(t, opts...)
	mustNil(t, srv.Register(routes(func(r *gin.RouterGroup) { r.GET("/ping", ok) })))

	return srv
}

// within returns fn's result, failing t when fn does not return within d.
func within[T any](t *testing.T, d time.Duration, msg string, fn func() T) T {
	t.Helper()

	ch := make(chan T, 1)
	go func() { ch <- fn() }()

	select {
	case v := <-ch:
		return v
	case <-time.After(d):
		t.Fatalf("after %s: %s", d, msg)

		var zero T

		return zero
	}
}

// startReturned starts srv with ctx, fails t unless Start returns nil, shuts
// srv down when t ends and returns its base URL.
func startReturned(ctx context.Context, t *testing.T, srv *ginsrvr.Server) string {
	t.Helper()

	if err := within(t, containerWait, "Start did not return", func() error { return srv.Start(ctx) }); err != nil {
		t.Fatalf("Start = %v, want nil", err)
	}

	t.Cleanup(func() { //nolint:contextcheck // cleanups run after t.Context() is cancelled, so the stop uses its own context
		if err := within(t, containerWait, "Shutdown did not return", func() error { return srv.Shutdown(context.Background()) }); err != nil {
			t.Errorf("Shutdown = %v at cleanup", err)
		}
	})

	registerClient(t, srv.Addr()) // its cleanup runs before the one above

	return "http://" + srv.Addr()
}

// TestStart_ReturnsWhileServing covers server-lifecycle / Scenario: Start
// returns while serving, gin rows.
func TestStart_ReturnsWhileServing(t *testing.T) {
	t.Parallel()

	for _, cancelStart := range []bool{false, true} {
		name := "nothing"
		if cancelStart {
			name = "the Start context is cancelled"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background()) //nolint:usetesting // the server must outlive the context under test
			defer cancel()

			srv := pingServer(t)
			base := startReturned(ctx, t, srv)

			if cancelStart {
				cancel()
			}

			if got := get(t, base+"/ping").status; got != http.StatusOK {
				t.Fatalf("GET /ping = %d, want 200", got)
			}

			select {
			case <-srv.Ready():
			default:
				t.Fatal("Ready is not closed")
			}
			if err := srv.HealthCheck(t.Context()); err != nil {
				t.Fatalf("HealthCheck = %v, want nil", err)
			}
		})
	}
}

// TestRun_StopByContext covers server-lifecycle / Scenario: Run and stop by
// context, gin row: the Run context is cancelled while a request is in
// flight.
func TestRun_StopByContext(t *testing.T) {
	t.Parallel()

	started, release := make(chan struct{}), make(chan struct{})
	srv, _ := newServer(t)
	mustNil(t, srv.Register(routes(func(r *gin.RouterGroup) {
		r.GET("/work", func(c *gin.Context) { close(started); <-release; c.Status(http.StatusOK) })
	})))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	select {
	case <-srv.Ready():
	case err := <-done:
		t.Fatalf("Run returned before readiness: %v", err)
	case <-time.After(containerWait):
		t.Fatal("not ready")
	}

	status := make(chan int, 1)
	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+srv.Addr()+"/work", nil) //nolint:usetesting // the request must outlive the cancelled Run context
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			status <- -1

			return
		}
		_ = res.Body.Close()
		status <- res.StatusCode
	}()

	<-started
	cancel()
	select {
	case err := <-done:
		t.Fatalf("Run = %v before the in-flight request finished", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)

	if err := within(t, containerWait, "Run did not return", func() error { return <-done }); err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if got := <-status; got != http.StatusOK {
		t.Fatalf("in-flight status = %d, want 200", got)
	}
	http.DefaultClient.CloseIdleConnections()
}

// TestWait_AfterStart covers server-lifecycle / Scenario: Wait after Start,
// gin row: Shutdown returns nil.
func TestWait_AfterStart(t *testing.T) {
	t.Parallel()

	srv := pingServer(t)
	if err := within(t, containerWait, "Start did not return", func() error { return srv.Start(t.Context()) }); err != nil {
		t.Fatalf("Start = %v", err)
	}
	mustNil(t, srv.Shutdown(t.Context()))

	if err := within(t, containerWait, "Wait did not return", srv.Wait); err != nil {
		t.Fatalf("Wait = %v, want nil", err)
	}
}

// faultListener delegates to a real listener until fail is called; then
// the pending and later Accept calls return err.
type faultListener struct {
	net.Listener

	err     error
	trigger chan struct{}
}

// fail makes Accept return the fault, waking a pending Accept with a dial.
func (l *faultListener) fail() {
	close(l.trigger)

	if conn, err := net.Dial("tcp", l.Addr().String()); err == nil {
		_ = conn.Close()
	}
}

func (l *faultListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()

	select {
	case <-l.trigger:
		if conn != nil {
			_ = conn.Close()
		}

		return nil, l.err
	default:
		return conn, err
	}
}

var errAccept = errors.New("accept failed permanently")

// TestServeFailure_AfterStart covers server-lifecycle / Scenario: Listener
// fails while serving, gin rows.
func TestServeFailure_AfterStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fault string
		err   error
		want  error
	}{
		{"returns a permanent error E", errAccept, errAccept},
	}

	for _, tt := range tests {
		t.Run(tt.fault, func(t *testing.T) {
			t.Parallel()

			var h hookRecorder
			ln := &faultListener{Listener: listen(t), err: tt.err, trigger: make(chan struct{})}
			logs := &logBuffer{}
			srv, err := ginsrvr.New(append(h.options(), srvr.WithListener(ln), srvr.WithLogOutput(logs))...)
			mustNil(t, err)
			if err := within(t, containerWait, "Start did not return", func() error { return srv.Start(t.Context()) }); err != nil {
				t.Fatalf("Start = %v", err)
			}

			ln.fail()

			if err := within(t, containerWait, "Wait did not return", srv.Wait); !errors.Is(err, tt.want) {
				t.Fatalf("Wait = %v, want an error matching %v", err, tt.want)
			}
			if n := len(logs.withMsg(t, "server.failed")); n != 1 {
				t.Fatalf("server.failed records = %d, want 1:\n%s", n, logs.String())
			}
			if got := h.recorded(); len(got) != 2 || got[0] != "BeforeDrain" || got[1] != "AfterDrain" {
				t.Fatalf("hooks = %v, want [BeforeDrain AfterDrain]", got)
			}
			if err := srv.HealthCheck(t.Context()); !errors.Is(err, tt.want) || !errors.Is(err, srvr.ErrServerClosed) {
				t.Fatalf("HealthCheck = %v, want an error matching %v and ErrServerClosed", err, tt.want)
			}
			if err := srv.Shutdown(t.Context()); err != nil {
				t.Fatalf("later Shutdown = %v, want nil", err)
			}
			if err := srv.Start(t.Context()); !errors.Is(err, srvr.ErrServerClosed) {
				t.Fatalf("later Start = %v, want ErrServerClosed", err)
			}
		})
	}
}

// TestHealthCheck covers server-lifecycle / Scenario: Health across the
// lifecycle, gin rows, and the HealthCheck rows of engine-support /
// Scenario: Lifecycle misuse on every engine.
func TestHealthCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state string
		ctx   func(t *testing.T) context.Context
		want  error // nil: HealthCheck returns nil
	}{
		{state: "was never started", want: srvr.ErrServerNotStarted},
		{state: "is serving after Start"},
		{state: "has stopped", want: srvr.ErrServerClosed},
		{state: "is running, HealthCheck(nil)", ctx: func(*testing.T) context.Context { return nil }, want: srvr.ErrConfig},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			t.Parallel()

			srv := pingServer(t)
			switch tt.state {
			case "is serving after Start", "is running, HealthCheck(nil)":
				startReturned(t.Context(), t, srv)
			case "has stopped":
				mustNil(t, srv.Start(t.Context()))
				mustNil(t, srv.Shutdown(t.Context()))
			}

			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(t)
			}

			err := within(t, 10*time.Millisecond, "HealthCheck did not return within 10ms", func() error { return srv.HealthCheck(ctx) })
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("HealthCheck = %v, want %v", err, tt.want)
			}
		})
	}
}
