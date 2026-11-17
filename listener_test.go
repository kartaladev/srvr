package srvr_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/kartaladev/srvr"
)

// TestListenerOwnership covers server-lifecycle / Scenario: A passed
// listener is owned by the server.
func TestListenerOwnership(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(context.Background())
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

				return srv.Start(context.Background())
			},
			want: func(err error) bool { return errors.Is(err, srvr.ErrRouteConfig) },
		},
		{
			name: "two routes register the same pattern",
			call: func(t *testing.T, srv *srvr.Server) error {
				t.Helper()
				mustNil(t, srv.Register(okRoute("/dup")))
				mustNil(t, srv.Register(okRoute("/dup")))

				return srv.Start(context.Background())
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
			call: func(_ *testing.T, srv *srvr.Server) error { return srv.Shutdown(context.Background()) },
			want: func(err error) bool { return err == nil },
		},
		{
			name: "served and shut down",
			call: func(t *testing.T, srv *srvr.Server) error {
				t.Helper()
				ctx, stop := context.WithCancel(context.Background())
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

			err = bounded(t, "Start did not return", func() error { return recoverPanic(func() error { return srv.Start(context.Background()) }) })
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
