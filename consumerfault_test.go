package srvr_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/kartaladev/srvr"
)

// MiddlewareAPI registers one route under group middleware whose constructor
// panics with value.
type MiddlewareAPI struct {
	value any
	mount bool
}

func (a *MiddlewareAPI) Configure(g *srvr.Group) {
	g.Use(func(http.Handler) http.Handler { panic(a.value) })

	if a.mount {
		g.Mount("/static/", http.FileServerFS(fstest.MapFS{"a.txt": {Data: []byte("a")}}))

		return
	}

	g.GET("/x", status(http.StatusOK))
}

// TestMiddleware_ConstructorPanics covers route-registration / Scenario:
// Middleware constructor panics.
func TestMiddleware_ConstructorPanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		route string
		api   *MiddlewareAPI
		text  string
	}{
		{`"GET /x"`, &MiddlewareAPI{value: "mw"}, "mw"},
		{`"GET /x"`, &MiddlewareAPI{value: selfPanicError{}}, "<panic in Error(): srvr_test.selfPanicError>"},
		{`"/static/"`, &MiddlewareAPI{value: "mw", mount: true}, "mw"},
	}

	for _, tt := range tests {
		t.Run(tt.route+" "+tt.text, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			mustNil(t, srv.Register(tt.api))

			err := startBounded(context.Background(), t, srv)

			text := safeText(err)
			if !errors.Is(err, srvr.ErrRouteConfig) || errors.Is(err, errPanicked) {
				t.Fatalf("Start = %s, want an error wrapping ErrRouteConfig, without panicking", text)
			}

			for _, want := range []string{tt.route, "*srvr_test.MiddlewareAPI", tt.text} {
				if !strings.Contains(text, want) {
					t.Errorf("Start = %s, want it to contain %s", text, want)
				}
			}

			select {
			case <-srv.Ready():
				t.Fatal("Ready was closed")
			default:
			}

			if err := shutdownBounded(t, srv); err != nil {
				t.Fatalf("later Shutdown = %v, want nil", err)
			}
		})
	}
}

// panicListener delegates to a real listener; its Accept panics with
// acceptPanic after accepted connections, once gate (if any) is closed,
// and its Close panics with closePanic when that is not nil.
type panicListener struct {
	net.Listener

	acceptPanic any
	accepted    int
	gate        chan struct{}
	calls       atomic.Int32
	closePanic  any
}

func (l *panicListener) Accept() (net.Conn, error) {
	if l.acceptPanic != nil && int(l.calls.Add(1)) > l.accepted {
		if l.gate != nil {
			<-l.gate
		}

		panic(l.acceptPanic)
	}

	return l.Listener.Accept()
}

func (l *panicListener) Close() error {
	if l.closePanic != nil {
		// Close the real listener first, so the test can see it closed.
		_ = l.Listener.Close()
		panic(l.closePanic)
	}

	return l.Listener.Close()
}

// TestListener_AcceptPanics covers server-lifecycle / Scenario: A listener
// whose Accept panics.
func TestListener_AcceptPanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    any
		accepted int
		text     string
	}{
		{"accept, no connection", "accept", 0, "accept"},
		{"accept, after one answered connection", "accept", 1, "accept"},
		{"self-panicking value, no connection", selfPanicError{}, 0, "<panic in Error(): srvr_test.selfPanicError>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ln := &panicListener{Listener: listen(t), acceptPanic: tt.value, accepted: tt.accepted, gate: make(chan struct{})}
			addr := ln.Addr().String()
			logs := &logBuffer{}

			var c hookCounts
			srv, err := srvr.New(append(c.options(), srvr.WithListener(ln), srvr.WithLogOutput(logs))...)
			mustNil(t, err)
			mustNil(t, srv.Register(okRoute("/x")))

			done := startBackground(t, srv)
			if tt.accepted > 0 {
				if got := get(t, "http://"+addr+"/x").status; got != http.StatusOK {
					t.Fatalf("GET /x = %d, want 200", got)
				}
			}
			close(ln.gate)

			err = receive(t, done, "Start did not return")
			text := safeText(err)
			if !errors.Is(err, srvr.ErrConfig) || errors.Is(err, errPanicked) || !strings.Contains(text, "Listener") || !strings.Contains(text, tt.text) {
				t.Fatalf("Start = %s, want an error wrapping ErrConfig containing Listener and %s, without panicking", text, tt.text)
			}

			failed := logs.find(t, "server.failed")
			if len(failed) != 1 || !strings.Contains(safeText(failed[0]["exception.message"]), tt.text) {
				t.Fatalf("want one server.failed record containing %s, got log %s", tt.text, logs)
			}
			if len(logs.find(t, "server.stopped")) != 1 {
				t.Fatalf("want one server.stopped record, got log %s", logs)
			}
			if b, a := c.before.Load(), c.after.Load(); b != 1 || a != 1 {
				t.Fatalf("hooks ran before=%d after=%d, want 1 and 1", b, a)
			}

			waitRefused(t, addr)

			if err := shutdownBounded(t, srv); err != nil {
				t.Fatalf("later Shutdown = %v, want nil", err)
			}
		})
	}
}

// TestListener_ClosePanics covers server-lifecycle / Scenario: A listener
// whose Close panics.
func TestListener_ClosePanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		call string
		run  func(t *testing.T, srv *srvr.Server) error
		want error
	}{
		{"Shutdown before Start", shutdownBounded, nil},
		{"Start with an already-cancelled context", func(t *testing.T, srv *srvr.Server) error {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			return startBounded(ctx, t, srv)
		}, context.Canceled},
		{"Start, where a route configurer panics", func(t *testing.T, srv *srvr.Server) error {
			mustNil(t, srv.Register(&PanicAPI{}))

			return startBounded(context.Background(), t, srv)
		}, srvr.ErrRouteConfig},
		{"Shutdown on a running server", func(t *testing.T, srv *srvr.Server) error {
			done := startBackground(t, srv)
			err := shutdownBounded(t, srv)
			if startErr := receive(t, done, "Start did not return"); startErr != nil {
				t.Errorf("Start = %s, want nil", safeText(startErr))
			}

			return err
		}, nil},
		{"Start, after its context ends while running", func(t *testing.T, srv *srvr.Server) error {
			ctx, cancel := context.WithCancel(context.Background())
			done := startWith(ctx, t, srv)
			cancel()

			return receive(t, done, "Start did not return")
		}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.call, func(t *testing.T) {
			t.Parallel()

			ln := &panicListener{Listener: listen(t), closePanic: "close"}
			srv, err := srvr.New(srvr.WithListener(ln), srvr.WithLogOutput(&logBuffer{}))
			mustNil(t, err)

			if err := tt.run(t, srv); !errors.Is(err, tt.want) {
				t.Fatalf("%s = %s, want %v", tt.call, safeText(err), tt.want)
			}

			if err := shutdownBounded(t, srv); err != nil {
				t.Fatalf("later Shutdown = %s, want nil", safeText(err))
			}

			if err := startBounded(context.Background(), t, srv); !errors.Is(err, srvr.ErrServerClosed) {
				t.Fatalf("later Start = %s, want ErrServerClosed", safeText(err))
			}
		})
	}
}
