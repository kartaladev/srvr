package ginsrvr_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr/ginsrvr"
	"go.uber.org/goleak"
)

// serveGoroutine is the function of srvr's serve goroutine, which a leak
// report of a server never shut down names.
const serveGoroutine = "github.com/kartaladev/srvr/internal/engine.(*Lifecycle[...]).serveOn"

// leakOptions are the goleak options of the ginsrvr test binary. gin
// starts no long-lived goroutine of its own, so it ignores nothing.
var leakOptions []goleak.Option

// TestMain fails the test binary when a goroutine is still running after
// its tests passed.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m, leakOptions...) }

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

// startOK registers GET /ok on srv, starts it with start, which stops it
// at cleanup, and sends GET /ok with keep-alive.
func startOK(t *testing.T, srv *ginsrvr.Server) {
	t.Helper()

	if err := srv.Register(routes(func(g *gin.RouterGroup) { g.GET("/ok", ok) })); err != nil {
		t.Fatal(err)
	}

	if got := do(t, http.MethodGet, startReturned(context.Background(), t, srv)+"/ok", nil, nil).status; got != http.StatusOK { //nolint:usetesting // the server outlives the context; the cleanup shuts it down
		t.Fatalf("GET /ok = %d", got)
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

// TestLeakCheck proves the leak check this module's test binary runs
// reports a goroutine left running. Not parallel: goleak.Find sees every
// goroutine of the process.
func TestLeakCheck(t *testing.T) {
	tests := []struct {
		name  string
		start func(t *testing.T) (release func())
		want  string // a function the report names; "" for no report
	}{
		{"blocked on a channel that is never closed", func(*testing.T) func() { return startBlocked() }, "blockUntil"},
		{"blocked on a channel the test closes at cleanup", func(t *testing.T) func() {
			t.Helper()

			t.Cleanup(startBlocked())
			return nil
		}, ""},
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

			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("leak reported: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("leak report = %v, want one naming %s", err, tt.want)
			}
		})
	}
}

// TestLifecycle_Goroutines covers test-quality / Scenario: Lifecycle leaves
// no goroutine behind (ginsrvr rows). Not parallel: the checks see every
// goroutine of the process.
func TestLifecycle_Goroutines(t *testing.T) {
	tests := []struct {
		name string
		leak string // the function a "yes" row's report names; "" for "no"
		run  func(t *testing.T, srv *ginsrvr.Server)
	}{
		{"one route, Start, GET /ok with keep-alive, Shutdown", "", func(t *testing.T, srv *ginsrvr.Server) {
			t.Helper()
			startOK(t, srv)
			if err := srv.Shutdown(t.Context()); err != nil {
				t.Fatalf("Shutdown = %v", err)
			}
		}},
		{"one route, Run with a context cancelled after it is running", "", func(t *testing.T, srv *ginsrvr.Server) {
			t.Helper()
			mustNil(t, srv.Register(routes(func(r *gin.RouterGroup) { r.GET("/ok", ok) })))
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- srv.Run(ctx) }()
			<-srv.Ready()
			cancel()
			if err := within(t, containerWait, "Run did not return", func() error { return <-done }); err != nil {
				t.Fatalf("Run = %v", err)
			}
		}},
		{"one route, Start and never Shutdown", serveGoroutine, func(t *testing.T, srv *ginsrvr.Server) {
			t.Helper()
			startOK(t, srv) // its cleanup stops srv after the check
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.leak == "" {
				checkGoroutines(t)

				srv, _ := newServer(t)
				tt.run(t, srv)

				return
			}

			before := goleak.IgnoreCurrent()
			srv, _ := newServer(t)
			tt.run(t, srv)

			if err := findLeaks(before); err == nil || !strings.Contains(err.Error(), tt.leak) {
				t.Fatalf("leak report = %v, want one naming %s", err, tt.leak)
			}
		})
	}
}
