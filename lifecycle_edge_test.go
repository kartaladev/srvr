package srvr_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

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

// TestShutdown_WhileStarting: a Shutdown during the configure phase stops
// the server before it serves; nothing was served, so only AfterDrain runs.
func TestShutdown_WhileStarting(t *testing.T) {
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
	mustNil(t, <-done)
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
