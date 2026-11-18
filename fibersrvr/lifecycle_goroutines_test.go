package fibersrvr_test

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/goleak"
)

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
				if err := srv.Shutdown(context.Background()); err != nil {
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
