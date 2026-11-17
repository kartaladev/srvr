# Spec Delta

## Purpose

Defines how every failure a srvr server answers becomes an RFC 9457 problem rendered by prblm, so clients get one consistent error format that never leaks internals.

## ADDED Requirements

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
The consumer SHALL be able to pass prblm options (mappers, enrichers, namespace, types) and an error hook. srvr's own hook, which records the error for the request's access record, SHALL always run, and the consumer's hook SHALL run after it. A panic in the consumer's hook SHALL NOT affect the response.

#### Scenario: Consumer mapper and hook
- **Given** a server configured with `<option>`
- **When** a handler returns `<error>`
- **Then** the response status is `<status>`
- **And** the consumer hook `<hook>`

##### Examples:
| option | error | status | hook |
|--------|-------|--------|------|
| `WithProblemOptions(prblm.WithMappers(prblm.Status(ErrQuota, 429)))` | `ErrQuota` | `429` | (none) |
| `WithErrorHook(fn)` | `prblm.ErrNotFound` | `404` | receives the error and a problem with status `404` |
| `WithErrorHook(fn)` where `fn` panics | `prblm.ErrNotFound` | `404` | panics and the access record is still written |
| `WithErrorHook(nil)` | (none) | (none) | construction returns an error wrapping `srvr.ErrConfig` |
| `WithProblemOptions(prblm.WithNamespace("not a uri"))` | (none) | (none) | construction returns an error wrapping `srvr.ErrConfig` naming `ProblemOptions` |
| `WithProblemOptions(opt)` where `opt` panics with a value whose `Error()` panics with itself | (none) | (none) | construction returns an error wrapping `srvr.ErrConfig` naming `ProblemOptions`, without panicking |
