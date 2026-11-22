# error-rendering Specification

## Purpose

Lets a consumer replace prblm with its own error renderer on every engine, while srvr keeps prblm as the byte-for-byte unchanged default and keeps every safety rule (cycle guard, commit check, safe 500, no internals, redaction) around whatever renderer runs. "Every engine" means one test case run on `net/http`, `gin` and `fiber`; `R` is a test renderer that answers `<status>` with `Content-Type: application/vnd.test+json` and body `{"code":"<code>"}` unless a row says otherwise.

## Requirements

### Requirement: prblm is the default renderer and its output is unchanged
Without `WithErrorRenderer`, every error answer SHALL be rendered by prblm with the server's prblm options and SHALL be identical, in status, headers and body, to the answer of srvr v0.3.0 for the same request, on every engine.

#### Scenario: Default rendering is unchanged
- **Given** a server on every engine with no renderer option and `<setup>`
- **When** a client sends `<request>`
- **Then** status, headers (except `Date`) and body equal the v0.3.0 golden response recorded for that engine and row

##### Examples:
| setup | request | answer |
|-------|---------|--------|
| a route `GET /x` returning `prblm.ErrConflict` | `GET /x` | `409` problem |
| a route `GET /x` returning `errors.New("boom")` | `GET /x` | `500` problem without `boom` |
| a route `GET /x` panicking with `"boom"` | `GET /x` | `500` problem without `boom` |
| no route | `GET /missing` | `404` problem |
| a route `POST /x` only | `GET /x` | `405` problem with `Allow` |
| `MaxRequestBodyBytes` `10`, a route `POST /x` reading the body | `POST /x` with 11 bytes | `413` problem |
| `Static("/assets", site)` | `GET /assets/missing.js` | `404` problem |
| a route `GET /x` returning `fmt.Errorf("q: %w", context.Canceled)` after the client disconnected (net/http) | `GET /x` | `499` problem titled `Client Closed Request` |
| a route `GET /x` returning `fmt.Errorf("q: %w", context.Canceled)` after the client disconnected (gin) | `GET /x` | empty `200` with `Content-Length: 0`, nothing rendered |
| a route `GET /x` returning an error whose `Unwrap` chain is cyclic | `GET /x` | the safe `500` problem |
| `WithProblemOptions(prblm.WithMappers(prblm.Status(ErrQuota, 429)))`, a route returning `ErrQuota` | `GET /x` | `429` problem |

### Requirement: A custom renderer answers every error
With `WithErrorRenderer(r)`, srvr SHALL call `r` once for every error answer it makes (handler errors, recovered panics, router 404 and 405, static misses, oversized bodies, a client closing the request, fiber's pre-routing rejections) and write `r`'s status, headers and body unchanged except for the rules of "srvr enforces safety around every renderer". prblm SHALL NOT render any response of that server.

#### Scenario: Custom renderer on every engine
- **Given** a server on every engine with `WithErrorRenderer(R)` and `<setup>`
- **When** a client sends `<request>`
- **Then** `R` is called once with an error matching `<match>` and the response is `<status>` `application/vnd.test+json` with `R`'s body
- **And** the access record is `http.request.failed` with `problem.status` `<status>` and no `problem.type`

##### Examples:
| setup | request | match | status |
|-------|---------|-------|--------|
| a route `GET /x` returning `ErrQuota` | `GET /x` | `errors.Is(err, ErrQuota)` | `429` |
| a route `GET /x` panicking with `"boom"` | `GET /x` | `errors.As(err, new(*srvr.PanicError))` with `Value` `"boom"` | `500` |
| no route | `GET /missing` | `errors.Is(err, srvr.ErrNotFound)` and `errors.Is(err, prblm.ErrNotFound)` | `404` |
| a route `POST /x` only | `GET /x` | `errors.As(err, new(*srvr.MethodNotAllowedError))` with `Allow` `["POST"]` | `405` |
| `Static("/assets", site)` | `GET /assets/missing.js` | `errors.Is(err, srvr.ErrNotFound)` | `404` |
| `Static("/assets", site)` | `DELETE /assets/app.js` | `*srvr.MethodNotAllowedError` with `Allow` `["GET","HEAD"]` | `405` |
| `MaxRequestBodyBytes` `10`, a route reading the body | `POST /x` with 11 bytes | `errors.Is(err, srvr.ErrRequestTooLarge)` | `413` |
| a route returning `fmt.Errorf("q: %w", context.Canceled)` after the client disconnected (net/http, gin) | `GET /x` | `errors.Is(err, srvr.ErrClientClosed)` and `errors.Is(err, context.Canceled)` | `499` |
| fiber with headers over its read buffer (fiber only) | `GET /x` with a 16 KiB header | `errors.Is(err, srvr.ErrHeadersTooLarge)` | `431` |
| fiber with `ReadTimeout` `50ms` (fiber only) | a request whose headers arrive after `100ms` | `errors.Is(err, srvr.ErrRequestTimeout)` | `408` |
| gin route calling `c.AbortWithStatus(401)` (gin only) | `GET /x` | not called; the response is the bare `401` gin wrote, as today (`ErrorHook` still receives it, see error-responses) | `401` |

#### Scenario: What the renderer receives
- **Given** a net/http server with `WithErrorRenderer(R)` recording its arguments and a route `GET /items/{id}` returning `ErrQuota`
- **When** a client sends `GET /items/7?q=secret` with `Accept: application/json` and `traceparent` `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01`
- **Then** the renderer's `ErrorRequest` has `<field>` `<value>`

##### Examples:
| field | value |
|-------|-------|
| `Method` | `GET` |
| `Path` | `/items/7` (always set: it is for the response, never logged by srvr) |
| `Route` | `/items/{id}` (the logged `http.route` form: no method) |
| `Header.Get("Accept")` | `application/json` |
| `TraceID` | `4bf92f3577b34da6a3ce929d0e0e4736` |
| the context | `srvr.TraceFromContext` returns that trace |
| `Query` | not a field: the query string is never passed |

### Requirement: Renderer configuration is validated
`WithErrorRenderer` SHALL reject a nil or typed-nil renderer and SHALL be rejected together with `WithProblemOptions`, failing construction on every engine with an error wrapping `srvr.ErrConfig` that names the field and value. The renderer SHALL have no config-struct field.

#### Scenario: Rejected renderer configuration
- **Given** engine `<engine>` constructed with `<options>`
- **When** `New` returns
- **Then** the error wraps `srvr.ErrConfig` and its `*srvr.ConfigError` names `<field>` with `<message>`

##### Examples: rejected
| engine | options | field | message |
|--------|---------|-------|---------|
| `net/http` | `WithErrorRenderer(nil)` | `ErrorRenderer` | `is nil` |
| `gin` | `WithErrorRenderer(nil)` | `ErrorRenderer` | `is nil` |
| `fiber` | `WithErrorRenderer(nil)` | `ErrorRenderer` | `is nil` |
| `net/http` | `WithErrorRenderer((*myRenderer)(nil))` | `ErrorRenderer` | `is nil` |
| `net/http` | `WithErrorRenderer(R)`, `WithProblemOptions(prblm.WithNamespace("https://errors.example/p"))` | `ErrorRenderer` | `cannot be combined with ProblemOptions` |
| `gin` | `WithProblemOptions(...)` then `WithErrorRenderer(R)` (order does not matter) | `ErrorRenderer` | `cannot be combined with ProblemOptions` |
| `fiber` | `WithErrorRenderer(R)`, `WithProblemOptions()` (no options) | `ErrorRenderer` | `cannot be combined with ProblemOptions` |
| `net/http` | `WithErrorRenderer(nil)`, `WithAddr("bad")` | both `ErrorRenderer` and `Addr` | joined |

##### Examples: accepted
| engine | options | field | message |
|--------|---------|-------|---------|
| `net/http` | `WithErrorRenderer(R)` twice | (none) | the later renderer is used |
| `net/http` | `WithErrorRenderer(R)`, `WithErrorHook(fn)` | (none) | `fn` runs for every answered error |
| `net/http` | `WithConfig(c)` with every field set, then `WithErrorRenderer(R)` | (none) | `R` renders; `Config` has no renderer field |

### Requirement: srvr enforces safety around every renderer
srvr SHALL guard the error against cyclic chains before calling the renderer, SHALL NOT call it once the response started, and SHALL replace a faulty answer (panic, status outside `400`-`599`, body over 1 MiB, body without `Content-Type`, returned after the 5-second render deadline) with its fixed safe `500` problem, logging the fault. The renderer's context SHALL keep the request's values but not its cancellation.

#### Scenario: Faulty renderer
- **Given** a server on every engine with `WithErrorRenderer(F)` and a route `GET /x` returning `errors.New("db down")`
- **When** a client sends `GET /x` and `F` `<fault>`
- **Then** the response is srvr's safe `500` problem, without `db down`
- **And** the access record is `http.request.failed` at `ERROR` with `problem.status` `500`, the original error's `error.chain`, and `srvr.renderer.failure` `<failure>`
- **And** the server answers the next request `200`

##### Examples:
| fault | failure |
|-------|---------|
| panics with `"render boom"` | `panic` |
| panics with `nil` | `panic` |
| returns status `0` | `invalid_status` |
| returns status `200` | `invalid_status` |
| returns status `399` | `invalid_status` |
| returns status `600` | `invalid_status` |
| returns status `-1` | `invalid_status` |
| returns a body of exactly 1 MiB with `Content-Type` | (none: sent as returned) |
| returns a body of 1 MiB + 1 byte | `oversized_body` |
| returns a 10-byte body without `Content-Type` | `missing_content_type` |
| returns status `404`, no body and no headers | (none: a bare `404` is sent) |
| returns after its context's 5-second deadline (the test shortens the deadline through `internal/engine`'s test hook) | `deadline` |
| returns before the deadline while the client already disconnected | (none: sent as returned) |
| returns a nil `Header` | (none: sent without extra headers) |
| returns `Header` with `Content-Length: 999` and `Transfer-Encoding: chunked` | (none: those two are dropped, srvr sets `Content-Length` from the body) |

#### Scenario: Hostile errors and committed responses
- **Given** a server on every engine with `WithErrorRenderer(R)` and a route `GET /x` that `<behaviour>`
- **When** a client sends `GET /x`
- **Then** `<outcome>`

##### Examples:
| behaviour | outcome |
|-----------|---------|
| returns an error whose `Unwrap` chain is cyclic | `R` receives an error that `errors.Is` can walk and that wraps no cycle; `R`'s answer is sent |
| returns an error whose `Unwrap` panics | `R` is called once with a walkable error; `R`'s answer is sent |
| returns a typed-nil error `(*myErr)(nil)` | `R` is called with it, as prblm is today; no panic escapes |
| writes `200` then returns `errors.New("late")` (on fiber: starts a `200` body stream) | `R` is not called; the client keeps the `200`; the record has `srvr.response.committed` `true` |
| writes `200` then panics (on fiber: starts a `200` body stream) | `R` is not called; the record has `error.kind` `panic` |
| writes a buffered `200` body then panics (fiber only: fasthttp sends nothing before the handler returns, so only a started body stream counts as committed) | `R` is called; the client gets `R`'s answer, not the `200`; the record has no `srvr.response.committed` |
| returns `ErrQuota` and `R` writes `Cache-Control: no-store` while the handler had set `Cache-Control: max-age=60` | the response has one `Cache-Control` line, `no-store` (the renderer's header replaces the handler's) |
| returns `ErrQuota` and `R` sets `Vary: Accept` while the handler set `Vary: Origin` | `Vary` lists `Origin` and `Accept` |
| 50 concurrent requests, each failing | every response is `R`'s; the test passes under `-race` |

#### Scenario: Renderer and lifecycle
- **Given** engine `<engine>` with `WithErrorRenderer(R)`
- **When** `<event>`
- **Then** `<outcome>`

##### Examples:
| engine | event | outcome |
|--------|-------|---------|
| `net/http` | `Shutdown` while `R` renders a request still draining | `R`'s answer reaches the client before the drain ends |
| `net/http` | `R` blocks past the drain deadline | `Shutdown` returns its deadline error; goleak finds no goroutine started by srvr for rendering |
| `fiber` | `R` blocks past the render deadline and then returns | the safe `500` is sent once `R` returns; srvr starts no goroutine for rendering |
| `gin` | a renderer is set and `Start` is called twice | the second `Start` returns `srvr.ErrServerStarted`; the renderer is unaffected |
