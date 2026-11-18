package engine

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"slices"

	"github.com/kartaladev/prblm"
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
}

// ResolveConfig applies opts in argument order, fills the defaults and
// validates the result. Invalid configuration returns an error wrapping
// [ErrConfig] that names every offending field and value; nothing is
// silently replaced by a default.
func ResolveConfig(opts []Option) (*Core, error) {
	s, err := newSettings(opts)
	if err != nil {
		return nil, err
	}

	c := &Core{settings: s, cfg: s.resolve(), observers: guardObservers(s.Observers)}

	if c.log, err = newLogHandler(&c.settings.Log); err != nil {
		return nil, err
	}

	c.serviceAttrs = s.Service.attrs(s.ReadBuild)

	if c.problems, err = newProblems(c.ProblemOptions()); err != nil {
		return nil, err
	}

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

// Settings returns a copy of the applied settings; they cannot change
// after ResolveConfig validated them.
func (c *Core) Settings() Settings { return c.settings }

// Config returns the server configuration with the defaults applied.
func (c *Core) Config() Config { return c.cfg }

// Problems returns the problem handler built from [Core.ProblemOptions].
func (c *Core) Problems() *prblm.Handler { return c.problems }

// ProblemOptions returns the prblm options every problem renderer of the
// server must be built from: the consumer's, then srvr's trace_id enricher
// and its OnError hook, last so that they win.
func (c *Core) ProblemOptions() []prblm.Option {
	return slices.Concat(c.settings.ProblemOptions, []prblm.Option{prblm.WithEnrichers(traceIDEnricher), prblm.OnError(c.onError)})
}

// EngineOptions returns, in order, the values of the engine-specific
// options for engine. An engine-specific option of another engine is an
// error wrapping [ErrConfig] naming that option: configuration is never
// silently ignored.
func (c *Core) EngineOptions(engine string) ([]any, error) {
	var (
		values []any
		errs   []error
	)

	for _, o := range c.settings.engineOpts {
		if o.engine != engine {
			errs = append(errs, ConfigError("Option", o.name, "belongs to the "+o.engine+" engine, not "+engine))

			continue
		}

		values = append(values, o.value)
	}

	return values, errors.Join(errs...)
}

// The sentinel errors of every srvr engine, documented on the names
// package srvr re-exports them under (srvr.ErrConfig and so on), so
// errors.Is matches either name.
var (
	ErrConfig        = errors.New("srvr: invalid configuration")
	ErrServerStarted = errors.New("srvr: server already started")
	ErrServerClosed  = errors.New("srvr: server closed")
	ErrRouteConfig   = errors.New("srvr: invalid route configuration")
	ErrShutdownHook  = errors.New("srvr: shutdown hook failed")
)

// IsNil reports whether v is nil or an interface holding a nil pointer,
// map, slice, function or channel (a typed nil).
func IsNil(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface, reflect.UnsafePointer:
		return rv.IsNil()
	default:
		return false
	}
}

// ErrorHook is called for every error the server answers with a problem,
// with the problem sent (nil for a note about a problem that was not sent,
// such as a panic after the response started). Consumers use it as
// srvr.ErrorHook.
type ErrorHook func(ctx context.Context, err error, p *prblm.Problem)

// newProblems builds a problem handler from opts and returns an invalid
// option, which makes prblm.NewHandler panic, as an error wrapping
// ErrConfig.
func newProblems(opts []prblm.Option) (h *prblm.Handler, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = ConfigError("ProblemOptions", v, "is invalid")
		}
	}()

	return prblm.NewHandler(opts...), nil
}

// onError is srvr's prblm hook. It records the failure for the request's
// access record, which the access stage writes, and calls the consumer's
// hook, if any, containing a panic in it.
func (c *Core) onError(ctx context.Context, err error, p *prblm.Problem) {
	recordFailure(ctx, err, p)

	if c.settings.ErrorHook == nil {
		return
	}

	defer func() { _ = recover() }()

	c.settings.ErrorHook(ctx, err, p)
}
