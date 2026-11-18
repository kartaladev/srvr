package paritytest

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"go.uber.org/goleak"

	"github.com/kartaladev/srvr"
)

// blockedHandler returns a gin handler that closes entered and blocks until
// release is closed. It is a named function so a leak report names it.
func blockedHandler(entered chan<- struct{}, release <-chan struct{}) gin.HandlerFunc {
	return func(*gin.Context) {
		close(entered)
		<-release
	}
}

// TestLeakIgnores covers test-quality / Scenario: Each ignore is needed and
// narrow (paritytest rows of both tables). Not parallel: goleak.Find sees
// every goroutine of the process.
func TestLeakIgnores(t *testing.T) {
	tests := []struct {
		name string
		// run starts the goroutine inside a subtest, whose cleanups stop
		// the server, and returns the function that ends it, or nil.
		run func(t *testing.T) (release func())
		// fn is the function the check without leakOptions reports.
		fn string
		// reported tells whether the check with leakOptions still reports
		// fn.
		reported bool
	}{
		{"fasthttp Date refresher", runFiber, fasthttpServerDate, false},
		{"a gin handler blocked on a channel never closed", runBlockedGin, "paritytest.blockedHandler", true},
		{"fasthttp idle-worker cleaner at the default duration", runFiberDefaultIdle, fasthttpWorkerCleaner, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := goleak.IgnoreCurrent()

			var release func()
			t.Run("run", func(t *testing.T) { release = tt.run(t) })
			if release != nil {
				defer func() {
					release()
					if err := findLeaks(before); err != nil {
						t.Errorf("goroutines did not end after the release: %v", err)
					}
				}()
			}

			if err := goleak.Find(); !names(err, tt.fn) {
				t.Fatalf("without leakOptions the check = %v, want a report naming %s", err, tt.fn)
			}

			if tt.reported {
				if err := findLeaks(before); !names(err, tt.fn) {
					t.Fatalf("with leakOptions the check = %v, want a report naming %s", err, tt.fn)
				}

				return
			}

			if err := findLeaks(before); err != nil {
				t.Fatalf("with leakOptions the check reports: %v", err)
			}

			// Without IgnoreCurrent: the Date refresher starts once per
			// process and may predate this test.
			if err := findLeaks(); names(err, tt.fn) {
				t.Fatalf("with leakOptions the check still reports %s: %v", tt.fn, err)
			}
		})
	}
}

// runFiber serves GET /ok on a fiber server, which t's cleanup stops.
func runFiber(t *testing.T) func() {
	base, _ := fiberServer(t, okFiber)
	getOK(t, base)

	return nil
}

// okFiber registers GET /ok answering 200.
func okFiber(r fiber.Router) {
	r.Get("/ok", func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
}

// getOK sends GET /ok to base and fails t unless it answers 200.
func getOK(t *testing.T, base string) {
	t.Helper()

	res, _, err := send(t, http.MethodGet, base+"/ok", nil)
	must(t, err)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /ok = %d", res.StatusCode)
	}
}

// runFiberDefaultIdle serves GET /ok, as runFiber does, on a fiber server
// left at fasthttp's default MaxIdleWorkerDuration (10s), whose idle-worker
// cleaner outlives the Shutdown t's cleanup runs. The returned function
// waits until the cleaner's next wake-up has ended it, so the test
// binary's own check does not report it.
func runFiberDefaultIdle(t *testing.T) func() {
	base, _ := fiberServerIdle(t, 0, okFiber)
	getOK(t, base)

	return func() {
		for deadline := time.Now().Add(cleanerWait); time.Now().Before(deadline); {
			if !names(findLeaks(), fasthttpWorkerCleaner) {
				return
			}
		}
	}
}

const (
	// fasthttpWorkerCleaner is fasthttp's idle-worker cleaner (v1.78.0).
	fasthttpWorkerCleaner = "github.com/valyala/fasthttp.(*workerPool).Start.func2"
	// cleanerWait bounds the wait for a cleaner at fasthttp's default
	// MaxIdleWorkerDuration (10s) to end: one full sleep plus a margin.
	cleanerWait = 10*time.Second + 2*time.Second
)

// runBlockedGin sends GET /block to a gin server whose handler blocks until
// the returned function is called; the request gives up once the handler
// is entered and t's cleanup stops the server after a 100ms drain.
func runBlockedGin(t *testing.T) func() {
	entered, release := make(chan struct{}), make(chan struct{})
	base, _ := ginServer(t, func(g *gin.RouterGroup) { g.GET("/block", blockedHandler(entered, release)) },
		srvr.WithShutdownTimeout(100*time.Millisecond))

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
		}
		cancel()
	}()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/block", nil)
	if res, err := clientFor(req.URL.String()).Do(req); err == nil {
		_ = res.Body.Close()
		t.Fatalf("got %d, want no response", res.StatusCode)
	}

	return func() { close(release) }
}
