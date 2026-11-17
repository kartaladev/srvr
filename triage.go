package srvr

import "github.com/kartaladev/srvr/engine"

// WithErrorRedactor applies fn to exception.message and
// exception.stacktrace before they are logged, for example to mask e-mail
// addresses in database errors. It applies to request failure records and
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
			s.Errs = append(s.Errs, engine.ConfigError("ErrorRedactor", "<nil>", "is nil"))

			return
		}

		s.Redactor = fn
	}
}
