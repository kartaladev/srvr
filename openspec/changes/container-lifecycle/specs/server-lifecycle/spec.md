# Spec Delta

## ADDED Requirements

### Requirement: Start returns once the server is serving
`Start(ctx)` SHALL configure routes, run route checks, bind the listener, close `Ready()` and return `nil` while the server keeps serving. Its context SHALL bound startup only: cancelling it or letting its deadline pass after `Start` returned SHALL NOT stop the server. Startup failures are returned as before.

#### Scenario: Start returns while serving
- **Given** engine `<engine>` with one route `GET /ping` and `Start(ctx)` returned `nil`
- **When** `<after>` happens
- **Then** `GET /ping` answers `<status>`
- **And** `Ready()` is closed and `HealthCheck` returns `nil`

##### Examples:
| engine | after | status |
|--------|-------|--------|
| `net/http` | (nothing) | `200` |
| `gin` | (nothing) | `200` |
| `fiber` | (nothing) | `200` |
| `net/http` | the `Start` context is cancelled | `200` |
| `net/http` | the `Start` context's `50ms` deadline passes | `200` |
| `gin` | the `Start` context is cancelled | `200` |
| `fiber` | the `Start` context is cancelled | `200` |

#### Scenario: Start bounded by its context
- **Given** engine `net/http` whose route check `<check>`
- **When** `Start` is called with a context `<ctx>`
- **Then** `Start` returns `<result>` within `1s`
- **And** `Ready()` `<ready>`

##### Examples:
| check | ctx | result | ready |
|-------|-----|--------|-------|
| returns `nil` at once | without deadline | `nil` | is closed |
| waits on its context | with a `50ms` deadline | an error matching `context.DeadlineExceeded` | is never closed |
| waits on its context | cancelled after `50ms` | an error matching `context.Canceled` | is never closed |
| returns `nil` at once | `nil` | an error wrapping `srvr.ErrConfig` | is never closed |

### Requirement: Run serves until its context ends
`Run(ctx)` SHALL start the server as `Start` does and then serve until `ctx` is done, `Shutdown` is called or serving fails; it SHALL then stop gracefully within `ShutdownTimeout` and return what `Wait` returns. A startup failure SHALL be returned as `Start` returns it, without serving.

#### Scenario: Run and stop by context
- **Given** engine `<engine>` running `Run(ctx)` with one route `GET /ping`
- **When** `<trigger>` happens
- **Then** `Run` returns `<result>`
- **And** a request in flight at that moment `<in_flight>`

##### Examples:
| engine | trigger | result | in_flight |
|--------|---------|--------|-----------|
| `net/http` | the `Run` context is cancelled | `nil` | completes with status `200` |
| `net/http` | `Shutdown(ctx)` is called from another goroutine | `nil` | completes with status `200` |
| `net/http` | the `Run` context deadline passes | `nil` | completes with status `200` |
| `gin` | the `Run` context is cancelled | `nil` | completes with status `200` |
| `fiber` | the `Run` context is cancelled | `nil` | completes with status `200` |
| `net/http` | the passed listener's `Accept` returns a permanent error `E` | an error matching `E` | completes with status `200` |

#### Scenario: Run fails to start
- **Given** engine `net/http` where `<condition>`
- **When** `Run` is called
- **Then** `Run` returns an error matching `<error>` without serving

##### Examples:
| condition | error |
|-----------|-------|
| the address is already in use | `*net.OpError` |
| a route configurer panics | `srvr.ErrRouteConfig` |
| the `Run` context is already cancelled | `context.Canceled` |
| `Run(nil)` is called | `srvr.ErrConfig` |
| `Shutdown` was called before `Run` | `srvr.ErrServerClosed` |

### Requirement: Wait reports how serving ended
`Wait()` SHALL block until a server whose `Start` returned `nil` has stopped, then return the serve failure joined with shutdown-hook errors, or `nil`. Every caller, concurrent or later, SHALL get the same result. On a server never started and not closed it SHALL return `srvr.ErrServerNotStarted` at once; after a failed `Start` or a `Shutdown` before `Start` it SHALL return `nil` at once.

#### Scenario: Wait after Start
- **Given** engine `<engine>` whose `Start` returned `nil`
- **When** `<stop>` and `Wait` is called `<callers>`
- **Then** every `Wait` call returns `<result>`

##### Examples:
| engine | stop | callers | result |
|--------|------|---------|--------|
| `net/http` | `Shutdown(ctx)` returns `nil` | once | `nil` |
| `net/http` | `Shutdown(ctx)` is called | from `3` goroutines before the stop | `nil` |
| `net/http` | `Shutdown(ctx)` returns `nil` | twice after the stop | `nil` both times |
| `net/http` | `Shutdown(ctx)` is called and an `AfterDrain` hook returns an error | once | an error wrapping `srvr.ErrShutdownHook` |
| `net/http` | the passed listener's `Accept` returns a permanent error | once | that error |
| `gin` | `Shutdown(ctx)` returns `nil` | once | `nil` |
| `fiber` | `Shutdown(ctx)` returns `nil` | once | `nil` |

#### Scenario: Wait misuse
- **Given** a `net/http` server that `<state>`
- **When** `Wait` is called
- **Then** it returns `<result>` within `100ms`

##### Examples:
| state | result |
|-------|--------|
| was never started | an error matching `srvr.ErrServerNotStarted` |
| is starting in another goroutine whose route check then fails | `nil` once that `Start` returned |
| failed to bind in `Start` | `nil` |
| was shut down before `Start` | `nil` |
| was started with `Run` and its context was cancelled | `nil` |

### Requirement: A serve failure after Start is reported
When serving fails or ends on its own after `Start` returned (no `Shutdown` and no `Run` context end), the server SHALL stop in order (hooks run, connections drain within `ShutdownTimeout`), log one `server.failed` record, and report the failure through `Wait` and `HealthCheck`. A later `Shutdown` SHALL return `nil` and a later `Start` SHALL return `srvr.ErrServerClosed`.

#### Scenario: Listener fails while serving
- **Given** engine `<engine>` serving on a passed listener whose `Accept` `<fault>` after `Start` returned
- **When** the failure happens
- **Then** `Wait` returns an error matching `<error>` and `HealthCheck` returns an error matching `<error>` and `srvr.ErrServerClosed`
- **And** one `server.failed` record is written and the `AfterDrain` hook ran `1` time

##### Examples:
| engine | fault | error |
|--------|-------|-------|
| `net/http` | returns a permanent error `E` | `E` |
| `net/http` | panics with `boom` | an error containing `boom` |
| `gin` | returns a permanent error `E` | `E` |
| `fiber` | returns a permanent error `E` | `E` |
| `fiber` | returns `net.ErrClosed` although the listener was not closed | `srvr.ErrServerClosed` (serving ended without a stop) |
| `net/http` | returns `net.ErrClosed` although the listener was not closed | `net.ErrClosed` |

#### Scenario: Shutdown during the stop a serve failure began
- **Given** engine `net/http` serving after `Start` with a `BeforeDrain` hook that blocks for `100ms`, and the passed listener's `Accept` has just returned a permanent error `E`
- **When** `Shutdown` is called with a `1s` context while that hook runs
- **Then** `Shutdown` returns `<shutdown>` after the stop finished and `Wait` returns `<wait>`
- **And** the `BeforeDrain` hook ran `<runs>` time

##### Examples:
| shutdown | wait | runs |
|----------|------|------|
| `nil` | an error matching `E` | `1` |

### Requirement: Health is observable
`HealthCheck(ctx)` SHALL return `nil` while the server is serving and an error otherwise, without blocking and without sending anything to clients. The error SHALL match `srvr.ErrServerNotStarted` before `Start`, `srvr.ErrServerClosed` while stopping or after the stop, and SHALL also wrap the serve failure when there was one.

#### Scenario: Health across the lifecycle
- **Given** engine `<engine>` whose server `<state>`
- **When** `HealthCheck(context.Background())` is called
- **Then** it returns `<result>` within `10ms`

##### Examples:
| engine | state | result |
|--------|-------|--------|
| `net/http` | was never started | an error matching `srvr.ErrServerNotStarted` |
| `net/http` | is starting, with a route check waiting | an error matching `srvr.ErrServerNotStarted` |
| `net/http` | is serving after `Start` | `nil` |
| `net/http` | is serving inside `Run` | `nil` |
| `net/http` | is draining a request after `Shutdown` began | an error matching `srvr.ErrServerClosed` |
| `net/http` | has stopped | an error matching `srvr.ErrServerClosed` |
| `net/http` | failed to bind in `Start` | an error matching `srvr.ErrServerClosed` |
| `gin` | is serving after `Start` | `nil` |
| `gin` | has stopped | an error matching `srvr.ErrServerClosed` |
| `fiber` | is serving after `Start` | `nil` |
| `fiber` | has stopped | an error matching `srvr.ErrServerClosed` |

#### Scenario: Health with a hostile context
- **Given** a `net/http` server that is serving after `Start`
- **When** `HealthCheck` is called with `<ctx>`
- **Then** it returns `<result>`

##### Examples:
| ctx | result |
|-----|--------|
| `nil` | an error wrapping `srvr.ErrConfig` |
| an already cancelled context | `nil` |
| a context whose `Err()` panics | `nil` |

### Requirement: Lifecycle methods match container contracts
Every engine's server SHALL have the methods lifecycle containers detect by shape: `Start(context.Context) error` that returns once serving, `Shutdown(context.Context) error` and `HealthCheck(context.Context) error`. srvr SHALL NOT import any container module to provide this.

#### Scenario: Container-shaped lifecycle
- **Given** engine `<engine>` and an interface declared in the test with `Start(context.Context) error`, `Shutdown(context.Context) error` and `HealthCheck(context.Context) error`
- **When** a container-like driver calls `Start`, then `HealthCheck`, then `Shutdown`, each with a `1s` context
- **Then** the server satisfies the interface, `Start` returns `nil` within `1s`, `HealthCheck` returns `nil`, `Shutdown` returns `nil`
- **And** the module's dependency list `<deps>`

##### Examples:
| engine | deps |
|--------|------|
| `net/http` | contains no container module (`go list -deps` has no `doger`, `samber/do`, `uber.org/fx`, `uber.org/dig`) |
| `gin` | contains no container module |
| `fiber` | contains no container module |

## MODIFIED Requirements

### Requirement: Shutdown hooks run once on every stop path
`WithShutdownHook(phase, fn)` SHALL run `fn` once when the server stops: `Run`'s context ending, `Shutdown`, serving failing after `Start`, or a `Start` failure after the server left its initial state (configure, route or bind failure, context already done). Only `AfterDrain` hooks run when nothing was served. `Start(nil)`, `ErrServerStarted` and `ErrServerClosed` run no hooks. `BeforeDrain` hooks run after the listener closes, in the drain budget; `AfterDrain` after draining, in `ShutdownHookTimeout`.

#### Scenario: Hook phases and order
- **Given** a server running `Run(ctx)` with hooks `A` and `B` at `srvr.BeforeDrain`, hook `C` at `srvr.AfterDrain`, and a request in flight that needs `100ms`
- **When** the `Run` context is cancelled
- **Then** the observed order is `<order>`

##### Examples:
| order |
|-------|
| new connections refused, `A`, `B`, the in-flight request completes with `200`, `C`, `Run` returns `nil` |

#### Scenario: Hooks on every stop path
- **Given** a server with one `BeforeDrain` hook and one `AfterDrain` hook that `<state>`
- **When** `<trigger>`
- **Then** the `BeforeDrain` hook ran `<before>` times and the `AfterDrain` hook ran `<after>` times

##### Examples:
| state | trigger | before | after |
|-------|---------|--------|-------|
| is running inside `Run` | the `Run` context is cancelled | `1` | `1` |
| is running inside `Run` | the `Run` context deadline passes | `1` | `1` |
| is running after `Start` | `Shutdown(ctx)` is called | `1` | `1` |
| is running after `Start` | the `Start` context is cancelled, counted `100ms` later and before any `Shutdown` | `0` | `0` |
| is running after `Start` | the passed listener's `Accept` returns a permanent error | `1` | `1` |
| is running inside `Run` | `Shutdown` is called twice, then the `Run` context is cancelled | `1` | `1` |
| is running inside `Run` | `Shutdown` and `Run` context cancellation happen concurrently | `1` | `1` |
| was never started | `Shutdown(ctx)` is called | `0` | `1` |
| cannot bind its address | `Start` is called | `0` | `1` |
| has a route configurer that panics | `Start` is called | `0` | `1` |
| has two routes with the same pattern | `Start` is called | `0` | `1` |
| is new | `Start` is called with an already cancelled context | `0` | `1` |
| is new | `Start(nil)` is called | `0` | `0` |
| is running after `Start` | `Start` is called again and returns `srvr.ErrServerStarted` | `0` | `0` |
| was stopped by cancelling its `Run` context | `Start` is called again and returns `srvr.ErrServerClosed` | `1` | `1` |
| has no hooks registered | `Shutdown(ctx)` is called | `0` | `0` |

### Requirement: Start is single-use
A server SHALL be started at most once. A second or concurrent `Start` SHALL return an error without affecting the running server.

#### Scenario: Start twice
- **Given** a server that `<state>`
- **When** `<call>` is called again
- **Then** that call returns an error matching `<error>`
- **And** the first server `<first>`

##### Examples:
| state | call | error | first |
|-------|------|-------|-------|
| is running after `Start` | `Start` | `srvr.ErrServerStarted` | keeps serving |
| is running after `Start` | `Run` | `srvr.ErrServerStarted` | keeps serving |
| is running inside `Run` | `Start` | `srvr.ErrServerStarted` | keeps serving |
| is starting in another goroutine at the same time | `Start` | `srvr.ErrServerStarted` | keeps serving |
| has stopped | `Start` | `srvr.ErrServerClosed` | stays stopped |
| has stopped | `Run` | `srvr.ErrServerClosed` | stays stopped |

### Requirement: Lifecycle events are logged
The server SHALL log `server.started` with the bound address, `server.stopping` when draining begins, `server.stopped` with the drain duration, `server.failed` with the error when `Start` fails or serving fails, and `server.shutdown_hook.failed` for each failing shutdown hook. These records SHALL carry service and build attributes and no `trace_id`.

#### Scenario: Lifecycle records
- **Given** a server with the default logger writing to a buffer
- **When** `<action>`
- **Then** a record with `msg` `<event>` and level `<level>` is written
- **And** it contains attribute `<attribute>`

##### Examples:
| action | event | level | attribute |
|--------|-------|-------|-----------|
| the server becomes ready | `server.started` | `INFO` | `server.address` |
| shutdown begins | `server.stopping` | `INFO` | `service.name` |
| shutdown completes | `server.stopped` | `INFO` | `srvr.drain.duration_ms` |
| binding fails | `server.failed` | `ERROR` | `exception.message` |
| serving fails because the passed listener's `Accept` panics | `server.failed` | `ERROR` | `exception.message` |
| serving fails after `Start` returned because the passed listener's `Accept` returns a permanent error | `server.failed` | `ERROR` | `exception.message` |
| the drain deadline passes | `server.stopped` | `WARN` | `srvr.drain.forced` |
| a shutdown hook returns an error | `server.shutdown_hook.failed` | `WARN` | `srvr.shutdown_hook.phase` |
| a shutdown hook panics | `server.shutdown_hook.failed` | `WARN` | `exception.message` |

## REMOVED Requirements

### Requirement: Start serves until its context ends
**Reason**: `Start` now returns once the server is serving so lifecycle containers can run it; blocking moved to `Run`.
**Migration**: Replace `srv.Start(ctx)` that is expected to block with `srv.Run(ctx)`, which keeps the removed contract (serve until `ctx` is done or `Shutdown`, drain, return `nil`). See "Run serves until its context ends".
