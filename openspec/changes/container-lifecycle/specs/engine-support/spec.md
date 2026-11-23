# Spec Delta

## MODIFIED Requirements

### Requirement: Engines share one lifecycle state machine
Every engine SHALL implement `Start(ctx)`, `Run(ctx)`, `Wait()`, `HealthCheck(ctx)`, `Shutdown(ctx)`, `Ready()` and `Addr()` with the core's states and errors: `Start` returns once serving, single-use `Start` and `Run`, idempotent `Shutdown`, `srvr.ErrServerStarted`, `srvr.ErrServerClosed` and `srvr.ErrServerNotStarted` on misuse, readiness only after the listener is bound.

#### Scenario: Lifecycle misuse on every engine
- **Given** a `<engine>` server that `<state>`
- **When** the consumer calls `<call>`
- **Then** the call returns `<result>`

##### Examples:
| engine | state | call | result |
|--------|-------|------|--------|
| `gin` | is running | `Start(ctx)` | an error matching `srvr.ErrServerStarted` |
| `fiber` | is running | `Start(ctx)` | an error matching `srvr.ErrServerStarted` |
| `gin` | is running | `Run(ctx)` | an error matching `srvr.ErrServerStarted` |
| `fiber` | is running | `Run(ctx)` | an error matching `srvr.ErrServerStarted` |
| `gin` | was never started | `Shutdown(ctx)` | `nil` |
| `fiber` | was never started | `Shutdown(ctx)` | `nil` |
| `gin` | was never started | `Wait()` | an error matching `srvr.ErrServerNotStarted` |
| `fiber` | was never started | `HealthCheck(ctx)` | an error matching `srvr.ErrServerNotStarted` |
| `gin` | has stopped | `Start(ctx)` | an error matching `srvr.ErrServerClosed` |
| `fiber` | has stopped | `Start(ctx)` | an error matching `srvr.ErrServerClosed` |
| `gin` | has stopped | `HealthCheck(ctx)` | an error matching `srvr.ErrServerClosed` |
| `gin` | is running | `Register(c)` | an error matching `srvr.ErrServerStarted` |
| `fiber` | has stopped | `Register(c)` | an error matching `srvr.ErrServerClosed` |
| `fiber` | is running | `Shutdown(nil)` | an error wrapping `srvr.ErrConfig` and the server keeps serving |
| `gin` | is running | `HealthCheck(nil)` | an error wrapping `srvr.ErrConfig` |

### Requirement: Shutdown hooks run on every engine's stop paths
Every engine SHALL run the core's `srvr.WithShutdownHook` hooks exactly as net/http does: `BeforeDrain` hooks after accepting stops and before draining, `AfterDrain` hooks after draining, once per server on every stop path. A `Start` failure after the server left its initial state SHALL run only `AfterDrain` hooks; `Start(nil)`, `srvr.ErrServerStarted` and `srvr.ErrServerClosed` SHALL run none.

#### Scenario: Hooks per engine and stop path
- **Given** a `<engine>` server with one `BeforeDrain` hook and one `AfterDrain` hook, each recording its call
- **When** `<stop>`
- **Then** the recorded calls are `<calls>`

##### Examples:
| engine | stop | calls |
|--------|------|-------|
| `gin` | the `Run` context is cancelled while serving | `BeforeDrain`, `AfterDrain` |
| `fiber` | the `Run` context is cancelled while serving | `BeforeDrain`, `AfterDrain` |
| `gin` | the `Start` context is cancelled after `Start` returned, then `Shutdown(ctx)` | `BeforeDrain`, `AfterDrain` (from `Shutdown` only) |
| `fiber` | the passed listener's `Accept` returns a permanent error after `Start` returned | `BeforeDrain`, `AfterDrain` |
| `gin` | `Shutdown(ctx)` is called while serving, then again | `BeforeDrain`, `AfterDrain` |
| `fiber` | `Shutdown(ctx)` is called while serving, then again | `BeforeDrain`, `AfterDrain` |
| `fiber` | `Shutdown(ctx)` with a `50ms` deadline while a request needs `5s` | `BeforeDrain`, `AfterDrain` |
| `gin` | `Shutdown(ctx)` on a server that was never started | `AfterDrain` |
| `fiber` | `Shutdown(ctx)` on a server that was never started | `AfterDrain` |
| `gin` | `Start` fails because the address is in use | `AfterDrain` |
| `fiber` | `Start` fails because the address is in use | `AfterDrain` |
| `fiber` | `Start` fails because a route check returns an error | `AfterDrain` |
| `gin` | `Start(nil)` is called, then `Shutdown(ctx)` | `AfterDrain` (from `Shutdown` only) |
| `fiber` | a second `Start` returns `srvr.ErrServerStarted`, then the first is stopped by `Shutdown(ctx)` | `BeforeDrain`, `AfterDrain` |
