# Spec Delta

## MODIFIED Requirements

### Requirement: Engines share one per-request state
Every engine SHALL keep one `engine.RequestState` per request, reachable through `engine.StateFrom(ctx)` from any stage inside srvr's pipeline, holding the matched route, whether a route matched, the response status, the failure (returned error or recovered panic), the trace context and the trace an observer's trace source supplied, which `srvr.TraceFromContext`, request loggers, the problem `trace_id` and the access record read in place of the trace context when it is valid. Later stages SHALL read and extend this one state instead of adding their own per-request holders.

#### Scenario: Request state after the chain
- **Given** engine `<engine>` with route `GET /users/{id}` in that engine's syntax
- **When** a client sends `<request>` and srvr's access stage reads the state after the chain returns
- **Then** the state has route `<route>`, matched `<matched>`, status `<status>` and failed `<failed>`

##### Examples:
| engine | request | route | matched | status | failed |
|--------|---------|-------|---------|--------|--------|
| `net/http` | `GET /users/42` | `/users/{id}` | `true` | `200` | `false` |
| `gin` | `GET /users/42` | `/users/:id` | `true` | `200` | `false` |
| `fiber` | `GET /users/42` | `/users/:id` | `true` | `200` | `false` |
| `net/http` | `GET /nope` | (empty) | `false` | `404` | `true` |
| `gin` | `GET /nope` | (empty) | `false` | `404` | `true` |
| `fiber` | `GET /nope` | (empty) | `false` | `404` | `true` |
| `gin` | `DELETE /users/42` | (empty) | `false` | `405` | `true` |
| `fiber` | `DELETE /users/42` | (empty) | `false` | `405` | `true` |
| `fiber` | `GET /users/42` with a handler returning `errors.New("boom")` | `/users/:id` | `true` | `500` | `true` |

#### Scenario: No request state outside a request
- **Given** a context `<ctx>`
- **When** the consumer calls `engine.StateFrom` with it
- **Then** it returns `<result>`

##### Examples:
| ctx | result |
|-----|--------|
| `context.Background()` | `nil` and `false` |
| `nil` | `nil` and `false` |
| a request context inside srvr's pipeline on any engine | that request's state and `true` |
