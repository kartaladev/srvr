package srvr

import (
	"slices"

	"github.com/kartaladev/srvr/engine"
)

// WithAccessLogConfig sets every field of [AccessLogConfig].
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
