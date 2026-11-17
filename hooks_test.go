package srvr_test

import (
	"context"
	"net"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

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
				conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", addr.Load().(string))
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

	ctx, cancel := context.WithCancel(context.Background())
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
				ctx, cancel := context.WithCancel(context.Background())
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
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
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
				mustNil(t, srv.Shutdown(context.Background()))
				mustNil(t, <-done)
			},
			before: 1, after: 1,
		},
		{
			name: "running, Shutdown twice then Start context cancelled",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				done := startWith(ctx, t, srv)
				mustNil(t, srv.Shutdown(context.Background()))
				mustNil(t, srv.Shutdown(context.Background()))
				cancel()
				mustNil(t, <-done)
			},
			before: 1, after: 1,
		},
		{
			name: "running, Shutdown and cancellation concurrently",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				done := startWith(ctx, t, srv)
				var wg sync.WaitGroup
				wg.Go(func() { _ = srv.Shutdown(context.Background()) })
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
				mustNil(t, srv.Shutdown(context.Background()))
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
				if err := srv.Start(context.Background()); err == nil {
					t.Fatal("Start = nil, want a bind error")
				}
			},
			before: 0, after: 1,
		},
		{
			name: "new, Start with an already cancelled context",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
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
				if err := srv.Start(context.Background()); err == nil {
					t.Fatal("second Start = nil")
				}
			},
			before: 0, after: 0,
		},
		{
			name: "stopped by its Start context, Start again returns ErrServerClosed",
			trigger: func(t *testing.T, srv *srvr.Server, _ []srvr.Option) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				done := startWith(ctx, t, srv)
				cancel()
				mustNil(t, <-done)
				if err := srv.Start(context.Background()); err == nil {
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
				mustNil(t, srv.Shutdown(context.Background()))
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
