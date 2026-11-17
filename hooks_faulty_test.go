package srvr_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

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

			ctx, cancel := context.WithCancel(context.Background())
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

			assertErr(t, recoverPanic(func() error { return srv.Shutdown(context.Background()) }), []error{srvr.ErrShutdownHook}, "")
			assertErr(t, shutdownBounded(t, srv), nil, "")
			assertErr(t, srv.Start(context.Background()), []error{srvr.ErrServerClosed}, "")
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

			ctx, cancel := context.WithTimeout(context.Background(), tt.deadline)
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

		ctx, cancel := context.WithCancel(context.Background())
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
				inner <- srv.Shutdown(context.Background()) //nolint:contextcheck // the row calls Shutdown with a background context.

				return nil
			}),
		)

		ctx, cancel := context.WithCancel(context.Background())
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
			startWith(context.Background(), t, srv)

			ctx := newHostileContext(t, 50*time.Millisecond, tt.err)
			err := bounded(t, "Shutdown did not return", func() error { return recoverPanic(func() error { return srv.Shutdown(ctx) }) })
			if errors.Is(err, errPanicked) || !errors.Is(err, srvr.ErrShutdownHook) || !errors.Is(err, tt.err) {
				t.Fatalf("Shutdown = %s, want ErrShutdownHook and the context error, without panicking", safeText(err))
			}

			assertErr(t, shutdownBounded(t, srv), nil, "")
		})
	}
}
