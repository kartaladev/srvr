package srvr_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/kartaladev/srvr"
)

// abandonedHook returns a hook that ignores its context and blocks until
// release is closed. It is a named function so a leak report names it.
func abandonedHook(release <-chan struct{}) func(context.Context) error {
	return func(context.Context) error {
		<-release

		return nil
	}
}

// TestAbandonedHook_Goroutines covers test-quality / Scenario: Abandoned
// consumer code is the only goroutine left (hook rows). Not parallel: the
// checks see every goroutine of the process.
func TestAbandonedHook_Goroutines(t *testing.T) {
	tests := []struct {
		name    string
		phase   srvr.ShutdownPhase
		opts    []srvr.Option
		ctx     time.Duration // the Shutdown context's deadline; 0: none
		release bool          // check after releasing the hook
	}{
		{"BeforeDrain hook, 50ms Shutdown context, after releasing the hook", srvr.BeforeDrain, nil, 50 * time.Millisecond, true},
		{"BeforeDrain hook, 50ms Shutdown context, before releasing the hook", srvr.BeforeDrain, nil, 50 * time.Millisecond, false},
		{"AfterDrain hook, 50ms ShutdownHookTimeout, after releasing the hook", srvr.AfterDrain, []srvr.Option{srvr.WithShutdownHookTimeout(50 * time.Millisecond)}, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := goleak.IgnoreCurrent()
			release := make(chan struct{})
			end := sync.OnceFunc(func() { close(release) })
			defer end()

			srv := newServer(t, append([]srvr.Option{srvr.WithShutdownHook(tt.phase, abandonedHook(release))}, tt.opts...)...)
			done := startWith(context.Background(), t, srv)

			ctx := context.Background()
			if tt.ctx > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.ctx)
				defer cancel()
			}

			// The abandoned hook's error, which both calls report, is not
			// under test here.
			_ = srv.Shutdown(ctx)
			_ = receive(t, done, "Start did not return")

			if tt.release {
				end()
				mustNil(t, findLeaks(before))

				return
			}

			onlyLeak(t, before, "srvr_test.abandonedHook")
		})
	}
}
