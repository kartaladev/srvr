package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

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
