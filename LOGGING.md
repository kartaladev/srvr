# srvr log schema

srvr writes one JSON object per line (log/slog) to standard output by default. The keys below are a
public, versioned commitment: every record srvr writes itself carries `srvr.log.schema`. Records your
own code writes (through `srvr.LoggerFromContext` or `slog.Default()`) do not.

Current version: **`srvr.log.schema` = 1**

Change policy: adding a key keeps the version. Renaming or removing a key, or changing its meaning or
value type, increases it. `TestLogSchemaGolden` pins the keys of every event and checks they are
listed in this document.

## Common keys

| Key | Type | On | Meaning |
|-----|------|----|---------|
| `time`, `level`, `msg` | string | every record | slog's own keys; `msg` is the event name |
| `srvr.log.schema` | number | every srvr record | schema version |
| `trace_id`, `span_id` | string | every record written with a request context | W3C trace context, always top-level |
| `srvr.attr.truncated` | bool | a record with a cut value | a string attribute, error text, message or `slog.Any` value was cut to `MaxAttrBytes` (default 1024) and ends in `…[truncated]` |
| `service.name` | string | lifecycle and failed records | `ServiceConfig.Name`, default `unknown_service:<executable>` |
| `service.version` | string | lifecycle and failed records | `ServiceConfig.Version`, default the module version from build information |
| `vcs.revision` | string | lifecycle and failed records, when known | commit from build information |
| `deployment.environment.name` | string | lifecycle and failed records, when set | `ServiceConfig.Environment` |

## Request events

`http.request.completed` (no error) and `http.request.failed` (an error was returned, a panic was
recovered, the request was aborted, or no route matched). Exactly one per request. Level: `INFO` below
400, `WARN` for 4xx and aborted requests, `ERROR` for 5xx. A recovered panic is `ERROR`; an error that
arrived after the response started takes the higher of the levels of the status sent and of the
status the error would have been answered with, so a late 5xx error is `ERROR` and a late 4xx error is
`WARN` after a 2xx and `ERROR` after a 5xx. `AccessLogConfig`
(`Disabled`, `SkipPaths`) suppresses only `http.request.completed` records below 500; an
`http.request.failed` record is never suppressed, except by the opt-in unmatched-record limit below.

A handler error matching `context.Canceled` returned after the client went away (the request context
is cancelled) is answered `499` "Client Closed Request" instead of `500`, so its record is
`http.request.failed` at `WARN` with `problem.status` `499`: a disconnect is not a server fault. The
same error while the request context is live or past its deadline (a server-side cancellation or timeout)
stays `500` at `ERROR`. On
fiber a disconnect is not detected, so it stays `500`.

| Key | Type | Meaning |
|-----|------|---------|
| `http.request.method` | string | request method |
| `http.route` | string | the matched pattern's path, such as `/users/{id}`; absent when no route matched |
| `http.response.status_code` | number | final status sent (`200` when the handler wrote none, even after a `1xx`); absent for an aborted request that sent no final status |
| `http.response.body.size` | number | body bytes written |
| `duration_ms` | number | handling time in milliseconds |
| `user_agent.original` | string | `User-Agent`, when sent; never with `AccessLogConfig.OmitUserAgent` |
| `url.path` | string | only with `AccessLogConfig.IncludePath` |
| `url.query` | string | only with `AccessLogConfig.IncludeQuery` |
| `client.address` | string | only with `AccessLogConfig.IncludeClientAddress` |
| `srvr.unmatched.suppressed` | number | only with an unmatched-record limit: the unmatched records suppressed since the last one written, on the first one written after them |
| `problem` (`status`, `type`, `title`) | group | the problem sent to the client (failed records) |
| `srvr.response.committed` | bool | the response had started when the error arrived, so no problem was sent |
| `srvr.response.aborted` | bool | the handler panicked with `http.ErrAbortHandler` |
| `error.type` | string | returned error: the Go type of the innermost error reached through `Unwrap() error` (an error wrapping several errors is itself the innermost); panic: the constant `panic` |
| `error.kind` | string | `panic` for recovered and aborting panics |
| `error.chain` | array of string | Go types from outermost to innermost, breadth-first, at most 32 levels and 64 errors |
| `srvr.error.truncated` | bool | the chain walk hit a limit or an `Unwrap` panicked |
| `error.fingerprint` | string | 16 hex characters from method, route and the type chain (panics: panic value type and top application frame), never message text; a method outside the semantic-convention set counts as `_OTHER` |
| `exception.message` | string | full error text, or the panic value; at most 8 KiB; passes the redactor (see [Redaction](#redaction)) |
| `exception.type` | string | Go type of a panic value |
| `exception.stacktrace` | string | goroutine stack at the panic, at most 32 KiB; redacted like the message |
| `srvr.stacktrace.truncated` | bool | the stack was cut |
| `code.function.name` | string | the route handler's function (method values without `-fm`; mounted handlers as `<type>.ServeHTTP`) |
| `code.file.path`, `code.line.number` | string, number | its location, omitted when the runtime cannot tell (method-value wrappers) |

### Unmatched-record limit

Every request no route matched (`404` or `405` answered by the router) writes an
`http.request.failed` record, so a path scanner controls the log volume. `WithUnmatchedRecordLimit(burst,
interval)` (or `AccessLogConfig.UnmatchedLimit{Burst, Interval}`) writes at most `burst` of these
records per `interval` per server and counts the rest; the count goes, as `srvr.unmatched.suppressed`,
on the first unmatched record written in a later window, and what is left on `server.stopped`. Off by
default (`Burst` `0`): failed records are the triage record, so a limit loosens a triage default and
is the consumer's explicit choice. Handler errors, panics, `5xx` and `http.request.completed` records
are never limited; observers (`otelsrvr` metrics and spans) still see every request; a suppressed
record discards the request's debug buffer. A negative `Burst`, or a positive one with `Interval` not
above zero, fails construction. Adding `srvr.unmatched.suppressed` kept `srvr.log.schema` at 1.

Never logged at any level: request or response bodies, `Authorization`, `Cookie`, `Set-Cookie`, the
query string, the raw path and the client address, unless the named opt-in above is set. Error text
goes to logs, never to 5xx responses.

## Debug buffer keys

With `WithDebugBuffer` (off by default), records logged with a request context below the log level are
kept per request and written only when the request fails (status at or above the flush threshold,
default 500, or a recovered panic), just before its access record. Adding these keys kept
`srvr.log.schema` at 1.

| Key | Type | On | Meaning |
|-----|------|----|---------|
| `srvr.buffered` | bool | a flushed record | always `true`, top-level even under `WithGroup`; the record keeps its original `time`, `level`, attributes and `trace_id` |
| `srvr.debug_buffer.flushed` | number | the access record of a request that flushed (also with nothing buffered) | records written from the buffer |
| `srvr.debug_buffer.dropped` | number | same | records evicted for the per-request limits, larger than `MaxBytes` on their own, or refused by the process-wide `TotalBytes` budget |
| `srvr.debug_buffer.truncated` | bool | same | the process-wide budget refused at least one record |

### Debug buffering

**Semantics.** Off by default. With `WithDebugBuffer` (or `LogConfig.DebugBuffer.Enabled`), a record
logged with a request's context, or through `srvr.LoggerFromContext`, below the log level and at or
above `BufferLevel` (default `DEBUG`) is kept in that request's buffer instead of being dropped.
Records at or above the log level are written at once, as always, and never written twice. When the
request ends, the buffer is flushed if the request failed with a status at or above `FlushAtStatus`
(default `500`; for a returned error, the status resolved for it even when another status was
already sent), panicked, or was aborted with `http.ErrAbortHandler`. Otherwise it is discarded
unwritten, including when the access record is suppressed (skip paths, a disabled access log, or a
log level that disables the access record's level). A client disconnect answered `499` (see
[Request events](#request-events)) does not flush. Records logged after the request ended, or with
another context (such as `context.Background()` in a goroutine), follow the level rule unbuffered.

**Cost.** Every below-level call made with a request context builds a full record (slog's `Enabled`
short-circuit no longer applies to it) and the request holds it until it ends: about 5 KB and 5
allocations for five buffered records, and about 11 more allocations to write them on failure
(`BenchmarkDebugBuffer`). On failure the log gets `DEBUG` content, which can be more detailed than
`INFO`; it passes the same truncation, redaction and `ReplaceAttr` as every record.

**Ordering.** On a flush, the buffered records are written in the order they were logged, then the
access record. They keep their original `time`, `level`, attributes, groups and `trace_id`, and carry
`srvr.buffered` `true`. Records at or above the log level were written when logged, so an `INFO`
record can precede earlier `DEBUG` records in the stream: order a `trace_id` by `time`. Flushed
records reach your handler's `Handle` without its `Enabled` being asked again, so a handler that
filters by level inside `Handle` drops them. A `slog.Any` value holding a pointer is rendered at flush
time and shows later changes.

**Limits.** Per request, `MaxRecords` (default 100) and `MaxBytes` (default 64 KiB, estimated from the
truncated text plus a fixed overhead per record) evict the oldest records; a single record larger than
`MaxBytes` is dropped. All requests of a server share `TotalBytes` (default 16 MiB); a record that does
not fit is dropped and the request is marked truncated. The access record of a request that flushed
reports `srvr.debug_buffer.flushed`, `srvr.debug_buffer.dropped` and `srvr.debug_buffer.truncated`.
Invalid limits fail construction with an error naming the field (`DebugBuffer.MaxRecords` and so on).

**Sample flushed sequence** (log level `INFO`; the handler logged two `DEBUG` records and an `INFO`
checkpoint, then returned an error):

```json
{"time":"2026-11-19T10:00:00.002Z","level":"INFO","msg":"checkpoint","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7"}
{"time":"2026-11-19T10:00:00.001Z","level":"DEBUG","msg":"load user","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7","srvr.buffered":true,"user.id":"42"}
{"time":"2026-11-19T10:00:00.003Z","level":"DEBUG","msg":"insert user","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7","srvr.buffered":true,"db.attempt":2}
{"time":"2026-11-19T10:00:00.004Z","level":"ERROR","msg":"http.request.failed","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7","srvr.log.schema":1,"http.request.method":"POST","http.route":"/users","http.response.status_code":500,"http.response.body.size":108,"duration_ms":4.1,"user_agent.original":"curl/8.0","problem":{"status":500,"type":"about:blank","title":"Internal Server Error"},"error.type":"syscall.Errno","exception.message":"insert user: dial: connection refused","error.chain":["*fmt.wrapError","*net.OpError","syscall.Errno"],"error.fingerprint":"9f2c4e1a7b3d5c60","code.function.name":"main.UsersAPI.Create","code.file.path":"/app/users.go","code.line.number":42,"service.name":"registration-api","service.version":"1.4.2","srvr.debug_buffer.flushed":2,"srvr.debug_buffer.dropped":0,"srvr.debug_buffer.truncated":false}
```

## Lifecycle events

| Event | Level | Keys |
|-------|-------|------|
| `server.started` | INFO | `server.address` |
| `server.stopping` | INFO | (common keys) |
| `server.stopped` | INFO, or WARN when the drain deadline passed | `srvr.drain.duration_ms`, `srvr.drain.forced`, `srvr.unmatched.suppressed` (unmatched records suppressed and not yet reported, with an unmatched-record limit) |
| `server.failed` | ERROR, when `Start` fails: before serving (binding, configuration, a done context) or because serving failed on its own (for example a `WithListener` listener whose `Accept` panics) | `exception.message` |
| `server.shutdown_hook.failed` | WARN | `srvr.shutdown_hook.phase`, `srvr.shutdown_hook.index`, `code.function.name`, `exception.message` |
| `server.config.warning` | WARN, once per server, for a configuration that works but is likely unintended: at construction for engine settings, at `Start` for route-dependent ones; it never fails either | `srvr.warning.code` (stable snake_case: `gin_debug_mode`, `fiber_header_limit_capped`), `srvr.warning.message` |

Lifecycle records carry the service keys and never `trace_id`. Their `exception.message` is built
like a request failure record's: it passes the redactor (see [Redaction](#redaction); `[redaction failed]`
when a consumer redactor panics) and is limited to 8 KiB, not to `MaxAttrBytes`. With a negative
`MaxAttrBytes` nothing is cut, but `exception.*` text and `error` values still reach a consumer
handler as plain strings. A consumer handler and the redactor are called synchronously: one that
blocks stalls the request or the `Start`/`Shutdown` call writing the record.

## Observer events

| Event | Level | Keys |
|-------|-------|------|
| `srvr.observer.fault` | ERROR, once per observer and fault kind, when a `WithObserver` observer panics; the request is answered as if the observer were absent | `srvr.observer.fault.kind` (`begin_panic`: `Begin` panicked, or the context it returned panicked when read; `end_panic`: the end function panicked, also with `http.ErrAbortHandler`, which is never re-raised; `trace_source_panic`: its `TraceFromContext` panicked, and srvr kept its own trace), `srvr.observer.type` (the observer's Go type), `exception.message` (the panic value's text, redacted and limited like lifecycle text) |

The record is written with the request's context, so it carries `trace_id` and `span_id` of the first
request that hit the fault, and the service keys. Observers run synchronously on the request path
(`Begin` before routing, the end function just before the access record): a slow observer delays the
response, and is neither detected nor logged. Adding this event kept `srvr.log.schema` at 1.

## Events of companion modules

| Event | Level | Keys |
|-------|-------|------|
| `otel.export.failed` | WARN, written by `otelsetup` only when `SetGlobal` installed its OpenTelemetry error handler, at most once per minute per error kind; written to the logger given with `otelsetup.WithLogger` (default `slog.Default()`) | `error.type` (the error's Go type), `exception.message` (the error text, passed through srvr's default redactor) |

It carries `srvr.log.schema` 1; adding it kept the version.

## Lost records

A record the output or a consumer `slog.Handler` fails to take (an error, a short write, or a panic
in `Enabled`, `WithAttrs`, `WithGroup` or `Handle`) is skipped and counted in
`srvr.LogRecordsLost()`, never logged and never allowed to affect the response. The counter is
process-wide (every server of every engine) and never reset; export it as a metric or check it in
a health probe to notice a broken log output.

## What reaches the log by default

- `user_agent.original` is on every access record of a request that sent `User-Agent`. It is how
  triage tells a browser, a mobile app version or a crawler apart, and srvr cuts it to `MaxAttrBytes` like any value. Some mobile SDKs put
  device identifiers in it; if that counts as personal data for you, omit it with
  `WithoutAccessLogUserAgent()` (or `AccessLogConfig.OmitUserAgent`). It is kept by default because
  it is the cheapest way to tell clients and scanners apart in triage.
- Paths, query strings, client addresses, headers, cookies and bodies are not logged unless an
  explicitly named option turns them on (`WithAccessLogIncludePath`, `WithAccessLogIncludeQuery`,
  `WithAccessLogIncludeClientAddress`).

## Trusting incoming trace context

srvr continues a valid incoming `traceparent` by default, so on a server not behind a trusted gateway
any client chooses the `trace_id`; set `TraceConfig.IgnoreIncoming` (or
`WithIgnoreIncomingTraceContext`) there. Its godoc gives the reason.

## Redaction

`exception.message` and `exception.stacktrace` of `http.request.failed`, `server.failed` and
`server.shutdown_hook.failed` pass a redactor before they are written. With no option, srvr's default
redactor (`engine.RedactSecrets`) replaces with `***`:

- the value of every URL query parameter with a non-empty name (`?api_key=SECRET&amount=5` becomes
  `?api_key=***&amount=***`);
- the value of every `key=value`, `key: value` or `"key":"value"` pair whose key contains `token`, `key`,
  `secret`, `password`, `passwd`, `authorization`, `cookie`, `session` or `credential`, in any case,
  together with an auth scheme before it (`Authorization: Bearer abc` becomes `Authorization: ***`).

A colon needs a space or a quote after it, so stack frames (`cookie.go:42`) and `host:port` stay
readable. The default masks too much rather than too little: it may hide harmless text, never shows a
matched secret. `WithErrorRedactor(fn)` replaces it; `WithoutErrorRedaction()` writes the text
unredacted, which loosens a safety default. The last of the two options wins. The redactor did not
change the schema: `exception.message` was always the text after the redactor.

## Values of any kind

An attribute your code logs with `slog.Any` (a map, slice, struct, `[]byte` or `fmt.Stringer`) is
written unchanged when its formatted text (`fmt`, or `String()`) fits `MaxAttrBytes`. Otherwise it is
replaced by that text cut like a string, and `srvr.attr.truncated` is set. A value whose `String`
panics is written as `<panic formatting value>`. The size check uses formatted text, so it is
approximate for values near the limit. A negative `MaxAttrBytes` keeps every value.

## Sample triage record

```json
{"time":"2026-11-17T10:00:00Z","level":"ERROR","msg":"http.request.failed","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7","srvr.log.schema":1,"http.request.method":"POST","http.route":"/users","http.response.status_code":500,"http.response.body.size":108,"duration_ms":3.2,"user_agent.original":"curl/8.0","problem":{"status":500,"type":"about:blank","title":"Internal Server Error"},"error.type":"syscall.Errno","exception.message":"insert user: dial: connection refused","error.chain":["*fmt.wrapError","*net.OpError","syscall.Errno"],"error.fingerprint":"9f2c4e1a7b3d5c60","code.function.name":"main.UsersAPI.Create","code.file.path":"/app/users.go","code.line.number":42,"service.name":"registration-api","service.version":"1.4.2","vcs.revision":"9c1e2ab"}
```

The client received the problem with the same `trace_id`, so one id links what the client saw to this
record.
