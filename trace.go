package srvr

import "github.com/kartaladev/srvr/engine"

// WithTraceConfig sets every field of [TraceConfig].
func WithTraceConfig(c TraceConfig) Option {
	return func(s *engine.Settings) { s.Trace = c }
}

// WithIgnoreIncomingTraceContext starts a new trace for every request,
// for a server facing clients whose trace ids must not be trusted.
func WithIgnoreIncomingTraceContext() Option {
	return func(s *engine.Settings) { s.Trace.IgnoreIncoming = true }
}
