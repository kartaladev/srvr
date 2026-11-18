package srvr_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

// inFlight sends GET url in the background and returns a channel with the
// status, or -1 when the request failed.
func inFlight(url string) <-chan int {
	out := make(chan int, 1)

	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
		res, err := clientFor(req.URL.String()).Do(req)
		if err != nil {
			out <- -1

			return
		}
		res.Body.Close()
		out <- res.StatusCode
	}()

	return out
}

// TestStart_Stop covers server-lifecycle / Scenario: Start and stop by context.
func TestStart_Stop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		trigger string
		// run returns the Start context and a function that triggers the stop.
		run func(t *testing.T, srv *srvr.Server) (context.Context, func())
	}{
		{
			trigger: "the Start context is cancelled",
			run: func(t *testing.T, _ *srvr.Server) (context.Context, func()) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())

				return ctx, cancel
			},
		},
		{
			trigger: "Shutdown(ctx) is called from another goroutine",
			run: func(_ *testing.T, srv *srvr.Server) (context.Context, func()) {
				return context.Background(), func() {
					go func() { _ = srv.Shutdown(context.Background()) }()
				}
			},
		},
		{
			trigger: "the Start context deadline passes",
			run: func(t *testing.T, _ *srvr.Server) (context.Context, func()) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
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
			done := startWith(ctx, t, srv)

			status := inFlight("http://" + srv.Addr() + "/work")
			<-started
			trigger()
			// Start must still be draining: the request is in flight.
			select {
			case err := <-done:
				t.Fatalf("Start = %v before the in-flight request finished", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(release)

			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("Start = %v, want nil", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Start did not return")
			}

			if got := <-status; got != http.StatusOK {
				t.Fatalf("in-flight status = %d, want 200", got)
			}
		})
	}
}

// TestStart_WithListener serves on a listener the consumer opened.
func TestStart_WithListener(t *testing.T) {
	t.Parallel()

	ln := listen(t)

	srv, err := srvr.New(srvr.WithListener(ln))
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Register(okRoute("/ping")); err != nil {
		t.Fatal(err)
	}
	base := startServer(t, srv)

	if base != "http://"+ln.Addr().String() {
		t.Fatalf("base = %s, want the listener's address %s", base, ln.Addr())
	}
	if got := get(t, base+"/ping").status; got != http.StatusOK {
		t.Fatalf("GET /ping = %d", got)
	}
}

// failingListener fails every Accept with a permanent error.
type failingListener struct{ net.Listener }

func (l failingListener) Accept() (net.Conn, error) { return nil, errors.New("accept failed") }

// TestStart_ServeFails: when serving fails on its own, Start returns the
// error and the server still stops in order.
func TestStart_ServeFails(t *testing.T) {
	t.Parallel()

	ln := listen(t)

	var c hookCounts
	srv, err := srvr.New(append(c.options(), srvr.WithListener(failingListener{ln}))...)
	if err != nil {
		t.Fatal(err)
	}

	err = srv.Start(context.Background())
	if err == nil || err.Error() != "accept failed" {
		t.Fatalf("Start = %v, want the accept error", err)
	}
	if b, a := c.before.Load(), c.after.Load(); b != 1 || a != 1 {
		t.Fatalf("hooks ran before=%d after=%d, want 1 and 1", b, a)
	}
}

// TestShutdown_WhileConfiguring: a Shutdown while a configurer runs (it
// receives no context) stops the server before it serves: Start returns
// ErrServerClosed (server-lifecycle "Shutdown interrupts a starting
// server"); nothing was served, so only AfterDrain runs.
func TestShutdown_WhileConfiguring(t *testing.T) {
	t.Parallel()

	var c hookCounts
	srv := newServer(t, c.options()...)

	entered, release := make(chan struct{}), make(chan struct{})
	err := srv.Register(configFunc(func(*srvr.Group) {
		close(entered)
		<-release
	}))
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- srv.Start(context.Background()) }()
	<-entered

	shut := make(chan error, 1)
	go func() { shut <- srv.Shutdown(context.Background()) }()

	// The first Shutdown has moved the server to stopping once Register
	// reports it closed.
	eventually(t, func() bool { return errors.Is(srv.Register(okRoute("/x")), srvr.ErrServerClosed) }, "Shutdown did not stop the starting server")
	close(release)

	// A second Shutdown waits for the stop too and returns nil.
	mustNil(t, srv.Shutdown(context.Background()))
	if err := <-done; !errors.Is(err, srvr.ErrServerClosed) {
		t.Fatalf("Start = %v, want an error wrapping ErrServerClosed", err)
	}
	mustNil(t, <-shut)

	select {
	case <-srv.Ready():
		t.Fatal("Ready() was closed")
	default:
	}
	if b, a := c.before.Load(), c.after.Load(); b != 0 || a != 1 {
		t.Fatalf("hooks ran before=%d after=%d, want 0 and 1", b, a)
	}
}

// TestShutdown_WaitExpires: a Shutdown that waits for another call's stop
// returns its own context error when that expires first.
func TestShutdown_WaitExpires(t *testing.T) {
	t.Parallel()

	started, release := make(chan struct{}, 1), make(chan struct{})
	srv := newServer(t)
	if err := srv.Register(workRoute(time.Hour, started, release)); err != nil {
		t.Fatal(err)
	}
	done := startBackground(t, srv)
	status := inFlight("http://" + srv.Addr() + "/work")
	<-started

	first := make(chan error, 1)
	go func() { first <- srv.Shutdown(context.Background()) }()
	waitRefused(t, srv.Addr())

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting Shutdown = %v, want DeadlineExceeded", err)
	}

	close(release)
	<-status
	mustNil(t, <-first)
	mustNil(t, <-done)
}

// TestShutdownHooks_Unbounded: a negative ShutdownHookTimeout gives the
// AfterDrain hooks a context without deadline.
func TestShutdownHooks_Unbounded(t *testing.T) {
	t.Parallel()

	var hasDeadline atomic.Bool
	srv := newServer(t,
		srvr.WithShutdownHookTimeout(-1),
		srvr.WithShutdownHook(srvr.AfterDrain, func(ctx context.Context) error {
			_, ok := ctx.Deadline()
			hasDeadline.Store(ok)

			return nil
		}),
	)
	mustNil(t, srv.Shutdown(context.Background()))

	if hasDeadline.Load() {
		t.Fatal("the AfterDrain hook got a deadline, want none")
	}
}

// TestLifecycleErrorText covers server-lifecycle / Scenario: Lifecycle
// error text is redacted and limited.
func TestLifecycleErrorText(t *testing.T) {
	t.Parallel()

	secretMasker := func(s string) string { return strings.ReplaceAll(s, "secret", "***") }
	panicky := func(string) string { panic("redactor") }
	expander := func(string) string { return xs(9000) }

	// hook registers fn as an AfterDrain hook and calls Shutdown before Start.
	hook := func(fn func(context.Context) error) func(*testing.T, []srvr.Option) {
		return func(t *testing.T, opts []srvr.Option) {
			t.Helper()
			srv := newServer(t, append(opts, srvr.WithShutdownHook(srvr.AfterDrain, fn))...)
			_ = srv.Shutdown(context.Background())
		}
	}
	hookReturns := func(err error) func(*testing.T, []srvr.Option) {
		return hook(func(context.Context) error { return err })
	}
	hookPanics := hook(func(context.Context) error { panic("secret") })
	startDone := func(err error) func(*testing.T, []srvr.Option) {
		return func(t *testing.T, opts []srvr.Option) {
			t.Helper()
			srv := newServer(t, opts...)
			_ = srv.Start(newDoneContext(err))
		}
	}

	const hookFailed, failed = "server.shutdown_hook.failed", "server.failed"

	dbErr, stopErr := errors.New("close db: secret"), errors.New("stop: secret")

	tests := []struct {
		name     string
		redactor func(string) string
		failure  func(*testing.T, []srvr.Option)
		event    string
		message  string
	}{
		{"hook, no redactor", nil, hookReturns(dbErr), hookFailed, "close db: secret"},
		{"hook, masker", secretMasker, hookReturns(dbErr), hookFailed, "close db: ***"},
		{"hook, panicking redactor", panicky, hookReturns(dbErr), hookFailed, "[redaction failed]"},
		{"hook panics, masker", secretMasker, hookPanics, hookFailed, "panic: ***"},
		{"start, masker", secretMasker, startDone(stopErr), failed, "stop: ***"},
		{"start, panicking redactor", panicky, startDone(stopErr), failed, "[redaction failed]"},
		{"hook, 5000 bytes", nil, hookReturns(errors.New(xs(5000))), hookFailed, xs(5000)},
		{"hook, 9000 bytes", nil, hookReturns(errors.New(xs(9000))), hookFailed, xs(8192) + cut},
		{"hook, expanding redactor", expander, hookReturns(dbErr), hookFailed, xs(8192) + cut},
		{"start, 9000 bytes", nil, startDone(errors.New(xs(9000))), failed, xs(8192) + cut},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out := &logBuffer{}
			opts := []srvr.Option{srvr.WithLogOutput(out)}
			if tt.redactor != nil {
				opts = append(opts, srvr.WithErrorRedactor(tt.redactor))
			}
			tt.failure(t, opts)

			recs := out.find(t, tt.event)
			if len(recs) != 1 {
				t.Fatalf("%d %s records, want 1:\n%s", len(recs), tt.event, out.String())
			}
			got, _ := recs[0]["exception.message"].(string)
			if got != tt.message {
				t.Fatalf("exception.message = %.80q (%d bytes), want %.80q (%d bytes)", got, len(got), tt.message, len(tt.message))
			}
		})
	}
}

// TestStart_Twice covers server-lifecycle / Scenario: Start twice.
func TestStart_Twice(t *testing.T) {
	t.Parallel()

	t.Run("is running", func(t *testing.T) {
		t.Parallel()

		srv := newServer(t)
		if err := srv.Register(okRoute("/ping")); err != nil {
			t.Fatal(err)
		}
		base := startServer(t, srv)

		if err := srv.Start(context.Background()); !errors.Is(err, srvr.ErrServerStarted) {
			t.Fatalf("second Start = %v, want ErrServerStarted", err)
		}
		if got := get(t, base+"/ping").status; got != http.StatusOK {
			t.Fatalf("GET /ping = %d, want 200: the first server must keep serving", got)
		}
	})

	t.Run("is starting in another goroutine at the same time", func(t *testing.T) {
		t.Parallel()

		srv := newServer(t)
		if err := srv.Register(okRoute("/ping")); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		const n = 8
		results := make(chan error, n)
		var wg sync.WaitGroup
		for range n {
			wg.Go(func() { results <- srv.Start(ctx) })
		}

		// All but one Start return at once.
		for range n - 1 {
			select {
			case err := <-results:
				if !errors.Is(err, srvr.ErrServerStarted) {
					t.Fatalf("concurrent Start = %v, want ErrServerStarted", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("concurrent Start did not return")
			}
		}

		<-srv.Ready()
		if got := get(t, "http://"+srv.Addr()+"/ping").status; got != http.StatusOK {
			t.Fatalf("GET /ping = %d, want 200", got)
		}

		cancel()
		wg.Wait()
		if err := <-results; err != nil {
			t.Fatalf("serving Start = %v, want nil", err)
		}
	})

	t.Run("has stopped", func(t *testing.T) {
		t.Parallel()

		srv := newServer(t)
		ctx, cancel := context.WithCancel(context.Background())
		done := startWith(ctx, t, srv)
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		addr := srv.Addr()

		if err := srv.Start(context.Background()); !errors.Is(err, srvr.ErrServerClosed) {
			t.Fatalf("Start after stop = %v, want ErrServerClosed", err)
		}

		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+addr+"/", nil)
		if res, err := clientFor(req.URL.String()).Do(req); err == nil {
			res.Body.Close()
			t.Fatal("a request after stop was served: the server must stay stopped")
		}
	})
}

// TestStart_Fails covers server-lifecycle / Scenario: Start fails.
func TestStart_Fails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		condition string
		// setup returns the server and the Start context.
		setup func(t *testing.T) (*srvr.Server, context.Context)
		want  func(error) bool
	}{
		{
			condition: "the address is already in use",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()
				taken := listen(t)

				srv, err := srvr.New(srvr.WithAddr(taken.Addr().String()))
				if err != nil {
					t.Fatal(err)
				}

				return srv, context.Background()
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

				return srv, context.Background()
			},
			want: func(err error) bool { return errors.Is(err, srvr.ErrRouteConfig) },
		},
		{
			condition: "two routes register the same pattern",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()
				srv := newServer(t)
				mustNil(t, srv.Register(okRoute("/dup")))
				mustNil(t, srv.Register(okRoute("/dup")))

				return srv, context.Background()
			},
			want: func(err error) bool { return errors.Is(err, srvr.ErrRouteConfig) },
		},
		{
			condition: "the Start context is already cancelled",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				return newServer(t), ctx
			},
			want: func(err error) bool { return errors.Is(err, context.Canceled) },
		},
		{
			condition: "the Start context is done and its Err returns an error whose Error panics",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()

				return newServer(t), newDoneContext(badError{})
			},
			want: func(err error) bool { return errors.Is(err, badError{}) },
		},
		{
			condition: "the Start context is done and its Err returns a typed-nil error",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()

				return newServer(t), newDoneContext((*derefError)(nil))
			},
			want: func(err error) bool {
				var target *derefError

				return errors.As(err, &target) && target == nil
			},
		},
		{
			condition: "Start(nil) is called",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()

				return newServer(t), nil
			},
			want: func(err error) bool { return errors.Is(err, srvr.ErrConfig) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.condition, func(t *testing.T) {
			t.Parallel()

			srv, ctx := tt.setup(t)
			done := make(chan error, 1)
			go func() { done <- recoverPanic(func() error { return srv.Start(ctx) }) }()

			select {
			case err := <-done:
				if !tt.want(err) {
					t.Fatalf("Start = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Start did not return")
			}

			select {
			case <-srv.Ready():
				t.Fatal("Ready() was closed")
			default:
			}
		})
	}
}

// doneContext is a done context whose Err returns err.
type doneContext struct {
	context.Context //nolint:containedctx // embedded only for Done, Deadline and Value.

	err error
}

func newDoneContext(err error) doneContext {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	return doneContext{Context: ctx, err: err}
}

func (c doneContext) Err() error { return c.err }

// TestReady covers server-lifecycle / Scenario: Ephemeral port.
func TestReady(t *testing.T) {
	t.Parallel()

	fixed := freeAddr(t)

	tests := []struct {
		addr  string
		check func(t *testing.T, bound string)
	}{
		{
			addr: "127.0.0.1:0",
			check: func(t *testing.T, bound string) {
				t.Helper()
				host, port, err := net.SplitHostPort(bound)
				if err != nil || host != "127.0.0.1" || port == "0" || port == "" {
					t.Fatalf("Addr() = %q, want 127.0.0.1:<non-zero port>", bound)
				}
			},
		},
		{
			// The Examples row uses 18080; a port free on this machine keeps the
			// row meaningful without depending on 18080 being unused.
			addr: fixed,
			check: func(t *testing.T, bound string) {
				t.Helper()
				if bound != fixed {
					t.Fatalf("Addr() = %q, want %q", bound, fixed)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()

			srv, err := srvr.New(srvr.WithAddr(tt.addr))
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- srv.Start(ctx) }()
			t.Cleanup(func() {
				cancel()
				if err := <-done; err != nil {
					t.Errorf("Start = %v", err)
				}
			})
			waitReady(t, srv, done)

			tt.check(t, srv.Addr())
		})
	}
}

// TestAddr_BeforeReady covers server-lifecycle / Scenario: Address before readiness.
func TestAddr_BeforeReady(t *testing.T) {
	t.Parallel()

	t.Run("has not been started", func(t *testing.T) {
		t.Parallel()

		if got := newServer(t).Addr(); got != "" {
			t.Fatalf("Addr() = %q, want empty", got)
		}
	})

	t.Run("failed to bind", func(t *testing.T) {
		t.Parallel()

		taken := listen(t)

		srv, err := srvr.New(srvr.WithAddr(taken.Addr().String()))
		if err != nil {
			t.Fatal(err)
		}

		if err := srv.Start(context.Background()); !errors.Is(err, syscall.EADDRINUSE) {
			t.Fatalf("Start = %v, want address in use", err)
		}

		if got := srv.Addr(); got != "" {
			t.Fatalf("Addr() = %q, want empty", got)
		}
	})
}

// freeAddr returns a loopback address with a port that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()

	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	return ln.Addr().String()
}

// TestShutdown_WhileStarting covers the net/http rows of server-lifecycle /
// Scenario: Shutdown during the configure phase, both tables.
func TestShutdown_WhileStarting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// check is the route check; it signals entered once running.
		check func(ctx context.Context, entered chan<- struct{}) error
		// shutdowns is the number of concurrent Shutdown calls made while
		// the check runs; 0 calls Shutdown once before Start.
		shutdowns     int
		wantShutdown  error
		startAtLeast  time.Duration // Start returns no earlier
		shutdownLimit time.Duration // each Shutdown returns within
	}{
		{
			name: "route check waiting on its context",
			check: func(ctx context.Context, entered chan<- struct{}) error {
				close(entered)
				<-ctx.Done()

				return ctx.Err()
			},
			shutdowns: 1, shutdownLimit: time.Second,
		},
		{
			name: "route check ignoring its context for 2s",
			check: func(_ context.Context, entered chan<- struct{}) error {
				close(entered)
				time.Sleep(2 * time.Second)

				return nil
			},
			shutdowns: 1, wantShutdown: context.DeadlineExceeded, startAtLeast: 2 * time.Second, shutdownLimit: 1500 * time.Millisecond,
		},
		// misuse around the interruption
		{name: "Shutdown before Start", shutdowns: 0, shutdownLimit: time.Second},
		{
			name: "route check, two concurrent Shutdowns",
			check: func(ctx context.Context, entered chan<- struct{}) error {
				close(entered)
				<-ctx.Done()

				return ctx.Err()
			},
			shutdowns: 2, shutdownLimit: time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var c hookCounts
			entered := make(chan struct{})
			opts := c.options()
			if tt.check != nil {
				opts = append(opts, srvr.WithRouteCheck(func(ctx context.Context, _ engine.Routes) error { return tt.check(ctx, entered) }))
			}
			srv := newServer(t, opts...)
			mustNil(t, srv.Register(okRoute("/x")))

			// shutdown calls Shutdown with a 1s context and reports whether it
			// returned within the limit; it never touches t, as it may run on
			// a goroutine of its own.
			shutdown := func() error {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()

				begin := time.Now()
				err := srv.Shutdown(ctx)
				if d := time.Since(begin); d > tt.shutdownLimit {
					return fmt.Errorf("shutdown took %s, want at most %s: %w", d, tt.shutdownLimit, err)
				}

				return err
			}

			begin := time.Now()
			done := make(chan error, 1)
			if tt.shutdowns == 0 {
				mustNil(t, shutdown())
				done <- srv.Start(context.Background())
			} else {
				go func() { done <- recoverPanic(func() error { return srv.Start(context.Background()) }) }()
				receive(t, entered, "the route check did not run")

				results := make(chan error, tt.shutdowns)
				for range tt.shutdowns {
					go func() { results <- shutdown() }()
				}
				for range tt.shutdowns {
					if err := receive(t, results, "Shutdown did not return"); !errors.Is(err, tt.wantShutdown) || (tt.wantShutdown == nil && err != nil) {
						t.Fatalf("Shutdown = %v, want %v", err, tt.wantShutdown)
					}
				}
			}

			err := receive(t, done, "Start did not return")
			if !errors.Is(err, srvr.ErrServerClosed) {
				t.Fatalf("Start = %v, want an error wrapping ErrServerClosed", err)
			}
			if d := time.Since(begin); d < tt.startAtLeast {
				t.Fatalf("Start returned after %s, want at least %s", d, tt.startAtLeast)
			}
			if srv.Addr() != "" {
				t.Fatalf("Addr = %q, want no listener bound", srv.Addr())
			}
			if err := srv.Start(context.Background()); !errors.Is(err, srvr.ErrServerClosed) {
				t.Fatalf("second Start = %v, want an error wrapping ErrServerClosed", err)
			}
			if b, a := c.before.Load(), c.after.Load(); b != 0 || a != 1 {
				t.Fatalf("hooks ran before=%d after=%d, want 0 and 1", b, a)
			}
		})
	}
}

// TestShutdown_WhileStarting_Branches covers the hot-path branches of a
// starting server's interruption beyond the spec rows: Shutdown between
// binding and serving, and a route check left unrun once interrupted.
func TestShutdown_WhileStarting_Branches(t *testing.T) {
	t.Parallel()

	t.Run("between binding and serving", func(t *testing.T) {
		t.Parallel()

		// The listener's Addr, which Start reads after binding, calls
		// Shutdown and returns once the server is stopping.
		var srv *srvr.Server
		shut := make(chan error, 1)
		ln := listen(t)
		withAddr := addrListener{Listener: ln, addr: func() net.Addr {
			go func() { shut <- srv.Shutdown(context.Background()) }()
			eventually(t, func() bool { return errors.Is(srv.Register(okRoute("/y")), srvr.ErrServerClosed) }, "Shutdown did not stop the server")

			return ln.Addr()
		}}
		srv, err := srvr.New(srvr.WithListener(withAddr), srvr.WithLogOutput(io.Discard))
		mustNil(t, err)
		mustNil(t, srv.Register(okRoute("/x")))

		if err := srv.Start(context.Background()); !errors.Is(err, srvr.ErrServerClosed) {
			t.Fatalf("Start = %v, want an error wrapping ErrServerClosed", err)
		}
		mustNil(t, receive(t, shut, "Shutdown did not return"))
	})

	t.Run("a later route check does not run", func(t *testing.T) {
		t.Parallel()

		entered := make(chan struct{})
		var second atomic.Bool
		srv := newServer(t,
			srvr.WithRouteCheck(func(ctx context.Context, _ engine.Routes) error {
				close(entered)
				<-ctx.Done()

				return nil // ignores the interruption
			}),
			srvr.WithRouteCheck(func(context.Context, engine.Routes) error { second.Store(true); return nil }),
		)
		mustNil(t, srv.Register(okRoute("/x")))

		done := make(chan error, 1)
		go func() { done <- srv.Start(context.Background()) }()
		receive(t, entered, "the first route check did not run")
		mustNil(t, srv.Shutdown(context.Background()))

		if err := receive(t, done, "Start did not return"); !errors.Is(err, srvr.ErrServerClosed) {
			t.Fatalf("Start = %v, want an error wrapping ErrServerClosed", err)
		}
		if second.Load() {
			t.Fatal("the second route check ran after Shutdown")
		}
	})
}
