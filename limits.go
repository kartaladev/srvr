package srvr

import (
	"time"

	"github.com/kartaladev/srvr/engine"
)

// WithReadHeaderTimeout bounds reading request headers (default 10s); a
// negative d disables it.
func WithReadHeaderTimeout(d time.Duration) Option {
	return func(s *engine.Settings) { s.ReadHeaderTimeout = d }
}

// WithReadTimeout bounds reading a whole request (default 60s); a negative
// d disables it, for example for streaming uploads.
func WithReadTimeout(d time.Duration) Option {
	return func(s *engine.Settings) { s.ReadTimeout = d }
}

// WithWriteTimeout bounds writing a response (default 60s); a negative d
// disables it, for example for streaming responses.
func WithWriteTimeout(d time.Duration) Option {
	return func(s *engine.Settings) { s.WriteTimeout = d }
}

// WithIdleTimeout bounds how long a keep-alive connection waits for its
// next request (default 120s); a negative d disables it.
func WithIdleTimeout(d time.Duration) Option {
	return func(s *engine.Settings) { s.IdleTimeout = d }
}

// WithMaxRequestBodyBytes limits request bodies (default 4 MiB); a handler
// reading past it gets an error rendered as a 413 problem. A negative n
// disables the limit, which loosens a safety default.
func WithMaxRequestBodyBytes(n int64) Option {
	return func(s *engine.Settings) { s.MaxRequestBodyBytes = n }
}

// WithShutdownTimeout bounds draining in-flight requests when Start's
// context ends (default 15s). It must not be negative.
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *engine.Settings) { s.ShutdownTimeout = d }
}
