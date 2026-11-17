package srvr

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ErrShutdownHook is wrapped by the error Start or Shutdown returns when a
// shutdown hook failed: it returned an error, panicked or outlived its
// budget.
var ErrShutdownHook = errors.New("srvr: shutdown hook failed")

// ShutdownPhase is when a shutdown hook runs while the server stops.
type ShutdownPhase int

const (
	// BeforeDrain hooks run after the listener is closed and before
	// in-flight requests are drained, within the drain budget
	// (ShutdownTimeout), for example to signal open sessions to finish.
	BeforeDrain ShutdownPhase = iota + 1
	// AfterDrain hooks run after the drain, with their own budget
	// (ShutdownHookTimeout), for example to flush telemetry or close a
	// database pool. They also run when Start fails after leaving its
	// initial state and when Shutdown closes a server that never started.
	AfterDrain
)

// String returns the phase name.
func (p ShutdownPhase) String() string {
	switch p {
	case BeforeDrain:
		return "BeforeDrain"
	case AfterDrain:
		return "AfterDrain"
	default:
		return "ShutdownPhase(" + strconv.Itoa(int(p)) + ")"
	}
}

// defaultShutdownHookTimeout gives AfterDrain hooks time to flush without
// starving on a slow drain: 15s drain plus 5s fits a 30s grace period.
const defaultShutdownHookTimeout = 5 * time.Second

// hook is a registered shutdown hook.
type hook struct {
	phase ShutdownPhase
	fn    func(context.Context) error
}

// WithShutdownHook registers fn to run once when the server stops, in the
// given phase, in registration order within the phase. fn receives a
// context bounded by the phase's budget; srvr stops waiting for a hook at
// the budget, and a hook that ignores its context leaks its goroutine.
//
// A hook that fails does not stop the shutdown: the remaining hooks and the
// drain still run, and the call that stopped the server returns an error
// wrapping [ErrShutdownHook].
func WithShutdownHook(phase ShutdownPhase, fn func(ctx context.Context) error) Option {
	return func(s *settings) {
		switch {
		case phase != BeforeDrain && phase != AfterDrain:
			s.errs = append(s.errs, configError("ShutdownPhase", phase, "is not BeforeDrain or AfterDrain"))
		case fn == nil:
			s.errs = append(s.errs, configError("ShutdownHook", phase, "function is nil"))
		default:
			s.hooks = append(s.hooks, hook{phase: phase, fn: fn})
		}
	}
}

// WithShutdownHookTimeout sets the budget of the AfterDrain hooks (default
// 5s); a negative d leaves them unbounded.
func WithShutdownHookTimeout(d time.Duration) Option {
	return func(s *settings) { s.ShutdownHookTimeout = d }
}

// runHooks runs the hooks of phase in order, each within ctx, and returns
// their failures, each wrapping ErrShutdownHook.
func (s *Server) runHooks(ctx context.Context, phase ShutdownPhase) error {
	var errs []error

	for i, h := range s.settings.hooks {
		if h.phase != phase {
			continue
		}

		if err := runHook(ctx, h.fn); err != nil {
			errs = append(errs, fmt.Errorf("%w: %s hook %d: %w", ErrShutdownHook, phase, i, err))
		}
	}

	return errors.Join(errs...)
}

// runHook runs fn in its own goroutine, recovering a panic, and stops
// waiting when ctx is done.
func runHook(ctx context.Context, fn func(context.Context) error) error {
	result := make(chan error, 1)

	go func() {
		defer func() {
			if v := recover(); v != nil {
				result <- fmt.Errorf("panic: %v", v)
			}
		}()

		result <- fn(ctx)
	}()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		// A hook that finished as the budget ran out still counts.
		select {
		case err := <-result:
			return err
		default:
			return fmt.Errorf("abandoned: %w", ctx.Err())
		}
	}
}

// afterDrainContext returns the context AfterDrain hooks run with: it keeps
// the values of parent but not its cancellation or deadline, so a slow
// drain cannot starve them, and it is bounded by ShutdownHookTimeout
// unless that is disabled. The hooks of the phase share the budget.
func (s *Server) afterDrainContext(parent context.Context) (context.Context, context.CancelFunc) {
	parent = context.WithoutCancel(parent)

	if s.cfg.ShutdownHookTimeout < 0 {
		return context.WithCancel(parent)
	}

	return context.WithTimeout(parent, s.cfg.ShutdownHookTimeout)
}
