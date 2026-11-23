package srvr_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

// pingServer is newServer with the route GET /ping answering 200.
func pingServer(t *testing.T, opts ...srvr.Option) *srvr.Server {
	t.Helper()

	srv := newServer(t, opts...)
	mustNil(t, srv.Register(okRoute("/ping")))

	return srv
}

// startReturned starts srv with ctx, fails t unless Start returns nil
// within waitLimit, shuts srv down when t ends and returns its base URL.
func startReturned(ctx context.Context, t *testing.T, srv *srvr.Server) string {
	t.Helper()

	if err := startBounded(ctx, t, srv); err != nil {
		t.Fatalf("Start = %v, want nil", err)
	}

	t.Cleanup(func() { //nolint:contextcheck // cleanups run after t.Context() is cancelled; shutdownBounded uses its own
		if err := shutdownBounded(t, srv); err != nil {
			t.Errorf("Shutdown = %v at cleanup", err)
		}
	})

	registerClient(t, srv.Addr()) // its cleanup runs before the one above

	return "http://" + srv.Addr()
}

// TestStart_ReturnsWhileServing covers server-lifecycle / Scenario: Start
// returns while serving, net/http rows.
func TestStart_ReturnsWhileServing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		after func(cancel context.CancelFunc)
		// deadline, when set, bounds the Start context.
		deadline time.Duration
	}{
		{name: "nothing", after: func(context.CancelFunc) {}},
		{name: "the Start context is cancelled", after: func(cancel context.CancelFunc) { cancel() }},
		{name: "the Start context's 50ms deadline passes", deadline: 50 * time.Millisecond, after: func(context.CancelFunc) { time.Sleep(100 * time.Millisecond) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background()) //nolint:usetesting // the server must outlive the context under test
			if tt.deadline > 0 {
				ctx, cancel = context.WithTimeout(context.Background(), tt.deadline) //nolint:usetesting // as above
			}
			defer cancel()

			srv := pingServer(t)
			base := startReturned(ctx, t, srv)

			tt.after(cancel)

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

// TestStart_BoundedByContext covers server-lifecycle / Scenario: Start
// bounded by its context.
func TestStart_BoundedByContext(t *testing.T) {
	t.Parallel()

	immediate := func(context.Context, srvr.Routes) error { return nil }
	waiting := func(ctx context.Context, _ srvr.Routes) error { <-ctx.Done(); return ctx.Err() }

	tests := []struct {
		name      string
		check     srvr.RouteCheck
		ctx       func() (context.Context, context.CancelFunc)
		want      error // nil: Start returns nil
		wantReady bool
	}{
		{
			name: "returns nil at once, without deadline", check: immediate,
			ctx:       func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }, //nolint:usetesting // the server outlives the context
			wantReady: true,
		},
		{
			name: "waits on its context, with a 50ms deadline", check: waiting,
			ctx: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 50*time.Millisecond) //nolint:usetesting // the server outlives the context
			},
			want: context.DeadlineExceeded,
		},
		{
			name: "waits on its context, cancelled after 50ms", check: waiting,
			ctx: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background()) //nolint:usetesting // as above
				time.AfterFunc(50*time.Millisecond, cancel)

				return ctx, cancel
			},
			want: context.Canceled,
		},
		{
			name: "returns nil at once, nil context", check: immediate,
			ctx:  func() (context.Context, context.CancelFunc) { return nil, func() {} },
			want: srvr.ErrConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := tt.ctx()
			defer cancel()

			srv := pingServer(t, srvr.WithRouteCheck(tt.check))
			t.Cleanup(func() { _ = shutdownBounded(t, srv) })

			err := receiveWithin(t, bounded(t, "Start did not return", func() <-chan error {
				ch := make(chan error, 1)
				go func() { ch <- recoverPanic(func() error { return srv.Start(ctx) }) }()

				return ch
			}), time.Second, "Start did not return within 1s")

			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("Start = %v, want %v", err, tt.want)
			}

			select {
			case <-srv.Ready():
				if !tt.wantReady {
					t.Fatal("Ready is closed, want never closed")
				}
			default:
				if tt.wantReady {
					t.Fatal("Ready is not closed")
				}
			}
		})
	}
}

// TestRun_StopByContext covers server-lifecycle / Scenario: Run and stop by
// context, net/http rows.
func TestRun_StopByContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		trigger string
		// run returns the Run context and a function that triggers the stop.
		run func(t *testing.T, srv *srvr.Server) (context.Context, func())
	}{
		{
			trigger: "the Run context is cancelled",
			run: func(t *testing.T, _ *srvr.Server) (context.Context, func()) {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())

				return ctx, cancel
			},
		},
		{
			trigger: "Shutdown(ctx) is called from another goroutine",
			run: func(t *testing.T, srv *srvr.Server) (context.Context, func()) {
				t.Helper()

				return t.Context(), func() {
					go func() { _ = srv.Shutdown(context.Background()) }() //nolint:usetesting // the goroutine is not joined and may outlive the test body
				}
			},
		},
		{
			trigger: "the Run context deadline passes",
			run: func(t *testing.T, _ *srvr.Server) (context.Context, func()) {
				t.Helper()
				ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
				t.Cleanup(cancel)

				return ctx, func() { <-ctx.Done() }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.trigger, func(t *testing.T) {
			t.Parallel()

			started, release := make(chan struct{}, 1), make(chan struct{})
			srv := newServer(t)
			if err := srv.Register(workRoute(time.Hour, started, release)); err != nil {
				t.Fatal(err)
			}

			ctx, trigger := tt.run(t, srv)
			done := runWith(ctx, t, srv)

			status := inFlight("http://" + srv.Addr() + "/work")
			// The deadline row's clock runs from before Start: under load it
			// can pass before the request reaches the handler. Fail with a
			// message then, instead of waiting on started forever.
			select {
			case <-started:
			case got := <-status:
				t.Fatalf("the request ended (status %d) before reaching the handler: the stop came first", got)
			case <-time.After(waitLimit):
				t.Fatalf("after %s: the request did not reach the handler", waitLimit)
			}
			trigger()
			// Run must still be draining: the request is in flight.
			select {
			case err := <-done:
				t.Fatalf("Run = %v before the in-flight request finished", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(release)

			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Run = %v, want nil", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Run did not return")
			}

			if got := receive(t, status, "the in-flight request did not finish"); got != http.StatusOK {
				t.Fatalf("in-flight status = %d, want 200", got)
			}
		})
	}
}

// TestRun_FailsToStart covers server-lifecycle / Scenario: Run fails to
// start.
func TestRun_FailsToStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		condition string
		setup     func(t *testing.T) (*srvr.Server, context.Context)
		want      func(error) bool
	}{
		{
			condition: "the address is already in use",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()
				taken := listen(t)

				srv, err := srvr.New(srvr.WithAddr(taken.Addr().String()))
				mustNil(t, err)

				return srv, t.Context()
			},
			want: func(err error) bool {
				var opErr *net.OpError

				return errors.As(err, &opErr)
			},
		},
		{
			condition: "a route configurer panics",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()
				srv := newServer(t)
				mustNil(t, srv.Register(&PanicAPI{}))

				return srv, t.Context()
			},
			want: func(err error) bool { return errors.Is(err, srvr.ErrRouteConfig) },
		},
		{
			condition: "the Run context is already cancelled",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()

				return newServer(t), ctx
			},
			want: func(err error) bool { return errors.Is(err, context.Canceled) },
		},
		{
			condition: "Run(nil) is called",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()

				return newServer(t), nil
			},
			want: func(err error) bool { return errors.Is(err, srvr.ErrConfig) },
		},
		{
			condition: "Shutdown was called before Run",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()
				srv := newServer(t)
				mustNil(t, shutdownBounded(t, srv))

				return srv, t.Context()
			},
			want: func(err error) bool { return errors.Is(err, srvr.ErrServerClosed) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.condition, func(t *testing.T) {
			t.Parallel()

			srv, ctx := tt.setup(t)

			err := bounded(t, "Run did not return", func() error {
				return recoverPanic(func() error { return srv.Run(ctx) })
			})
			if !tt.want(err) {
				t.Fatalf("Run = %v, want an error matching the row", err)
			}

			select {
			case <-srv.Ready():
				t.Fatal("Ready is closed: the server served")
			default:
			}
		})
	}
}

// waitAsync calls srv.Wait in the background and returns the channel its
// result arrives on.
func waitAsync(srv *srvr.Server) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- recoverPanic(srv.Wait) }()

	return ch
}

// TestWait_AfterStart covers server-lifecycle / Scenario: Wait after Start,
// net/http rows. The permanent-Accept-error row is in
// TestServeFailure_AfterStart, which asserts Wait for every engine.
func TestWait_AfterStart(t *testing.T) {
	t.Parallel()

	hookErr := errors.New("flush failed")

	tests := []struct {
		name string
		opts []srvr.Option
		// early is the number of Wait calls made before the stop.
		early int
		// late is the number of Wait calls made after the stop.
		late int
		want error
	}{
		{name: "Shutdown returns nil, once", late: 1},
		{name: "Shutdown is called, 3 goroutines before the stop", early: 3},
		{name: "Shutdown returns nil, twice after the stop", late: 2},
		{
			name: "an AfterDrain hook returns an error, once", late: 1, want: srvr.ErrShutdownHook,
			opts: []srvr.Option{srvr.WithShutdownHook(srvr.AfterDrain, func(context.Context) error { return hookErr })},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := pingServer(t, tt.opts...)
			if err := startBounded(context.Background(), t, srv); err != nil { //nolint:usetesting // the server outlives the context
				t.Fatalf("Start = %v", err)
			}

			var waits []<-chan error
			for range tt.early {
				waits = append(waits, waitAsync(srv))
			}

			_ = shutdownBounded(t, srv)

			for range tt.late {
				waits = append(waits, waitAsync(srv))
			}

			for i, ch := range waits {
				err := receive(t, ch, "Wait did not return")
				if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
					t.Fatalf("Wait #%d = %v, want %v", i, err, tt.want)
				}
			}
		})
	}
}

// TestWait_Misuse covers server-lifecycle / Scenario: Wait misuse.
func TestWait_Misuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state func(t *testing.T) *srvr.Server
		want  error
	}{
		{
			name:  "was never started",
			state: func(t *testing.T) *srvr.Server { t.Helper(); return newServer(t) },
			want:  srvr.ErrServerNotStarted,
		},
		{
			name: "is starting in another goroutine whose route check then fails",
			state: func(t *testing.T) *srvr.Server {
				t.Helper()
				entered, release := make(chan struct{}), make(chan struct{})
				srv := newServer(t, srvr.WithRouteCheck(func(context.Context, srvr.Routes) error {
					close(entered)
					<-release

					return errors.New("check failed")
				}))
				started := make(chan error, 1)
				go func() { started <- srv.Start(context.Background()) }() //nolint:usetesting // joined below
				receive(t, entered, "the route check did not run")
				t.Cleanup(func() { _ = receive(t, started, "Start did not return") })
				time.AfterFunc(20*time.Millisecond, func() { close(release) })

				return srv
			},
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
		},
		{
			name: "was shut down before Start",
			state: func(t *testing.T) *srvr.Server {
				t.Helper()
				srv := newServer(t)
				mustNil(t, shutdownBounded(t, srv))

				return srv
			},
		},
		{
			name: "was started with Run and its context was cancelled",
			state: func(t *testing.T) *srvr.Server {
				t.Helper()
				srv := newServer(t)
				ctx, cancel := context.WithCancel(t.Context())
				done := runWith(ctx, t, srv)
				cancel()
				mustNil(t, receive(t, done, "Run did not return"))

				return srv
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := tt.state(t)

			// The starting row's Start returns 20ms after this call, so the
			// limit is counted from that return, not from here.
			limit := 100 * time.Millisecond
			if tt.name == "is starting in another goroutine whose route check then fails" {
				limit += 20 * time.Millisecond
			}

			err := receiveWithin(t, waitAsync(srv), limit, "Wait did not return in time")
			if tt.want == nil && err != nil || tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("Wait = %v, want %v", err, tt.want)
			}
		})
	}
}

// faultListener delegates to a real listener until fail is called; then
// the pending and later Accept calls run fault: return its error or panic.
type faultListener struct {
	net.Listener

	fault   func() (net.Conn, error)
	trigger chan struct{}
}

func newFaultListener(t *testing.T, fault func() (net.Conn, error)) *faultListener {
	t.Helper()

	return &faultListener{Listener: listen(t), fault: fault, trigger: make(chan struct{})}
}

// fail makes Accept run the fault, waking a pending Accept with a dial.
func (l *faultListener) fail(t *testing.T) {
	t.Helper()

	close(l.trigger)

	conn, err := net.Dial("tcp", l.Addr().String())
	if err == nil {
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

		return l.fault()
	default:
		return conn, err
	}
}

var errAccept = errors.New("accept failed permanently")

// errListener fails every Accept with errAccept from the first call.
type errListener struct{ net.Listener }

func (errListener) Accept() (net.Conn, error) { return nil, errAccept }

// TestServeFailure_AfterStart covers server-lifecycle / Scenario: Listener
// fails while serving, net/http rows, and the permanent-Accept-error row of
// Scenario: Wait after Start.
func TestServeFailure_AfterStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fault string
		do    func() (net.Conn, error)
		match func(error) bool
	}{
		{"returns a permanent error E", func() (net.Conn, error) { return nil, errAccept }, func(err error) bool { return errors.Is(err, errAccept) }},
		{"panics with boom", func() (net.Conn, error) { panic("boom") }, func(err error) bool { return err != nil && strings.Contains(err.Error(), "boom") }},
		{"returns net.ErrClosed although the listener was not closed", func() (net.Conn, error) { return nil, net.ErrClosed }, func(err error) bool { return errors.Is(err, net.ErrClosed) }},
	}

	for _, tt := range tests {
		t.Run(tt.fault, func(t *testing.T) {
			t.Parallel()

			var c hookCounts
			out := &logBuffer{}
			ln := newFaultListener(t, tt.do)
			srv, err := srvr.New(append(c.options(), srvr.WithListener(ln), srvr.WithLogOutput(out))...)
			mustNil(t, err)
			mustNil(t, startBounded(context.Background(), t, srv)) //nolint:usetesting // the server outlives the context

			ln.fail(t)

			if err := receive(t, waitAsync(srv), "Wait did not return"); !tt.match(err) {
				t.Fatalf("Wait = %v, want an error matching the row", err)
			}
			if n := len(out.find(t, "server.failed")); n != 1 {
				t.Fatalf("server.failed records = %d, want 1:\n%s", n, out.String())
			}
			if b, a := c.before.Load(), c.after.Load(); b != 1 || a != 1 {
				t.Fatalf("hooks ran before=%d after=%d, want 1 and 1", b, a)
			}
			if err := shutdownBounded(t, srv); err != nil {
				t.Fatalf("later Shutdown = %v, want nil", err)
			}
			if err := startBounded(t.Context(), t, srv); !errors.Is(err, srvr.ErrServerClosed) {
				t.Fatalf("later Start = %v, want ErrServerClosed", err)
			}
		})
	}
}

// TestServeFailure_DuringRun covers server-lifecycle / Scenario: Run and
// stop by context, row "the passed listener's Accept returns a permanent
// error E".
func TestServeFailure_DuringRun(t *testing.T) {
	t.Parallel()

	started, release := make(chan struct{}, 1), make(chan struct{})
	ln := newFaultListener(t, func() (net.Conn, error) { return nil, errAccept })
	srv, err := srvr.New(srvr.WithListener(ln))
	mustNil(t, err)
	mustNil(t, srv.Register(workRoute(time.Hour, started, release)))

	done := runWith(t.Context(), t, srv)
	status := inFlight("http://" + srv.Addr() + "/work")
	receive(t, started, "the request did not reach the handler")
	ln.fail(t)
	close(release)

	if err := receive(t, done, "Run did not return"); !errors.Is(err, errAccept) {
		t.Fatalf("Run = %v, want the accept error", err)
	}
	if got := receive(t, status, "the in-flight request did not finish"); got != http.StatusOK {
		t.Fatalf("in-flight status = %d, want 200", got)
	}
}

// TestServeFailure_ShutdownDuringStop covers server-lifecycle / Scenario:
// Shutdown during the stop a serve failure began.
func TestServeFailure_ShutdownDuringStop(t *testing.T) {
	t.Parallel()

	var runs atomic.Int32
	entered := make(chan struct{})
	ln := newFaultListener(t, func() (net.Conn, error) { return nil, errAccept })
	srv, err := srvr.New(srvr.WithListener(ln), srvr.WithShutdownHook(srvr.BeforeDrain, func(context.Context) error {
		runs.Add(1)
		close(entered)
		time.Sleep(100 * time.Millisecond)

		return nil
	}))
	mustNil(t, err)
	mustNil(t, startBounded(context.Background(), t, srv)) //nolint:usetesting // the server outlives the context

	ln.fail(t)
	receive(t, entered, "the BeforeDrain hook did not run")

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown = %v, want nil", err)
	}
	// The stop has finished, so Wait returns at once.
	if err := receiveWithin(t, waitAsync(srv), 50*time.Millisecond, "Shutdown returned before the stop finished"); !errors.Is(err, errAccept) {
		t.Fatalf("Wait = %v, want the accept error", err)
	}
	if n := runs.Load(); n != 1 {
		t.Fatalf("BeforeDrain ran %d times, want 1", n)
	}
}

// TestRun_CancelDuringShutdown pins the "Shutdown and Run context
// cancellation happen concurrently" row of server-lifecycle / Scenario:
// Hooks on every stop path deterministically: the Run context ends while a
// Shutdown is inside its BeforeDrain hook, so Run finds the stop begun,
// waits for it and the hooks run once.
func TestRun_CancelDuringShutdown(t *testing.T) {
	t.Parallel()

	var c hookCounts
	entered, release := make(chan struct{}), make(chan struct{})
	srv := newServer(t, append(c.options(), srvr.WithShutdownHook(srvr.BeforeDrain, func(context.Context) error {
		close(entered)
		<-release

		return nil
	}))...)

	ctx, cancel := context.WithCancel(t.Context())
	done := runWith(ctx, t, srv)

	stopped := make(chan error, 1)
	go func() { stopped <- srv.Shutdown(context.Background()) }() //nolint:usetesting // joined below
	receive(t, entered, "the BeforeDrain hook did not run")

	cancel()
	select {
	case err := <-done:
		t.Fatalf("Run = %v before the Shutdown's stop finished", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)

	mustNil(t, receive(t, stopped, "Shutdown did not return"))
	mustNil(t, receive(t, done, "Run did not return"))
	if b, a := c.before.Load(), c.after.Load(); b != 1 || a != 1 {
		t.Fatalf("hooks ran before=%d after=%d, want 1 and 1", b, a)
	}
}

// TestLifecycleRecords_ImmediateServeFailure covers server-lifecycle /
// Scenario: Records order when serving fails at once.
func TestLifecycleRecords_ImmediateServeFailure(t *testing.T) {
	t.Parallel()

	want := []string{"server.started", "server.failed", "server.stopping", "server.stopped"}

	out := &logBuffer{}
	srv, err := srvr.New(srvr.WithListener(errListener{listen(t)}), srvr.WithLogOutput(out))
	mustNil(t, err)
	mustNil(t, startBounded(t.Context(), t, srv))

	if err := receive(t, waitAsync(srv), "Wait did not return"); !errors.Is(err, errAccept) {
		t.Fatalf("Wait = %v, want the accept error", err)
	}
	select {
	case <-srv.Ready():
	default:
		t.Fatal("Ready is not closed after the server closed")
	}

	got := slices.DeleteFunc(messages(out.records(t)), func(m string) bool { return !slices.Contains(want, m) })
	if !slices.Equal(got, want) {
		t.Fatalf("records = %v, want %v", got, want)
	}
}
