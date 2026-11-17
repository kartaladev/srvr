# Proposal

## Why

Teams in the organisation each assemble their own HTTP server around net/http, Gin or Fiber, and each reinvents lifecycle handling, error rendering and logging differently. Incidents are then triaged from logs that lack correlation ids, error chains or build information. srvr gives every service one server library with safe defaults, RFC 9457 errors through prblm, and logs designed so an AI (or a human) can triage a failure from a single `trace_id`. This first change delivers the library on the standard-library engine only, so it is usable and releasable before any third-party engine exists.

## What Changes

- New module `github.com/kartaladev/srvr` (Go 1.27). Dependencies: standard library and `github.com/kartaladev/prblm` core only.
- A `Server` with `Start(ctx) error` (blocks until the context is done or serving fails, then drains) and `Shutdown(ctx) error` (graceful, idempotent), plus `Ready()` and `Addr()` for readiness and tests.
- Ordered shutdown hooks (`WithShutdownHook` with `BeforeDrain` and `AfterDrain` phases) that run exactly once on every stop path, including `Start` returning because its context ended; later changes (OpenTelemetry flush, scrty session signals) use the same mechanism.
- Generic route registration: `RouteConfigurer[G]` with `Configure(group G)` and `RouteRegistrar[G]` with `Register(RouteConfigurer[G]) error`. In this change `G` is `*srvr.Group`, a thin group type over `http.ServeMux` (Go 1.22+ patterns) with prefixes, ordered middleware and error-returning handlers.
- Every error response is an RFC 9457 problem rendered by prblm: handler errors, unmatched routes (404), wrong methods (405 with `Allow`), oversized bodies (413) and recovered panics (500).
- Structured logging with `log/slog`: JSON lines to stdout, top-level `trace_id`/`span_id` on every request record (also from `WithGroup` loggers), one access record per request with stable event names and OpenTelemetry semantic-convention keys (unmatched 404/405 log `http.request.failed`), every string attribute truncated, and a versioned schema (`srvr.log.schema`).
- W3C Trace Context handling without OpenTelemetry: a valid incoming `traceparent` is continued, otherwise a new trace id is generated; the id is added to problem bodies as `trace_id`.
- Self-contained error records for AI-assisted triage: full error-chain text and types, a stable fingerprint, the handler's code location, panic stacks, the problem actually sent, and service/build information.
- Configuration both programmatic (functional options) and declarative (tagged config structs any loader can fill, passed with `WithConfig`, `WithLogConfig`, `WithAccessLogConfig`, `WithServiceConfig`, `WithTraceConfig`); options apply in argument order. srvr reads no environment variables or files itself. Invalid configuration is a construction error.

## Defaults and Customization

Zero configuration gives:
- Listens on `:8080`; `ReadHeaderTimeout` 10s, `ReadTimeout` 60s, `WriteTimeout` 60s, `IdleTimeout` 120s, `MaxHeaderBytes` 1 MiB, request bodies limited to 4 MiB, shutdown drain 15s, `AfterDrain` hooks 5s.
- Error responses: RFC 9457 JSON (XML on request), no error text on 5xx, no stack traces, no internal paths, `trace_id` extension member.
- Logs: JSON to stdout at `INFO`, no `source`, access log on (`INFO` below 400, `WARN` 4xx, `ERROR` 5xx), every string attribute cut to 1024 bytes (error message 8 KiB, stack 32 KiB). Never logged: request/response bodies, `Authorization`, `Cookie`, `Set-Cookie`, query strings, raw URL path, client address.
- Incoming `traceparent` is honoured.

Opt-ins (named, each a deliberate choice):
- Timeouts, limits, address, listener, drain time; a negative timeout or limit disables it - **loosens a safety default**.
- Log level (static or runtime `*slog.LevelVar`), format (`json`/`text`), `AddSource`, output writer, string attribute limit (negative disables it - **loosens a safety default**), a full consumer `slog.Handler` or `*slog.Logger`, access-log skip paths, access log off.
- `IncludePath`, `IncludeQuery`, `IncludeClientAddress` in access records - **each loosens a privacy default**.
- Ignore incoming `traceparent` (always start a new trace).
- Extra prblm options (mappers, enrichers, namespace, types) and an error hook that runs alongside srvr's own.
- Service name, version and environment for log records (defaults from build information).

## Capabilities

### New Capabilities
- `server-lifecycle`: constructing, starting, readiness, graceful and forced shutdown, and lifecycle misuse of a server.
- `route-registration`: registering route configurers, groups, prefixes, middleware ordering, pattern joining and registration failures on the net/http engine.
- `error-responses`: turning handler errors, unmatched routes, wrong methods, oversized bodies and panics into RFC 9457 problems through prblm.
- `trace-context`: reading, validating and generating W3C trace context per request and exposing the trace id to handlers, logs and problems.
- `structured-logging`: slog configuration, JSON stdout output, trace correlation, access records, event names, attribute keys and redaction.
- `triage-error-records`: the content, limits and safety of the error records that support AI-assisted triage.

### Modified Capabilities
<!-- none: first change of the project -->

## Scope by Gall's Law

The simplest version that works end to end: one route on `*srvr.Group`, served by `Server.Start`, a handler error rendered as a problem, one JSON access line with a `trace_id`, and a clean shutdown. Everything else in this change grows that working system one slice at a time (errors, then logging config, then triage records).

Deliberately left for later changes:
- Gin and Fiber v3 engines and cross-engine parity - `gin-and-fiber-engines`.
- Metrics and tracing (the `Observer` hook, `otelsrvr`, `otelsetup`) - `otel-observability`.
- scrty support - `scrty-integration`.
- Per-request debug buffering flushed on failure - `triage-debug-buffer`.
- Not planned yet: TLS termination, HTTP/2 cleartext, health endpoints, stack capture at error creation. Recorded as `unassigned` carry-overs.

## Impact

- New public API and a new, versioned log schema: both are compatibility commitments from the first release. Adding a log key keeps `srvr.log.schema`; renaming or removing a key, or changing its meaning or type, bumps it.
- New module and its only third-party dependency, prblm v0.1.x (pre-1.0; minor releases may change its API).
- prblm's `OnError` option replaces any earlier hook, so srvr owns that hook and offers its own composable error hook; a consumer passing `prblm.OnError` directly would disable srvr's error logging (documented, and flagged upstream).
- Builds on no earlier change. Later changes assume this change's `Server`, `RouteConfigurer[G]`, problem rendering and log schema.
