// Package srvr runs an HTTP server on the net/http engine with safe
// defaults, RFC 9457 problem responses rendered by
// github.com/kartaladev/prblm and structured slog logs designed for
// triage.
//
// A consumer constructs a [Server] with [New], contributes routes through
// [RouteConfigurer] values registered with [Server.Register], and runs it
// with [Server.Start]. Every request gets a W3C trace context
// ([TraceFromContext]), one access record and, when it fails, a
// self-contained error record: error chain, fingerprint, handler location,
// panic stack, the problem sent and the service build. The log keys are a
// versioned schema (srvr.log.schema) documented in LOGGING.md.
//
// Every behaviour has a safe default and can be changed with functional
// options or with the tagged config structs [Config], [LogConfig],
// [AccessLogConfig], [ServiceConfig] and [TraceConfig], which any loader
// can fill; srvr reads no environment variables or files itself.
//
// The same configuration runs on the Gin and Fiber v3 engines through the
// modules github.com/kartaladev/srvr/ginsrvr and
// github.com/kartaladev/srvr/fibersrvr, which document where those engines
// differ. On net/http, when a client disconnects before a handler's error
// is rendered, the problem is still written to the closed connection and
// dropped; the failed record counts its bytes.
package srvr
