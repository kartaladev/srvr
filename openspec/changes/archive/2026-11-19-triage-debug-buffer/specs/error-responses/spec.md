# Spec Delta

## ADDED Requirements

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

## MODIFIED Requirements

### Requirement: Consumers extend error rendering
The consumer SHALL be able to pass prblm options (mappers, enrichers, namespace, types and prblm's own error hooks, `prblm.OnError` and `prblm.AddErrorHook`) and an error hook through `WithErrorHook`. srvr's own hook, which records the error for the request's access record, SHALL always run: srvr SHALL add it with `prblm.AddErrorHook`, so no consumer prblm option replaces or removes it, and a consumer's `prblm.OnError` SHALL run too. prblm calls the consumer's `prblm.OnError` first, then the hooks added with `prblm.AddErrorHook` in order, srvr's last; srvr's hook calls the `WithErrorHook` hook after recording. A panic in any consumer hook SHALL NOT affect the response or the access record.

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

#### Scenario: Consumer prblm error hooks
- **Given** a net/http server configured with `<option>` and a route `GET /x` whose handler returns `prblm.ErrNotFound`
- **When** a client sends `GET /x`
- **Then** the response is a `404` problem
- **And** `<hooks>`
- **And** the access record is `http.request.failed` with `problem.status` `404`

##### Examples:
| option | hooks |
|--------|-------|
| (none) | no consumer hook runs |
| `WithProblemOptions(prblm.OnError(fn))` | `fn` receives `prblm.ErrNotFound` and a problem with status `404`, once |
| `WithProblemOptions(prblm.OnError(fn))` where `fn` panics | `fn` panics once |
| `WithProblemOptions(prblm.OnError(fn))`, `WithErrorHook(gn)` | `fn` and then `gn` each receive `prblm.ErrNotFound` and a problem with status `404`, once |
| `WithProblemOptions(prblm.AddErrorHook(fn))` | `fn` receives `prblm.ErrNotFound` and a problem with status `404`, once |
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
