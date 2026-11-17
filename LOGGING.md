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
| `srvr.attr.truncated` | bool | a record with a cut value | a string attribute, error text or message was cut to `MaxAttrBytes` (default 1024) and ends in `…[truncated]` |
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
`http.request.failed` record is never suppressed.

| Key | Type | Meaning |
|-----|------|---------|
| `http.request.method` | string | request method |
| `http.route` | string | the matched pattern's path, such as `/users/{id}`; absent when no route matched |
| `http.response.status_code` | number | final status sent (`200` when the handler wrote none, even after a `1xx`); absent for an aborted request that sent no final status |
| `http.response.body.size` | number | body bytes written |
| `duration_ms` | number | handling time in milliseconds |
| `user_agent.original` | string | `User-Agent`, when sent |
| `url.path` | string | only with `AccessLogConfig.IncludePath` |
| `url.query` | string | only with `AccessLogConfig.IncludeQuery` |
| `client.address` | string | only with `AccessLogConfig.IncludeClientAddress` |
| `problem` (`status`, `type`, `title`) | group | the problem sent to the client (failed records) |
| `srvr.response.committed` | bool | the response had started when the error arrived, so no problem was sent |
| `srvr.response.aborted` | bool | the handler panicked with `http.ErrAbortHandler` |
| `error.type` | string | returned error: the Go type of the innermost error reached through `Unwrap() error` (an error wrapping several errors is itself the innermost); panic: the constant `panic` |
| `error.kind` | string | `panic` for recovered and aborting panics |
| `error.chain` | array of string | Go types from outermost to innermost, breadth-first, at most 32 levels and 64 errors |
| `srvr.error.truncated` | bool | the chain walk hit a limit or an `Unwrap` panicked |
| `error.fingerprint` | string | 16 hex characters from method, route and the type chain (panics: panic value type and top application frame), never message text |
| `exception.message` | string | full error text, or the panic value; at most 8 KiB; passes the `WithErrorRedactor` redactor |
| `exception.type` | string | Go type of a panic value |
| `exception.stacktrace` | string | goroutine stack at the panic, at most 32 KiB; redacted like the message |
| `srvr.stacktrace.truncated` | bool | the stack was cut |
| `code.function.name` | string | the route handler's function (method values without `-fm`; mounted handlers as `<type>.ServeHTTP`) |
| `code.file.path`, `code.line.number` | string, number | its location, omitted when the runtime cannot tell (method-value wrappers) |

Never logged at any level: request or response bodies, `Authorization`, `Cookie`, `Set-Cookie`, the
query string, the raw path and the client address, unless the named opt-in above is set. Error text
goes to logs, never to 5xx responses.

## Lifecycle events

| Event | Level | Keys |
|-------|-------|------|
| `server.started` | INFO | `server.address` |
| `server.stopping` | INFO | (common keys) |
| `server.stopped` | INFO, or WARN when the drain deadline passed | `srvr.drain.duration_ms`, `srvr.drain.forced` |
| `server.failed` | ERROR, when `Start` fails: before serving (binding, configuration, a done context) or because serving failed on its own (for example a `WithListener` listener whose `Accept` panics) | `exception.message` |
| `server.shutdown_hook.failed` | WARN | `srvr.shutdown_hook.phase`, `srvr.shutdown_hook.index`, `code.function.name`, `exception.message` |

Lifecycle records carry the service keys and never `trace_id`. Their `exception.message` is built
like a request failure record's: it passes the `WithErrorRedactor` redactor (`[redaction failed]`
when the redactor panics) and is limited to 8 KiB, not to `MaxAttrBytes`. With a negative
`MaxAttrBytes` nothing is cut, but `exception.*` text and `error` values still reach a consumer
handler as plain strings. A consumer handler and the redactor are called synchronously: one that
blocks stalls the request or the `Start`/`Shutdown` call writing the record.

## Sample triage record

```json
{"time":"2026-11-17T10:00:00Z","level":"ERROR","msg":"http.request.failed","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7","srvr.log.schema":1,"http.request.method":"POST","http.route":"/users","http.response.status_code":500,"http.response.body.size":108,"duration_ms":3.2,"user_agent.original":"curl/8.0","problem":{"status":500,"type":"about:blank","title":"Internal Server Error"},"error.type":"syscall.Errno","exception.message":"insert user: dial: connection refused","error.chain":["*fmt.wrapError","*net.OpError","syscall.Errno"],"error.fingerprint":"9f2c4e1a7b3d5c60","code.function.name":"main.UsersAPI.Create","code.file.path":"/app/users.go","code.line.number":42,"service.name":"registration-api","service.version":"1.4.2","vcs.revision":"9c1e2ab"}
```

The client received the problem with the same `trace_id`, so one id links what the client saw to this
record.
