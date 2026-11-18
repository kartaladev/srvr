package engine_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.uber.org/goleak"
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
