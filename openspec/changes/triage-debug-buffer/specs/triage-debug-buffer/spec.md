# Spec Delta

## Purpose

Keeps a failed request's below-level log records in a bounded per-request buffer and writes them just before its failure record, so triage sees what the request did before it failed without logging every request at `DEBUG`.

## ADDED Requirements

### Requirement: Buffering is off by default
With no debug-buffer configuration, records below the effective log level SHALL be dropped exactly as without this capability, on success and on failure, and no per-request buffer SHALL be allocated.

#### Scenario: Default drops below-level records
- **Given** a server at log level `INFO` with `<config>` and a handler that logs `DEBUG` `loading user` and then `<outcome>`
- **When** a client sends `GET /users/42`
- **Then** the log output `<contains>` a record with `msg` `loading user`

##### Examples:
| config | outcome | contains |
|--------|---------|----------|
| (none) | returns `errors.New("boom")` | does not contain |
| (none) | writes `200` | does not contain |
| `DebugBufferConfig{Enabled: false}` | panics with `"boom"` | does not contain |

### Requirement: Buffered records are flushed before the failure record
With buffering enabled, a below-level record at or above the buffer level, logged with the request context, SHALL be buffered; when the request fails at or above the flush threshold, buffered records SHALL be written in logging order just before the request's access record.

#### Scenario: Flush on failure
- **Given** buffering enabled at log level `INFO` and a handler that logs `DEBUG` `step one`, then `DEBUG` `step two`, then `<outcome>`
- **When** a client sends `POST /registrations`
- **Then** the log output, in order, is `<sequence>`

##### Examples: flushed
| outcome | sequence |
|---------|----------|
| returns `errors.New("db down")` | `step one`, `step two`, `http.request.failed` |
| logs `INFO` `checkpoint` and returns `errors.New("db down")` | `checkpoint`, `step one`, `step two`, `http.request.failed` |
| panics with `"boom"` | `step one`, `step two`, `http.request.failed` |
| returns `prblm.WithStatus(errors.New("x"), 503)` | `step one`, `step two`, `http.request.failed` |
| writes `200` then returns `errors.New("late")` | `step one`, `step two`, `http.request.failed` |
| writes `500` directly and returns `nil` | `step one`, `step two`, `http.request.completed` |

##### Examples: discarded
| outcome | sequence |
|---------|----------|
| writes `200` | `http.request.completed` |
| returns `prblm.ErrNotFound` | `http.request.failed` |
| writes `201` then returns `prblm.ErrConflict` | `http.request.failed` |

#### Scenario: Marking of flushed records
- **Given** buffering enabled and a handler that logs `DEBUG` `step one` at time `<logged_at>` with attribute `user.id` `42`, then returns `errors.New("boom")` at time `<failed_at>`
- **When** the buffer is flushed
- **Then** the `step one` record has `time` `<logged_at>`, `level` `DEBUG`, `srvr.buffered` `true`, `user.id` `42` and the request's `trace_id`
- **And** the `http.request.failed` record has `time` `<failed_at>` and no `srvr.buffered` key

##### Examples:
| logged_at | failed_at |
|-----------|-----------|
| `2026-11-17T09:41:07.100Z` | `2026-11-17T09:41:07.120Z` |

### Requirement: Flushing requires a failing status at or above the threshold
A request SHALL flush its buffer when its failure status is at or above the flush threshold (default `500`) or a panic was recovered, unless its access record is suppressed or its level is not enabled, in which case the buffer SHALL be discarded. The failure status is the status resolved for a returned error, even when the response was already committed with another status; without a returned error it is the written status.

#### Scenario: Flush threshold
- **Given** buffering enabled with flush threshold `<threshold>` and a handler that logs `DEBUG` `ctx` and ends with `<status>`
- **When** the request completes
- **Then** the `ctx` record is `<written>`

##### Examples: default threshold
| threshold | status | written |
|-----------|--------|---------|
| (default) | `200` | not written |
| (default) | `499` | not written |
| (default) | `500` | written |
| (default) | `599` | written |

##### Examples: lowered threshold
| threshold | status | written |
|-----------|--------|---------|
| `400` | `399` | not written |
| `400` | `400` | written |
| `400` | `404` | written |

##### Examples: rejected configuration
| threshold | status | written |
|-----------|--------|---------|
| `99` | (none) | construction error wrapping `srvr.ErrConfig` naming `FlushStatus` |
| `600` | (none) | construction error wrapping `srvr.ErrConfig` naming `FlushStatus` |

#### Scenario: Suppressed access record
- **Given** buffering enabled with flush threshold `<threshold>`, access logging configured with `<access>`, and a handler for `GET /healthz` that logs `DEBUG` `ctx` and writes `<status>` itself
- **When** the request completes
- **Then** the `ctx` record is `<written>` and the access record is `<record>`

##### Examples:
| threshold | access | status | written | record |
|-----------|--------|--------|---------|--------|
| `400` | `AccessLogConfig{SkipPaths: []string{"/healthz"}}` | `404` | not written | not written |
| `400` | `AccessLogConfig{Disabled: true}` | `404` | not written | not written |
| `400` | (none), with log level `ERROR` | `404` | not written | not written |
| `400` | (none) | `404` | written | `http.request.completed` |
| (default) | `AccessLogConfig{SkipPaths: []string{"/healthz"}}` | `500` | written | `http.request.completed` |

### Requirement: Records at or above the log level are never duplicated
Records at or above the effective log level SHALL be written immediately and SHALL NOT be buffered; records below the buffer level SHALL be dropped. Each record SHALL appear in the output at most once.

#### Scenario: Level routing
- **Given** buffering enabled with log level `<log_level>` and buffer level `<buffer_level>`, and a handler that logs at `<record_level>` then returns `errors.New("boom")`
- **When** the request fails
- **Then** the record is written `<times>` with `srvr.buffered` `<buffered>`

##### Examples:
| log_level | buffer_level | record_level | times | buffered |
|-----------|--------------|--------------|-------|----------|
| `INFO` | `DEBUG` | `DEBUG` | once | `true` |
| `INFO` | `DEBUG` | `INFO` | once | (none) |
| `INFO` | `DEBUG` | `ERROR` | once | (none) |
| `INFO` | `DEBUG` | `DEBUG-4` | never | (none) |
| `WARN` | `DEBUG` | `INFO` | once | `true` |
| `DEBUG` | `DEBUG` | `DEBUG` | once | (none) |

#### Scenario: Runtime level change during a request
- **Given** buffering enabled, a `*slog.LevelVar` at `INFO`, and a handler that logs `DEBUG` `before`, waits while the level is set to `<new_level>`, logs `DEBUG` `after`, then returns `errors.New("boom")`
- **When** the request fails
- **Then** `before` is written once with `srvr.buffered` `true`
- **And** `after` is written once with `srvr.buffered` `<after_buffered>`

##### Examples:
| new_level | after_buffered |
|-----------|----------------|
| `DEBUG` | (none) |
| `WARN` | `true` |

### Requirement: Buffers are bounded per request and process-wide
A request's buffer SHALL hold at most the configured records (default 100) and bytes (default 64 KiB), evicting the oldest and counting evictions. All buffers together SHALL use at most the configured budget (default 16 MiB); records that do not fit SHALL be dropped and counted.

#### Scenario: Per-request limits
- **Given** buffering enabled with `<limits>` and a handler that logs `<logged>` `DEBUG` records of about 100 bytes each, numbered from 1, then returns `errors.New("boom")`
- **When** the request fails
- **Then** the flushed records are numbers `<flushed>`
- **And** the failed record has `srvr.debug_buffer.flushed` `<count>` and `srvr.debug_buffer.dropped` `<dropped>`

##### Examples: within limits
| limits | logged | flushed | count | dropped |
|--------|--------|---------|-------|---------|
| (default) | `0` | (none) | `0` | `0` |
| (default) | `1` | `1` | `1` | `0` |
| (default) | `100` | `1`-`100` | `100` | `0` |

##### Examples: over limits
| limits | logged | flushed | count | dropped |
|--------|--------|---------|-------|---------|
| (default) | `101` | `2`-`101` | `100` | `1` |
| `MaxRecords: 10` | `25` | `16`-`25` | `10` | `15` |
| `MaxBytes: 1024` | `50` | the newest records fitting in 1024 bytes | at most `10` | at least `40` |
| `MaxBytes: 50` | `1` | (none) | `0` | `1` |
| `MaxBytes: 2048` | `1` with a 5000-byte attribute, truncated to 1024 bytes when buffered | `1` | `1` | `0` |
| `MaxBytes: 2048` | `1` with `slog.Any("err", err)` whose `Error()` is 5000 bytes, truncated to 1024 bytes when buffered | `1` | `1` | `0` |

#### Scenario: Process-wide budget
- **Given** buffering enabled with `TotalBytes` `<budget>` and `<requests>` concurrent requests that each buffer `<per_request>` bytes, all holding their buffers at the same time, and then fail
- **When** all requests complete
- **Then** buffered memory never exceeds `<budget>`
- **And** every failed record has `srvr.debug_buffer.truncated` `<truncated>`

##### Examples:
| budget | requests | per_request | truncated |
|--------|----------|-------------|-----------|
| `16777216` | `10` | `65536` | `false` |
| `65536` | `4` | `32768` | `true` for at least two requests |

#### Scenario: Invalid limits
- **Given** a debug-buffer configuration with `<field>` set to `<value>`
- **When** the server is constructed
- **Then** construction returns `<result>`

##### Examples:
| field | value | result |
|-------|-------|--------|
| `MaxRecords` | `0` | a server with the default `100` |
| `MaxRecords` | `-1` | an error wrapping `srvr.ErrConfig` naming `MaxRecords` |
| `MaxBytes` | `-1` | an error wrapping `srvr.ErrConfig` naming `MaxBytes` |
| `TotalBytes` | `1024` with `MaxBytes` `65536` | an error wrapping `srvr.ErrConfig` naming `TotalBytes` |
| `MaxBytes` | `0` | a server with the default `65536` |
| `TotalBytes` | `0` | a server with the default `16777216` |
| `TotalBytes` | `65536` with `MaxBytes` `65536` | a server |
| `Level` | `"loud"` | an error wrapping `srvr.ErrConfig` naming `Level` |
| `Level` | `"warn"` with log level `INFO` | an error wrapping `srvr.ErrConfig` naming `Level` |
| `Level` | `"info"` with log level `INFO` | a server that buffers nothing |
| `Level` | `"warn"` with `WithLogHandler(h)` | a server |
| `option` | `WithDebugBuffer(nil)` | an error wrapping `srvr.ErrConfig` |
| `MaxRecords` | `-1` via `WithDebugBuffer(srvr.MaxRecords(-1))` | an error wrapping `srvr.ErrConfig` naming `MaxRecords` |
| `Level` | `slog.LevelWarn` via `WithDebugBuffer(srvr.BufferLevel(slog.LevelWarn))` with log level `INFO` | an error wrapping `srvr.ErrConfig` naming `Level` |

### Requirement: The failed record summarises the buffer
When buffering is enabled, every access record whose status is at or above the flush threshold, or that records a recovered panic, SHALL carry `srvr.debug_buffer.flushed`, `srvr.debug_buffer.dropped` and `srvr.debug_buffer.truncated`, even when nothing was buffered.

#### Scenario: Summary attributes
- **Given** buffering `<enabled>` and a handler that logs `<logged>` `DEBUG` records and returns `errors.New("boom")`
- **When** the request fails
- **Then** the failed record's summary is `<summary>`

##### Examples:
| enabled | logged | summary |
|---------|--------|---------|
| enabled | `3` | `flushed` `3`, `dropped` `0`, `truncated` `false` |
| enabled | `0` | `flushed` `0`, `dropped` `0`, `truncated` `false` |
| disabled | `3` | no `srvr.debug_buffer.*` keys |

### Requirement: Buffering is configurable programmatically and by config struct
The consumer SHALL be able to enable buffering and set the buffer level, flush threshold and limits through `WithDebugBuffer` options, `WithDebugBufferConfig` or `LogConfig.DebugBuffer`; empty or zero values SHALL mean the default. Options SHALL apply in argument order, the later one winning; `WithLogConfig` sets every `LogConfig` field, including `DebugBuffer`.

#### Scenario: Configuration sources
- **Given** a server configured with `<config>`
- **When** a handler logs `DEBUG` `ctx` and returns `prblm.ErrNotFound`
- **Then** the `ctx` record is `<written>`

##### Examples:
| config | written |
|--------|---------|
| `WithDebugBuffer()` | not written |
| `WithDebugBuffer(srvr.FlushAtStatus(400))` | written |
| JSON `{"debug_buffer":{"enabled":true,"flush_status":400}}` decoded into `LogConfig` | written |
| JSON `{"debug_buffer":{"enabled":true}}` decoded into `LogConfig` | not written |
| JSON `{"debug_buffer":{"flush_status":400}}` decoded into `LogConfig` | not written |
| `WithDebugBufferConfig(DebugBufferConfig{Enabled: true, FlushStatus: 400})` | written |
| `WithDebugBufferConfig(DebugBufferConfig{FlushStatus: 400})` | not written |
| `WithDebugBufferConfig(DebugBufferConfig{Enabled: true, FlushStatus: 400})`, then `WithDebugBuffer(srvr.FlushAtStatus(500))` | not written |
| `WithDebugBuffer(srvr.FlushAtStatus(500))`, then `WithDebugBufferConfig(DebugBufferConfig{Enabled: true, FlushStatus: 400})` | written |
| `WithDebugBuffer(srvr.BufferLevel(slog.LevelInfo), srvr.FlushAtStatus(400))` | not written |
| `WithDebugBuffer(srvr.FlushAtStatus(400))`, then `WithLogConfig(LogConfig{Format: "json"})` | not written |
| `WithLogConfig(LogConfig{Format: "json"})`, then `WithDebugBuffer(srvr.FlushAtStatus(400))` | written |

### Requirement: Derived loggers and concurrent logging share the request buffer
Records logged with the request context, or a context derived from it, through any logger derived with `With` or `WithGroup`, or from goroutines running during the request, SHALL go to the same buffer, keep their attributes, groups and logging context when flushed, carry `srvr.buffered` at the top level, and be safe under concurrent use.

#### Scenario: Derived loggers
- **Given** buffering enabled and a handler that logs `DEBUG` `a` through `<logger>` and returns `errors.New("boom")`
- **When** the buffer is flushed
- **Then** the `a` record has `<attributes>` and a top-level `srvr.buffered` `true`

##### Examples:
| logger | attributes |
|--------|------------|
| `srvr.LoggerFromContext(ctx)` | `trace_id` |
| `srvr.LoggerFromContext(ctx).With("repo", "users")` | `repo` `users` and `trace_id` |
| `srvr.LoggerFromContext(ctx).WithGroup("db").With("table", "users")` | `db.table` `users` and a top-level `trace_id` (not `db.trace_id`) |
| `srvr.LoggerFromContext(ctx)` with `DebugContext(context.WithValue(ctx, tenantKey{}, "acme"), ...)` and a consumer handler that adds `tenant` from the context | `tenant` `acme` and `trace_id` |
| `slog.Default()` with `DebugContext(ctx, ...)` after `WithSetDefaultLogger()` | `trace_id` |

#### Scenario: Concurrent logging in one request
- **Given** buffering enabled and a handler that starts `<goroutines>` goroutines each logging `<each>` `DEBUG` records with the request context, waits for them, then returns `errors.New("boom")`
- **When** the request fails
- **Then** `srvr.debug_buffer.flushed` plus `srvr.debug_buffer.dropped` equals `<total>`
- **And** `go test -race` reports no race

##### Examples:
| goroutines | each | total |
|------------|------|-------|
| `8` | `10` | `80` |
| `50` | `10` | `500` |

### Requirement: Records after the request ends are not buffered
Once a request's outcome is decided, its buffer SHALL be sealed and released. Records logged later with that request's context SHALL follow the normal level rule (written if at or above the log level, otherwise dropped) and SHALL never be flushed.

#### Scenario: Late records
- **Given** buffering enabled and a handler that starts a goroutine which logs `<level>` `late` after the response, then the handler returns `<outcome>`
- **When** the goroutine logs after the request completes
- **Then** `late` is `<written>`

##### Examples:
| level | outcome | written |
|-------|---------|---------|
| `DEBUG` | `errors.New("boom")` | not written |
| `DEBUG` | `nil` with `200` | not written |
| `INFO` | `errors.New("boom")` | written once, without `srvr.buffered` |

### Requirement: Flushing is fault tolerant
A flushed record that fails to write, or whose handler panics, SHALL be counted in `srvr.LogWriteErrors()` and skipped; the remaining records and the `http.request.failed` record SHALL still be written, and the response SHALL be unaffected.

#### Scenario: Faulty handler during flush
- **Given** buffering enabled with a consumer `slog.Handler` that `<fault>` on the record `step two`, and a handler that logs `DEBUG` `step one`, `step two`, `step three` then returns `errors.New("boom")`
- **When** the request fails
- **Then** the response status is `500`
- **And** the written records are `<written>` and `srvr.LogWriteErrors()` increases by `<errors>`

##### Examples:
| fault | written | errors |
|-------|---------|--------|
| returns an error | `step one`, `step three`, `http.request.failed` | `1` |
| panics | `step one`, `step three`, `http.request.failed` | `1` |
| works | `step one`, `step two`, `step three`, `http.request.failed` | `0` |

### Requirement: Flushed records bypass the base handler's level check only
Flushed records SHALL be delivered to the base handler's `Handle` without consulting its `Enabled`, and SHALL pass through the same truncation of string attributes, error values and the message (applied when the record is buffered, with core-server's limits), redaction and `ReplaceAttr` as records written immediately.

#### Scenario: Redaction and truncation apply to flushed records
- **Given** buffering enabled and a handler that logs `DEBUG` with attribute `<attribute>` then returns `errors.New("boom")`
- **When** the buffer is flushed
- **Then** the flushed record's attribute value is `<logged>`

##### Examples:
| attribute | logged |
|-----------|--------|
| `note` = 5000 bytes of `x` | 1024 bytes of `x` followed by `…[truncated]`, with `srvr.attr.truncated` `true` |
| `note` = 5000 bytes of `x` with `LogConfig{MaxAttrBytes: 2048}` | 2048 bytes of `x` followed by `…[truncated]`, with `srvr.attr.truncated` `true` |
| `err` = `slog.Any("err", errors.New(<5000 bytes of x>))` | the string of 1024 bytes of `x` followed by `…[truncated]`, with `srvr.attr.truncated` `true` |
| the record `msg` = 5000 bytes of `x` | `msg` of 1024 bytes of `x` followed by `…[truncated]`, with `srvr.attr.truncated` `true` |
| `note` = `a\nlevel=ERROR` | `a\nlevel=ERROR` as an escaped JSON string on one line |
| `secret` with a consumer `ReplaceAttr` masking `secret` | `***` |

#### Scenario: Consumer handler with its own level filter
- **Given** buffering enabled with a consumer handler `h` whose `Enabled` returns false below `INFO` and whose `Handle` `<handle>`, and a handler that logs `DEBUG` `ctx` then returns `errors.New("boom")`
- **When** the request fails
- **Then** `h` receives the `ctx` record `<received>`

##### Examples:
| handle | received |
|--------|----------|
| writes every record it receives | once, with `srvr.buffered` `true` |
| drops records below `INFO` itself | once, and the record is not in its output |

### Requirement: Buffering behaves identically on every engine
For the same handler behaviour, net/http, gin and fiber v3 SHALL buffer, flush, mark and summarise records identically, and on fiber v3 the buffer SHALL belong to the request, never to the pooled `fiber.Ctx`.

#### Scenario: Engine parity
- **Given** buffering enabled on engine `<engine>` and a handler that logs `DEBUG` `ctx` and `<outcome>`
- **When** a client sends `GET /items/1`
- **Then** the `ctx` record is `<written>` and the access record is `<record>`

##### Examples:
| engine | outcome | written | record |
|--------|---------|---------|--------|
| net/http | returns `errors.New("boom")` | written | `http.request.failed` |
| gin | returns `errors.New("boom")` via `c.Error` | written | `http.request.failed` |
| fiber v3 | returns `errors.New("boom")` | written | `http.request.failed` |
| net/http | writes `200` | not written | `http.request.completed` |
| gin | writes `200` | not written | `http.request.completed` |
| fiber v3 | writes `200` | not written | `http.request.completed` |

#### Scenario: Fiber context reuse
- **Given** buffering enabled on fiber v3, request A that logs `DEBUG` `from A` and succeeds, then request B to the same app, served after A so it may reuse A's pooled `fiber.Ctx`, that logs `DEBUG` `from B` and returns `errors.New("boom")`
- **When** request B fails
- **Then** the flushed records are `<flushed>`

##### Examples:
| flushed |
|---------|
| `from B` only, with request B's `trace_id` |

### Requirement: Unusual request endings are handled deterministically
A hijacked connection or protocol upgrade SHALL discard its buffer unless a panic was recovered; a panic with `http.ErrAbortHandler` SHALL flush before core-server's aborted-request access record; a request ended by a server timeout SHALL flush when the written status is at or above the threshold; a streaming response SHALL flush only when it ends failed.

#### Scenario: Unusual endings
- **Given** buffering enabled on net/http and a handler that logs `DEBUG` `ctx` and then `<ending>`
- **When** the request ends
- **Then** the `ctx` record is `<written>`

##### Examples:
| ending | written |
|--------|---------|
| hijacks the connection and returns `nil` | not written |
| hijacks the connection and then panics | written |
| panics with `http.ErrAbortHandler` | written |
| is wrapped by `http.TimeoutHandler` with `10ms` and sleeps `100ms` | written |
| streams 3 chunks with `200` and then returns `errors.New("broken")` | written |
| streams 3 chunks with `200` and returns `nil` | not written |
| the client disconnects and the handler returns `context.Canceled` (answered `499`, error-responses / Scenario: Client closed request) | not written |

#### Scenario: Aborted request flush order
- **Given** buffering enabled on net/http and a handler that logs `DEBUG` `ctx` and then panics with `http.ErrAbortHandler`
- **When** the request ends
- **Then** the `<record>` record is written `<position>`

##### Examples:
| record | position |
|--------|----------|
| `ctx` | first, with `srvr.buffered` `true` |
| access | after `ctx`, with `msg` `http.request.failed`, level `WARN`, `error.kind` `panic`, `error.type` `panic`, `srvr.response.aborted` `true` and no `problem` |
