package engine

import (
	"errors"
	"log/slog"
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

	c := &Core{settings: s, cfg: s.resolve()}

	if c.log, err = newLogHandler(&c.settings.Log); err != nil {
		return nil, err
	}

	c.serviceAttrs = s.Service.attrs(s.ReadBuild)

	if s.Log.SetDefault {
		slog.SetDefault(slog.New(c.log))
	}

	if c.problems, err = newProblems(c.ProblemOptions()); err != nil {
		return nil, err
	}

	return c, nil
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
