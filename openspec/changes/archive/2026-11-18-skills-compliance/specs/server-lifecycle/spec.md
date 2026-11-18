# Spec Delta

## ADDED Requirements

### Requirement: Shutdown interrupts a starting server
`Shutdown` called while `Start` is still configuring routes, running route checks or binding SHALL cancel the context those steps receive, so `Start` returns `srvr.ErrServerClosed` without binding, `AfterDrain` hooks run once, and the server ends closed on every engine.

#### Scenario: Shutdown during the configure phase
- **Given** engine `<engine>` whose `<step>` waits on its context and `Start(context.Background())` running
- **When** `Shutdown` is called with a `1s` context while `<step>` is waiting
- **Then** `Start` returns `<start>` and `Shutdown` returns `<shutdown>` within `1s`
- **And** a second `Start` returns an error wrapping `srvr.ErrServerClosed`

##### Examples:
| engine | step | start | shutdown |
|--------|------|-------|----------|
| `net/http` | route check | an error wrapping `srvr.ErrServerClosed` | `nil` |
| `gin` | route check | an error wrapping `srvr.ErrServerClosed` | `nil` |
| `fiber` | route check | an error wrapping `srvr.ErrServerClosed` | `nil` |
| `net/http` | route check that ignores its context and blocks for `2s` | an error wrapping `srvr.ErrServerClosed` once the check returns | `context.DeadlineExceeded` |

##### Examples: misuse around the interruption
| engine | step | start | shutdown |
|--------|------|-------|----------|
| `net/http` | (none: `Shutdown` before `Start`) | an error wrapping `srvr.ErrServerClosed`, no listener bound | `nil` |
| `net/http` | route check, with `Shutdown` called twice concurrently | an error wrapping `srvr.ErrServerClosed` | `nil` for both calls |

### Requirement: Consumer code ending its goroutine is contained
A shutdown hook or route check that ends its goroutine with `runtime.Goexit` (for example `t.FailNow` in a test) SHALL be treated like one that panicked: it is reported once as failed and SHALL NOT make `Start` or `Shutdown` wait forever, even with an unbounded hook budget.

#### Scenario: Goexit in consumer code
- **Given** engine `net/http` with `<code>` calling `runtime.Goexit` and `ShutdownHookTimeout` `<budget>`
- **When** the server `<action>`
- **Then** the call returns within `1s` with `<result>`

##### Examples:
| code | budget | action | result |
|------|--------|--------|--------|
| a route check | (default) | starts with `context.Background()` | an error wrapping `srvr.ErrRouteConfig` |
| a `BeforeDrain` hook | (default) | is shut down | an error wrapping `srvr.ErrShutdownHook` and one `server.shutdown_hook.failed` record |
| an `AfterDrain` hook | `-1` (unbounded) | is shut down | an error wrapping `srvr.ErrShutdownHook` and one `server.shutdown_hook.failed` record |
| a `BeforeDrain` hook that returns `nil` | (default) | is shut down | `nil` and no `server.shutdown_hook.failed` record |

### Requirement: The serve goroutine has ended when the server closes
When `Start` or `Shutdown` reports the server closed, srvr's goroutine serving the listener SHALL have returned, or, if a consumer listener's `Close` never unblocks `Accept`, SHALL be abandoned only after a bounded wait of `1s`.

#### Scenario: Serve goroutine after stop
- **Given** engine `net/http` serving `<listener>`
- **When** `Shutdown` returns
- **Then** the serve goroutine `<state>`

##### Examples:
| listener | state |
|----------|-------|
| its own `:0` listener | has returned (goleak reports no `serveOn` goroutine) |
| a `WithListener` listener | has returned |
| a `WithListener` listener whose `Close` does not unblock `Accept` | is still blocked, and `Shutdown` returned within `1s` plus the drain time |
