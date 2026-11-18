package srvr

import (
	"context"
	"io"
	"log/slog"
	"net"
	"slices"
	"time"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/engine"
)

// WithConfig sets every field of [Config], a zero field meaning its
// default. It replaces what earlier options set for those fields.
func WithConfig(c Config) Option {
	return func(s *engine.Settings) { s.Config = c }
}

// WithAddr sets the TCP address the server listens on. The default is
// ":8080", the conventional container port.
func WithAddr(addr string) Option {
	return func(s *engine.Settings) { s.Addr = addr }
}

// WithMaxHeaderBytes limits the size of the request headers (default
// 1 MiB; see [Config] for the reason). n must be positive: unlike a zero Config field, an explicit zero
// is a mistake.
func WithMaxHeaderBytes(n int) Option {
	return func(s *engine.Settings) {
		if n == 0 {
			s.Errs = append(s.Errs, engine.NewConfigError("MaxHeaderBytes", n, "must be positive"))
		}

		s.MaxHeaderBytes = n
	}
}

// WithListener makes the server serve on ln instead of listening on Addr,
// for example a listener from socket activation or a test. It cannot be
// combined with an address.
//
// The server owns ln once New returns it, as http.Server.Serve does; if New
// fails, the caller keeps ln. The server closes ln whenever it closes,
// whether it served or not: after serving, when Start fails, and when
// Shutdown is called before Start. Start(nil) and a Start returning
// ErrServerStarted do not close ln; a Start returning ErrServerClosed does
// not either (ln was closed when the server closed).
//
// A nil or typed-nil ln fails New. Start reads ln.Addr() before serving: a
// nil or typed-nil address, or a panic in Addr or in the address's String,
// fails Start with an error wrapping ErrConfig naming Listener, like any
// other Start failure, instead of serving under an invented address.
//
// The server calls ln's Accept and Close under recover. A panic in Accept,
// which net/http calls on a goroutine of its own where it would end the
// process, stops serving: Start writes server.failed, stops the server in
// order (BeforeDrain hooks, drain, AfterDrain hooks) and returns an error
// wrapping ErrConfig naming Listener. The server calls ln's Close once and
// ignores its error or panic on every path, including
// http.Server.Shutdown's own close: the stop completes, its result is the
// one a successful Close gives, and the drain is never reported as forced
// because of it. Accepted connections are
// used as ln returns them.
func WithListener(ln net.Listener) Option {
	return func(s *engine.Settings) {
		if engine.IsNil(ln) {
			s.Errs = append(s.Errs, engine.NewConfigError("Listener", engine.TypeName(ln), "is nil"))

			return
		}

		s.Listener = ln
	}
}

// WithRouteCheck adds check, which receives the server's route listing
// (one [Route] per declared route, pattern in the engine's syntax,
// configurer named by its Go type; middleware and implicit routes are
// left out) after the configure phase and before the listener is bound.
// Checks run in order, each with Start's context and its own copy of the
// listing; an error or panic fails Start with an error wrapping
// [ErrRouteConfig]. When Start's context ends first, Start returns its
// error and abandons a check that ignores it. check must not be nil.
func WithRouteCheck(check RouteCheck) Option {
	return func(s *engine.Settings) {
		if check == nil {
			s.Errs = append(s.Errs, engine.NewConfigError("RouteCheck", "<nil>", "is nil"))

			return
		}

		s.RouteChecks = append(s.RouteChecks, check)
	}
}

// WithReadHeaderTimeout bounds reading request headers (default 10s; see
// [Config] for the reason); a negative d disables it, which loosens a
// safety default.
func WithReadHeaderTimeout(d time.Duration) Option {
	return func(s *engine.Settings) { s.ReadHeaderTimeout = d }
}

// WithReadTimeout bounds reading a whole request (default 60s; see [Config]
// for the reason); a negative d disables it, for example for streaming
// uploads, which loosens a safety default.
func WithReadTimeout(d time.Duration) Option {
	return func(s *engine.Settings) { s.ReadTimeout = d }
}

// WithWriteTimeout bounds writing a response (default 60s; see [Config] for
// the reason); a negative d disables it, for example for streaming
// responses, which loosens a safety default.
func WithWriteTimeout(d time.Duration) Option {
	return func(s *engine.Settings) { s.WriteTimeout = d }
}

// WithIdleTimeout bounds how long a keep-alive connection waits for its
// next request (default 120s; see [Config] for the reason); a negative d
// disables it, which loosens a safety default.
func WithIdleTimeout(d time.Duration) Option {
	return func(s *engine.Settings) { s.IdleTimeout = d }
}

// WithMaxRequestBodyBytes limits request bodies (default 4 MiB; see
// [Config] for the reason); a handler reading past it gets an error
// rendered as a 413 problem. A negative n disables the limit, which
// loosens a safety default.
func WithMaxRequestBodyBytes(n int64) Option {
	return func(s *engine.Settings) { s.MaxRequestBodyBytes = n }
}

// WithShutdownTimeout bounds draining in-flight requests when Start's
// context ends (default 15s; see [Config] for the reason). It must not be
// negative.
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *engine.Settings) { s.ShutdownTimeout = d }
}

// WithServiceConfig sets every field of [ServiceConfig].
func WithServiceConfig(c ServiceConfig) Option {
	return func(s *engine.Settings) { s.Service = c }
}

// WithServiceName sets service.name.
func WithServiceName(name string) Option {
	return func(s *engine.Settings) { s.Service.Name = name }
}

// WithServiceVersion sets service.version.
func WithServiceVersion(version string) Option {
	return func(s *engine.Settings) { s.Service.Version = version }
}

// WithServiceEnvironment sets deployment.environment.name.
func WithServiceEnvironment(env string) Option {
	return func(s *engine.Settings) { s.Service.Environment = env }
}

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
			s.Errs = append(s.Errs, engine.NewConfigError("ShutdownPhase", phase, "is not BeforeDrain or AfterDrain"))
		case fn == nil:
			s.Errs = append(s.Errs, engine.NewConfigError("ShutdownHook", phase, "function is nil"))
		default:
			s.Hooks = append(s.Hooks, engine.Hook{Phase: phase, Fn: fn})
		}
	}
}

// WithShutdownHookTimeout sets the budget of the AfterDrain hooks (default
// 5s; see [Config] for the reason); a negative d leaves them unbounded,
// which loosens a safety default.
func WithShutdownHookTimeout(d time.Duration) Option {
	return func(s *engine.Settings) { s.ShutdownHookTimeout = d }
}

// WithTraceConfig sets every field of [TraceConfig].
func WithTraceConfig(c TraceConfig) Option {
	return func(s *engine.Settings) { s.Trace = c }
}

// WithIgnoreIncomingTraceContext starts a new trace for every request,
// for a server facing clients whose trace ids must not be trusted.
func WithIgnoreIncomingTraceContext() Option {
	return func(s *engine.Settings) { s.Trace.IgnoreIncoming = true }
}

// WithAccessLogConfig sets every field of [AccessLogConfig]. New copies
// SkipPaths, so changing the caller's slice later never changes which
// records are written.
func WithAccessLogConfig(c AccessLogConfig) Option {
	return func(s *engine.Settings) { s.Access = c }
}

// WithAccessLogDisabled suppresses http.request.completed records below
// status 500; failed records are still written.
func WithAccessLogDisabled() Option {
	return func(s *engine.Settings) { s.Access.Disabled = true }
}

// WithAccessLogSkipPaths suppresses http.request.completed records below
// status 500 for requests to exactly these paths, added to earlier ones;
// failed records are still written.
func WithAccessLogSkipPaths(paths ...string) Option {
	return func(s *engine.Settings) { s.Access.SkipPaths = slices.Concat(s.Access.SkipPaths, paths) }
}

// WithAccessLogIncludePath adds url.path to access records, which loosens
// a privacy default: paths can hold personal data.
func WithAccessLogIncludePath() Option {
	return func(s *engine.Settings) { s.Access.IncludePath = true }
}

// WithAccessLogIncludeQuery adds url.query to access records, which
// loosens a privacy default: query strings can hold tokens.
func WithAccessLogIncludeQuery() Option {
	return func(s *engine.Settings) { s.Access.IncludeQuery = true }
}

// WithAccessLogIncludeClientAddress adds client.address to access records,
// which loosens a privacy default: an IP address is personal data.
func WithAccessLogIncludeClientAddress() Option {
	return func(s *engine.Settings) { s.Access.IncludeClientAddress = true }
}

// WithLogConfig sets every field of [LogConfig], replacing what any
// earlier log option set, LogConfig.DebugBuffer included: a
// [WithDebugBuffer] or [WithDebugBufferConfig] before it has no effect.
// Pass it first, or set its DebugBuffer field.
func WithLogConfig(c LogConfig) Option {
	return func(s *engine.Settings) {
		s.Log.LogConfig = c
		s.Log.LevelVar = nil
	}
}

// WithLogLevel sets the minimum log level (default INFO; see
// [LogConfig] for the reason).
func WithLogLevel(l slog.Level) Option {
	return func(s *engine.Settings) {
		s.Log.Level = l.String()
		s.Log.LevelVar = nil
	}
}

// WithLogLevelVar makes the minimum log level follow lv, so it can change
// at runtime. lv must not be nil.
func WithLogLevelVar(lv *slog.LevelVar) Option {
	return func(s *engine.Settings) {
		if lv == nil {
			s.Errs = append(s.Errs, engine.NewConfigError("LevelVar", "<nil>", "is nil"))

			return
		}

		s.Log.LevelVar = lv
	}
}

// WithLogFormat sets the log format, "json" (default) or "text".
func WithLogFormat(format string) Option {
	return func(s *engine.Settings) { s.Log.Format = format }
}

// WithLogAddSource adds the caller's file and line to every record a
// consumer writes; srvr's own records never carry it.
func WithLogAddSource() Option {
	return func(s *engine.Settings) { s.Log.AddSource = true }
}

// WithLogMaxAttrBytes caps string attributes and other slog.Any values at
// n bytes (default 1024; see [LogConfig] for the reason); a negative n
// disables the cap, which loosens a safety default. Error values still
// reach the handler as their guarded text, a plain string.
func WithLogMaxAttrBytes(n int) Option {
	return func(s *engine.Settings) { s.Log.MaxAttrBytes = n }
}

// WithLogOutput sets where the server's log lines go. The default is
// standard output, where a log agent ships them. w must not be nil.
func WithLogOutput(w io.Writer) Option {
	return func(s *engine.Settings) {
		if engine.IsNil(w) {
			s.Errs = append(s.Errs, engine.NewConfigError("Output", "<nil>", "is nil"))

			return
		}

		s.Log.Output = w
	}
}

// WithLogHandler makes srvr write through h instead of its own JSON or
// text handler. srvr still adds trace correlation, truncation and failure
// containment around it. It cannot be combined with level, format, source
// or output settings, which belong to h. h must not be nil.
//
// srvr calls h synchronously, on the goroutine that logs: a handler that
// blocks stalls the request, or the Start or Shutdown call, that writes
// the record. srvr does not bound it, because an asynchronous handler would
// lose records on a crash.
func WithLogHandler(h slog.Handler) Option {
	return func(s *engine.Settings) { setLogHandler(s, "Handler", "WithLogHandler", h) }
}

// WithLogger is WithLogHandler(l.Handler()). l must not be nil and must
// have a handler: a zero slog.Logger, or one built on a typed-nil handler,
// fails New instead of silently dropping every record.
func WithLogger(l *slog.Logger) Option {
	return func(s *engine.Settings) {
		if l == nil {
			s.Errs = append(s.Errs, engine.NewConfigError("Logger", "<nil>", "is nil"))

			return
		}

		setLogHandler(s, "Logger handler", "WithLogger", l.Handler())
	}
}

// WithSetDefaultLogger makes the server's logger the process default
// (slog.SetDefault) once the server's construction succeeds, so
// slog.Default() records written with a request context carry its trace
// correlation. A construction that fails, on any engine, leaves
// slog.Default() unchanged. It is opt-in because it changes global state.
func WithSetDefaultLogger() Option {
	return func(s *engine.Settings) { s.Log.SetDefault = true }
}

// setLogHandler makes h the base handler of s, set by the option named
// from; a nil or typed-nil h is an error naming field.
func setLogHandler(s *engine.Settings, field, from string, h slog.Handler) {
	if engine.IsNil(h) {
		s.Errs = append(s.Errs, engine.NewConfigError(field, engine.TypeName(h), "is nil"))

		return
	}

	s.Log.Handler, s.Log.HandlerFrom = h, from
}

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
			s.Errs = append(s.Errs, engine.NewConfigError("ErrorHook", "<nil>", "is nil"))

			return
		}

		s.ErrorHook = fn
	}
}

// WithErrorRedactor applies fn to exception.message and
// exception.stacktrace before they are logged, for example to mask e-mail
// addresses in database errors. fn replaces srvr's default redactor
// ([engine.RedactSecrets], which masks URL query values and secret-looking
// key=value pairs), so a consumer redactor that should keep that masking
// calls it itself. It applies to request failure records and
// to the lifecycle records server.failed and server.shutdown_hook.failed,
// whose exception.message is consumer text too (a hook's database close
// error may carry a connection string); the 8 KiB message limit applies
// after it. A redactor that panics replaces the
// attribute with "[redaction failed]", never the unredacted text. fn must
// not be nil.
//
// srvr calls fn synchronously: in the request's access stage, after the
// response, and in the Start or Shutdown call that writes a lifecycle
// record. A redactor that blocks stalls that request or call.
func WithErrorRedactor(fn func(string) string) Option {
	return func(s *engine.Settings) {
		if fn == nil {
			s.Errs = append(s.Errs, engine.NewConfigError("ErrorRedactor", "<nil>", "is nil"))

			return
		}

		s.Redactor = fn
	}
}

// WithoutErrorRedaction writes exception.message and exception.stacktrace
// unredacted, which loosens a safety default: by default srvr masks URL
// query values and secret-looking key=value pairs in error text (see
// [engine.RedactSecrets]), because credentials in error text must not reach
// the logs. Options apply in order: a later [WithErrorRedactor] replaces
// this, and this replaces an earlier one.
func WithoutErrorRedaction() Option {
	return func(s *engine.Settings) { s.Redactor = func(text string) string { return text } }
}

// DebugBufferOption sets one field of the debug buffer configuration; pass
// it to [WithDebugBuffer].
type DebugBufferOption func(*DebugBufferConfig)

// WithDebugBuffer turns per-request debug buffering on. Records logged
// with a request's context (or through [LoggerFromContext]) below the log
// level, at DEBUG or above, are kept in memory for that request instead of
// being dropped. When the request fails with status 500 or above, or
// panics, they are written just before its access record, in logging
// order, with their original time and trace_id and srvr.buffered true;
// on any other outcome they are discarded unwritten. Records at or above
// the log level are written immediately as always, never twice.
//
// Default off, because it has costs a consumer should choose knowingly:
// every below-level call made with a request context builds a full record
// (slog's Enabled short-circuit no longer applies to it), each request
// holds up to 100 records and 64 KiB, and on failure the log stream gets
// DEBUG content, which may be more detailed than INFO. Buffered records
// go through the same truncation (when buffered, so the limits count the
// cut sizes), redaction and ReplaceAttr as every other record.
//
// Flushed records reach the base handler's Handle without its Enabled
// being asked again: a consumer handler that filters by level inside
// Handle drops them. A slog.Any value holding a pointer is rendered when
// the record is flushed, so it shows changes made after it was logged.
// Records logged without the request's context, such as with
// context.Background() in a goroutine, follow the level rule unbuffered.
//
// opts change the defaults (see [DebugBufferConfig]) in argument order. A
// nil opt fails New. A later [WithLogConfig] replaces the whole
// configuration.
func WithDebugBuffer(opts ...DebugBufferOption) Option {
	return func(s *engine.Settings) {
		s.Log.DebugBuffer.Enabled = true

		for i, opt := range opts {
			if opt == nil {
				s.Errs = append(s.Errs, engine.NewConfigError("DebugBufferOption", i, "is nil"))

				continue
			}

			opt(&s.Log.DebugBuffer)
		}
	}
}

// BufferLevel sets the lowest level buffered (default DEBUG). It must not
// be above a log level set with a level option or LogConfig; equal to it,
// nothing is buffered.
func BufferLevel(l slog.Level) DebugBufferOption {
	return func(c *DebugBufferConfig) { c.Level = l.String() }
}

// FlushAtStatus sets [DebugBufferConfig].FlushStatus: requests failing with
// status n or above flush their buffer. A threshold below the default
// 500, such as 400, logs more client-error context.
func FlushAtStatus(n int) DebugBufferOption {
	return func(c *DebugBufferConfig) { c.FlushStatus = n }
}

// MaxRecords caps the records one request buffers (default 100); the
// oldest are evicted and counted as dropped.
func MaxRecords(n int) DebugBufferOption {
	return func(c *DebugBufferConfig) { c.MaxRecords = n }
}

// MaxBytes caps the estimated bytes one request buffers (default 64 KiB);
// the oldest records are evicted and counted as dropped, and a single
// record larger than n is dropped.
func MaxBytes(n int) DebugBufferOption {
	return func(c *DebugBufferConfig) { c.MaxBytes = n }
}

// TotalBytes caps the estimated bytes all requests of the server buffer
// together (default 16 MiB); a record that does not fit is dropped and the
// request's buffer counts as truncated.
func TotalBytes(n int) DebugBufferOption {
	return func(c *DebugBufferConfig) { c.TotalBytes = n }
}

// WithDebugBufferConfig sets every field of [DebugBufferConfig], a zero
// field meaning its default, replacing what earlier [WithDebugBuffer]
// options set; a later WithDebugBuffer changes the fields its options
// name. A later [WithLogConfig] replaces the whole configuration.
func WithDebugBufferConfig(c DebugBufferConfig) Option {
	return func(s *engine.Settings) { s.Log.DebugBuffer = c }
}
