package srvr_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"go.uber.org/goleak"
)

// leakOptions are the goleak options of the core's test binaries. The core
// has no deliberate long-lived goroutine, so it ignores nothing.
var leakOptions []goleak.Option

// TestMain fails the test binary when a goroutine is still running after
// its tests passed.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m, leakOptions...) }

// serveGoroutine is the function of srvr's goroutine serving a listener.
const serveGoroutine = "github.com/kartaladev/srvr/internal/engine.(*Lifecycle[...]).serveOn"

// findLeaks runs goleak.Find with leakOptions plus extra, without
// appending to the shared leakOptions slice.
func findLeaks(extra ...goleak.Option) error {
	return goleak.Find(slices.Concat(leakOptions, extra)...)
}

// checkGoroutines fails t when a goroutine started since this call is
// still running after t and all its cleanups end. Call it first in the
// test: the check is registered as t's first cleanup, so it runs last,
// after the cleanups that stop servers and close clients. Only for tests
// that are not parallel: a parallel test would see the goroutines of the
// tests running alongside it.
func checkGoroutines(t *testing.T) {
	t.Helper()

	current := goleak.IgnoreCurrent()
	t.Cleanup(func() {
		if err := findLeaks(current); err != nil {
			t.Error(err)
		}
	})
}

// reportedGoroutine matches the header of one goroutine in a goleak report.
var reportedGoroutine = regexp.MustCompile(`Goroutine \d+ in state`)

// onlyLeak fails t unless exactly one goroutine started since before is
// still running and its stack names fn.
func onlyLeak(t *testing.T, before goleak.Option, fn string) {
	t.Helper()

	err := findLeaks(before)
	if err == nil {
		t.Fatalf("no leak reported, want one naming %s", fn)
	}

	if n := len(reportedGoroutine.FindAllString(err.Error(), -1)); n != 1 || !strings.Contains(err.Error(), fn) {
		t.Fatalf("leak report has %d goroutines, want only one naming %s: %v", n, fn, err)
	}
}

// blockUntil blocks until ch is closed. It is a named function so a leak
// report names it.
func blockUntil(ch <-chan struct{}) { <-ch }

// startBlocked starts a goroutine running blockUntil and returns the
// function that ends it.
func startBlocked() (release func()) {
	ch := make(chan struct{})
	go blockUntil(ch)
	return func() { close(ch) }
}

// TestLeakCheck proves the leak check the core's test binaries run reports
// a goroutine left running and nothing else. Not parallel: goleak.Find sees
// every goroutine of the process.
func TestLeakCheck(t *testing.T) {
	tests := []struct {
		name string
		// start makes the goroutine run inside a subtest and returns a
		// function the test calls after the check to end it, or nil.
		start func(t *testing.T) (release func())
		want  string // a function the report names; "" for no report
	}{
		{"blocked on a channel that is never closed", func(*testing.T) func() {
			return startBlocked()
		}, "srvr_test.blockUntil"},
		{"blocked on a channel the test closes at cleanup", func(t *testing.T) func() {
			t.Helper()
			t.Cleanup(startBlocked())
			return nil
		}, ""},
		{"none", func(*testing.T) func() { return nil }, ""},
		{"ends on its own 20ms after the test", func(*testing.T) func() {
			go time.Sleep(20 * time.Millisecond)
			return nil
		}, ""},
		// The sleep outlives goleak's retry window (about 430ms); the case
		// then waits for it to end so the binary-level check stays green.
		{"sleeping in time.Sleep with no srvr frame, never ending", func(*testing.T) func() {
			go time.Sleep(700 * time.Millisecond)
			return nil
		}, "time.Sleep"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := goleak.IgnoreCurrent()

			var release func()
			t.Run("run", func(t *testing.T) { release = tt.start(t) })

			err := findLeaks(before)
			if release != nil {
				release()
			}
			eventually(t, func() bool { return findLeaks(before) == nil }, "the case's goroutines did not end")

			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("leak reported: %v", err)
			case tt.want != "" && err == nil:
				t.Fatalf("no leak reported, want one naming %s", tt.want)
			case tt.want != "" && !strings.Contains(err.Error(), tt.want):
				t.Fatalf("leak report does not name %s: %v", tt.want, err)
			}
		})
	}
}

// abandonedHook returns a hook that ignores its context and blocks until
// release is closed. It is a named function so a leak report names it.
func abandonedHook(release <-chan struct{}) func(context.Context) error {
	return func(context.Context) error {
		<-release

		return nil
	}
}

// TestAbandonedHook_Goroutines covers test-quality / Scenario: Abandoned
// consumer code is the only goroutine left (hook rows). Not parallel: the
// checks see every goroutine of the process.
func TestAbandonedHook_Goroutines(t *testing.T) {
	tests := []struct {
		name    string
		phase   srvr.ShutdownPhase
		opts    []srvr.Option
		ctx     time.Duration // the Shutdown context's deadline; 0: none
		release bool          // check after releasing the hook
	}{
		{"BeforeDrain hook, 50ms Shutdown context, after releasing the hook", srvr.BeforeDrain, nil, 50 * time.Millisecond, true},
		{"BeforeDrain hook, 50ms Shutdown context, before releasing the hook", srvr.BeforeDrain, nil, 50 * time.Millisecond, false},
		{"AfterDrain hook, 50ms ShutdownHookTimeout, after releasing the hook", srvr.AfterDrain, []srvr.Option{srvr.WithShutdownHookTimeout(50 * time.Millisecond)}, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := goleak.IgnoreCurrent()
			release := make(chan struct{})
			end := sync.OnceFunc(func() { close(release) })
			defer end()

			srv := newServer(t, append([]srvr.Option{srvr.WithShutdownHook(tt.phase, abandonedHook(release))}, tt.opts...)...)
			done := runWith(t.Context(), t, srv)

			ctx := t.Context()
			if tt.ctx > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.ctx)
				defer cancel()
			}

			// The abandoned hook's error, which both calls report, is not
			// under test here.
			_ = srv.Shutdown(ctx)
			_ = receive(t, done, "Start did not return")

			if tt.release {
				end()
				mustNil(t, findLeaks(before))

				return
			}

			onlyLeak(t, before, "srvr_test.abandonedHook")
		})
	}
}

// TestLifecycle_Goroutines covers test-quality / Scenario: Lifecycle leaves
// no goroutine behind (net/http rows). Not parallel: the checks see every
// goroutine of the process.
func TestLifecycle_Goroutines(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
		// leak is the function a "yes" row's report names; "" for "no".
		leak string
	}{
		{"one route, Start, GET /ok with keep-alive, Shutdown", func(t *testing.T) {
			t.Helper()
			srv := newServer(t)
			mustNil(t, srv.Register(okRoute("/ok")))
			startGetShutdown(t, srv, http.StatusOK)
		}, ""},
		{"BeforeDrain and AfterDrain hooks returning nil, Start, Shutdown", func(t *testing.T) {
			t.Helper()
			nop := func(context.Context) error { return nil }
			startGetShutdown(t, newServer(t, srvr.WithShutdownHook(srvr.BeforeDrain, nop), srvr.WithShutdownHook(srvr.AfterDrain, nop)), 0)
		}, ""},
		{"WithListener on 127.0.0.1:0, Start, Shutdown", func(t *testing.T) {
			t.Helper()
			srv, err := srvr.New(srvr.WithListener(listen(t)))
			mustNil(t, err)
			startGetShutdown(t, srv, 0)
		}, ""},
		{"one route, Shutdown before Start", func(t *testing.T) {
			t.Helper()
			srv := newServer(t)
			mustNil(t, srv.Register(okRoute("/ok")))

			mustNil(t, shutdownBounded(t, srv))
		}, ""},
		{"one route, Run with a context cancelled after it is running", func(t *testing.T) {
			t.Helper()
			srv := newServer(t)
			mustNil(t, srv.Register(okRoute("/ok")))
			ctx, cancel := context.WithCancel(t.Context())
			done := runWith(ctx, t, srv)

			cancel()
			mustNil(t, receive(t, done, "Run did not return"))
		}, ""},
		{"one route, Start, then its context cancelled, then Shutdown", func(t *testing.T) {
			t.Helper()
			srv := newServer(t)
			mustNil(t, srv.Register(okRoute("/ok")))
			ctx, cancel := context.WithCancel(t.Context())
			mustNil(t, startBounded(ctx, t, srv))

			cancel()
			mustNil(t, shutdownBounded(t, srv))
		}, ""},
		{"WithListener whose Accept returns a permanent error after Start, the failure, Wait", func(t *testing.T) {
			t.Helper()
			ln := newFaultListener(t, func() (net.Conn, error) { return nil, errAccept })
			srv, err := srvr.New(srvr.WithListener(ln), srvr.WithLogOutput(io.Discard))
			mustNil(t, err)
			mustNil(t, startBounded(t.Context(), t, srv))

			ln.fail(t)
			if err := receive(t, waitAsync(srv), "Wait did not return"); !errors.Is(err, errAccept) {
				t.Fatalf("Wait = %v, want the accept error", err)
			}
		}, ""},
		{"one route, Start, Wait from 3 goroutines, Shutdown", func(t *testing.T) {
			t.Helper()
			srv := newServer(t)
			mustNil(t, srv.Register(okRoute("/ok")))
			mustNil(t, startBounded(t.Context(), t, srv))

			waits := []<-chan error{waitAsync(srv), waitAsync(srv), waitAsync(srv)}
			mustNil(t, shutdownBounded(t, srv))
			for _, w := range waits {
				mustNil(t, receive(t, w, "Wait did not return"))
			}
		}, ""},
		{"address already in use, Start fails", func(t *testing.T) {
			t.Helper()
			srv := newServer(t, srvr.WithAddr(listen(t).Addr().String()))

			if err := startBounded(t.Context(), t, srv); err == nil {
				t.Fatal("Start = nil on an address in use")
			}
		}, ""},
		{"a handler that panics, Start, GET /ok, Shutdown", func(t *testing.T) {
			t.Helper()
			srv := newServer(t)
			mustNil(t, srv.Register(on("", "/ok", func(http.ResponseWriter, *http.Request) error { panic("boom") })))
			startGetShutdown(t, srv, http.StatusInternalServerError)
		}, ""},
		{"a handler blocking until its request context ends, Shutdown with a 50ms drain deadline", func(t *testing.T) {
			t.Helper()
			started := make(chan struct{})
			srv := newServer(t)
			mustNil(t, srv.Register(on("", "/block", func(_ http.ResponseWriter, r *http.Request) error {
				close(started)
				<-r.Context().Done()

				return nil
			})))
			done := runWith(t.Context(), t, srv)
			registerClient(t, srv.Addr())

			res := inFlight("http://" + srv.Addr() + "/block")
			receive(t, started, "GET /block did not reach its handler")

			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()

			if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Shutdown = %v, want the drain deadline", err)
			}
			receive(t, res, "GET /block did not end")
			mustNil(t, receive(t, done, "Run did not return"))
		}, ""},
		{"one route, Start and never Shutdown", func(t *testing.T) {
			t.Helper()
			srv := newServer(t)
			mustNil(t, srv.Register(okRoute("/ok")))
			mustNil(t, startBounded(context.Background(), t, srv)) //nolint:usetesting // the server outlives the context

			t.Cleanup(func() { // stop it after the check, so the binary stays green
				mustNil(t, shutdownBounded(t, srv))
			})
		}, serveGoroutine},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.leak == "" {
				checkGoroutines(t)
				tt.run(t)

				return
			}

			before := goleak.IgnoreCurrent()
			tt.run(t)

			err := findLeaks(before)
			if err == nil || !strings.Contains(err.Error(), tt.leak) {
				t.Fatalf("leak report = %v, want one naming %s", err, tt.leak)
			}
		})
	}
}

// TestLifecycle_GoroutinesWhileRunning covers test-quality / Scenario:
// Lifecycle leaves no goroutine behind, row "Run in a goroutine, the leak
// check run while it serves, then the Run context cancelled". Not parallel.
func TestLifecycle_GoroutinesWhileRunning(t *testing.T) {
	before := goleak.IgnoreCurrent()

	srv := newServer(t)
	mustNil(t, srv.Register(okRoute("/ok")))
	ctx, cancel := context.WithCancel(t.Context())
	done := runWith(ctx, t, srv)

	if err := findLeaks(before); err == nil || !strings.Contains(err.Error(), serveGoroutine) {
		t.Fatalf("leak report while serving = %v, want one naming %s", err, serveGoroutine)
	}

	cancel()
	mustNil(t, receive(t, done, "Run did not return"))

	if err := findLeaks(before); err != nil {
		t.Fatalf("leak report after Run returned = %v, want none", err)
	}
}

// startGetShutdown starts srv, sends GET /ok with keep-alive when want is
// not 0 and fails unless it answers want, then shuts srv down and waits
// for Wait to return.
func startGetShutdown(t *testing.T, srv *srvr.Server, want int) {
	t.Helper()

	mustNil(t, startBounded(t.Context(), t, srv))
	registerClient(t, srv.Addr())

	if want != 0 {
		if got := get(t, "http://"+srv.Addr()+"/ok").status; got != want {
			t.Fatalf("GET /ok = %d, want %d", got, want)
		}
	}

	mustNil(t, shutdownBounded(t, srv))
	mustNil(t, receive(t, waitAsync(srv), "Wait did not return"))
}

// stackBuf holds every goroutine's stack for serveOnRunning; only the
// non-parallel TestServeGoroutine uses it.
var stackBuf = make([]byte, 1<<20)

// serveOnRunning reports whether a goroutine is running srvr's serveOn now,
// read once from the stacks of every goroutine.
func serveOnRunning() bool {
	return bytes.Contains(stackBuf[:runtime.Stack(stackBuf, true)], []byte(serveGoroutine))
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

// TestServeGoroutine covers server-lifecycle / Scenario: Serve goroutine
// after stop. Not parallel: it reads every goroutine's stack.
func TestServeGoroutine(t *testing.T) {
	tests := []struct {
		name     string
		listener func(t *testing.T) (net.Listener, func())
		returned bool // the serve goroutine has returned when Shutdown returns
		// drain is the ShutdownTimeout; a stuck listener makes the drain
		// run to it and Shutdown report the forced drain.
		drain time.Duration
	}{
		{"its own :0 listener", func(*testing.T) (net.Listener, func()) { return nil, func() {} }, true, 0},
		{"a WithListener listener", func(t *testing.T) (net.Listener, func()) {
			t.Helper()
			return listen(t), func() {}
		}, true, 0},
		{"a WithListener listener whose Close does not unblock Accept", func(t *testing.T) (net.Listener, func()) {
			t.Helper()
			stuck := &stuckListener{Listener: listen(t), accepting: make(chan struct{}), release: make(chan struct{})}

			return stuck, func() { close(stuck.release) }
		}, false, 200 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkGoroutines(t)

			ln, release := tt.listener(t)
			srv := newServer(t)
			if ln != nil {
				var err error
				opts := []srvr.Option{srvr.WithListener(ln), srvr.WithLogOutput(io.Discard)}
				if tt.drain > 0 {
					opts = append(opts, srvr.WithShutdownTimeout(tt.drain))
				}
				srv, err = srvr.New(opts...)
				mustNil(t, err)
			}
			mustNil(t, srv.Register(okRoute("/ok")))
			done := runWith(t.Context(), t, srv)

			// The goroutine exists once Ready is closed, but its stack shows
			// serveOn only once it has been scheduled.
			eventually(t, serveOnRunning, "no serveOn goroutine while serving")
			if stuck, ok := ln.(*stuckListener); ok {
				receive(t, stuck.accepting, "Serve never called Accept")
			}

			begin := time.Now()
			if err := srv.Shutdown(t.Context()); (tt.drain == 0 && err != nil) || (tt.drain > 0 && !errors.Is(err, context.DeadlineExceeded)) {
				t.Fatalf("Shutdown = %v", err)
			}
			if d, limit := time.Since(begin), time.Second+tt.drain+300*time.Millisecond; d > limit {
				t.Fatalf("Shutdown took %s, want at most %s (1s plus the drain)", d, limit)
			}
			// serveOn closes its done channel just before it returns, so its
			// frame may show for an instant after Shutdown: allow 50ms.
			running := true
			for deadline := time.Now().Add(50 * time.Millisecond); running && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
				running = serveOnRunning()
			}
			release()

			mustNil(t, receive(t, done, "Start did not return"))
			if running == tt.returned {
				t.Fatalf("serveOn running after Shutdown = %v, want %v", running, !tt.returned)
			}
		})
	}
}
