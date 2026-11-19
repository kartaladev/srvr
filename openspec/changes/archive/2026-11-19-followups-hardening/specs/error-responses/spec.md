# Spec Delta

## MODIFIED Requirements

### Requirement: Consumers extend error rendering
The consumer SHALL be able to pass prblm options (mappers, enrichers, namespace, types, `prblm.OnError` and `prblm.AddErrorHook`) through `WithProblemOptions` and an error hook through `WithErrorHook`. srvr SHALL add its own hook, which records the error for the access record, with `prblm.AddErrorHook`, so no consumer option replaces or removes it. A panic in any consumer hook SHALL NOT affect the response or the access record.

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
- **And** the hooks that run do so in this order: the consumer's `prblm.OnError`, the hooks added with `prblm.AddErrorHook` in order, srvr's own hook, then the `WithErrorHook` hook

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
