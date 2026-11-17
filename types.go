package srvr

import (
	"context"
	"log/slog"

	"github.com/kartaladev/srvr/engine"
)

// The configuration types, sentinel errors and request accessors of srvr
// are declared in package engine, which every engine module (net/http,
// gin, fiber) shares, and re-exported here under the same names: code
// written against srvr compiles unchanged, one option list configures any
// engine, and errors.Is matches either name. Their fields and defaults are
// documented on the engine declarations.

type (
	// Option configures a server of any engine. Options apply in argument
	// order, so a later option wins over an earlier one.
	Option = engine.Option
	// Config holds the server settings that a configuration loader can
	// fill; see [engine.Config] for its fields and defaults. Pass it with
	// [WithConfig].
	Config = engine.Config
	// LogConfig configures srvr's logger; see [engine.LogConfig]. Pass it
	// with [WithLogConfig].
	LogConfig = engine.LogConfig
	// AccessLogConfig configures the access record written for every
	// request; see [engine.AccessLogConfig]. Pass it with
	// [WithAccessLogConfig].
	AccessLogConfig = engine.AccessLogConfig
	// TraceConfig configures trace context handling; see
	// [engine.TraceConfig]. Pass it with [WithTraceConfig].
	TraceConfig = engine.TraceConfig
	// ServiceConfig names the service in srvr's lifecycle and error
	// records; see [engine.ServiceConfig]. Pass it with [WithServiceConfig].
	ServiceConfig = engine.ServiceConfig
	// Trace is the W3C trace context of a request; see [engine.Trace].
	Trace = engine.Trace
	// ShutdownPhase is when a shutdown hook runs while the server stops:
	// [BeforeDrain] or [AfterDrain].
	ShutdownPhase = engine.ShutdownPhase
	// ErrorHook is called for every error the server answers with a
	// problem, with the problem sent (nil for a note about a problem that
	// was not sent, such as a panic after the response started).
	ErrorHook = engine.ErrorHook
)

const (
	// BeforeDrain hooks run after the listener is closed and before
	// in-flight requests are drained, within the drain budget
	// (ShutdownTimeout), for example to signal open sessions to finish.
	BeforeDrain = engine.BeforeDrain
	// AfterDrain hooks run after the drain, with their own budget
	// (ShutdownHookTimeout), for example to flush telemetry or close a
	// database pool. They also run when Start fails after leaving its
	// initial state and when Shutdown closes a server that never started.
	AfterDrain = engine.AfterDrain
)

var (
	// ErrConfig is wrapped by every error New returns for invalid
	// configuration. The message names the offending field and value.
	ErrConfig = engine.ErrConfig
	// ErrServerStarted is returned by Start and Register once the server
	// has been started.
	ErrServerStarted = engine.ErrServerStarted
	// ErrServerClosed is returned by Start and Register once the server has
	// stopped or was shut down before it started.
	ErrServerClosed = engine.ErrServerClosed
	// ErrRouteConfig is wrapped by the error Register or Start returns for
	// a broken route configuration: a nil configurer, an invalid or
	// conflicting pattern, or a configurer that panics.
	ErrRouteConfig = engine.ErrRouteConfig
	// ErrShutdownHook is wrapped by the error Start or Shutdown returns
	// when a shutdown hook failed: it returned an error, panicked or
	// outlived its budget.
	ErrShutdownHook = engine.ErrShutdownHook
)

// TraceFromContext returns the trace context of a request handled by the
// server, and false for any other context, including nil.
func TraceFromContext(ctx context.Context) (Trace, bool) { return engine.TraceFromContext(ctx) }

// LoggerFromContext returns the request-scoped logger of a request handled
// by the server: every record it writes carries the request's trace_id
// and span_id as top-level keys. For any other context, including nil, it
// returns slog.Default().
func LoggerFromContext(ctx context.Context) *slog.Logger { return engine.LoggerFromContext(ctx) }

// LogWriteErrors returns how many log records srvr's handlers failed to
// write: the output or a consumer handler returned an error, wrote short, or
// panicked (in Enabled, WithAttrs, WithGroup or Handle; the record is then
// skipped). The count is process-wide: one counter, shared by every server
// of every engine in the process and never reset, because loggers derived
// from slog.Default() do not know which server they belong to. srvr counts
// these failures instead of logging them, because the log is what failed,
// and a request never fails because its log record could not be written.
func LogWriteErrors() uint64 { return engine.LogWriteErrors() }
