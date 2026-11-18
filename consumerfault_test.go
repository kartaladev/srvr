package srvr_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/kartaladev/srvr"
)

// MiddlewareAPI registers one route under group middleware whose constructor
// panics with value.
type MiddlewareAPI struct {
	value any
	mount bool
}

func (a *MiddlewareAPI) Configure(g *srvr.Group) {
	g.Use(func(http.Handler) http.Handler { panic(a.value) })

	if a.mount {
		g.Mount("/static/", http.FileServerFS(fstest.MapFS{"a.txt": {Data: []byte("a")}}))

		return
	}

	g.GET("/x", status(http.StatusOK))
}

// TestMiddleware_ConstructorPanics covers route-registration / Scenario:
// Middleware constructor panics.
func TestMiddleware_ConstructorPanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		route string
		api   *MiddlewareAPI
		text  string
	}{
		{`"GET /x"`, &MiddlewareAPI{value: "mw"}, "mw"},
		{`"GET /x"`, &MiddlewareAPI{value: selfPanicError{}}, "<panic in Error(): srvr_test.selfPanicError>"},
		{`"/static/"`, &MiddlewareAPI{value: "mw", mount: true}, "mw"},
	}

	for _, tt := range tests {
		t.Run(tt.route+" "+tt.text, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			mustNil(t, srv.Register(tt.api))

			err := startBounded(t.Context(), t, srv)

			text := safeText(err)
			if !errors.Is(err, srvr.ErrRouteConfig) || errors.Is(err, errPanicked) {
				t.Fatalf("Start = %s, want an error wrapping ErrRouteConfig, without panicking", text)
			}

			for _, want := range []string{tt.route, "*srvr_test.MiddlewareAPI", tt.text} {
				if !strings.Contains(text, want) {
					t.Errorf("Start = %s, want it to contain %s", text, want)
				}
			}

			select {
			case <-srv.Ready():
				t.Fatal("Ready was closed")
			default:
			}

			if err := shutdownBounded(t, srv); err != nil {
				t.Fatalf("later Shutdown = %v, want nil", err)
			}
		})
	}
}

// panicListener delegates to a real listener; its Accept panics with
// acceptPanic after accepted connections, once gate (if any) is closed,
// and its Close panics with closePanic when that is not nil, or else
// returns closeErr after closing the real listener.
type panicListener struct {
	net.Listener

	acceptPanic any
	accepted    int
	gate        chan struct{}
	calls       atomic.Int32
	closePanic  any
	closeErr    error
}

func (l *panicListener) Accept() (net.Conn, error) {
	if l.acceptPanic != nil && int(l.calls.Add(1)) > l.accepted {
		if l.gate != nil {
			<-l.gate
		}

		panic(l.acceptPanic)
	}

	return l.Listener.Accept()
}

func (l *panicListener) Close() error {
	if l.closePanic != nil {
		// Close the real listener first, so the test can see it closed.
		_ = l.Listener.Close()
		panic(l.closePanic)
	}

	err := l.Listener.Close()
	if l.closeErr != nil {
		return l.closeErr
	}

	return err
}

// shutdownWhileRunning starts srv, shuts it down and returns Shutdown's
// result; Start must return nil.
func shutdownWhileRunning(t *testing.T, srv *srvr.Server) error {
	t.Helper()

	done := startBackground(t, srv)
	err := shutdownBounded(t, srv)
	if startErr := receive(t, done, "Start did not return"); startErr != nil {
		t.Errorf("Start = %s, want nil", safeText(startErr))
	}

	return err
}

// cancelWhileRunning starts srv, cancels its context and returns Start's
// result.
func cancelWhileRunning(t *testing.T, srv *srvr.Server) error {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	done := startWith(ctx, t, srv)
	cancel()

	return receive(t, done, "Start did not return")
}

// TestListener_AcceptPanics covers server-lifecycle / Scenario: A listener
// whose Accept panics.
func TestListener_AcceptPanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    any
		accepted int
		text     string
	}{
		{"accept, no connection", "accept", 0, "accept"},
		{"accept, after one answered connection", "accept", 1, "accept"},
		{"self-panicking value, no connection", selfPanicError{}, 0, "<panic in Error(): srvr_test.selfPanicError>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ln := &panicListener{Listener: listen(t), acceptPanic: tt.value, accepted: tt.accepted, gate: make(chan struct{})}
			addr := ln.Addr().String()
			logs := &logBuffer{}

			var c hookCounts
			srv, err := srvr.New(append(c.options(), srvr.WithListener(ln), srvr.WithLogOutput(logs))...)
			mustNil(t, err)
			mustNil(t, srv.Register(okRoute("/x")))

			done := startBackground(t, srv)
			if tt.accepted > 0 {
				if got := get(t, "http://"+addr+"/x").status; got != http.StatusOK {
					t.Fatalf("GET /x = %d, want 200", got)
				}
			}
			close(ln.gate)

			err = receive(t, done, "Start did not return")
			text := safeText(err)
			if !errors.Is(err, srvr.ErrConfig) || errors.Is(err, errPanicked) || !strings.Contains(text, "Listener") || !strings.Contains(text, tt.text) {
				t.Fatalf("Start = %s, want an error wrapping ErrConfig containing Listener and %s, without panicking", text, tt.text)
			}

			failed := logs.find(t, "server.failed")
			if len(failed) != 1 || !strings.Contains(safeText(failed[0]["exception.message"]), tt.text) {
				t.Fatalf("want one server.failed record containing %s, got log %s", tt.text, logs)
			}
			if len(logs.find(t, "server.stopped")) != 1 {
				t.Fatalf("want one server.stopped record, got log %s", logs)
			}
			if b, a := c.before.Load(), c.after.Load(); b != 1 || a != 1 {
				t.Fatalf("hooks ran before=%d after=%d, want 1 and 1", b, a)
			}

			waitRefused(t, addr)

			if err := shutdownBounded(t, srv); err != nil {
				t.Fatalf("later Shutdown = %v, want nil", err)
			}
		})
	}
}

// TestListener_ClosePanics covers server-lifecycle / Scenario: A listener
// whose Close panics.
func TestListener_ClosePanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		call string
		run  func(t *testing.T, srv *srvr.Server) error
		want error
	}{
		{"Shutdown before Start", shutdownBounded, nil},
		{"Start with an already-cancelled context", func(t *testing.T, srv *srvr.Server) error {
			t.Helper()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			return startBounded(ctx, t, srv)
		}, context.Canceled},
		{"Start, where a route configurer panics", func(t *testing.T, srv *srvr.Server) error {
			t.Helper()
			mustNil(t, srv.Register(&PanicAPI{}))

			return startBounded(t.Context(), t, srv)
		}, srvr.ErrRouteConfig},
		{"Shutdown on a running server", shutdownWhileRunning, nil},
		{"Start, after its context ends while running", cancelWhileRunning, nil},
	}

	for _, tt := range tests {
		t.Run(tt.call, func(t *testing.T) {
			t.Parallel()

			ln := &panicListener{Listener: listen(t), closePanic: "close"}
			srv, err := srvr.New(srvr.WithListener(ln), srvr.WithLogOutput(&logBuffer{}))
			mustNil(t, err)

			if err := tt.run(t, srv); !errors.Is(err, tt.want) {
				t.Fatalf("%s = %s, want %v", tt.call, safeText(err), tt.want)
			}

			if err := shutdownBounded(t, srv); err != nil {
				t.Fatalf("later Shutdown = %s, want nil", safeText(err))
			}

			if err := startBounded(t.Context(), t, srv); !errors.Is(err, srvr.ErrServerClosed) {
				t.Fatalf("later Start = %s, want ErrServerClosed", safeText(err))
			}
		})
	}
}

// TestListener_CloseErrors covers server-lifecycle / Scenario: A listener
// whose Close returns an error. Whether http.Server.Shutdown still tracks
// the listener depends on whether Serve has returned, so each row repeats
// the stop to reach both orders.
func TestListener_CloseErrors(t *testing.T) {
	t.Parallel()

	const runs = 200

	tests := []struct {
		call string
		run  func(t *testing.T, srv *srvr.Server) error
	}{
		{"Shutdown on a running server", shutdownWhileRunning},
		{"Start, after its context ends while running", cancelWhileRunning},
	}

	for _, tt := range tests {
		t.Run(tt.call, func(t *testing.T) {
			t.Parallel()

			for i := range runs {
				logs := &logBuffer{}
				ln := &panicListener{Listener: listen(t), closeErr: errors.New("close: secret-dsn")}
				srv, err := srvr.New(srvr.WithListener(ln), srvr.WithLogOutput(logs))
				mustNil(t, err)

				if err := tt.run(t, srv); err != nil {
					t.Fatalf("run %d: %s = %s, want <nil>", i, tt.call, safeText(err))
				}

				stopped := logs.find(t, "server.stopped")
				if len(stopped) != 1 {
					t.Fatalf("run %d: want one server.stopped record, got log %s", i, logs)
				}

				if _, forced := stopped[0]["srvr.drain.forced"]; forced || stopped[0]["level"] != "INFO" {
					t.Fatalf("run %d: server.stopped = %v, want INFO without srvr.drain.forced", i, stopped[0])
				}

				if text := logs.String(); strings.Contains(text, "secret-dsn") {
					t.Fatalf("run %d: log contains the Close error text: %s", i, text)
				}
			}
		})
	}
}

// TestListenerOwnership covers server-lifecycle / Scenario: A passed
// listener is owned by the server.
func TestListenerOwnership(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()

	tests := []struct {
		name string
		// opts are added to New's options.
		opts []srvr.Option
		// call registers what the row needs on srv and makes the call; it
		// returns the call's error. A nil call checks New's error instead.
		call    func(t *testing.T, srv *srvr.Server) error
		want    func(error) bool
		accepts bool
	}{
		{
			name: "a route configurer panics",
			call: func(t *testing.T, srv *srvr.Server) error {
				t.Helper()
				mustNil(t, srv.Register(&PanicAPI{}))

				return srv.Start(t.Context())
			},
			want: func(err error) bool { return errors.Is(err, srvr.ErrRouteConfig) },
		},
		{
			name: "two routes register the same pattern",
			call: func(t *testing.T, srv *srvr.Server) error {
				t.Helper()
				mustNil(t, srv.Register(okRoute("/dup")))
				mustNil(t, srv.Register(okRoute("/dup")))

				return srv.Start(t.Context())
			},
			want: func(err error) bool { return errors.Is(err, srvr.ErrRouteConfig) },
		},
		{
			name: "the Start context is already cancelled",
			call: func(_ *testing.T, srv *srvr.Server) error { return srv.Start(cancelled) },
			want: func(err error) bool { return errors.Is(err, context.Canceled) },
		},
		{
			name: "Shutdown before Start",
			call: func(t *testing.T, srv *srvr.Server) error {
				t.Helper()

				return srv.Shutdown(t.Context())
			},
			want: func(err error) bool { return err == nil },
		},
		{
			name: "served and shut down",
			call: func(t *testing.T, srv *srvr.Server) error {
				t.Helper()
				ctx, stop := context.WithCancel(t.Context())
				done := startWith(ctx, t, srv)
				stop()

				return receive(t, done, "Start did not return after its context was cancelled")
			},
			want: func(err error) bool { return err == nil },
		},
		{
			name: "Start(nil)",
			call: func(_ *testing.T, srv *srvr.Server) error {
				return srv.Start(nil) //nolint:staticcheck // the nil context is the row.
			},
			want:    func(err error) bool { return errors.Is(err, srvr.ErrConfig) },
			accepts: true,
		},
		{
			name:    "New fails because another option is invalid",
			opts:    []srvr.Option{srvr.WithShutdownHook(srvr.AfterDrain, nil)},
			want:    func(err error) bool { return errors.Is(err, srvr.ErrConfig) },
			accepts: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ln := listen(t)
			srv, err := srvr.New(append([]srvr.Option{srvr.WithListener(ln), srvr.WithLogOutput(&logBuffer{})}, tt.opts...)...)
			if tt.call != nil {
				mustNil(t, err)
				err = tt.call(t, srv)
			}
			if !tt.want(err) {
				t.Fatalf("call = %v", err)
			}

			conn, err := new(net.Dialer).DialContext(t.Context(), "tcp", ln.Addr().String())
			if err == nil {
				conn.Close()
			}
			if accepts := err == nil; accepts != tt.accepts {
				t.Fatalf("listener accepts connections = %v, want %v (dial: %v)", accepts, tt.accepts, err)
			}
		})
	}
}

// TestListener_UnusableAddress covers server-lifecycle / Scenario: A
// listener without a usable address.
func TestListener_UnusableAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fault string
		addr  func() net.Addr
	}{
		{"returns nil", func() net.Addr { return nil }},
		{"returns a typed-nil *net.TCPAddr", func() net.Addr { return (*net.TCPAddr)(nil) }},
		{"panics", func() net.Addr { panic("addr") }},
		{"returns an address whose String panics with a value whose Error panics", func() net.Addr { return hostileAddr{} }},
	}

	for _, tt := range tests {
		t.Run(tt.fault, func(t *testing.T) {
			t.Parallel()

			ln := listen(t)
			logs := &logBuffer{}
			srv, err := srvr.New(srvr.WithListener(addrListener{Listener: ln, addr: tt.addr}), srvr.WithLogOutput(logs))
			mustNil(t, err)

			err = bounded(t, "Start did not return", func() error { return recoverPanic(func() error { return srv.Start(t.Context()) }) })
			if !errors.Is(err, srvr.ErrConfig) || errors.Is(err, errPanicked) || !strings.Contains(safeText(err), "Listener") {
				t.Fatalf("Start = %s, want an error wrapping ErrConfig naming Listener, without panicking", safeText(err))
			}

			select {
			case <-srv.Ready():
				t.Fatal("Ready was closed")
			default:
			}

			waitRefused(t, ln.Addr().String())

			if len(logs.find(t, "server.failed")) != 1 {
				t.Fatalf("want one server.failed record, got log %s", logs)
			}

			if err := shutdownBounded(t, srv); err != nil {
				t.Fatalf("later Shutdown = %v, want nil", err)
			}

			if addr := bounded(t, "Addr blocked", srv.Addr); addr != "" {
				t.Fatalf("Addr = %q, want empty", addr)
			}

			err = bounded(t, "Register blocked", func() error { return srv.Register(okRoute("/late")) })
			if !errors.Is(err, srvr.ErrServerClosed) {
				t.Fatalf("Register = %v, want ErrServerClosed", err)
			}
		})
	}
}
