package fibersrvr_test

import (
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr/fibersrvr"
	"go.uber.org/goleak"
)

// serverLeakOptions ignore the two goroutines fasthttp (observed with
// v1.78.0) keeps running after a server stopped. Each ignore names one
// function; IgnoreAnyFunction, because both goroutines have time.Sleep on
// top, which IgnoreTopFunction could only match by ignoring every sleeping
// goroutine.
var serverLeakOptions = []goleak.Option{
	// fasthttp refreshes its Date header in a goroutine started once per
	// process that never stops.
	goleak.IgnoreAnyFunction(fasthttpServerDate),
	// fasthttp's idle-worker cleaner sleeps MaxIdleWorkerDuration (10s by
	// default) between checks of its stop channel, so it outlives Stop by
	// up to 10s.
	goleak.IgnoreAnyFunction(fasthttpWorkerCleaner),
}

// leakOptions are the goleak options of the fibersrvr test binary:
// serverLeakOptions and the open-file cache cleaner of a static mount with
// WithSendFileCache (owner decision 2026-11-19): fiber v3.7.0 never sets
// fasthttp.FS.CleanStop (res.go:1308), so the cleaner fasthttp starts per
// cached file handler (fs.go:1050) never stops.
var leakOptions = slices.Concat(serverLeakOptions, []goleak.Option{goleak.IgnoreTopFunction(fasthttpCacheCleaner)})

// serveGoroutine is the function of srvr's serve goroutine, which a leak
// report of a server never shut down names.
const serveGoroutine = "github.com/kartaladev/srvr/engine.(*Lifecycle[...]).serveOn"

// The functions leakOptions ignores.
const (
	fasthttpServerDate    = "github.com/valyala/fasthttp.updateServerDate.func1"
	fasthttpWorkerCleaner = "github.com/valyala/fasthttp.(*workerPool).Start.func2"
	fasthttpCacheCleaner  = "github.com/valyala/fasthttp.(*inMemoryCacheManager).handleCleanCache"
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
// at cleanup, sends GET /ok with keep-alive and returns the base URL.
func startOK(t *testing.T, srv *fibersrvr.Server) string {
	t.Helper()

	if err := srv.Register(routes(func(r fiber.Router) { r.Get("/ok", ok) })); err != nil {
		t.Fatal(err)
	}

	base := start(t, srv)
	if got := do(t, http.MethodGet, base+"/ok", nil, nil).status; got != http.StatusOK {
		t.Fatalf("GET /ok = %d", got)
	}

	return base
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

// TestLeakIgnores covers test-quality / Scenario: Each ignore is needed and
// narrow (fibersrvr rows of both tables). Not parallel: goleak.Find sees
// every goroutine of the process.
func TestLeakIgnores(t *testing.T) {
	tests := []struct {
		name string
		// fn is the function the check without leakOptions reports.
		fn string
		// shutdown stops the server before the checks; with leakOptions fn
		// is then not reported. A server never shut down is reported, and
		// stopped at cleanup.
		shutdown bool
		// cached serves a file of a static mount with WithSendFileCache
		// first, which starts the cache cleaner.
		cached bool
	}{
		{"fasthttp Date refresher", fasthttpServerDate, true, false},
		{"fasthttp idle-worker cleaner", fasthttpWorkerCleaner, true, false},
		{"fasthttp open-file cache cleaner of a cached static mount", fasthttpCacheCleaner, true, true},
		{"a fiber server started and never shut down", serveGoroutine, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := goleak.IgnoreCurrent()

			srv, _ := newServer(t)
			if tt.cached {
				mustNil(t, srv.Register(fibersrvr.Static("/files", mapSite(), fibersrvr.WithSendFileCache(time.Hour))))
			}

			base := startOK(t, srv)
			if tt.cached {
				if got := do(t, http.MethodGet, base+"/files/app.js", nil, nil).status; got != http.StatusOK {
					t.Fatalf("GET /files/app.js = %d", got)
				}
			}

			if tt.shutdown {
				if err := srv.Shutdown(t.Context()); err != nil {
					t.Fatalf("Shutdown = %v", err)
				}
			}

			if err := goleak.Find(); err == nil || !strings.Contains(err.Error(), tt.fn) {
				t.Fatalf("without leakOptions the check = %v, want a report naming %s", err, tt.fn)
			}

			err := findLeaks(before)
			if !tt.shutdown {
				if err == nil || !strings.Contains(err.Error(), tt.fn) {
					t.Fatalf("with leakOptions the check = %v, want a report naming %s", err, tt.fn)
				}

				return
			}

			if err != nil {
				t.Fatalf("with leakOptions the check reports: %v", err)
			}

			// Without IgnoreCurrent: the Date refresher starts once per
			// process and may predate this test.
			if err := findLeaks(); err != nil && strings.Contains(err.Error(), tt.fn) {
				t.Fatalf("with leakOptions the check still reports %s: %v", tt.fn, err)
			}
		})
	}
}

// TestLifecycle_Goroutines covers test-quality / Scenario: Lifecycle leaves
// no goroutine behind (fibersrvr rows). Not parallel: the checks see every
// goroutine of the process.
func TestLifecycle_Goroutines(t *testing.T) {
	tests := []struct {
		name string
		leak string // the function a "yes" row's report names; "" for "no"
	}{
		{"one route, Start, GET /ok with keep-alive, Shutdown", ""},
		{"one route, Start and never Shutdown", serveGoroutine},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.leak == "" {
				checkGoroutines(t)

				srv, _ := newServer(t)
				startOK(t, srv)
				if err := srv.Shutdown(t.Context()); err != nil {
					t.Fatalf("Shutdown = %v", err)
				}

				return
			}

			before := goleak.IgnoreCurrent()
			srv, _ := newServer(t)
			startOK(t, srv) // its cleanup stops srv after the check

			if err := findLeaks(before); err == nil || !strings.Contains(err.Error(), tt.leak) {
				t.Fatalf("leak report = %v, want one naming %s", err, tt.leak)
			}
		})
	}
}
