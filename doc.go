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
package srvr
