package srvr_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

// TestShutdown_Drain covers server-lifecycle / Scenario: Drain within and beyond the deadline.
func TestShutdown_Drain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		work     time.Duration // 0: no request in flight
		deadline time.Duration
		wantErr  error
		outcome  int // in-flight status; -1: connection closed
	}{
		{"work 10ms deadline 1s", 10 * time.Millisecond, time.Second, nil, http.StatusOK},
		{"work 5s deadline 50ms", 5 * time.Second, 50 * time.Millisecond, context.DeadlineExceeded, -1},
		{"no request deadline 1s", 0, time.Second, nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			started, release := make(chan struct{}, 1), make(chan struct{})
			t.Cleanup(func() { close(release) })

			srv := newServer(t)
			if err := srv.Register(workRoute(tt.work, started, release)); err != nil {
				t.Fatal(err)
			}
			done := startBackground(t, srv)

			var status <-chan int
			if tt.work > 0 {
				status = inFlight("http://" + srv.Addr() + "/work")
				receive(t, started, "the in-flight request did not reach its handler")
			}

			ctx, cancel := context.WithTimeout(t.Context(), tt.deadline)
			defer cancel()

			shutdown := make(chan error, 1)
			go func() { shutdown <- srv.Shutdown(ctx) }()
			if err := receive(t, shutdown, "Shutdown did not return"); !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
				t.Fatalf("Shutdown = %v, want %v", err, tt.wantErr)
			}
			if status != nil {
				if got := receive(t, status, "the in-flight request did not finish"); got != tt.outcome {
					t.Fatalf("in-flight = %d, want %d", got, tt.outcome)
				}
			}
			if err := receive(t, done, "Start did not return"); err != nil {
				t.Fatalf("Start = %v, want nil", err)
			}
		})
	}
}

// TestShutdown_Misuse covers server-lifecycle / Scenario: Shutdown misuse.
func TestShutdown_Misuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state string
		// shutdown brings the server into state and calls Shutdown, returning its result.
		shutdown func(t *testing.T, srv *srvr.Server) error
	}{
		{
			state: "was never started",
			shutdown: func(t *testing.T, srv *srvr.Server) error {
				t.Helper()

				return srv.Shutdown(t.Context())
			},
		},
		{
			state: "was already shut down",
			shutdown: func(t *testing.T, srv *srvr.Server) error {
				t.Helper()
				done := startBackground(t, srv)
				if err := srv.Shutdown(t.Context()); err != nil {
					t.Fatal(err)
				}
				mustNil(t, <-done)

				return srv.Shutdown(t.Context())
			},
		},
		{
			state: "is being shut down by another goroutine",
			shutdown: func(t *testing.T, srv *srvr.Server) error {
				t.Helper()
				started, release := make(chan struct{}, 1), make(chan struct{})
				if err := srv.Register(workRoute(time.Hour, started, release)); err != nil {
					t.Fatal(err)
				}
				done := startBackground(t, srv)
				status := inFlight("http://" + srv.Addr() + "/work")
				<-started

				var wg sync.WaitGroup
				first := make(chan error, 1)
				wg.Go(func() { first <- srv.Shutdown(t.Context()) })

				// Wait until the first Shutdown is draining: new connections are refused.
				waitRefused(t, srv.Addr())

				second := make(chan error, 1)
				wg.Go(func() { second <- srv.Shutdown(t.Context()) })

				select {
				case err := <-second:
					t.Fatalf("second Shutdown returned %v before the first finished", err)
				case <-time.After(50 * time.Millisecond):
				}

				close(release)
				wg.Wait()
				<-status
				mustNil(t, <-done)
				if err := <-first; err != nil {
					t.Fatalf("first Shutdown = %v", err)
				}

				return <-second
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			if err := tt.shutdown(t, srv); err != nil {
				t.Fatalf("Shutdown = %v, want nil", err)
			}
			if err := srv.Start(t.Context()); !errors.Is(err, srvr.ErrServerClosed) {
				t.Fatalf("later Start = %v, want ErrServerClosed", err)
			}
		})
	}
}

// TestShutdown_NilContext covers server-lifecycle / Scenario: Shutdown with a nil context.
func TestShutdown_NilContext(t *testing.T) {
	t.Parallel()

	srv := newServer(t)
	if err := srv.Register(okRoute("/ping")); err != nil {
		t.Fatal(err)
	}
	base := startServer(t, srv)

	//nolint:staticcheck // a nil context is the row under test.
	if err := srv.Shutdown(nil); !errors.Is(err, srvr.ErrConfig) {
		t.Fatalf("Shutdown(nil) = %v, want ErrConfig", err)
	}
	if got := get(t, base+"/ping").status; got != http.StatusOK {
		t.Fatalf("GET /ping = %d, want 200: the server must keep serving", got)
	}
}

// TestShutdown_DuringContextStop covers the "is being shut down by another
// goroutine" row of server-lifecycle / Scenario: Shutdown misuse when the
// other stop is Start's context ending: a BeforeDrain hook holds that stop
// sequence while Shutdown is called, so the order is deterministic.
func TestShutdown_DuringContextStop(t *testing.T) {
	t.Parallel()

	inHook, release := make(chan struct{}), make(chan struct{})
	srv := newServer(t, srvr.WithShutdownHook(srvr.BeforeDrain, func(context.Context) error {
		close(inHook)
		<-release

		return nil
	}))
	ctx, stop := context.WithCancel(t.Context())
	done := startWith(ctx, t, srv)

	stop()
	receive(t, inHook, "the BeforeDrain hook did not run")

	shutdown := make(chan error, 1)
	go func() { shutdown <- srv.Shutdown(t.Context()) }()

	select {
	case err := <-shutdown:
		t.Fatalf("Shutdown returned %v while the stop sequence was still running", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := receive(t, shutdown, "Shutdown did not return after the stop finished"); err != nil {
		t.Fatalf("Shutdown = %v, want nil", err)
	}
	if err := receive(t, done, "Start did not return"); err != nil {
		t.Fatalf("Start = %v, want nil", err)
	}
}

// recorder records events from concurrent goroutines.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(e string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) list() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.events)
}

// TestShutdownHooks_Order covers server-lifecycle / Scenario: Hook phases and order.
func TestShutdownHooks_Order(t *testing.T) {
	t.Parallel()

	var rec recorder
	var addr atomic.Value
	var release chan struct{}

	hook := func(name string) func(context.Context) error {
		return func(ctx context.Context) error {
			if name == "A" {
				a, ok := addr.Load().(string)
				if !ok {
					rec.add("no address stored")
				}
				conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", a)
				if err == nil {
					_ = conn.Close()
					rec.add("new connections accepted")
				} else {
					rec.add("new connections refused")
				}
			}
			rec.add(name)
			if name == "B" {
				close(release)
			}

			return nil
		}
	}

	srv := newServer(t,
		srvr.WithShutdownHook(srvr.BeforeDrain, hook("A")),
		srvr.WithShutdownHook(srvr.BeforeDrain, hook("B")),
		srvr.WithShutdownHook(srvr.AfterDrain, hook("C")),
	)
	started := make(chan struct{}, 1)
	release = make(chan struct{})
	err := srv.Register(configFunc(func(g *srvr.Group) {
		g.GET("/work", func(w http.ResponseWriter, _ *http.Request) error {
			started <- struct{}{}
			<-release // closed by hook B: the request stays in flight until then
			w.WriteHeader(http.StatusOK)
			rec.add("in-flight 200")

			return nil
		})
	}))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := startWith(ctx, t, srv)
	addr.Store(srv.Addr())

	status := inFlight("http://" + srv.Addr() + "/work")
	<-started
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Start = %v", err)
	}
	rec.add("Start returned nil")

	if got := <-status; got != http.StatusOK {
		t.Fatalf("in-flight = %d", got)
	}

	want := []string{"new connections refused", "A", "B", "in-flight 200", "C", "Start returned nil"}
	if got := rec.list(); !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// hookCounts counts the runs of one BeforeDrain and one AfterDrain hook.
type hookCounts struct{ before, after atomic.Int32 }

func (c *hookCounts) options() []srvr.Option {
	return []srvr.Option{
		srvr.WithShutdownHook(srvr.BeforeDrain, func(context.Context) error { c.before.Add(1); return nil }),
		srvr.WithShutdownHook(srvr.AfterDrain, func(context.Context) error { c.after.Add(1); return nil }),
	}
}

// TestShutdownHooks covers server-lifecycle / Scenario: Hooks on every stop path.
func TestShutdownHooks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		noHooks       bool
		trigger       func(t *testing.T, srv *srvr.Server, opts []srvr.Option)
		before, after int32
	}{
		{
			name: "running, Start context cancelled",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				done := startWith(ctx, t, srv)
				cancel()
				mustNil(t, <-done)
			},
			before: 1, after: 1,
		},
		{
			name: "running, Start context deadline passes",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
				defer cancel()
				done := startWith(ctx, t, srv)
				mustNil(t, <-done)
			},
			before: 1, after: 1,
		},
		{
			name: "running, Shutdown",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				done := startBackground(t, srv)
				mustNil(t, srv.Shutdown(t.Context()))
				mustNil(t, <-done)
			},
			before: 1, after: 1,
		},
		{
			name: "running, Shutdown twice then Start context cancelled",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				done := startWith(ctx, t, srv)
				mustNil(t, srv.Shutdown(t.Context()))
				mustNil(t, srv.Shutdown(t.Context()))
				cancel()
				mustNil(t, <-done)
			},
			before: 1, after: 1,
		},
		{
			name: "running, Shutdown and cancellation concurrently",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				done := startWith(ctx, t, srv)
				var wg sync.WaitGroup
				wg.Go(func() { _ = srv.Shutdown(t.Context()) })
				wg.Go(cancel)
				wg.Wait()
				mustNil(t, <-done)
			},
			before: 1, after: 1,
		},
		{
			name: "never started, Shutdown",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				mustNil(t, srv.Shutdown(t.Context()))
			},
			before: 0, after: 1,
		},
		{
			name: "cannot bind its address",
			trigger: func(t *testing.T, _ *srvr.Server, opts []srvr.Option) {
				t.Helper()
				taken := listen(t)
				srv, err := srvr.New(append(opts, srvr.WithAddr(taken.Addr().String()))...)
				if err != nil {
					t.Fatal(err)
				}
				if err := srv.Start(t.Context()); err == nil {
					t.Fatal("Start = nil, want a bind error")
				}
			},
			before: 0, after: 1,
		},
		{
			name: "has a route configurer that panics",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				mustNil(t, srv.Register(&PanicAPI{}))
				if err := srv.Start(t.Context()); !errors.Is(err, srvr.ErrRouteConfig) {
					t.Fatalf("Start = %v, want ErrRouteConfig", err)
				}
			},
			before: 0, after: 1,
		},
		{
			name: "has two routes with the same pattern",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				mustNil(t, srv.Register(okRoute("/dup")))
				mustNil(t, srv.Register(okRoute("/dup")))
				if err := srv.Start(t.Context()); !errors.Is(err, srvr.ErrRouteConfig) {
					t.Fatalf("Start = %v, want ErrRouteConfig", err)
				}
			},
			before: 0, after: 1,
		},
		{
			name: "new, Start with an already cancelled context",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if err := srv.Start(ctx); err == nil {
					t.Fatal("Start = nil")
				}
			},
			before: 0, after: 1,
		},
		{
			name: "new, Start(nil)",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				//nolint:staticcheck // a nil context is the row under test.
				if err := srv.Start(nil); err == nil {
					t.Fatal("Start(nil) = nil")
				}
			},
			before: 0, after: 0,
		},
		{
			name: "running, Start again returns ErrServerStarted",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				startServer(t, srv)
				if err := srv.Start(t.Context()); err == nil {
					t.Fatal("second Start = nil")
				}
			},
			before: 0, after: 0,
		},
		{
			name: "stopped by its Start context, Start again returns ErrServerClosed",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				ctx, cancel := context.WithCancel(t.Context())
				done := startWith(ctx, t, srv)
				cancel()
				mustNil(t, <-done)
				if err := srv.Start(t.Context()); err == nil {
					t.Fatal("Start after stop = nil")
				}
			},
			before: 1, after: 1,
		},
		{
			name:    "no hooks registered, Shutdown",
			noHooks: true,
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				mustNil(t, srv.Shutdown(t.Context()))
			},
			before: 0, after: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var c hookCounts
			var opts []srvr.Option
			if !tt.noHooks {
				opts = c.options()
			}
			srv := newServer(t, opts...)

			tt.trigger(t, srv, opts)

			if b, a := c.before.Load(), c.after.Load(); b != tt.before || a != tt.after {
				t.Fatalf("hooks ran before=%d after=%d, want %d and %d", b, a, tt.before, tt.after)
			}
		})
	}
}

// TestShutdownHooks_Faulty covers server-lifecycle / Scenario: Faulty hooks.
func TestShutdownHooks_Faulty(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	t.Cleanup(func() { close(block) })

	tests := []struct {
		fault    string
		hook     func(context.Context) error
		opts     []srvr.Option
		wantIs   []error
		contains string
		within   time.Duration
	}{
		{fault: "returns an error", hook: func(context.Context) error { return errors.New("flush failed") }, wantIs: []error{srvr.ErrShutdownHook}, contains: "flush failed"},
		{fault: "panics", hook: func(context.Context) error { panic("boom") }, wantIs: []error{srvr.ErrShutdownHook}, contains: "boom"},
		{fault: "panics with nil", hook: func(context.Context) error { panic(nil) }, wantIs: []error{srvr.ErrShutdownHook}, contains: "panic"},
		{
			fault:  "blocks and ignores its context",
			hook:   func(context.Context) error { <-block; return nil },
			opts:   []srvr.Option{srvr.WithShutdownHookTimeout(50 * time.Millisecond)},
			wantIs: []error{srvr.ErrShutdownHook, context.DeadlineExceeded},
			within: 200 * time.Millisecond,
		},
		{fault: "returns a typed-nil error", hook: func(context.Context) error { return (*derefError)(nil) }, wantIs: []error{srvr.ErrShutdownHook}},
		{fault: "returns an error whose Error panics", hook: func(context.Context) error { return badError{} }, wantIs: []error{srvr.ErrShutdownHook}},
		{fault: "returns an error whose Error panics with a value whose Error panics", hook: func(context.Context) error { return selfPanicError{} }, wantIs: []error{srvr.ErrShutdownHook, selfPanicError{}}},
		{fault: "panics with a value whose Error panics with itself", hook: func(context.Context) error { panic(selfPanicError{}) }, wantIs: []error{srvr.ErrShutdownHook}, contains: "<panic in Error(): srvr_test.selfPanicError>"},
		{fault: "returns nil", hook: func(context.Context) error { return nil }},
	}

	for _, tt := range tests {
		t.Run(tt.fault, func(t *testing.T) {
			t.Parallel()

			var secondRan atomic.Bool
			opts := append([]srvr.Option{
				srvr.WithShutdownHook(srvr.AfterDrain, tt.hook),
				srvr.WithShutdownHook(srvr.AfterDrain, func(context.Context) error { secondRan.Store(true); return nil }),
			}, tt.opts...)
			srv := newServer(t, opts...)

			ctx, cancel := context.WithCancel(t.Context())
			done := startWith(ctx, t, srv)

			begin := time.Now()
			cancel()
			err := <-done

			if tt.within > 0 && time.Since(begin) > tt.within {
				t.Fatalf("Start returned after %v, want within %v", time.Since(begin), tt.within)
			}
			assertErr(t, err, tt.wantIs, tt.contains)
			// The hooks of a phase share its budget, so after a blocking hook
			// the second one runs with a spent context and srvr does not wait
			// for it; it still runs.
			eventually(t, secondRan.Load, "the second hook did not run")
			assertErr(t, shutdownBounded(t, srv), nil, "")
		})
	}
}

// TestHostileHookErrors covers server-lifecycle / Scenario: Hostile hook
// errors when Shutdown is called before Start.
func TestHostileHookErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fault string
		hook  func(context.Context) error
	}{
		{"returns a typed-nil error", func(context.Context) error { return (*derefError)(nil) }},
		{"returns an error whose Error panics", func(context.Context) error { return badError{} }},
		{"returns an error whose Error panics with a value whose Error panics", func(context.Context) error { return selfPanicError{} }},
		{"panics with a value whose Error panics with itself", func(context.Context) error { panic(selfPanicError{}) }},
	}

	for _, tt := range tests {
		t.Run(tt.fault, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t, srvr.WithShutdownHook(srvr.AfterDrain, tt.hook))

			assertErr(t, recoverPanic(func() error { return srv.Shutdown(t.Context()) }), []error{srvr.ErrShutdownHook}, "")
			assertErr(t, shutdownBounded(t, srv), nil, "")
			assertErr(t, srv.Start(t.Context()), []error{srvr.ErrServerClosed}, "")
		})
	}
}

// TestShutdownHooks_FaultyDuringShutdown covers server-lifecycle / Scenario:
// Faulty hook during Shutdown.
func TestShutdownHooks_FaultyDuringShutdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		deadline, work time.Duration
		wantIs         []error
	}{
		{time.Second, 10 * time.Millisecond, []error{srvr.ErrShutdownHook}},
		{50 * time.Millisecond, 5 * time.Second, []error{srvr.ErrShutdownHook, context.DeadlineExceeded}},
	}

	for _, tt := range tests {
		t.Run(tt.deadline.String(), func(t *testing.T) {
			t.Parallel()

			started, release := make(chan struct{}, 1), make(chan struct{})
			t.Cleanup(func() { close(release) })

			srv := newServer(t, srvr.WithShutdownHook(srvr.BeforeDrain, func(context.Context) error {
				return errors.New("close failed")
			}))
			if err := srv.Register(workRoute(tt.work, started, release)); err != nil {
				t.Fatal(err)
			}
			done := startBackground(t, srv)
			status := inFlight("http://" + srv.Addr() + "/work")
			<-started

			ctx, cancel := context.WithTimeout(t.Context(), tt.deadline)
			defer cancel()

			assertErr(t, srv.Shutdown(ctx), tt.wantIs, "close failed")
			<-status
			if err := <-done; err != nil && !errors.Is(err, srvr.ErrShutdownHook) {
				t.Fatalf("Start = %v", err)
			}
		})
	}
}

// TestShutdownHooks_Blocking covers server-lifecycle / Scenario: Blocking and
// re-entrant hooks. The server.shutdown_hook.failed record is asserted by
// TestLifecycleRecords.
func TestShutdownHooks_Blocking(t *testing.T) {
	t.Parallel()

	t.Run("BeforeDrain hook blocks past the drain budget", func(t *testing.T) {
		t.Parallel()

		block := make(chan struct{})
		t.Cleanup(func() { close(block) })

		var afterRuns atomic.Int32
		srv := newServer(t,
			srvr.WithShutdownTimeout(50*time.Millisecond),
			srvr.WithShutdownHookTimeout(5*time.Second),
			srvr.WithShutdownHook(srvr.BeforeDrain, func(context.Context) error { <-block; return nil }),
			srvr.WithShutdownHook(srvr.AfterDrain, func(context.Context) error { afterRuns.Add(1); return nil }),
		)

		ctx, cancel := context.WithCancel(t.Context())
		done := startWith(ctx, t, srv)

		begin := time.Now()
		cancel()
		err := <-done

		if d := time.Since(begin); d > 300*time.Millisecond {
			t.Fatalf("Start returned after %v, want within 300ms", d)
		}
		assertErr(t, err, []error{srvr.ErrShutdownHook, context.DeadlineExceeded}, "")
		if n := afterRuns.Load(); n != 1 {
			t.Fatalf("AfterDrain hook ran %d times, want 1", n)
		}
	})

	t.Run("AfterDrain hook calls Shutdown", func(t *testing.T) {
		t.Parallel()

		var srv *srvr.Server
		inner := make(chan error, 1)
		srv = newServer(t,
			srvr.WithShutdownTimeout(15*time.Second),
			srvr.WithShutdownHookTimeout(100*time.Millisecond),
			srvr.WithShutdownHook(srvr.AfterDrain, func(context.Context) error {
				inner <- srv.Shutdown(context.Background()) //nolint:contextcheck,usetesting // the row calls Shutdown with a background context.

				return nil
			}),
		)

		ctx, cancel := context.WithCancel(t.Context())
		done := startWith(ctx, t, srv)

		begin := time.Now()
		cancel()
		err := <-done

		if d := time.Since(begin); d > 500*time.Millisecond {
			t.Fatalf("Start returned after %v, want within 500ms", d)
		}
		assertErr(t, err, []error{srvr.ErrShutdownHook, context.DeadlineExceeded}, "")

		select {
		case err := <-inner:
			if err != nil {
				t.Fatalf("the hook's Shutdown = %v, want nil", err)
			}
		case <-time.After(time.Second):
			t.Fatal("the hook's Shutdown did not return: deadlock")
		}
	})
}

// assertErr checks that err matches every target and contains text; with
// no targets it must be nil.
func assertErr(t *testing.T, err error, targets []error, text string) {
	t.Helper()

	if len(targets) == 0 {
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}

		return
	}

	for _, target := range targets {
		if !errors.Is(err, target) {
			t.Fatalf("err = %v, want it to match %v", err, target)
		}
	}

	if !strings.Contains(err.Error(), text) {
		t.Fatalf("err = %v, want it to contain %q", err, text)
	}
}

// TestShutdown_HostileContext covers server-lifecycle / Scenario: A
// Shutdown context with a hostile Err.
func TestShutdown_HostileContext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"an error whose Error panics with a value whose Error panics", selfPanicError{}},
		{"a typed-nil error", (*derefError)(nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			block := make(chan struct{})
			t.Cleanup(func() { close(block) })

			srv := newServer(t, srvr.WithShutdownHook(srvr.BeforeDrain, func(context.Context) error { <-block; return nil }))
			startWith(context.Background(), t, srv) //nolint:usetesting // Start is never awaited: it may run past the test body until the cleanup releases the hook

			ctx := newHostileContext(t, 50*time.Millisecond, tt.err)
			err := bounded(t, "Shutdown did not return", func() error { return recoverPanic(func() error { return srv.Shutdown(ctx) }) })
			if errors.Is(err, errPanicked) || !errors.Is(err, srvr.ErrShutdownHook) || !errors.Is(err, tt.err) {
				t.Fatalf("Shutdown = %s, want ErrShutdownHook and the context error, without panicking", safeText(err))
			}

			assertErr(t, shutdownBounded(t, srv), nil, "")
		})
	}
}

// TestGoexit covers server-lifecycle / Scenario: Goexit in consumer code.
func TestGoexit(t *testing.T) {
	t.Parallel()

	goexit := func(context.Context) error { runtime.Goexit(); return nil }

	tests := []struct {
		name string
		opts []srvr.Option
		// run starts or stops the server and returns the call's result.
		run      func(srv *srvr.Server) error
		want     error
		failures int // server.shutdown_hook.failed records
	}{
		{
			name: "route check",
			opts: []srvr.Option{srvr.WithRouteCheck(func(ctx context.Context, _ engine.Routes) error { return goexit(ctx) })},
			run:  func(srv *srvr.Server) error { return srv.Start(context.Background()) }, //nolint:usetesting // run never touches a *testing.T: it runs on a goroutine of its own
			want: srvr.ErrRouteConfig,
		},
		{
			name: "BeforeDrain hook",
			opts: []srvr.Option{srvr.WithShutdownHook(srvr.BeforeDrain, goexit)},
			run:  startThenShutdown,
			want: srvr.ErrShutdownHook, failures: 1,
		},
		{
			name: "AfterDrain hook, unbounded budget",
			opts: []srvr.Option{srvr.WithShutdownHook(srvr.AfterDrain, goexit), srvr.WithShutdownHookTimeout(-1)},
			run:  startThenShutdown,
			want: srvr.ErrShutdownHook, failures: 1,
		},
		{
			name: "BeforeDrain hook returning nil",
			opts: []srvr.Option{srvr.WithShutdownHook(srvr.BeforeDrain, func(context.Context) error { return nil })},
			run:  startThenShutdown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out := &logBuffer{}
			srv := newServer(t, append(tt.opts, srvr.WithLogOutput(out))...)
			mustNil(t, srv.Register(okRoute("/x")))

			done := make(chan error, 1)
			go func() { done <- recoverPanic(func() error { return tt.run(srv) }) }()

			err := receiveWithin(t, done, time.Second, "the call did not return")

			if (tt.want == nil && err != nil) || !errors.Is(err, tt.want) {
				t.Fatalf("result = %v, want %v", err, tt.want)
			}
			if n := len(out.find(t, "server.shutdown_hook.failed")); n != tt.failures {
				t.Fatalf("%d server.shutdown_hook.failed records, want %d:\n%s", n, tt.failures, out)
			}
		})
	}
}

// startThenShutdown starts srv, waits until it is ready, and returns the
// result of Shutdown, or an error when Start fails or does not end. It
// never touches a *testing.T: it runs on a goroutine of its own.
func startThenShutdown(srv *srvr.Server) error {
	started := make(chan error, 1)
	go func() { started <- srv.Start(context.Background()) }()

	select {
	case <-srv.Ready():
	case err := <-started:
		return fmt.Errorf("Start returned before readiness: %w", err)
	}

	err := srv.Shutdown(context.Background())

	select {
	case startErr := <-started:
		if startErr != nil && err == nil {
			return fmt.Errorf("start: %w", startErr)
		}
	case <-time.After(waitLimit):
		return errors.New("Start did not return after Shutdown")
	}

	return err
}

// TestShutdown_StuckListener covers server-lifecycle / Scenario: Stuck
// listener on every engine, the net/http row.
func TestShutdown_StuckListener(t *testing.T) {
	t.Parallel()

	tests := []struct{ engine string }{{"net/http"}}

	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			t.Parallel()

			stuck := &stuckListener{Listener: listen(t), accepting: make(chan struct{}), release: make(chan struct{})}
			release := sync.OnceFunc(func() { close(stuck.release) })
			t.Cleanup(release)

			srv, err := srvr.New(srvr.WithListener(stuck), srvr.WithShutdownTimeout(200*time.Millisecond), srvr.WithLogOutput(io.Discard))
			if err != nil {
				t.Fatal(err)
			}
			mustNil(t, srv.Register(okRoute("/ok")))

			started := make(chan error, 1)
			go func() { started <- recoverPanic(func() error { return srv.Start(context.Background()) }) }() //nolint:usetesting // Start must outlive the Shutdown calls under test
			receive(t, stuck.accepting, "Serve never called Accept")

			shutdown := func() <-chan error {
				done := make(chan error, 1)
				go func() { done <- recoverPanic(func() error { return srv.Shutdown(context.Background()) }) }() //nolint:usetesting // the scenario's context has no deadline

				return done
			}

			if err := receiveWithin(t, shutdown(), time.Second, "Shutdown did not return within 1s"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Shutdown = %v, want the forced drain's context.DeadlineExceeded", err)
			}
			if err := receiveWithin(t, shutdown(), time.Second, "later Shutdown did not return within 1s"); err != nil {
				t.Fatalf("later Shutdown = %v, want nil", err)
			}

			release()
			mustNil(t, receive(t, started, "Start did not return after Accept was released"))
		})
	}
}
