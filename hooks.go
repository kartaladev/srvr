package srvr

import (
	"context"
	"time"

	"github.com/kartaladev/srvr/engine"
)

// WithShutdownHook registers fn to run once when the server stops, in the
// given phase, in registration order within the phase. fn receives a
// context bounded by the phase's budget; srvr stops waiting for a hook at
// the budget, and a hook that ignores its context leaks its goroutine.
//
// A hook that fails does not stop the shutdown: the remaining hooks and the
// drain still run, and the call that stopped the server returns an error
// wrapping [ErrShutdownHook]. A hook fails when it returns a non-nil error
// (including a typed nil), panics or outlives its budget. Its error text is
// read without risk of panicking, even when Error panics, and errors.Is
// matches both ErrShutdownHook and the hook's error.
func WithShutdownHook(phase ShutdownPhase, fn func(ctx context.Context) error) Option {
	return func(s *engine.Settings) {
		switch {
		case phase != BeforeDrain && phase != AfterDrain:
			s.Errs = append(s.Errs, engine.ConfigError("ShutdownPhase", phase, "is not BeforeDrain or AfterDrain"))
		case fn == nil:
			s.Errs = append(s.Errs, engine.ConfigError("ShutdownHook", phase, "function is nil"))
		default:
			s.Hooks = append(s.Hooks, engine.Hook{Phase: phase, Fn: fn})
		}
	}
}

// WithShutdownHookTimeout sets the budget of the AfterDrain hooks (default
// 5s); a negative d leaves them unbounded.
func WithShutdownHookTimeout(d time.Duration) Option {
	return func(s *engine.Settings) { s.ShutdownHookTimeout = d }
}
