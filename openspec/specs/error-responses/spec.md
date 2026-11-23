# error-responses Specification

## Purpose

Defines how every failure a srvr server answers becomes an RFC 9457 problem rendered by prblm, so clients get one consistent error format that never leaks internals.

## Requirements

### Requirement: Handler errors become problems
An error returned by a handler SHALL be rendered by prblm as an RFC 9457 problem, using the server's prblm options. Unmapped errors SHALL become a `500` problem with no `detail`, and the error text SHALL NOT appear in the response.

#### Scenario: Error to problem
- **Given** a route `GET /items/{id}` whose handler returns `<error>`
- **When** a client sends `GET /items/7` with `Accept` `<accept>`
- **Then** the response status is `<status>` with content type `<content_type>`
- **And** the body has `title` `<title>` and does not contain `<hidden>`

##### Examples: mapped
| error | accept | status | content_type | title | hidden |
|-------|--------|--------|--------------|-------|--------|
| `prblm.ErrNotFound` | `application/json` | `404` | `application/problem+json` | `Not Found` | (none) |
| `fmt.Errorf("item 7: %w", prblm.ErrConflict)` | `*/*` | `409` | `application/problem+json` | `Conflict` | `item 7` |
| `prblm.WithStatus(errors.New("gone"), 410)` | (none) | `410` | `application/problem+json` | `Gone` | `gone` |
| `context.DeadlineExceeded` | `application/json` | `504` | `application/problem+json` | `Gateway Timeout` | `deadline` |

##### Examples: unmapped and negotiated
| error | accept | status | content_type | title | hidden |
|-------|--------|--------|--------------|-------|--------|
| `errors.New("pq: password authentication failed for user app")` | `application/json` | `500` | `application/problem+json` | `Internal Server Error` | `pq:` |
| `errors.New("x")` | `application/xml` | `500` | `application/problem+xml` | `Internal Server Error` | `x</` |
| `errors.New("x")` | `text/html` | `500` | `application/problem+json` | `Internal Server Error` | `x"` |

### Requirement: Problems carry the trace id
Every problem rendered by the server SHALL include a `trace_id` extension member equal to the request's trace id, and SHALL keep prblm's default `instance` (the request path without query string).

#### Scenario: Trace id in the problem
- **Given** a request carrying `traceparent` `<traceparent>` to a route whose handler returns `prblm.ErrNotFound`
- **When** the problem is rendered
- **Then** the body member `trace_id` is `<trace_id>`
- **And** the body member `instance` is `<instance>`

##### Examples:
| traceparent | trace_id | instance |
|-------------|----------|----------|
| `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01` | `4bf92f3577b34da6a3ce929d0e0e4736` | `/items/7` |
| (none) | a generated 32-character lowercase hex id | `/items/7` |

### Requirement: Unmatched routes are problems
A request that matches no route SHALL get a `404` problem, and a request whose path matches but whose method does not SHALL get a `405` problem with an `Allow` header listing the allowed methods. ServeMux's plain-text responses SHALL NOT reach the client.

#### Scenario: No route or wrong method
- **Given** routes `GET /users` and `POST /users`
- **When** a client sends `<request>`
- **Then** the response status is `<status>` with content type `application/problem+json`
- **And** the `Allow` header is `<allow>`

##### Examples:
| request | status | allow |
|---------|--------|-------|
| `GET /nope` | `404` | (none) |
| `DELETE /users` | `405` | `GET, HEAD, POST` |
| `GET /users/` | `404` | (none) |
| `PATCH /nope` | `404` | (none) |

### Requirement: Oversized request bodies are refused
Request bodies SHALL be limited to the configured `MaxRequestBodyBytes`. A handler that reads past the limit SHALL get an error that, when returned, renders a `413` problem.

#### Scenario: Body limit
- **Given** a server with `MaxRequestBodyBytes` `<limit>` and a handler that reads the whole body and returns the read error
- **When** a client sends `POST /upload` with a body of `<size>` bytes
- **Then** the response status is `<status>`

##### Examples:
| limit | size | status |
|-------|------|--------|
| (default) | `4194304` | `200` |
| (default) | `4194305` | `413` |
| `10` | `10` | `200` |
| `10` | `11` | `413` |
| `-1` | `8388608` | `200` |
| (default) | `0` | `200` |

### Requirement: Panics become safe problems
A panic in a handler or middleware SHALL be recovered and answered with a `500` problem that contains neither the panic value nor a stack trace. A panic with `http.ErrAbortHandler` SHALL abort the connection as net/http defines.

#### Scenario: Recovered panic
- **Given** a route whose handler panics with `<value>`
- **When** a client sends a request to it
- **Then** the client receives `<response>`
- **And** the server keeps serving the next request with status `200`

##### Examples:
| value | response |
|-------|----------|
| `"boom"` | status `500`, `title` `Internal Server Error`, body without `boom` |
| `errors.New("db down")` | status `500`, body without `db down` |
| `nil` via `panic(nil)` | status `500` (Go 1.21+ `*runtime.PanicNilError`) |
| `http.ErrAbortHandler` | an aborted connection and no response |

### Requirement: Errors after the response started are not rendered
When a handler returns an error or panics after the response status has been written, the server SHALL NOT write a problem or change the status; the error SHALL still be logged, at the level of the status it would have been answered with (a panic at `ERROR`), and never suppressed by the access log configuration.

#### Scenario: Late error
- **Given** a handler that writes status `<written>` and body `partial` and then `<fails>`
- **When** a client sends a request to it
- **Then** the client receives status `<written>` and a body starting with `partial`
- **And** the access record has `msg` `http.request.failed`, `level` `<level>`, `srvr.response.committed` `true` and no `problem` group

##### Examples:
| written | fails | level |
|---------|-------|-------|
| `200` | returns `errors.New("late")` | `ERROR` |
| `200` | panics with `"late"` | `ERROR` |
| `201` | returns `prblm.ErrConflict` | `WARN` |
| `500` | returns `prblm.ErrConflict` | `ERROR` |

### Requirement: Consumers extend error rendering
The consumer SHALL be able to pass prblm options (mappers, enrichers, namespace, types, `prblm.OnError` and `prblm.AddErrorHook`) through `WithProblemOptions`, or replace prblm with `WithErrorRenderer` (never both), and set an error hook (`WithErrorHook`) receiving the error and an `ErrorReport` of what was sent (status, type, title), never a `*prblm.Problem`. srvr's own recording hook SHALL NOT be replaceable. A panic in any consumer hook SHALL NOT affect the response or the access record.

#### Scenario: Consumer mapper and hook
- **Given** a server configured with `<option>`
- **When** a handler returns `<error>`
- **Then** the response status is `<status>`
- **And** the consumer hook `<hook>`

##### Examples:
| option | error | status | hook |
|--------|-------|--------|------|
| `WithProblemOptions(prblm.WithMappers(prblm.Status(ErrQuota, 429)))` | `ErrQuota` | `429` | (none) |
| `WithErrorHook(fn)` | `prblm.ErrNotFound` | `404` | receives the error and an `ErrorReport` with status `404`, type `about:blank`, title `Not Found` |
| `WithErrorRenderer(R)`, `WithErrorHook(fn)` | `ErrQuota` | `429` (from `R`) | receives `ErrQuota` and an `ErrorReport` with status `429`, empty type and title |
| `WithErrorRenderer(F)` where `F` panics, `WithErrorHook(fn)` | `ErrQuota` | `500` (safe problem) | receives `ErrQuota` and an `ErrorReport` with status `500`, type `about:blank`, title `Internal Server Error` |
| gin: `WithErrorRenderer(R)`, `WithErrorHook(fn)`, the handler calls `c.AbortWithStatus(401)` | (none) | `401` (gin's bare status; `R` not called) | receives `ginprblm.ErrStatusOnly` and an `ErrorReport` with status `401`, empty type and title |
| `WithErrorRenderer(R)`, `WithProblemOptions(prblm.OnError(fn))` | (none) | (none) | construction returns an error wrapping `srvr.ErrConfig` naming `ErrorRenderer` |
| `WithErrorHook(fn)` where `fn` panics | `prblm.ErrNotFound` | `404` | panics and the access record is still written |
| `WithErrorHook(nil)` | (none) | (none) | construction returns an error wrapping `srvr.ErrConfig` |
| `WithProblemOptions(prblm.WithNamespace("not a uri"))` | (none) | (none) | construction returns an error wrapping `srvr.ErrConfig` naming `ProblemOptions` |
| `WithProblemOptions(opt)` where `opt` panics with a value whose `Error()` panics with itself | (none) | (none) | construction returns an error wrapping `srvr.ErrConfig` naming `ProblemOptions`, without panicking |

#### Scenario: Consumer prblm error hooks
- **Given** a net/http server configured with `<option>` and a route `GET /x` whose handler returns `prblm.ErrNotFound`
- **When** a client sends `GET /x`
- **Then** the response is a `404` problem
- **And** `<hooks>`
- **And** the access record is `http.request.failed` with `problem.status` `404`
- **And** the hooks that run do so in this order: the consumer's `prblm.OnError`, the hooks added with `prblm.AddErrorHook` in order, srvr's own hook, then the `WithErrorHook` hook

##### Examples:
| option | hooks |
|--------|-------|
| (none) | no consumer hook runs |
| `WithProblemOptions(prblm.OnError(fn))` | `fn` receives `prblm.ErrNotFound` and a problem with status `404` (prblm's own hooks keep prblm's signature), once |
| `WithProblemOptions(prblm.OnError(fn))` where `fn` panics | `fn` panics once |
| `WithProblemOptions(prblm.OnError(fn))`, `WithErrorHook(gn)` | `fn` and then `gn` each receive `prblm.ErrNotFound` and a problem with status `404`, once |
| `WithProblemOptions(prblm.AddErrorHook(fn))` | `fn` receives `prblm.ErrNotFound` and a problem with status `404` (prblm's own hooks keep prblm's signature), once |
| `WithProblemOptions(prblm.OnError(fn), prblm.OnError(nil))` | no consumer hook runs (`prblm.OnError(nil)` removes only the consumer's hook) |

#### Scenario: Consumer prblm error hook on every engine
- **Given** engine `<engine>` configured with `WithProblemOptions(prblm.OnError(fn))` where `fn` `<behaves>`, and a route whose handler reports `prblm.ErrNotFound` in the engine's way
- **When** a client requests the route
- **Then** the response is a `404` problem
- **And** `fn` is called once with `prblm.ErrNotFound`
- **And** the access record is `http.request.failed` with `problem.status` `404`

##### Examples:
| engine | behaves |
|--------|---------|
| `net/http` | returns |
| `gin` | returns |
| `fiber` | returns |
| `net/http` | panics |
| `gin` | panics |
| `fiber` | panics |

### Requirement: A request the client closed is answered with 499
When a handler returns an error matching `errors.Is(err, context.Canceled)` while the request's own context is cancelled, the server SHALL answer a `499` problem titled `Client Closed Request` without `detail`. A `context.Canceled` returned while the request context is live, or ended by its deadline (a server-side timeout, not the client), SHALL stay a `500`. The consumer's mappers and describers, prblm's built-in mappers and a `prblm.WithStatus` hint other than `500` SHALL take precedence.

#### Scenario: Client closed request
- **Given** a net/http server with debug buffering enabled and a route `GET /x` whose handler logs `DEBUG` `ctx` and returns `<error>` `<when>`
- **When** a client sends `GET /x`
- **Then** the access record is `http.request.failed` at `<level>` with `http.response.status_code` and `problem.status` `<status>`
- **And** the `ctx` record is `<ctx>`

##### Examples:
| error | when | level | status | ctx |
|-------|------|-------|--------|-----|
| `context.Canceled` | after the client disconnected | `WARN` | `499` | not written |
| `fmt.Errorf("query users: %w", context.Canceled)` | after the client disconnected | `WARN` | `499` | not written |
| `context.Canceled` | while the client is connected | `ERROR` | `500` | written |
| `errors.New("boom")` | after the client disconnected | `ERROR` | `500` | written |
| `context.Canceled`, with `WithProblemOptions(prblm.WithMappers(prblm.Status(context.Canceled, 503)))` | after the client disconnected | `ERROR` | `503` | written |
| `fmt.Errorf("%w: %w", prblm.ErrUnavailable, context.Canceled)` | after the client disconnected | `ERROR` | `503` | written |
| `prblm.WithStatus(context.Canceled, 409)` | after the client disconnected | `WARN` | `409` | not written |
| `context.DeadlineExceeded` | after the client disconnected | `ERROR` | `504` | written |

#### Scenario: Client closed request on every engine
- **Given** engine `<engine>` and a route `GET /x` whose handler returns `fmt.Errorf("query: %w", context.Canceled)` after its client disconnected
- **When** the request ends
- **Then** the access record is `http.request.failed` at `<level>` with `problem.status` `<status>`

##### Examples:
| engine | level | status |
|--------|-------|--------|
| `net/http` | `WARN` | `499` |
| `gin` | `WARN` | `499` |
| `fiber` | `ERROR` | `500` (fasthttp does not detect the disconnect, so the request context stays live) |
