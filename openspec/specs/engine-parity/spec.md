# engine-parity Specification

## Purpose

Guarantees that a client and a log reader see the same behaviour from the net/http, Gin and Fiber v3 engines for the same failure, and lists the documented exceptions where an engine cannot match.

## Requirements

### Requirement: Identical problems across engines
For the same failure, every engine SHALL send the same status, content type, `Vary` and `Cache-Control` headers, and the same problem members (`type` when not `about:blank`, which prblm omits; `title`, `status`, `detail` when set, `instance`, `trace_id`), differing only in values derived from the request.

#### Scenario: Problem parity
- **Given** engine `<engine>` with a route at `/items/{id}` in that engine's syntax whose handler fails with `<failure>`
- **When** a client sends `GET /items/7` with `traceparent` `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01`
- **Then** the response status is `<status>` and the body equals `<body>`

##### Examples:
| engine | failure | status | body |
|--------|---------|--------|------|
| `net/http` | `prblm.ErrNotFound` | `404` | `{"title":"Not Found","status":404,"detail":"The requested resource was not found.","instance":"/items/7","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}` |
| `gin` | `prblm.ErrNotFound` | `404` | `{"title":"Not Found","status":404,"detail":"The requested resource was not found.","instance":"/items/7","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}` |
| `fiber` | `prblm.ErrNotFound` | `404` | `{"title":"Not Found","status":404,"detail":"The requested resource was not found.","instance":"/items/7","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}` |
| `net/http` | `errors.New("secret")` | `500` | `{"title":"Internal Server Error","status":500,"instance":"/items/7","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}` |
| `gin` | `errors.New("secret")` | `500` | `{"title":"Internal Server Error","status":500,"instance":"/items/7","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}` |
| `fiber` | `errors.New("secret")` | `500` | `{"title":"Internal Server Error","status":500,"instance":"/items/7","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}` |
| `net/http` | a panic with `"boom"` | `500` | `{"title":"Internal Server Error","status":500,"instance":"/items/7","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}` |
| `gin` | a panic with `"boom"` | `500` | `{"title":"Internal Server Error","status":500,"instance":"/items/7","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}` |
| `fiber` | a panic with `"boom"` | `500` | `{"title":"Internal Server Error","status":500,"instance":"/items/7","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}` |

### Requirement: Identical log records across engines
For the same request and outcome, every engine SHALL write access and failed-request records with the same `msg`, `level`, keys and values, except `http.route` (each engine's template syntax), `duration_ms` and code-location values. An unmatched path or method SHALL be logged as `http.request.failed` at `WARN` with a `problem` group and no `http.route` on every engine.

#### Scenario: Log parity
- **Given** engine `<engine>` serving `GET /items/{id}` in that engine's syntax with a handler that `<outcome>`
- **When** a client sends `GET /items/7` unless the outcome names another request
- **Then** the record has `msg` `<msg>`, `level` `<level>` and `http.route` `<route>`
- **And** its key set equals the net/http engine's key set for the same outcome

##### Examples:
| engine | outcome | msg | level | route |
|--------|---------|-----|-------|-------|
| `net/http` | succeeds with `200` | `http.request.completed` | `INFO` | `/items/{id}` |
| `gin` | succeeds with `200` | `http.request.completed` | `INFO` | `/items/:id` |
| `fiber` | succeeds with `200` | `http.request.completed` | `INFO` | `/items/:id` |
| `net/http` | fails with `errors.New("boom")` | `http.request.failed` | `ERROR` | `/items/{id}` |
| `gin` | fails with `errors.New("boom")` | `http.request.failed` | `ERROR` | `/items/:id` |
| `fiber` | fails with `errors.New("boom")` | `http.request.failed` | `ERROR` | `/items/:id` |
| `net/http` | fails with `prblm.ErrNotFound` | `http.request.failed` | `WARN` | `/items/{id}` |
| `gin` | fails with `prblm.ErrNotFound` | `http.request.failed` | `WARN` | `/items/:id` |
| `fiber` | fails with `prblm.ErrNotFound` | `http.request.failed` | `WARN` | `/items/:id` |
| `net/http` | is never reached because the client sends `GET /nope` | `http.request.failed` | `WARN` | (none) |
| `gin` | is never reached because the client sends `GET /nope` | `http.request.failed` | `WARN` | (none) |
| `fiber` | is never reached because the client sends `GET /nope` | `http.request.failed` | `WARN` | (none) |
| `net/http` | is never reached because the client sends `DELETE /items/7` | `http.request.failed` | `WARN` | (none) |
| `gin` | is never reached because the client sends `DELETE /items/7` | `http.request.failed` | `WARN` | (none) |
| `fiber` | is never reached because the client sends `DELETE /items/7` | `http.request.failed` | `WARN` | (none) |
| `net/http` | panics with `http.ErrAbortHandler` | `http.request.failed` | `WARN` | `/items/{id}` |
| `gin` | panics with `http.ErrAbortHandler` | `http.request.failed` | `WARN` | `/items/:id` |

### Requirement: Parity exceptions are explicit
Where an engine cannot match, the difference SHALL be listed here, documented in the engine's godoc, and covered by a test that pins the engine's actual behaviour.

#### Scenario: Documented differences
- **Given** engine `<engine>`
- **When** `<situation>`
- **Then** the engine's behaviour is `<behaviour>`

##### Examples:
| engine | situation | behaviour |
|--------|-----------|-----------|
| `gin` | a handler writes a success body and later calls `c.Error` | the success response is kept; the record has `srvr.response.committed` `true` |
| `fiber` | a handler sets a buffered body and then returns an error | the body is replaced by the problem (Fiber buffers responses); `srvr.response.committed` is `false` |
| `fiber` | a streaming body has started and an error follows | the stream is kept; the record has `srvr.response.committed` `true` |
| `fiber` | a handler returns `c.SendStream(bytes.NewReader([]byte("hello")))`, a stream without a declared length | the client gets `hello`; `http.response.body.size` is `0` (fiber logs a stream's declared `Content-Length`, and this stream has none; net/http and gin log the bytes written) |
| `net/http` | a client disconnects before the error is rendered | the problem write is attempted and dropped |
| `gin` | a client disconnects before the error is rendered | nothing is written |
| `fiber` | a client disconnects mid-request | the handler is not notified (fasthttp does not detect it) |
| `fiber` | a client disconnects and the handler then returns `fmt.Errorf("query: %w", context.Canceled)` | a `500` problem and an `ERROR` record, where net/http and gin answer `499` at `WARN`: the request context is not cancelled by the disconnect, so srvr cannot tell it from a server-side cancellation |
| `gin` | `HEAD /users` where only `GET /users` is registered | a `405` problem with `Allow` `GET` (gin does not route `HEAD` to `GET` handlers) |
| `gin` | wrong method where `GET` and `POST` are registered | `Allow` is `GET, POST` without `HEAD`, because gin does not route `HEAD` to `GET` handlers; net/http and fiber send `GET, HEAD, POST` |
| `fiber` | a `4194305`-byte body is sent to a handler that never reads it | a `413` problem before the handler runs (fasthttp reads the whole body before routing); the record has no `http.route`, while net/http and gin record the route because their limit trips when the handler reads |
| `fiber` | a client sends method `FOO` to a registered path | a plain-text `501` `Not Implemented`, no problem and no access record (fiber answers before any middleware; accepted exception) |
| `fiber` | `MaxHeaderBytes` is above `65536` and a request carries `70000` bytes of headers | a `431` problem, because fiber's header buffer is capped at `65536` bytes per connection; net/http accepts it |
| `fiber` | a handler panics with `http.ErrAbortHandler` | a `500` problem and an `ERROR` record with `error.kind` `panic`; no aborted connection and no `srvr.response.aborted` (fasthttp has no abort sentinel and does not recover a re-raised panic) |
| `gin` | a handler calls `c.Error(e)` where `e.Unwrap()` returns `e` | the request does not complete: gin's `c.Error` calls `errors.As` on `e` before srvr sees it and never returns, so no response and no access record are written (net/http and fiber answer a `500` problem); `ginsrvr` godoc warns against it (owner decision 2026-11-18, documented only) |
| `fiber` | `ReadHeaderTimeout` is set | it is not applied (fasthttp has no header-only timeout); `ReadTimeout` bounds reading the headers and body together |
