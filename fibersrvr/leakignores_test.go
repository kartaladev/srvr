package fibersrvr_test

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/goleak"
)

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
	}{
		{"fasthttp Date refresher", fasthttpServerDate, true},
		{"fasthttp idle-worker cleaner", fasthttpWorkerCleaner, true},
		{"a fiber server started and never shut down", serveGoroutine, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := goleak.IgnoreCurrent()

			srv, _ := newServer(t)
			startOK(t, srv)
			if tt.shutdown {
				if err := srv.Shutdown(context.Background()); err != nil {
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
