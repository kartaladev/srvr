package srvr

import (
	"slices"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/engine"
)

// WithProblemOptions passes prblm options (mappers, enrichers, namespace,
// types) to the problem handler that renders every error response. Later
// calls add to earlier ones.
//
// Do not pass [prblm.OnError]: prblm keeps a single hook and srvr installs
// its own last, which records the error for the request's log record, so a
// consumer OnError would never run. Use [WithErrorHook] instead.
func WithProblemOptions(opts ...prblm.Option) Option {
	return func(s *engine.Settings) { s.ProblemOptions = slices.Concat(s.ProblemOptions, opts) }
}

// WithErrorHook sets a hook called for every error the server answers,
// after srvr's own hook. A hook that panics does not affect the response.
// fn must not be nil.
func WithErrorHook(fn ErrorHook) Option {
	return func(s *engine.Settings) {
		if fn == nil {
			s.Errs = append(s.Errs, engine.ConfigError("ErrorHook", "<nil>", "is nil"))

			return
		}

		s.ErrorHook = fn
	}
}
