package srvr_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"
)

// leakOptions are the goleak options of the core's test binaries. The core
// has no deliberate long-lived goroutine, so it ignores nothing.
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
