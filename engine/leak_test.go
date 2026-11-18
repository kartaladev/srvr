package engine_test

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

// leakOptions are the goleak options of the engine package's test binary.
// The package has no deliberate long-lived goroutine, so it ignores
// nothing.
var leakOptions []goleak.Option

// TestMain fails the test binary when a goroutine is still running after
// its tests passed.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m, leakOptions...) }

// findLeaks runs goleak.Find with leakOptions plus extra, without
// appending to the shared leakOptions slice.
func findLeaks(extra ...goleak.Option) error {
	return goleak.Find(slices.Concat(leakOptions, extra)...)
}

// findLeaks, reportedGoroutine and onlyLeak repeat the core package's
// copies: a shared helper would be a non-test package importing goleak,
// which the core module's runtime dependency rule forbids.

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

// abandonedCheck returns a route check that ignores its context and blocks
// until release is closed. It is a named function so a leak report names
// it.
func abandonedCheck(release <-chan struct{}) engine.RouteCheck {
	return func(context.Context, engine.Routes) error {
		<-release

		return nil
	}
}

// TestAbandonedCheck_Goroutines covers test-quality / Scenario: Abandoned
// consumer code is the only goroutine left (route check rows). Not
// parallel: the checks see every goroutine of the process.
func TestAbandonedCheck_Goroutines(t *testing.T) {
	tests := []struct {
		name    string
		release bool // check after releasing the route check
	}{
		{"route check, 20ms Start context, after releasing the check", true},
		{"route check, 20ms Start context, before releasing the check", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := goleak.IgnoreCurrent()
			release := make(chan struct{})
			end := sync.OnceFunc(func() { close(release) })
			defer end()

			srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithRouteCheck(abandonedCheck(release)))
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()

			if err := srv.Start(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Start = %v, want the context's deadline", err)
			}

			if tt.release {
				end()
				if err := findLeaks(before); err != nil {
					t.Fatal(err)
				}

				return
			}

			onlyLeak(t, before, "engine_test.abandonedCheck")
		})
	}
}
