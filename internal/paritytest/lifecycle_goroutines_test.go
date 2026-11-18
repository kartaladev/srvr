package paritytest

import (
	"testing"

	"go.uber.org/goleak"
)

// TestLifecycle_Goroutines covers test-quality / Scenario: Lifecycle leaves
// no goroutine behind (parity harness rows). Not parallel: goleak.Find sees
// every goroutine of the process.
func TestLifecycle_Goroutines(t *testing.T) {
	tests := []struct {
		name string
		// run starts and uses a server inside a subtest, whose cleanups
		// shut it down.
		run func(t *testing.T)
	}{
		{"a fiber server of the harness, Start, GET /ok, Shutdown", func(t *testing.T) { runFiber(t) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := goleak.IgnoreCurrent()

			t.Run("run", tt.run)

			if err := findLeaks(before); err != nil {
				t.Fatalf("goroutines left after Shutdown: %v", err)
			}
		})
	}
}
