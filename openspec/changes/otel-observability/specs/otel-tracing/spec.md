# Spec Delta

## Purpose

Defines optional OpenTelemetry server spans in `otelsrvr`, W3C context propagation, and how srvr's log and problem trace correlation follows the active span when tracing is enabled.

## ADDED Requirements

### Requirement: Tracing is off by default
`otelsrvr.New()` SHALL NOT start spans unless tracing is enabled with `WithTracing()` or `WithConfig(otelsrvr.Config{Tracing: true})`; without it, srvr's own trace context from `core-server` SHALL stay in effect.

#### Scenario: Tracing switch
- **Given** `otelsrvr.New` with `<config>` and a tracer provider with an in-memory exporter set as `<provider>`
- **When** a client sends `GET /ping`
- **Then** the exporter holds `<spans>` spans

##### Examples:
| config | provider | spans |
|--------|----------|-------|
| (none) | `WithTracerProvider(tp)` | `0` |
| `WithTracing()` | `WithTracerProvider(tp)` | `1` |
| `WithConfig(otelsrvr.Config{Tracing: true})` | the global provider via `otel.SetTracerProvider(tp)` | `1` |
| `WithTracing()`, `WithTracerProvider(nil)` | (none) | construction error wrapping `otelsrvr.ErrConfig` |

### Requirement: Server spans follow semantic conventions
Each traced request SHALL produce one span of kind `SERVER` named `<method> <route>` (or `<method>` when unmatched), with the same bounded attributes as the metrics, status `Error` only for 5xx and recovered panics, and a recorded exception event for panics. `url.path` and `http.request.method_original` SHALL be added only when core `AccessLogConfig.IncludePath` is true.

#### Scenario: Span shape per engine
- **Given** a `<engine>` server with tracing enabled and route `GET /users/{id}` in that engine's syntax
- **When** a client sends `<request>` and the handler `<behaviour>`
- **Then** the span is named `<name>` with status `<status>`

##### Examples:
| engine | request | behaviour | name | status |
|--------|---------|-----------|------|--------|
| net/http | `GET /users/42` | writes `200` | `GET /users/{id}` | `Unset` |
| gin | `GET /users/42` | writes `200` | `GET /users/:id` | `Unset` |
| fiber | `GET /users/42` | writes `200` | `GET /users/:id` | `Unset` |
| fiber | `GET /users/42` | returns `errors.New("boom")` | `GET /users/:id` | `Error` |
| fiber | `GET /nope` | (not called) | `GET` | `Unset` |
| net/http | `GET /users/42` | returns `prblm.ErrNotFound` | `GET /users/{id}` | `Unset` |
| net/http | `GET /users/42` | returns `errors.New("boom")` | `GET /users/{id}` | `Error` |
| net/http | `GET /users/42` | panics with `"boom"` | `GET /users/{id}` | `Error` with an `exception` event |
| net/http | `GET /users/42` | panics with `http.ErrAbortHandler` | `GET /users/{id}` | `Error` with an `exception` event and no `http.response.status_code` |
| net/http | `GET /nope` | (not called) | `GET` | `Unset` |
| net/http | `XYZZY /users/42` | (not called) | `HTTP` | `Unset` |

#### Scenario: Span path attributes
- **Given** tracing enabled and core `AccessLogConfig.IncludePath` `<include>`
- **When** a client sends `<request>`
- **Then** the span has `url.path` `<path>` and `http.request.method_original` `<original>`

##### Examples:
| include | request | path | original |
|---------|---------|------|----------|
| `false` | `GET /users/42` | (none) | (none) |
| `true` | `GET /users/42` | `/users/42` | (none) |
| `false` | `XYZZY /users/42` | (none) | (none) |
| `true` | `XYZZY /users/42` | `/users/42` | `XYZZY` |
| `true` | `GET /` followed by 4096 `a` characters | the first 1024 bytes of the path followed by `…[truncated]` | (none) |

### Requirement: Context is propagated through the configured propagator
Spans SHALL continue a remote parent extracted with the configured propagator: by default the global propagator, or W3C TraceContext when the global one declares no fields. With core's ignore-incoming opt-in set (`WithIgnoreIncomingTraceContext()` or `WithTraceConfig(TraceConfig{IgnoreIncoming: true})`), otelsrvr SHALL NOT extract. Without a valid extracted parent the span SHALL be a new root.

#### Scenario: Parent extraction
- **Given** tracing enabled with propagator `<propagator>`
- **When** a request arrives with `traceparent` `<traceparent>`
- **Then** the span's trace id is `<trace_id>` and its parent is `<parent>`

##### Examples:
| propagator | traceparent | trace_id | parent |
|------------|-------------|----------|--------|
| `propagation.TraceContext{}` | `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01` | `4bf92f3577b34da6a3ce929d0e0e4736` | `00f067aa0ba902b7` (remote) |
| `propagation.TraceContext{}` | `garbage` | a new id | (none) |
| `propagation.TraceContext{}` | (none) | a new id | (none) |
| (default) with the global no-op propagator | `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01` | `4bf92f3577b34da6a3ce929d0e0e4736` | `00f067aa0ba902b7` (remote, W3C fallback) |
| (default) with a global propagator of `propagation.Baggage{}` only | `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01` | a new id | (none) |
| `propagation.TraceContext{}` with core `WithIgnoreIncomingTraceContext()` | `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01` | a new id | (none) |
| `propagation.TraceContext{}` with core `WithTraceConfig(TraceConfig{IgnoreIncoming: true})` | `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01` | a new id | (none) |

### Requirement: Log and problem correlation follows the active span
With tracing enabled, the request's `trace_id` and `span_id` in log records, problem bodies and `srvr.TraceFromContext` SHALL equal the active server span's ids, and handler spans started from the request context SHALL be its children.

#### Scenario: Correlation equality
- **Given** tracing enabled with a recording tracer provider
- **When** a handler starts a child span and returns `errors.New("boom")`
- **Then** `<artefact>` has trace id equal to the server span's and `<relation>`

##### Examples:
| artefact | relation |
|----------|----------|
| the `http.request.failed` record | `span_id` equal to the server span's |
| the problem body | `trace_id` member equal to the server span's trace id |
| the child span | parent equal to the server span |

#### Scenario: Unsampled spans
- **Given** tracing enabled with a never-sample sampler
- **When** a request arrives with `traceparent` `<traceparent>`
- **Then** log records carry trace id `<trace_id>`

##### Examples:
| traceparent | trace_id |
|-------------|----------|
| `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00` | `4bf92f3577b34da6a3ce929d0e0e4736` |
| (none) | a valid non-zero id from the non-recording span |

### Requirement: Spans always end
The server span SHALL be ended exactly once for every request, including panics, aborted handlers (`http.ErrAbortHandler`), client disconnects and shutdown-forced connection closes.

#### Scenario: Span ending
- **Given** tracing enabled and a handler that `<behaviour>`
- **When** the request finishes
- **Then** the exporter holds exactly `1` ended span

##### Examples:
| behaviour |
|-----------|
| writes `200` |
| panics with `"boom"` |
| panics with `http.ErrAbortHandler` |
| blocks until the client disconnects |
| blocks until `Shutdown` forces the connection closed |
