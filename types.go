package srvr

import (
	"context"
	"log/slog"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/internal/engine"
	"github.com/kartaladev/srvr/internal/errchain"
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
	// ConfigError is the error New returns for one invalid field;
	// errors.As reads its Field, Value and Reason, and it matches
	// [ErrConfig]. Several invalid fields are joined, the first found
	// first.
	ConfigError = errchain.ConfigError
	// Route is one route a configurer declared: method ("" for every
	// method), pattern in the engine's syntax and the configurer's Go type.
	Route = engine.Route
	// Routes is the route listing a [RouteCheck] receives.
	Routes = engine.Routes
	// RouteCheck checks the route listing after the configure phase and
	// before the listener is bound; pass it with [WithRouteCheck].
	RouteCheck = engine.RouteCheck
	// Config holds the server settings that a configuration loader can
	// fill; see [engine.Config] for its fields and defaults. Pass it with
	// [WithConfig].
	Config = engine.Config
	// LogConfig configures srvr's logger; see [engine.LogConfig]. Pass it
	// with [WithLogConfig].
	LogConfig = engine.LogConfig
	// DebugBufferConfig configures per-request debug buffering; see
	// [engine.DebugBufferConfig]. Pass it with [WithDebugBufferConfig] or
	// in [LogConfig].
	DebugBufferConfig = engine.DebugBufferConfig
	// AccessLogConfig configures the access record written for every
	// request; see [engine.AccessLogConfig]. Pass it with
	// [WithAccessLogConfig].
	AccessLogConfig = engine.AccessLogConfig
	// UnmatchedLimitConfig limits the failed records of requests no route
	// matched; see [engine.UnmatchedLimitConfig]. Set it as
	// AccessLogConfig.UnmatchedLimit or with [WithUnmatchedRecordLimit].
	UnmatchedLimitConfig = engine.UnmatchedLimitConfig
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
	// ErrorHook is called for every error the server answers, with an
	// [ErrorReport] of the answer sent: prblm's problem on the default
	// path, the renderer's answer (or the safe 500 replacing a faulty one)
	// with [WithErrorRenderer]. The zero report marks a note about an
	// answer that was not sent, such as a panic after the response
	// started. It also receives prblm's notes, which srvr does not record
	// as the request's failure: a mistake in rendering
	// (prblm.ErrInvalidStatus and the like), and, before the error itself,
	// a note wrapping prblm.ErrUnwalkableError when prblm cannot walk the
	// error's chain and sent the safe 500 problem instead.
	ErrorHook = engine.ErrorHook
	// ErrorReport describes the error answer sent: its status and, when
	// the answer gave them, its problem type and title (always on the
	// default path). It is passed by value, so a hook may keep it. It
	// stays comparable, but fields may be added in later versions: compare
	// its fields rather than whole values against a literal, and build it
	// with keyed literals; see [engine.ErrorReport].
	ErrorReport = engine.ErrorReport
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
	ErrConfig = errchain.ErrConfig
	// ErrServerStarted is returned by Start, Run and Register once the
	// server has been started.
	ErrServerStarted = errchain.ErrServerStarted
	// ErrServerNotStarted is returned by Wait on a server that was never
	// started, and matched by HealthCheck's error before Start returned.
	ErrServerNotStarted = errchain.ErrServerNotStarted
	// ErrServerClosed is returned by Start, Run and Register once the server
	// has stopped or was shut down before it started; HealthCheck and Wait
	// wrap it when serving ended without a stop.
	ErrServerClosed = errchain.ErrServerClosed
	// ErrRouteConfig is wrapped by the error Register or Start returns for
	// a broken route configuration: a nil configurer, an invalid or
	// conflicting pattern, or a configurer that panics.
	ErrRouteConfig = errchain.ErrRouteConfig
	// ErrShutdownHook is wrapped by the error Start, Run, Wait or Shutdown
	// returns when a shutdown hook failed: it returned an error, panicked or
	// outlived its budget.
	ErrShutdownHook = errchain.ErrShutdownHook
)

// The errors of srvr's own answers. The error hook, and an error renderer,
// receive them for the answers srvr makes itself, so they can be matched
// with errors.Is and errors.As without prblm. With the default renderer
// each one is answered with the same problem as before they existed.
var (
	// ErrNotFound is the error of a request no route or static file
	// matched (404). It is a distinct error of its own that matches
	// prblm.ErrNotFound with errors.Is, so a prblm mapper on that sentinel
	// still matches it, and its text is prblm.ErrNotFound's ("prblm: not
	// found"), as v0.3.0 logged. A handler may return it too.
	ErrNotFound = errchain.ErrNotFound
	// ErrRequestTooLarge is wrapped, together with the engine's own error
	// (*http.MaxBytesError on net/http and gin), by the error of a request
	// body over MaxRequestBodyBytes (413).
	ErrRequestTooLarge = errchain.ErrRequestTooLarge
	// ErrHeadersTooLarge is the error of a request whose headers exceed the
	// server's limit, which fiber rejects before routing (431).
	ErrHeadersTooLarge = errchain.ErrHeadersTooLarge
	// ErrRequestTimeout is the error of a request fiber's read timeout
	// ended before routing (408).
	ErrRequestTimeout = errchain.ErrRequestTimeout
	// ErrClientClosed is wrapped, together with the handler's error, by
	// the error of a request whose client went away (499).
	ErrClientClosed = errchain.ErrClientClosed
)

type (
	// MethodNotAllowedError is the error of a request whose path a route
	// or a static mount serves, but not with its method (405). Allow lists
	// the methods the path serves, sorted, as the Allow header sends them.
	MethodNotAllowedError = errchain.MethodNotAllowedError
	// PanicError is the error of a recovered panic, carrying the value and
	// the stack. It is prblm's type, which every engine's recovery
	// produces, so one errors.As finds it on every engine.
	PanicError = prblm.PanicError
)

type (
	// ErrorRenderer turns an error the server answers into the response
	// sent to the client, set with [WithErrorRenderer]. Without one, every
	// engine renders errors with prblm, as [DefaultErrorRenderer] does.
	// srvr calls it in the request goroutine, at most once per request,
	// with a walkable error and a context keeping the request's values but
	// not its cancellation, bounded by 5 seconds; see [WithErrorRenderer]
	// for every rule srvr keeps around it. It may be called concurrently
	// for different requests. On a 405 srvr does not add an Allow header:
	// the renderer sets it from [MethodNotAllowedError].Allow, found with
	// errors.As.
	ErrorRenderer = engine.ErrorRenderer
	// ErrorRendererFunc adapts a function to [ErrorRenderer].
	ErrorRendererFunc = engine.ErrorRendererFunc
	// ErrorRequest is what a renderer knows of the request: method, path
	// (never the query string; for the response, never logged by srvr),
	// matched route in the engine's syntax (as http.route logs it), trace
	// id and headers. Header.Get returns copies; on fiber the reader is
	// valid only during Render.
	ErrorRequest = engine.ErrorRequest
	// ErrorResponse is a renderer's answer: status (400-599), headers
	// (Content-Type required with a body; Content-Length and hop-by-hop
	// headers are dropped) and body (at most 1 MiB), plus a Type and Title
	// for the record's problem group and error hooks only, never sent; an
	// answer breaking a rule is replaced by srvr's safe 500.
	ErrorResponse = engine.ErrorResponse
)

// DefaultErrorRenderer returns prblm's renderer, the default of every
// engine, as an [ErrorRenderer]: a consumer's renderer can decorate some
// errors and delegate the rest to it. During a request it answers with
// that server's prblm options and trace id; outside one, with prblm's
// defaults and srvr's own answers ([ErrNotFound] and the others).
func DefaultErrorRenderer() ErrorRenderer { return engine.DefaultRenderer }

// TraceFromContext returns the trace context of a request handled by the
// server, and false for any other context, including nil.
func TraceFromContext(ctx context.Context) (Trace, bool) { return engine.TraceFromContext(ctx) }

// LoggerFromContext returns the request-scoped logger of a request handled
// by the server: every record it writes carries the request's trace_id
// and span_id as top-level keys. For any other context, including nil, it
// returns slog.Default().
func LoggerFromContext(ctx context.Context) *slog.Logger { return engine.LoggerFromContext(ctx) }

// LogRecordsLost returns how many log records were lost: srvr's handlers
// skipped them because the output or a consumer handler returned an error,
// wrote short, or panicked (in Enabled, WithAttrs, WithGroup or Handle). It
// counts every lost record, not only failed writes. The count is
// process-wide: one counter, shared by every server of every engine in the
// process and never reset, because loggers derived from slog.Default() do
// not know which server they belong to. srvr counts these failures instead
// of logging them, because the log is what failed, and a request never fails
// because its log record could not be written.
func LogRecordsLost() uint64 { return engine.LogRecordsLost() }

type (
	// Observer is told about every request the server handles; see
	// [engine.Observer]. Register it with [WithObserver].
	Observer = engine.Observer
	// RequestInfo is what an observer learns about a request before
	// routing; see [engine.RequestInfo].
	RequestInfo = engine.RequestInfo
	// ResultInfo is what an observer learns about a finished request; see
	// [engine.ResultInfo].
	ResultInfo = engine.ResultInfo
	// ErrorKind is how a request failed; see [engine.ErrorKind].
	ErrorKind = engine.ErrorKind
	// TraceSource is an optional extension of an Observer that supplies
	// the request's trace context; see [engine.TraceSource].
	TraceSource = engine.TraceSource
	// HeaderReader reads request headers during an observer's Begin; see
	// [engine.HeaderReader].
	HeaderReader = engine.HeaderReader
)

const (
	// ErrorKindNone is a request that did not fail.
	ErrorKindNone = engine.ErrorKindNone
	// ErrorKindError is a request that failed with a returned error or
	// matched no route.
	ErrorKindError = engine.ErrorKindError
	// ErrorKindPanic is a request whose handler panicked.
	ErrorKindPanic = engine.ErrorKindPanic
)

// WithObserver adds o to the server's observers, which every engine calls
// once per request, between the access log and panic recovery; observers
// run on the request path and must be fast (see [engine.Observer]).
// Default: no observer, so a server without one does no observer work. A
// nil or typed-nil o fails New with an error wrapping [ErrConfig].
func WithObserver(o Observer) Option {
	return func(s *engine.Settings) { s.Observers = append(s.Observers, o) }
}
