package srvr

import (
	"io"
	"log/slog"

	"github.com/kartaladev/srvr/engine"
)

// WithLogConfig sets every field of [LogConfig], replacing what earlier
// level options set.
func WithLogConfig(c LogConfig) Option {
	return func(s *engine.Settings) {
		s.Log.LogConfig = c
		s.Log.LevelVar = nil
	}
}

// WithLogLevel sets the minimum log level (default INFO).
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
			s.Errs = append(s.Errs, engine.ConfigError("LevelVar", "<nil>", "is nil"))

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

// WithLogMaxAttrBytes caps string attributes at n bytes (default 1024); a
// negative n disables the cap, which loosens a safety default. Error values
// still reach the handler as their guarded text, a plain string.
func WithLogMaxAttrBytes(n int) Option {
	return func(s *engine.Settings) { s.Log.MaxAttrBytes = n }
}

// WithLogOutput sets where the server's log lines go. The default is
// standard output, where a log agent ships them. w must not be nil.
func WithLogOutput(w io.Writer) Option {
	return func(s *engine.Settings) {
		if engine.IsNil(w) {
			s.Errs = append(s.Errs, engine.ConfigError("Output", "<nil>", "is nil"))

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
			s.Errs = append(s.Errs, engine.ConfigError("Logger", "<nil>", "is nil"))

			return
		}

		setLogHandler(s, "Logger handler", "WithLogger", l.Handler())
	}
}

// WithSetDefaultLogger makes the server's logger the process default
// (slog.SetDefault) when the server is constructed, so slog.Default()
// records written with a request context carry its trace correlation. It
// is opt-in because it changes global state.
func WithSetDefaultLogger() Option {
	return func(s *engine.Settings) { s.Log.SetDefault = true }
}

// setLogHandler makes h the base handler of s, set by the option named
// from; a nil or typed-nil h is an error naming field.
func setLogHandler(s *engine.Settings, field, from string, h slog.Handler) {
	if engine.IsNil(h) {
		s.Errs = append(s.Errs, engine.ConfigError(field, engine.TypeName(h), "is nil"))

		return
	}

	s.Log.Handler, s.Log.HandlerFrom = h, from
}
