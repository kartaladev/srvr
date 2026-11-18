package srvr_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/kartaladev/srvr"
)

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
			srv := newServer(t)
			mustNil(t, srv.Register(okRoute("/ok")))
			startGetShutdown(t, srv, http.StatusOK)
		}, ""},
		{"BeforeDrain and AfterDrain hooks returning nil, Start, Shutdown", func(t *testing.T) {
			nop := func(context.Context) error { return nil }
			startGetShutdown(t, newServer(t, srvr.WithShutdownHook(srvr.BeforeDrain, nop), srvr.WithShutdownHook(srvr.AfterDrain, nop)), 0)
		}, ""},
		{"WithListener on 127.0.0.1:0, Start, Shutdown", func(t *testing.T) {
			srv, err := srvr.New(srvr.WithListener(listen(t)))
			mustNil(t, err)
			startGetShutdown(t, srv, 0)
		}, ""},
		{"one route, Shutdown before Start", func(t *testing.T) {
			srv := newServer(t)
			mustNil(t, srv.Register(okRoute("/ok")))

			mustNil(t, shutdownBounded(t, srv))
		}, ""},
		{"one route, Start with a context cancelled after it is running", func(t *testing.T) {
			srv := newServer(t)
			mustNil(t, srv.Register(okRoute("/ok")))
			ctx, cancel := context.WithCancel(context.Background())
			done := startWith(ctx, t, srv)

			cancel()
			mustNil(t, receive(t, done, "Start did not return"))
		}, ""},
		{"address already in use, Start fails", func(t *testing.T) {
			srv := newServer(t, srvr.WithAddr(listen(t).Addr().String()))

			if err := startBounded(context.Background(), t, srv); err == nil {
				t.Fatal("Start = nil on an address in use")
			}
		}, ""},
		{"a handler that panics, Start, GET /ok, Shutdown", func(t *testing.T) {
			srv := newServer(t)
			mustNil(t, srv.Register(on("", "/ok", func(http.ResponseWriter, *http.Request) error { panic("boom") })))
			startGetShutdown(t, srv, http.StatusInternalServerError)
		}, ""},
		{"a handler blocking until its request context ends, Shutdown with a 50ms drain deadline", func(t *testing.T) {
			started := make(chan struct{})
			srv := newServer(t)
			mustNil(t, srv.Register(on("", "/block", func(_ http.ResponseWriter, r *http.Request) error {
				close(started)
				<-r.Context().Done()

				return nil
			})))
			done := startWith(context.Background(), t, srv)
			registerClient(t, srv.Addr())

			res := inFlight("http://" + srv.Addr() + "/block")
			receive(t, started, "GET /block did not reach its handler")

			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Shutdown = %v, want the drain deadline", err)
			}
			receive(t, res, "GET /block did not end")
			mustNil(t, receive(t, done, "Start did not return"))
		}, ""},
		{"one route, Start and never Shutdown", func(t *testing.T) {
			srv := newServer(t)
			mustNil(t, srv.Register(okRoute("/ok")))
			done := startWith(context.Background(), t, srv)

			t.Cleanup(func() { // stop it after the check, so the binary stays green
				mustNil(t, shutdownBounded(t, srv))
				mustNil(t, receive(t, done, "Start did not return"))
			})
		}, "engine.(*Lifecycle[...]).serve"},
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

// startGetShutdown starts srv, sends GET /ok with keep-alive when want is
// not 0 and fails unless it answers want, then shuts srv down and waits
// for Start to return.
func startGetShutdown(t *testing.T, srv *srvr.Server, want int) {
	t.Helper()

	done := startWith(context.Background(), t, srv)
	registerClient(t, srv.Addr())

	if want != 0 {
		if got := get(t, "http://"+srv.Addr()+"/ok").status; got != want {
			t.Fatalf("GET /ok = %d, want %d", got, want)
		}
	}

	mustNil(t, shutdownBounded(t, srv))
	mustNil(t, receive(t, done, "Start did not return"))
}
