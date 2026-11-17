package srvr

import (
	"context"
	"fmt"
	"slices"

	"github.com/kartaladev/prblm"
)

// ErrorHook is called for every error the server answers with a problem,
// with the problem sent (nil for a note about a problem that was not sent,
// such as a panic after the response started).
type ErrorHook func(ctx context.Context, err error, p *prblm.Problem)

// WithProblemOptions passes prblm options (mappers, enrichers, namespace,
// types) to the problem handler that renders every error response. Later
// calls add to earlier ones.
//
// Do not pass [prblm.OnError]: prblm keeps a single hook and srvr installs
// its own last, which records the error for the request's log record, so a
// consumer OnError would never run. Use [WithErrorHook] instead.
func WithProblemOptions(opts ...prblm.Option) Option {
	return func(s *settings) { s.problemOptions = slices.Concat(s.problemOptions, opts) }
}

// WithErrorHook sets a hook called for every error the server answers,
// after srvr's own hook. A hook that panics does not affect the response.
// fn must not be nil.
func WithErrorHook(fn ErrorHook) Option {
	return func(s *settings) {
		if fn == nil {
			s.errs = append(s.errs, configError("ErrorHook", "<nil>", "is nil"))

			return
		}

		s.errorHook = fn
	}
}

// newProblems builds the server's problem handler from the consumer's prblm
// options followed by srvr's own hook, and returns an invalid option, which
// makes prblm.NewHandler panic, as an error wrapping ErrConfig.
func newProblems(opts []prblm.Option, hook prblm.Option) (h *prblm.Handler, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = configError("ProblemOptions", fmt.Sprint(v), "is invalid")
		}
	}()

	return prblm.NewHandler(append(slices.Clone(opts), hook)...), nil
}

// onError is srvr's prblm hook. It calls the consumer's hook, if any,
// containing a panic in it.
func (s *Server) onError(ctx context.Context, err error, p *prblm.Problem) {
	if s.settings.errorHook == nil {
		return
	}

	defer func() { _ = recover() }()

	s.settings.errorHook(ctx, err, p)
}
