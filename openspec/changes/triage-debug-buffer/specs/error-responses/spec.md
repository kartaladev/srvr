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
