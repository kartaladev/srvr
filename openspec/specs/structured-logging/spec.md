# structured-logging Specification

## Purpose

Defines srvr's slog-based logging: configuration, JSON output to stdout, trace correlation, one access record per request with stable event names and attribute keys, and redaction that holds at every level.

## Requirements

### Requirement: JSON lines to stdout by default
With no logging configuration, the server SHALL write one JSON object per line to standard output through `log/slog` at level `INFO`, without a `source` attribute.

#### Scenario: Default output
- **Given** a server with no logging configuration
- **When** a client sends `GET /ping` and the handler writes `200`
- **Then** standard output receives one line that parses as a JSON object
- **And** it has `level` `<level>`, `msg` `<msg>` and no `<absent>` key

##### Examples:
| level | msg | absent |
|-------|-----|--------|
| `INFO` | `http.request.completed` | `source` |

### Requirement: Logging is configurable programmatically and by config struct
The consumer SHALL be able to set level, format (`json`, `text`), `AddSource`, output and `MaxAttrBytes` through individual options or `WithLogConfig(LogConfig)`, whose text fields are case-insensitive. Options apply in argument order; `WithLogConfig` sets every field, so an empty field means the default. Unknown values SHALL fail construction naming the field.

#### Scenario: Level and format
- **Given** a server configured with `<config>`
- **When** the server logs a `DEBUG` record and an `INFO` record
- **Then** the output contains `<records>` in format `<format>`

##### Examples: accepted
| config | records | format |
|--------|---------|--------|
| (none) | the `INFO` record only | JSON |
| `LogConfig{Level: "debug"}` | both records | JSON |
| `LogConfig{Level: "DEBUG"}` | both records | JSON |
| `LogConfig{Level: "warn"}` | neither record | JSON |
| `LogConfig{Level: "info-4"}` | both records | JSON |
| `LogConfig{Format: "text"}` | the `INFO` record only | slog text |
| `LogConfig{Format: "JSON"}` | the `INFO` record only | JSON |
| `WithLogLevel(slog.LevelDebug)` | both records | JSON |
| `WithLogConfig(LogConfig{Level: "debug"})` | both records | JSON |
| `WithLogConfig(LogConfig{Level: "debug"})` then `WithLogLevel(slog.LevelWarn)` | neither record | JSON |
| `WithLogLevel(slog.LevelDebug)` then `WithLogConfig(LogConfig{Format: "text"})` | the `INFO` record only | slog text |

##### Examples: rejected
| config | records | format |
|--------|---------|--------|
| `LogConfig{Level: "verbose"}` | construction error wrapping `srvr.ErrConfig` naming `Level` | (none) |
| `LogConfig{Format: "xml"}` | construction error wrapping `srvr.ErrConfig` naming `Format` | (none) |
| `WithLogOutput(nil)` | construction error wrapping `srvr.ErrConfig` naming `Output` | (none) |
| `WithLogConfig(LogConfig{Level: "verbose"})` | construction error wrapping `srvr.ErrConfig` naming `Level` | (none) |

#### Scenario: Config struct decoded from a file
- **Given** the JSON document `<document>` decoded with `encoding/json` into `srvr.LogConfig`
- **When** the server is constructed with it
- **Then** construction returns `<result>`

##### Examples:
| document | result |
|----------|--------|
| `{"level":"debug","format":"text","add_source":true}` | a server logging `DEBUG` text with `source` |
| `{}` | a server with the defaults |
| `{"level":"loud"}` | a construction error wrapping `srvr.ErrConfig` naming `Level` and `loud` |
| `{"level":"debug","unknown":1}` | a server logging `DEBUG` JSON (unknown keys are the loader's concern) |

### Requirement: Log level can change at runtime
With `WithLogLevelVar(*slog.LevelVar)` the server SHALL apply level changes to records written after the change, including while requests are in flight.

#### Scenario: Runtime level change
- **Given** a server configured with a `*slog.LevelVar` set to `<before>`
- **When** the consumer sets it to `<after>` and a handler then logs at `DEBUG` through the request logger
- **Then** the `DEBUG` record is `<written>`

##### Examples:
| before | after | written |
|--------|-------|---------|
| `INFO` | `DEBUG` | written |
| `DEBUG` | `INFO` | not written |
| `INFO` | `DEBUG`, changed concurrently with 100 in-flight requests | written, and `go test -race` reports no race |

### Requirement: Source location is optional and points to the caller
With `AddSource` enabled, records SHALL carry a `source` attribute naming the code that called the logger, not srvr's internal handler. Access records written by srvr itself SHALL NOT carry `source`.

#### Scenario: Source attribute
- **Given** a server configured with `<config>`
- **When** a handler at `handlers.go:<line>` logs `INFO` through the request logger
- **Then** the handler's record `source` is `<source>`
- **And** the access record has no `source`

##### Examples:
| config | line | source |
|--------|------|--------|
| `LogConfig{AddSource: true}` | `42` | a file ending in `handlers.go` with line `42` |
| `WithLogAddSource()` | `42` | a file ending in `handlers.go` with line `42` |
| (none) | `42` | (none) |

### Requirement: Consumers may supply their own handler or logger
The consumer SHALL be able to supply a `slog.Handler` or `*slog.Logger`; srvr SHALL still add trace correlation to it. Supplying one together with level, format, source or output settings SHALL fail construction. A consumer handler whose `Enabled`, `WithAttrs`, `WithGroup` or `Handle` fails (returns an error or panics) SHALL never affect the response: srvr counts the failure in `srvr.LogWriteErrors()` and skips that record.

#### Scenario: Own handler
- **Given** a server configured with `<config>`
- **When** the server is constructed and a request is handled
- **Then** the result is `<result>`

##### Examples:
| config | result |
|--------|--------|
| `WithLogHandler(h)` | `h` receives the access record with `trace_id` |
| `WithLogger(l)` | the handler of `l` receives the access record with `trace_id` |
| `WithLogHandler(h)` and `LogConfig{Level: "debug"}` | construction error wrapping `srvr.ErrConfig` naming `Level` |
| `WithLogHandler(nil)` | construction error wrapping `srvr.ErrConfig` |
| `WithLogHandler(h)` where `h.Handle` returns an error | the response is unaffected and status is `200` |
| `WithLogHandler(h)` where `h.Handle` panics | the response is unaffected and status is `200` |
| `WithLogHandler(h)` where `h.Enabled` panics for request records | the response is unaffected, status is `200`, the record is skipped and `srvr.LogWriteErrors()` increases |
| `WithLogHandler(h)` where `h.WithAttrs` panics | the response is unaffected, status is `200`, the record is skipped and `srvr.LogWriteErrors()` increases |
| `WithLogHandler(h)` where `h.WithGroup` panics and the handler logs through `srvr.LoggerFromContext(ctx).WithGroup("db")` | the response is unaffected, status is `200`, the record is skipped and `srvr.LogWriteErrors()` increases |

### Requirement: Request records carry trace correlation
Every record written with a request's context SHALL carry `trace_id` and `span_id`; records written without a request context SHALL carry neither key, never an empty value. Handlers SHALL get a request-scoped logger through `srvr.LoggerFromContext`.

#### Scenario: Correlation keys
- **Given** a record logged `<where>`
- **When** the record is written
- **Then** its `trace_id` is `<trace_id>`

##### Examples:
| where | trace_id |
|-------|----------|
| by a handler with `srvr.LoggerFromContext(r.Context())` | the request's trace id |
| by a handler with `srvr.LoggerFromContext(r.Context()).WithGroup("db")` | the request's trace id, as a top-level key |
| by a handler with `slog.Default().InfoContext(r.Context(), ...)` after `WithSetDefaultLogger()` | the request's trace id |
| by srvr at server start | (none) |
| with `srvr.LoggerFromContext(context.Background())` | (none) |

### Requirement: Correlation keys stay top-level under groups
`trace_id` and `span_id` SHALL be top-level keys of every request record, whatever `WithGroup` and `WithAttrs` calls derived the logger, and SHALL never appear inside a group.

#### Scenario: Grouped loggers
- **Given** a handler logger derived as `<logger>` from the request logger
- **When** the handler logs `INFO` `query` with attribute `k` `v`
- **Then** the record has top-level `trace_id` and `span_id` equal to the request's
- **And** the rest of the record is `<shape>`

##### Examples:
| logger | shape |
|--------|-------|
| `l` (no derivation) | `"k":"v"` |
| `l.WithGroup("db")` | `"db":{"k":"v"}` |
| `l.WithGroup("a").WithGroup("b")` | `"a":{"b":{"k":"v"}}` |
| `l.With("table", "users").WithGroup("db")` | `"table":"users","db":{"k":"v"}` |
| `l.WithGroup("db").With("table", "users")` | `"db":{"table":"users","k":"v"}` |
| `l.WithGroup("")` | `"k":"v"` (an empty group name is ignored, as in `log/slog`) |
| `slog.Default().WithGroup("db")` after `WithSetDefaultLogger()` | `"db":{"k":"v"}` |

### Requirement: One access record per request
The server SHALL write exactly one access record per request when the response finishes or is aborted, with `msg` `http.request.completed` or, when an error was returned, a panic recovered, the request aborted or no route matched, `http.request.failed`. Its status SHALL be the final one sent. Its level SHALL be `INFO` below 400, `WARN` for 4xx and aborted requests, `ERROR` for 5xx. Skip paths and disabling the access log SHALL suppress only `http.request.completed` records with status below 500.

#### Scenario: Access record content
- **Given** route `GET /users/{id}` whose handler `<behaviour>`
- **When** a client sends `GET /users/42`
- **Then** one access record is written with `msg` `<msg>` and `level` `<level>`
- **And** it has `http.request.method` `GET`, `http.route` `<route>`, `http.response.status_code` `<status>` and a `duration_ms` number

##### Examples:
| behaviour | msg | level | route | status |
|-----------|-----|-------|-------|--------|
| writes `200` | `http.request.completed` | `INFO` | `/users/{id}` | `200` |
| writes `304` | `http.request.completed` | `INFO` | `/users/{id}` | `304` |
| writes only `103` Early Hints and nothing else | `http.request.completed` | `INFO` | `/users/{id}` | `200` (the status the client receives; an informational status is never reported) |
| writes `404` directly and returns `nil` | `http.request.completed` | `WARN` | `/users/{id}` | `404` |
| returns `prblm.ErrNotFound` | `http.request.failed` | `WARN` | `/users/{id}` | `404` |
| returns `errors.New("boom")` | `http.request.failed` | `ERROR` | `/users/{id}` | `500` |
| panics | `http.request.failed` | `ERROR` | `/users/{id}` | `500` |

#### Scenario: Aborted request
- **Given** route `GET /users/{id}` whose handler `<behaviour>`
- **When** a client sends `GET /users/42`
- **Then** the client's connection is closed `<response>`
- **And** one access record is written with `msg` `http.request.failed`, `level` `WARN`, `error.kind` `panic`, `error.type` `panic`, `srvr.response.aborted` `true`, `http.response.status_code` `<status>` and no `problem` group

##### Examples:
| behaviour | response | status |
|-----------|----------|--------|
| panics with `http.ErrAbortHandler` | without a response | (none) |
| writes `200` and part of the body, then panics with `http.ErrAbortHandler` | after status `200` and a partial body | `200` |

#### Scenario: Unmatched requests
- **Given** no route matches `<request>`
- **When** the request is handled
- **Then** the access record has `msg` `<msg>`, `level` `<level>`, `http.route` `<route>` and `http.response.status_code` `<status>`
- **And** its `problem.status` is `<status>`

##### Examples:
| request | msg | level | route | status |
|---------|-----|-------|-------|--------|
| `GET /nope` | `http.request.failed` | `WARN` | (none) | `404` |
| `DELETE /users/42` with only `GET /users/{id}` registered | `http.request.failed` | `WARN` | (none) | `405` |

#### Scenario: Access log configuration
- **Given** a server configured with `<config>`
- **When** a client sends `<request>`
- **Then** an access record is `<written>`

##### Examples:
| config | request | written |
|--------|---------|---------|
| (none) | `GET /healthz` | written |
| `AccessLogConfig{SkipPaths: ["/healthz"]}` | `GET /healthz` | not written |
| `AccessLogConfig{SkipPaths: ["/healthz"]}` | `GET /healthz` returning `500` | written |
| `AccessLogConfig{SkipPaths: ["/healthz"]}` | `GET /healthz/deep` | written |
| `AccessLogConfig{Disabled: true}` | `GET /ping` | not written |
| `AccessLogConfig{Disabled: true}` | `GET /ping` returning `500` | written |
| `WithAccessLogConfig(AccessLogConfig{SkipPaths: []string{"/healthz"}})` | `GET /healthz` | not written |
| `WithAccessLogSkipPaths("/healthz")` | `GET /healthz` | not written |
| `WithAccessLogDisabled()` | `GET /ping` | not written |
| `WithAccessLogDisabled()` then `WithAccessLogConfig(AccessLogConfig{})` | `GET /ping` | written |

### Requirement: Failed-request records are never suppressed
An `http.request.failed` record SHALL be written whatever the access log configuration. A recovered panic SHALL be logged at `ERROR`, and an error that arrived after the response started at the higher of the level of the status sent and of the status the error would have been answered with: a late 4xx error after a `2xx` is `WARN`, after a `5xx` it is `ERROR`.

#### Scenario: Failures while the access log is suppressed
- **Given** a server configured with `<config>`
- **When** a client sends `<request>`
- **Then** an `http.request.failed` record is `<written>`

##### Examples:
| config | request | written |
|--------|---------|---------|
| `AccessLogConfig{SkipPaths: ["/healthz"]}` | `GET /healthz` returning `prblm.ErrNotFound` | written |
| `WithAccessLogDisabled()` | `GET /ping` returning `prblm.ErrNotFound` | written |
| `WithAccessLogDisabled()` | `GET /nope` (no route) | written |
| `WithAccessLogDisabled()` | `GET /late` writing `200` and then panicking | written, at `ERROR` |
| `WithAccessLogDisabled()` | `GET /late` writing `200` and then returning `errors.New("late")` | written, at `ERROR` |

### Requirement: Sensitive data is never logged by default
Access and error records SHALL NOT contain request or response bodies, `Authorization`, `Cookie` or `Set-Cookie` values, query strings, the raw URL path or the client address, at any log level, unless the matching named opt-in is enabled.

#### Scenario: Redaction at every level
- **Given** a server at level `<level>` with `<config>`
- **When** a client sends `GET /users/alice@example.com?token=s3cr3t` to route `GET /users/{id}` with `Authorization: Bearer abc.def` and `Cookie: sid=xyz`, and the handler returns `errors.New("boom")`
- **Then** no log line contains `<forbidden>`
- **And** the access record contains `<allowed>`

##### Examples: defaults
| level | config | forbidden | allowed |
|-------|--------|-----------|---------|
| `INFO` | (none) | `s3cr3t` | `http.route` |
| `DEBUG` | (none) | `abc.def` | `http.route` |
| `DEBUG` | (none) | `sid=xyz` | `http.route` |
| `DEBUG` | (none) | `alice@example.com` | `http.route` |

##### Examples: opt-ins
| level | config | forbidden | allowed |
|-------|--------|-----------|---------|
| `INFO` | `AccessLogConfig{IncludePath: true}` | `s3cr3t` | `url.path` `/users/alice@example.com` |
| `INFO` | `AccessLogConfig{IncludeQuery: true}` | `abc.def` | `url.query` `token=s3cr3t` |
| `INFO` | `AccessLogConfig{IncludeClientAddress: true}` | `sid=xyz` | `client.address` |
| `INFO` | `WithAccessLogIncludePath()` | `s3cr3t` | `url.path` `/users/alice@example.com` |

### Requirement: Hostile values cannot corrupt the log stream
Logged values SHALL be JSON-escaped so control characters cannot start a new line. srvr's handler SHALL cut every string attribute (after `LogValuer` resolution), every `error` value's text and the record `msg` to `MaxAttrBytes` (default 1024) plus `…[truncated]`, never mid-character, including `With` attributes and groups, and SHALL add top-level `srvr.attr.truncated` `true`. Only srvr's own `exception.*` attributes have larger limits.

#### Scenario: Hostile user agent
- **Given** a request with `User-Agent` `<agent>`
- **When** the access record is written
- **Then** the record is a single line and `user_agent.original` is `<logged>`

##### Examples:
| agent | logged |
|-------|--------|
| `curl/8.0` | `curl/8.0` |
| `a\nlevel=ERROR msg=forged` | `a\nlevel=ERROR msg=forged` as an escaped JSON string |
| 1024 bytes of `x` | 1024 bytes of `x` |
| 1025 bytes of `x` | 1024 bytes of `x` followed by `…[truncated]` |
| 1023 bytes of `x` followed by `é` | 1023 bytes of `x` followed by `…[truncated]` |
| 5000 bytes of `x` | 1024 bytes of `x` followed by `…[truncated]` |
| (none) | (none) |

#### Scenario: Truncating any string attribute
- **Given** a server configured with `<config>`
- **When** a handler logs `INFO` through the request logger with `<attribute>` set to `<value>`
- **Then** the logged value is `<logged>`
- **And** `srvr.attr.truncated` is `<flag>`

##### Examples:
| config | attribute | value | logged | flag |
|--------|-----------|-------|--------|------|
| (none) | `note` | 1024 bytes of `x` | 1024 bytes of `x` | (none) |
| (none) | `note` | 1025 bytes of `x` | 1024 bytes of `x` followed by `…[truncated]` | `true` |
| (none) | `note` | 1023 bytes of `x` followed by `é` | 1023 bytes of `x` followed by `…[truncated]` | `true` |
| (none) | `note` inside `WithGroup("db")` | 2000 bytes of `x` | `db.note` of 1024 bytes of `x` followed by `…[truncated]` | `true` (top-level) |
| (none) | `note` added with `l.With("note", ...)` | 2000 bytes of `x` | 1024 bytes of `x` followed by `…[truncated]` | `true` |
| (none) | `n` | the integer `5` | `5` | (none) |
| (none) | `err` via `slog.Any("err", e)` | an error whose message is `boom` | `boom` | (none) |
| (none) | `err` via `slog.Any("err", e)` | an error whose message is 2000 bytes of `x` | the string of 1024 bytes of `x` followed by `…[truncated]` | `true` |
| (none) | `v` | a `slog.LogValuer` resolving to 2000 bytes of `x` | 1024 bytes of `x` followed by `…[truncated]` | `true` |
| (none) | the record message (`msg`) | 2000 bytes of `x` | 1024 bytes of `x` followed by `…[truncated]` | `true` |
| (none) | `exception.message` (a consumer attribute) | 2000 bytes of `x` | 1024 bytes of `x` followed by `…[truncated]` | `true` |
| `WithLogMaxAttrBytes(16)` | `note` | 20 bytes of `x` | 16 bytes of `x` followed by `…[truncated]` | `true` |
| `LogConfig{MaxAttrBytes: 16}` | `note` | 20 bytes of `x` | 16 bytes of `x` followed by `…[truncated]` | `true` |
| `LogConfig{MaxAttrBytes: 0}` | `note` | 1025 bytes of `x` | 1024 bytes of `x` followed by `…[truncated]` | `true` |
| `LogConfig{MaxAttrBytes: -1}` | `note` | 5000 bytes of `x` | 5000 bytes of `x` | (none) |
| `WithLogHandler(h)` | `note` | 1025 bytes of `x` | `h` receives 1024 bytes of `x` followed by `…[truncated]` | `true` |

#### Scenario: Error attributes keep their own limits
- **Given** a server with no logging configuration
- **When** a handler returns an error whose message is `<message>`
- **Then** the failed record's `exception.message` is `<logged>`

##### Examples:
| message | logged |
|---------|--------|
| 5000 bytes of `x` | 5000 bytes of `x` |
| 9000 bytes of `x` | 8192 bytes of `x` followed by `…[truncated]` |

### Requirement: Logging an error value never fails the request
Reading the text of an `error` value logged through srvr's handler SHALL never fail the request: a typed nil SHALL be logged as `<nil T>` and an `Error()` that panics as `<panic in Error(): T>`, `T` being the error's Go type.

#### Scenario: Hostile logged errors
- **Given** a server with no logging configuration
- **When** a handler logs `INFO` through the request logger with `<attribute>` set to `<value>` and writes `200`
- **Then** the response status is `200`
- **And** the logged value is `<logged>`

##### Examples:
| attribute | value | logged |
|-----------|-------|--------|
| `err` via `slog.Any("err", e)` | a typed-nil `*MyErr` whose `Error()` dereferences it | `<nil *MyErr>`, the type with its package |
| `err` via `slog.Any("err", e)` | an error whose `Error()` panics | `<panic in Error(): T>`, `T` its Go type |
| `err` added with `l.With(slog.Any("err", e))` | a typed-nil `*MyErr` whose `Error()` dereferences it | `<nil *MyErr>`, the type with its package |

#### Scenario: Text reaches a consumer handler as strings when the limit is disabled
- **Given** a server configured with `WithLogMaxAttrBytes(-1)` and `WithLogHandler(h)`, where `h` records each attribute's `slog.Kind`
- **When** `<event>`
- **Then** `h` receives `<attribute>` as a `slog.KindString` value `<value>`

##### Examples:
| event | attribute | value |
|-------|-----------|-------|
| an `AfterDrain` hook returns `errors.New("flush failed")` and `Shutdown` is called before `Start` | `exception.message` of `server.shutdown_hook.failed` | `flush failed` |
| a handler returns `errors.New("boom")` | `exception.message` of `http.request.failed` | `boom` |
| a handler panics with `"boom"` | `exception.stacktrace` of `http.request.failed` | the panic stack |
| a handler logs `INFO` through the request logger with `slog.Any("err", e)`, `e` a typed-nil `*MyErr` | `err` | `<nil *MyErr>` |
| a handler logs `INFO` through the request logger with `slog.Any("err", e)`, `e`'s `Error()` panicking with a value whose `Error()` panics | `err` | `<panic in Error(): T>`, `T` its Go type |

### Requirement: The log schema is versioned
Every record srvr writes itself (lifecycle, access and failed records, warnings) SHALL carry `srvr.log.schema` `1`; records written by consumer code SHALL NOT. Adding a key SHALL keep the version; renaming or removing a key, or changing a key's meaning or value type, SHALL increase it.

#### Scenario: Schema version attribute
- **Given** a server with no logging configuration
- **When** `<record>` is written
- **Then** its `srvr.log.schema` is `<version>`

##### Examples:
| record | version |
|--------|---------|
| `server.started` | `1` |
| `http.request.completed` | `1` |
| `http.request.failed` | `1` |
| `server.stopped` | `1` |
| a handler's own record through `srvr.LoggerFromContext` | (none) |

### Requirement: Log write failures never fail requests
When the log output fails or blocks briefly, the response SHALL be unaffected; write errors SHALL be counted and reported through `srvr.LogWriteErrors()` instead of being logged. The counter is process-wide: it is shared by every server in the process and never reset.

#### Scenario: Broken output
- **Given** a server whose log output `<fault>`
- **When** a client sends `GET /ping`
- **Then** the response status is `200`
- **And** `srvr.LogWriteErrors()` increases by `<count>`

##### Examples:
| fault | count |
|-------|-------|
| returns `syscall.EPIPE` on every write | `1` |
| returns a short write | `1` |
| works | `0` |

### Requirement: Service and build information is attached
Lifecycle and error records SHALL carry `service.name`, `service.version` and `deployment.environment.name` from `WithServiceConfig(ServiceConfig)` or `WithServiceName`, `WithServiceVersion` and `WithServiceEnvironment`, defaulting `service.name` to `unknown_service:<executable>` and `service.version` and `vcs.revision` to the binary's build information when present.

#### Scenario: Service attributes
- **Given** a server configured with `<config>`
- **When** the server starts
- **Then** the `server.started` record has `service.name` `<name>` and `service.version` `<version>`

##### Examples:
| config | name | version |
|--------|------|---------|
| `ServiceConfig{Name: "registration-api", Version: "1.4.2"}` | `registration-api` | `1.4.2` |
| (none), binary `regapi` built from a tagged module `v1.4.2` | `unknown_service:regapi` | `v1.4.2` |
| (none), built with `go run` | `unknown_service:<executable>` | `(devel)` |
| `WithServiceConfig(ServiceConfig{Name: "registration-api", Version: "1.4.2"})` | `registration-api` | `1.4.2` |
| `WithServiceConfig(ServiceConfig{Name: "registration-api"})` then `WithServiceName("orders")` | `orders` | the build version |
| `WithServiceName("orders")` then `WithServiceConfig(ServiceConfig{})` | `unknown_service:<executable>` | the build version |

### Requirement: Values of any kind are capped
An attribute holding a value of any other kind (a map, slice, struct, `[]byte` or `fmt.Stringer` passed with `slog.Any`, directly, in a group or through `With`) SHALL be written unchanged when its formatted text fits `MaxAttrBytes`, and otherwise SHALL be replaced by that text cut like a string, setting top-level `srvr.attr.truncated` `true`. A value whose formatting panics SHALL be replaced by `<panic formatting value>`.

#### Scenario: Capping slog.Any values
- **Given** a server configured with `<config>`
- **When** a handler logs `INFO` through the request logger with `<attribute>` holding `<value>`
- **Then** the logged value is `<logged>`
- **And** `srvr.attr.truncated` is `<flag>`

##### Examples: within the cap
| config | attribute | value | logged | flag |
|--------|-----------|-------|--------|------|
| (none) | `slog.Any("hdr", h)` | an `http.Header` `{"X-A": ["1"]}` | the JSON object `{"X-A":["1"]}` | (none) |
| (none) | `slog.Any("ids", s)` | the `[]int` `[1, 2, 3]` | the JSON array `[1,2,3]` | (none) |
| (none) | `slog.Any("p", v)` | a struct whose formatted text is exactly 1024 bytes | the value as the handler renders it | (none) |

##### Examples: over the cap
| config | attribute | value | logged | flag |
|--------|-----------|-------|--------|------|
| (none) | `slog.Any("hdr", h)` | an `http.Header` with one 5000-byte value | the first 1024 bytes of its formatted text followed by `…[truncated]` | `true` |
| (none) | `slog.Any("list", s)` | a `[]string` of 50 strings of 100 bytes | the first 1024 bytes of its formatted text followed by `…[truncated]` | `true` |
| (none) | `slog.Any("p", v)` inside `WithGroup("db")` | a struct whose formatted text is 1025 bytes | `db.p` of the first 1024 bytes followed by `…[truncated]` | `true` (top-level) |
| (none) | `slog.Any("p", v)` added with `l.With(...)` | a struct whose formatted text is 2000 bytes | the first 1024 bytes followed by `…[truncated]` | `true` |
| `LogConfig{MaxAttrBytes: 16}` | `slog.Any("ids", s)` | a `[]int` whose formatted text is 20 bytes | the first 16 bytes followed by `…[truncated]` | `true` |
| `LogConfig{MaxAttrBytes: -1}` | `slog.Any("hdr", h)` | an `http.Header` with one 5000-byte value | the value as the handler renders it | (none) |

##### Examples: faulty values
| config | attribute | value | logged | flag |
|--------|-----------|-------|--------|------|
| (none) | `slog.Any("v", s)` | a `fmt.Stringer` whose `String` panics | `<panic formatting value>` | (none) |
| (none) | `slog.Any("v", p)` | a typed-nil pointer to a struct | the value as the handler renders it | (none) |
| (none) | `slog.Any("v", nil)` | `nil` | the value as the handler renders it | (none) |

### Requirement: Access log configuration is copied at construction
The skip paths a consumer passes, through `WithAccessLogConfig` or `WithAccessLogSkipPaths`, SHALL be copied when `New` applies the options, so changing the caller's slice afterwards never changes which records are written and never races with requests.

#### Scenario: Caller mutates skip paths after New
- **Given** a server built with `<option>` holding the slice `s` with `/healthz`
- **When** the caller sets `s[0]` to `/other` after `New` and a client sends `GET /healthz` answered `200`
- **Then** the `http.request.completed` record for `/healthz` is `<written>`

##### Examples:
| option | written |
|--------|---------|
| `WithAccessLogConfig(AccessLogConfig{SkipPaths: s})` | not written |
| `WithAccessLogSkipPaths(s...)` | not written |
| `WithAccessLogConfig(AccessLogConfig{SkipPaths: s})`, with the mutation running concurrently with 100 requests | not written for any, and `go test -race` reports no race |
