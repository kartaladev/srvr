package srvr_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

// healthWithin calls srv.HealthCheck(ctx) and fails t unless it returns
// within 10ms.
func healthWithin(ctx context.Context, t *testing.T, srv *srvr.Server) error {
	t.Helper()

	ch := make(chan error, 1)
	go func() { ch <- recoverPanic(func() error { return srv.HealthCheck(ctx) }) }()

	return receiveWithin(t, ch, 10*time.Millisecond, "HealthCheck did not return within 10ms")
}

// TestHealthCheck covers server-lifecycle / Scenario: Health across the
// lifecycle, net/http rows.
func TestHealthCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// state brings the server into the row's state and returns it.
		state func(t *testing.T) *srvr.Server
		want  []error // nil: HealthCheck returns nil
	}{
		{
			name:  "was never started",
			state: func(t *testing.T) *srvr.Server { t.Helper(); return newServer(t) },
			want:  []error{srvr.ErrServerNotStarted},
		},
		{
			name: "is starting, with a route check waiting",
			state: func(t *testing.T) *srvr.Server {
				t.Helper()
				entered := make(chan struct{})
				srv := newServer(t, srvr.WithRouteCheck(func(ctx context.Context, _ srvr.Routes) error {
					close(entered)
					<-ctx.Done()

					return ctx.Err()
				}))
				started := make(chan error, 1)
				go func() { started <- srv.Start(context.Background()) }() //nolint:usetesting // joined in the cleanup
				receive(t, entered, "the route check did not run")
				t.Cleanup(func() {
					_ = shutdownBounded(t, srv)
					_ = receive(t, started, "Start did not return")
				})

				return srv
			},
			want: []error{srvr.ErrServerNotStarted},
		},
		{
			name: "is serving after Start",
			state: func(t *testing.T) *srvr.Server {
				t.Helper()
				srv := pingServer(t)
				startReturned(t.Context(), t, srv)

				return srv
			},
		},
		{
			name: "is serving inside Run",
			state: func(t *testing.T) *srvr.Server {
				t.Helper()
				srv := pingServer(t)
				startServer(t, srv)

				return srv
			},
		},
		{
			name: "is draining a request after Shutdown began",
			state: func(t *testing.T) *srvr.Server {
				t.Helper()
				started, release := make(chan struct{}, 1), make(chan struct{})
				srv := newServer(t)
				mustNil(t, srv.Register(workRoute(time.Hour, started, release)))
				mustNil(t, startBounded(context.Background(), t, srv)) //nolint:usetesting // the server outlives the context
				status := inFlight("http://" + srv.Addr() + "/work")
				receive(t, started, "the request did not reach the handler")
				stopped := make(chan error, 1)
				go func() { stopped <- srv.Shutdown(context.Background()) }() //nolint:usetesting // joined in the cleanup
				eventually(t, func() bool { return errors.Is(srv.HealthCheck(t.Context()), srvr.ErrServerClosed) }, "the stop did not begin")
				t.Cleanup(func() {
					close(release)
					_ = receive(t, stopped, "Shutdown did not return")
					if got := receive(t, status, "the request did not finish"); got != http.StatusOK {
						t.Errorf("in-flight status = %d, want 200", got)
					}
				})

				return srv
			},
			want: []error{srvr.ErrServerClosed},
		},
		{
			name: "has stopped",
			state: func(t *testing.T) *srvr.Server {
				t.Helper()
				srv := pingServer(t)
				mustNil(t, startBounded(t.Context(), t, srv))
				mustNil(t, shutdownBounded(t, srv))

				return srv
			},
			want: []error{srvr.ErrServerClosed},
		},
		{
			name: "failed to bind in Start",
			state: func(t *testing.T) *srvr.Server {
				t.Helper()
				srv, err := srvr.New(srvr.WithAddr(listen(t).Addr().String()))
				mustNil(t, err)
				if err := startBounded(t.Context(), t, srv); err == nil {
					t.Fatal("Start = nil, want a bind error")
				}

				return srv
			},
			want: []error{srvr.ErrServerClosed},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := tt.state(t)

			err := healthWithin(context.Background(), t, srv) //nolint:usetesting // the row's context
			if tt.want == nil && err != nil {
				t.Fatalf("HealthCheck = %v, want nil", err)
			}
			for _, w := range tt.want {
				if !errors.Is(err, w) {
					t.Fatalf("HealthCheck = %v, want an error matching %v", err, w)
				}
			}
		})
	}
}

// TestHealthCheck_ServeFailure covers the "and srvr.ErrServerClosed" part
// of server-lifecycle / Scenario: Listener fails while serving: HealthCheck
// wraps both the serve failure and srvr.ErrServerClosed.
func TestHealthCheck_ServeFailure(t *testing.T) {
	t.Parallel()

	ln := newFaultListener(t, func() (net.Conn, error) { return nil, errAccept })
	srv, err := srvr.New(srvr.WithListener(ln))
	mustNil(t, err)
	mustNil(t, startBounded(t.Context(), t, srv))
	ln.fail(t)
	_ = receive(t, waitAsync(srv), "Wait did not return")

	err = healthWithin(t.Context(), t, srv)
	if !errors.Is(err, errAccept) || !errors.Is(err, srvr.ErrServerClosed) {
		t.Fatalf("HealthCheck = %v, want the accept error and ErrServerClosed", err)
	}
}

// TestHealthCheck_HostileContext covers server-lifecycle / Scenario: Health
// with a hostile context.
func TestHealthCheck_HostileContext(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(context.Background()) //nolint:usetesting // the row's context
	cancel()

	tests := []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "nil", ctx: nil, want: srvr.ErrConfig},
		{name: "an already cancelled context", ctx: cancelled},
		{name: "a context whose Err panics", ctx: panicErrContext{Context: context.Background()}}, //nolint:usetesting // the row's context
	}

	srv := pingServer(t)
	startReturned(t.Context(), t, srv)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := healthWithin(tt.ctx, t, srv)
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("HealthCheck = %v, want %v", err, tt.want)
			}
		})
	}
}

// panicErrContext is a context whose Err panics.
type panicErrContext struct{ context.Context }

func (panicErrContext) Err() error { panic("Err called") }
