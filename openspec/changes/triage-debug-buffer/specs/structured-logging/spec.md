# Spec Delta

## MODIFIED Requirements

### Requirement: Consumers may supply their own handler or logger
The consumer SHALL be able to supply a `slog.Handler` or `*slog.Logger`; srvr SHALL still add trace correlation to it. Supplying one together with level, format, source or output settings SHALL fail construction. A consumer handler whose `Enabled`, `WithAttrs`, `WithGroup` or `Handle` fails (returns an error or panics) SHALL never affect the response: srvr counts the failure in `srvr.LogRecordsLost()` and skips that record.

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
| `WithLogHandler(h)` where `h.Enabled` panics for request records | the response is unaffected, status is `200`, the record is skipped and `srvr.LogRecordsLost()` increases |
| `WithLogHandler(h)` where `h.WithAttrs` panics | the response is unaffected, status is `200`, the record is skipped and `srvr.LogRecordsLost()` increases |
| `WithLogHandler(h)` where `h.WithGroup` panics and the handler logs through `srvr.LoggerFromContext(ctx).WithGroup("db")` | the response is unaffected, status is `200`, the record is skipped and `srvr.LogRecordsLost()` increases |

### Requirement: Failed-request records are never suppressed
An `http.request.failed` record SHALL be written whatever the access log configuration, except that an opt-in unmatched-record limit may suppress the record of a request no route matched (see "Unmatched failed records can be rate limited"); records of handler errors, panics and `5xx` responses are never suppressed. A recovered panic SHALL be logged at `ERROR`, and an error that arrived after the response started at the higher of the level of the status sent and of the status the error would have been answered with: a late 4xx error after a `2xx` is `WARN`, after a `5xx` it is `ERROR`.

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
| `WithAccessLogDisabled()` and `WithUnmatchedRecordLimit(1, time.Minute)` | `GET /a` returning `errors.New("boom")`, ten times within a minute | written, ten times |
| `WithUnmatchedRecordLimit(1, time.Minute)` | `GET /a` panicking, ten times within a minute | written, ten times |

### Requirement: Log write failures never fail requests
When the log output fails or blocks briefly, the response SHALL be unaffected; write errors SHALL be counted and reported through `srvr.LogRecordsLost()` instead of being logged. The counter is process-wide: it is shared by every server in the process and never reset.

#### Scenario: Broken output
- **Given** a server whose log output `<fault>`
- **When** a client sends `GET /ping`
- **Then** the response status is `200`
- **And** `srvr.LogRecordsLost()` increases by `<count>`

##### Examples:
| fault | count |
|-------|-------|
| returns `syscall.EPIPE` on every write | `1` |
| returns a short write | `1` |
| works | `0` |

## ADDED Requirements

### Requirement: Unmatched failed records can be rate limited
When `AccessLogConfig.UnmatchedLimit` sets `Burst` above zero, the server SHALL write at most `Burst` `http.request.failed` records of requests no route matched per `Interval`, count the rest, and add the count as `srvr.unmatched.suppressed` to the next such record written and to `server.stopped`. The limit is per server and off by default. A negative `Burst`, or a positive `Burst` with an `Interval` not above zero, SHALL fail construction.

#### Scenario: Limiting unmatched failed records
- **Given** a server configured with `<config>`
- **When** `<requests>`
- **Then** `<written>`

##### Examples: within and over the limit
| config | requests | written |
|--------|----------|---------|
| (none) | 100 `GET /nope-<n>` within one second | 100 `http.request.failed` records, none with `srvr.unmatched.suppressed` |
| `WithUnmatchedRecordLimit(10, time.Second)` | exactly 10 `GET /nope-<n>` within one second | 10 records, none with `srvr.unmatched.suppressed` |
| `WithUnmatchedRecordLimit(10, time.Second)` | 11 `GET /nope-<n>` within one second | 10 records |
| `WithUnmatchedRecordLimit(10, time.Second)` | 15 `GET /nope-<n>` within one second, then `GET /nope-x` two seconds later | 11 records; the last has `srvr.unmatched.suppressed` `5` |
| `WithUnmatchedRecordLimit(10, time.Second)` | 15 `DELETE /users/42` with only `GET /users/{id}` registered, within one second | 10 records with status `405` |
| `WithUnmatchedRecordLimit(10, time.Second)` | 1000 `GET /nope-<n>` from 50 concurrent clients within one second | exactly 10 records; with one more request two seconds later, its record has `srvr.unmatched.suppressed` `990` |
| `WithUnmatchedRecordLimit(1, time.Minute)` | 3 `GET /nope-<n>`, then `Shutdown` | 1 record; `server.stopped` has `srvr.unmatched.suppressed` `2` |
| `WithUnmatchedRecordLimit(1, time.Minute)` | 1 `GET /nope`, then `Shutdown` | 1 record; `server.stopped` has no `srvr.unmatched.suppressed` |
| `AccessLogConfig{UnmatchedLimit: UnmatchedLimitConfig{Burst: 1, Interval: time.Minute}}` | 3 `GET /nope-<n>` | 1 record |
| `WithUnmatchedRecordLimit(1, time.Minute)` | 3 `GET /nope-<n>`, the server observed by `otelsrvr` | 1 record; `http.server.request.duration` counts 3 requests |

##### Examples: rejected configuration
| config | written |
|--------|---------|
| `WithUnmatchedRecordLimit(-1, time.Second)` | construction error wrapping `srvr.ErrConfig` naming `UnmatchedLimit.Burst` |
| `WithUnmatchedRecordLimit(10, 0)` | construction error wrapping `srvr.ErrConfig` naming `UnmatchedLimit.Interval` |
| `WithUnmatchedRecordLimit(10, -time.Second)` | construction error wrapping `srvr.ErrConfig` naming `UnmatchedLimit.Interval` |
| `WithUnmatchedRecordLimit(0, 0)` | a server without a limit |

### Requirement: The user agent can be omitted from access records
When `AccessLogConfig.OmitUserAgent` is true, access records SHALL NOT carry `user_agent.original`; by default they carry it.

#### Scenario: Omitting the user agent
- **Given** a server configured with `<config>`
- **When** a client sends `GET /ping` with `User-Agent: probe/1.0` and the handler `<behaviour>`
- **Then** the access record `<user agent>`

##### Examples:
| config | behaviour | user agent |
|--------|-----------|------------|
| (none) | writes `200` | has `user_agent.original` `probe/1.0` |
| `WithoutAccessLogUserAgent()` | writes `200` | has no `user_agent.original` |
| `WithoutAccessLogUserAgent()` | returns `errors.New("boom")` | is `http.request.failed` and has no `user_agent.original` |
| `AccessLogConfig{OmitUserAgent: true}` | writes `200` | has no `user_agent.original` |
| `WithoutAccessLogUserAgent()` then `WithAccessLogConfig(AccessLogConfig{})` | writes `200` | has `user_agent.original` `probe/1.0` |
