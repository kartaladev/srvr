package srvr_test

import (
	"context"
	"errors"
	"net"
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
