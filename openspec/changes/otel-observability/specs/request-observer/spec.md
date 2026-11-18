# Spec Delta

## Purpose

Defines the engine-neutral per-request observer hook in the core module, so metrics and tracing can be added to any engine without the core depending on OpenTelemetry.

## ADDED Requirements

### Requirement: Observers are called once per request
A server configured with `WithObserver(o)` SHALL call `o.Begin(ctx, RequestInfo)` once per request before the handler runs and the returned end function once after the response finishes, on every engine, with the same `RequestInfo` and `ResultInfo` values.

#### Scenario: Begin and end per engine
- **Given** a `<engine>` server with a recording observer and route `GET /users/{id}` in that engine's syntax
- **When** a client sends `GET /users/42` and the handler `<behaviour>`
- **Then** the observer records one `Begin` with method `GET` and one end with route `<route>` and status `<status>`

##### Examples:
| engine | behaviour | route | status |
|--------|-----------|-------|--------|
| net/http | writes `200` | `/users/{id}` | `200` |
| gin | writes `200` | `/users/:id` | `200` |
| fiber | writes `200` | `/users/:id` | `200` |
| net/http | returns `prblm.ErrNotFound` | `/users/{id}` | `404` |
| gin | returns `prblm.ErrNotFound` | `/users/:id` | `404` |
| fiber | returns `prblm.ErrNotFound` | `/users/:id` | `404` |
| net/http | returns `errors.New("boom")` | `/users/{id}` | `500` |
| gin | returns `errors.New("boom")` | `/users/:id` | `500` |
| fiber | returns `errors.New("boom")` | `/users/:id` | `500` |
| net/http | panics | `/users/{id}` | `500` |
| gin | panics | `/users/:id` | `500` |
| fiber | panics | `/users/:id` | `500` |

##### Examples: unmatched
| engine | behaviour | route | status |
|--------|-----------|-------|--------|
| net/http | (not called; request is `GET /nope`) | (empty) | `404` |
| gin | (not called; request is `GET /nope`) | (empty) | `404` |
| fiber | (not called; request is `GET /nope`) | (empty) | `404` |

#### Scenario: Aborts and late failures per engine
- **Given** a `<engine>` server with a recording observer and route `GET /items/{id}` in that engine's syntax
- **When** a client sends `GET /items/42` and the handler `<behaviour>`
- **Then** `ResultInfo` has status `<status>`, aborted `<aborted>`, committed `<committed>` and response size `<size>`

##### Examples:
| engine | behaviour | status | aborted | committed | size |
|--------|-----------|--------|---------|-----------|------|
| net/http | panics with `http.ErrAbortHandler` | `0` | `true` | `false` | `0` |
| gin | panics with `http.ErrAbortHandler` | `0` | `true` | `false` | `0` |
| fiber | panics with `http.ErrAbortHandler` | `500` | `false` | `false` | the problem body length (documented fiber exception: fasthttp has no abort sentinel, so the panic is recovered like any other) |
| fiber | streams the body `streamed` with `SendStream` and its declared length `8`, then returns `errors.New("late")` | `200` | `false` | `true` | the access record's response size (`8`) |

### Requirement: Observers sit between the access log and recovery
The observer SHALL run inside trace-context handling and the access log and outside panic recovery and error rendering (ginprblm middleware, fiberprblm error handler), so it sees the request's trace id at `Begin` and the final rendered status, including recovered panics, returned errors and unmatched 404/405, at end. The observer SHALL NOT render errors itself.

#### Scenario: Observer view of the request
- **Given** a server with a recording observer
- **When** a client sends a request with `traceparent` `<traceparent>` and the handler `<behaviour>`
- **Then** at `Begin` `srvr.TraceFromContext` returns trace id `<trace_id>`
- **And** at end `ResultInfo` has status `<status>` and error kind `<kind>`

##### Examples:
| traceparent | behaviour | trace_id | status | kind |
|-------------|-----------|----------|--------|------|
| `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01` | writes `200` | `4bf92f3577b34da6a3ce929d0e0e4736` | `200` | `none` |
| (none) | returns `errors.New("boom")` | a generated id | `500` | `error` |
| (none) | panics with `"boom"` | a generated id | `500` | `panic` |
| (none) | (not called; request is `GET /nope` on gin) | a generated id | `404` | `error` |
| (none) | (not called; request is `GET /nope` on fiber) | a generated id | `404` | `error` |
| (none) | (not called; request is `GET /nope` on net/http) | a generated id | `404` | `error` |

### Requirement: Result information is complete and bounded
`ResultInfo` SHALL carry the status code, the matched route template (empty when unmatched), the duration, request and response body sizes, the error kind (`none`, `error`, `panic`), the returned error, and whether the response was committed before an error.

#### Scenario: Result fields
- **Given** route `POST /items` whose handler `<behaviour>`
- **When** a client sends `<request>`
- **Then** `ResultInfo` has route `<route>`, status `<status>` and response size `<size>`

##### Examples:
| behaviour | request | route | status | size |
|-----------|---------|-------|--------|------|
| writes `201` with a 12-byte body | `POST /items` with a 5-byte body | `/items` | `201` | `12` |
| (not called) | `GET /nope` | (empty) | `404` | the problem body length |
| (not called) | `DELETE /items` | (empty) | `405` | the problem body length |
| writes `200` with 3 bytes then returns `errors.New("late")` | `POST /items` | `/items` | `200` | `3` |

### Requirement: Several observers compose in order
Multiple `WithObserver` options SHALL compose: `Begin` runs in registration order, each receiving the context returned by the previous, and end functions run in reverse order.

#### Scenario: Composition
- **Given** observers `<observers>` registered in that order
- **When** a request completes
- **Then** the call order is `<order>`

##### Examples:
| observers | order |
|-----------|-------|
| `A`, `B` | `A.Begin`, `B.Begin`, `B.end`, `A.end` |
| `A` | `A.Begin`, `A.end` |

### Requirement: Faulty observers never break requests
A nil observer SHALL fail construction. An observer whose `Begin` panics, returns a nil context or returns a nil end function, or whose end function panics, SHALL NOT change the response; the fault SHALL be logged once per observer and fault kind at `ERROR` as `srvr.observer.fault`.

#### Scenario: Observer faults
- **Given** a server with an observer that `<fault>`
- **When** a client sends `GET /ping` whose handler writes `200`
- **Then** the outcome is `<outcome>`

##### Examples:
| fault | outcome |
|-------|---------|
| is `nil` | construction returns an error wrapping `srvr.ErrConfig` |
| is a typed-nil pointer | construction returns an error wrapping `srvr.ErrConfig` |
| panics in `Begin` | status `200` and one `srvr.observer.fault` record with `srvr.observer.fault.kind` `begin_panic` |
| returns a `nil` context from `Begin` | status `200`, the handler receives the original context |
| returns a `nil` end function | status `200` and no panic at end |
| panics in the end function | status `200` and one `srvr.observer.fault` record with `srvr.observer.fault.kind` `end_panic` |
| panics in `Begin` on 1000 requests | status `200` for all and exactly one `srvr.observer.fault` record |
| blocks in `Begin` for `50ms` | status `200` after at least `50ms` and no `srvr.observer.fault` record (blocking is documented, not detected) |
| blocks in the end function for `50ms` | status `200` after at least `50ms`, the access record written after the end function returns, and no `srvr.observer.fault` record (observers run on the request path, as `Begin` does) |
| panics in the end function with `http.ErrAbortHandler` | status `200` and one `srvr.observer.fault` record with `srvr.observer.fault.kind` `end_panic` (the observer's panic is never re-raised) |

### Requirement: Observers may supply the trace context
An observer that implements the trace-source extension SHALL be able to provide the request's trace id and span id; when it does, `srvr.TraceFromContext`, log records and problem `trace_id` SHALL use them instead of core's own trace context.

#### Scenario: Trace source
- **Given** a server with `<observer>`
- **When** a request with `traceparent` `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01` is handled
- **Then** the access record `span_id` is `<span_id>`

##### Examples:
| observer | span_id |
|----------|---------|
| (none) | srvr's generated span id |
| an observer supplying span id `1111111111111111` | `1111111111111111` |
| an observer supplying an all-zero span id | srvr's generated span id |
