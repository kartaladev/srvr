package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"
)

// ShutdownPhase is when a shutdown hook runs while the server stops.
// Consumers use it as srvr.ShutdownPhase.
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

// Hook is a registered shutdown hook: srvr.WithShutdownHook appends one
// to [Settings].
type Hook struct {
	Phase ShutdownPhase
	Fn    func(context.Context) error
}

// runHooks runs the hooks of phase in order, each within ctx, and returns
// their failures, each wrapping ErrShutdownHook.
func (c *Core) runHooks(ctx context.Context, phase ShutdownPhase) error {
	var errs []error

	for i, h := range c.settings.Hooks {
		if h.Phase != phase {
			continue
		}

		if err := runHook(ctx, h.Fn); err != nil {
			c.logEvent(ctx, slog.LevelWarn, eventHookFailed,
				slog.String(keyHookPhase, phase.String()),
				slog.Int(keyHookIndex, i),
				slog.String(keyFunction, FuncLocation(h.Fn).Function()),
				c.exceptionMessage(errorText(err)),
			)
			errs = append(errs, fmt.Errorf("%w: %s hook %d: %w", ErrShutdownHook, phase, i, &guardedError{err}))
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
				result <- errors.New("panic: " + ValueText(v))
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
			// For BeforeDrain hooks ctx.Err() comes from the consumer's
			// Shutdown context.
			return fmt.Errorf("abandoned: %w", &guardedError{ctx.Err()})
		}
	}
}

// afterDrainContext returns the context AfterDrain hooks run with: it keeps
// the values of parent but not its cancellation or deadline, so a slow
// drain cannot starve them, and it is bounded by ShutdownHookTimeout
// unless that is disabled. The hooks of the phase share the budget.
func (c *Core) afterDrainContext(parent context.Context) (context.Context, context.CancelFunc) {
	parent = context.WithoutCancel(parent)

	if c.cfg.ShutdownHookTimeout < 0 {
		return context.WithCancel(parent)
	}

	return context.WithTimeout(parent, c.cfg.ShutdownHookTimeout)
}
