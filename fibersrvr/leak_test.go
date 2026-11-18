package fibersrvr_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"go.uber.org/goleak"

	"github.com/kartaladev/srvr/fibersrvr"
)

// leakOptions are the goleak options of the fibersrvr test binary: the
// two goroutines fasthttp (observed with v1.78.0) keeps running after a
// server stopped. Each ignore names one function; IgnoreAnyFunction,
// because both goroutines have time.Sleep on top, which IgnoreTopFunction
// could only match by ignoring every sleeping goroutine.
var leakOptions = []goleak.Option{
	// fasthttp refreshes its Date header in a goroutine started once per
	// process that never stops.
	goleak.IgnoreAnyFunction(fasthttpServerDate),
	// fasthttp's idle-worker cleaner sleeps MaxIdleWorkerDuration (10s by
	// default) between checks of its stop channel, so it outlives Stop by
	// up to 10s.
	goleak.IgnoreAnyFunction(fasthttpWorkerCleaner),
}

// serveGoroutine is the function of srvr's serve goroutine, which a leak
// report of a server never shut down names.
const serveGoroutine = "github.com/kartaladev/srvr/engine.(*Lifecycle[...]).serve"

// The functions leakOptions ignores.
const (
	fasthttpServerDate    = "github.com/valyala/fasthttp.updateServerDate.func1"
	fasthttpWorkerCleaner = "github.com/valyala/fasthttp.(*workerPool).Start.func2"
)

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
func startOK(t *testing.T, srv *fibersrvr.Server) {
	t.Helper()

	if err := srv.Register(routes(func(r fiber.Router) { r.Get("/ok", ok) })); err != nil {
		t.Fatal(err)
	}

	if got := do(t, http.MethodGet, start(t, srv)+"/ok", nil, nil).status; got != http.StatusOK {
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
