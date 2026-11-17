package srvr

import (
	"net"
	"net/http"
	"slices"
)

// AccessLogConfig configures the access record written for every request.
// Pass it with [WithAccessLogConfig]; zero values are the defaults.
//
// Suppression (Disabled, SkipPaths) applies only to http.request.completed
// records with a status below 500: http.request.failed records (a returned
// error, a recovered panic, an aborted or unmatched request) are the triage
// record and are always written, whatever their status. The
// Include fields each loosen a privacy default: by default the access
// record holds the route pattern but not the path, the query string or the
// client address, which may carry personal data or credentials.
type AccessLogConfig struct {
	// Disabled suppresses http.request.completed records below status 500.
	Disabled bool `json:"disabled" yaml:"disabled" env:"DISABLED"`
	// SkipPaths suppresses http.request.completed records below status 500
	// for requests whose path equals one of these exactly, such as
	// "/healthz".
	SkipPaths []string `json:"skip_paths" yaml:"skip_paths" env:"SKIP_PATHS"`
	// IncludePath adds the request path as url.path.
	IncludePath bool `json:"include_path" yaml:"include_path" env:"INCLUDE_PATH"`
	// IncludeQuery adds the raw query string as url.query.
	IncludeQuery bool `json:"include_query" yaml:"include_query" env:"INCLUDE_QUERY"`
	// IncludeClientAddress adds the client's IP address as client.address.
	IncludeClientAddress bool `json:"include_client_address" yaml:"include_client_address" env:"INCLUDE_CLIENT_ADDRESS"`
}

// WithAccessLogConfig sets every field of [AccessLogConfig].
func WithAccessLogConfig(c AccessLogConfig) Option {
	return func(s *settings) { s.access = c }
}

// WithAccessLogDisabled suppresses http.request.completed records below
// status 500; failed records are still written.
func WithAccessLogDisabled() Option {
	return func(s *settings) { s.access.Disabled = true }
}

// WithAccessLogSkipPaths suppresses http.request.completed records below
// status 500 for requests to exactly these paths, added to earlier ones;
// failed records are still written.
func WithAccessLogSkipPaths(paths ...string) Option {
	return func(s *settings) { s.access.SkipPaths = slices.Concat(s.access.SkipPaths, paths) }
}

// WithAccessLogIncludePath adds url.path to access records, which loosens
// a privacy default: paths can hold personal data.
func WithAccessLogIncludePath() Option {
	return func(s *settings) { s.access.IncludePath = true }
}

// WithAccessLogIncludeQuery adds url.query to access records, which
// loosens a privacy default: query strings can hold tokens.
func WithAccessLogIncludeQuery() Option {
	return func(s *settings) { s.access.IncludeQuery = true }
}

// WithAccessLogIncludeClientAddress adds client.address to access records,
// which loosens a privacy default: an IP address is personal data.
func WithAccessLogIncludeClientAddress() Option {
	return func(s *settings) { s.access.IncludeClientAddress = true }
}

// suppressed reports whether the http.request.completed record of a
// request to path with status is suppressed.
func (c *AccessLogConfig) suppressed(path string, status int) bool {
	return status < http.StatusInternalServerError && (c.Disabled || slices.Contains(c.SkipPaths, path))
}

// clientAddress returns the IP address of a RemoteAddr.
func clientAddress(remote string) string {
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}

	return remote
}
