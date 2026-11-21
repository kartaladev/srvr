package engine

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/internal/errchain"
)

// Core is the resolved, validated configuration of one server and the
// engine-neutral machinery every engine shares: the log handler, the
// service attributes and the problem handler with srvr's own prblm
// options. An engine builds one with [ResolveConfig] and runs it with a
// [Lifecycle].
type Core struct {
	settings Settings
	// cfg is the server configuration with the defaults applied.
	cfg Config
	// problems renders every error response of the net/http engine and
	// validates the prblm options for the others.
	problems *prblm.Handler
	// log is the handler srvr writes its records through and request
	// loggers are bound from.
	log *logHandler
	// serviceAttrs are added to lifecycle and failed records.
	serviceAttrs []slog.Attr
	// observers are the server's observers, guarded.
	observers []*guardedObserver
	// renderDeadline bounds a consumer renderer's call: defaultRenderDeadline,
	// shortened by tests.
	renderDeadline time.Duration
	// unmatched limits the failed records of unmatched requests; nil
	// without a limit.
	unmatched *unmatchedLimiter
}

// ResolveConfig applies opts in argument order, fills the defaults and
// validates the result. Invalid configuration returns an error wrapping
// [errchain.ErrConfig] that names every offending field and value; nothing is
// silently replaced by a default.
func ResolveConfig(opts []Option) (*Core, error) {
	s, errs := newSettings(opts)

	// The problem handler is built even when a field is invalid, so a
	// rejected problem option is reported with every other field.
	c := &Core{settings: s}

	problems, err := newProblems(c.ProblemOptions())
	if err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	c.problems, c.cfg, c.observers = problems, s.resolve(), guardObservers(s.Observers)
	c.renderDeadline = defaultRenderDeadline
	c.unmatched = newUnmatchedLimiter(s.Access.UnmatchedLimit, s.Now)
	c.log = newLogHandler(&c.settings.Log)
	c.serviceAttrs = s.Service.attrs(s.ReadBuild)

	return c, nil
}

// SetDefaultLogger makes the server's logger the process default
// (slog.SetDefault) when srvr.WithSetDefaultLogger asked for it, and does
// nothing otherwise. It changes process-wide state, so every engine calls
// it as the last step of a construction that succeeds: a New that returns
// an error leaves slog.Default() unchanged.
func (c *Core) SetDefaultLogger() {
	if c.settings.Log.SetDefault {
		slog.SetDefault(slog.New(c.log))
	}
}

// MaxHeaderBytesSet returns the MaxHeaderBytes the consumer set, or 0 when
// it was left to the default; an engine with its own header buffer limit
// warns when a set value exceeds it.
func (c *Core) MaxHeaderBytesSet() int { return c.settings.MaxHeaderBytes }

// Config returns the server configuration with the defaults applied.
func (c *Core) Config() Config { return c.cfg }

// Problems returns the problem handler built from [Core.ProblemOptions].
func (c *Core) Problems() *prblm.Handler { return c.problems }

// ProblemOptions returns the prblm options every problem renderer of the
// server must be built from: the consumer's, then srvr's mapper for a
// request the client closed (after the consumer's mappers, which win), its
// trace_id enricher (last, so that it wins) and its error hook, added with
// prblm.AddErrorHook so that no consumer option replaces it (hook order:
// srvr.WithProblemOptions). It returns a new slice; a hook an engine
// appends with prblm.AddErrorHook runs after srvr's, with the request's
// failure already recorded in its [RequestState].
func (c *Core) ProblemOptions() []prblm.Option {
	return slices.Concat(
		c.settings.ProblemOptions,
		srvrProblemOptions(c.onError),
	)
}

// srvrProblemOptions returns srvr's own prblm options: its mappers (after
// any consumer's), the trace_id enricher and hook, added with
// prblm.AddErrorHook so that no consumer option replaces it.
func srvrProblemOptions(hook func(context.Context, error, *prblm.Problem)) []prblm.Option {
	return []prblm.Option{
		prblm.WithMappers(clientClosedMapper, answerMapper),
		prblm.WithEnrichers(traceIDEnricher),
		prblm.AddErrorHook(hook),
	}
}

// HasErrorRenderer reports whether the consumer set a renderer, which
// answers every error in place of prblm.
func (c *Core) HasErrorRenderer() bool { return c.settings.ErrorRenderer != nil }

// EngineOptions returns, in order, the values of the engine-specific
// options for engine. An engine-specific option of another engine is an
// error wrapping [errchain.ErrConfig] naming that option: configuration is never
// silently ignored.
func (c *Core) EngineOptions(engine string) ([]any, error) {
	var (
		values []any
		errs   []error
	)

	for _, o := range c.settings.engineOpts {
		if o.engine != engine {
			errs = append(errs, errchain.NewConfigError("Option", o.name, "belongs to the "+o.engine+" engine, not "+engine))

			continue
		}

		values = append(values, o.value)
	}

	return values, errors.Join(errs...)
}

// ErrorHook is called for every error the server answers with a problem,
// with the problem sent (nil for a note about a problem that was not sent,
// such as a panic after the response started). It also receives prblm's
// notes, which srvr does not record as the request's failure: a
// mistake in rendering, and, before the error itself, a note
// wrapping prblm.ErrUnwalkableError when prblm cannot walk the error's
// chain and sent the safe 500 problem instead. Consumers use it as
// srvr.ErrorHook.
type ErrorHook func(ctx context.Context, err error, p *prblm.Problem)

// newProblems builds a problem handler from opts and returns an invalid
// option, which makes prblm.NewHandler panic, as an error wrapping
// errchain.ErrConfig.
func newProblems(opts []prblm.Option) (h *prblm.Handler, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = errchain.NewConfigError("ProblemOptions", v, "is invalid")
		}
	}()

	return prblm.NewHandler(opts...), nil
}

// onError is srvr's prblm hook. It records the failure for the request's
// access record, which the access stage writes, and calls the consumer's
// hook, if any, containing a panic in it.
//
// While a consumer's renderer delegates to [DefaultRenderer] (whose
// capture slot is in ctx), the answer is only captured: [Core.Render]
// records the answer actually sent and calls the hook once. prblm's notes
// still reach the hook.
func (c *Core) onError(ctx context.Context, err error, p *prblm.Problem) {
	if c.HasErrorRenderer() && ctx.Value(captureKey{}) != nil && !isNote(err) {
		captureProblem(ctx, err, p)

		return
	}

	recordFailure(ctx, err, p)
	captureProblem(ctx, err, p)
	c.callHook(ctx, err, p)
}

// callHook calls the consumer's error hook, if any, containing a panic in
// it.
func (c *Core) callHook(ctx context.Context, err error, p *prblm.Problem) {
	if c.settings.ErrorHook == nil {
		return
	}

	defer func() { _ = recover() }()

	c.settings.ErrorHook(ctx, err, p)
}
