# Spec Delta

## Purpose

Defines how a srvr server is constructed, started, observed as ready and shut down, including safe defaults and the behaviour under lifecycle misuse.

## ADDED Requirements

### Requirement: Construction validates configuration
Constructing a server SHALL validate every option and config field and SHALL return an error naming the offending field and value instead of a server when any is invalid. Construction SHALL NOT open a listener.

#### Scenario: Valid and invalid configuration
- **Given** a server configured with `<field>` set to `<value>`
- **When** the consumer constructs the server
- **Then** construction returns `<outcome>`
- **And** the error message contains `<message>`

##### Examples: accepted
| field | value | outcome | message |
|-------|-------|---------|---------|
| (none) | (none) | a server | (none) |
| `Addr` | `127.0.0.1:0` | a server | (none) |
| `ReadHeaderTimeout` | `-1s` | a server | (none) |
| `MaxRequestBodyBytes` | `-1` | a server | (none) |

##### Examples: rejected
| field | value | outcome | message |
|-------|-------|---------|---------|
| `Addr` | `localhost:99999` | an error wrapping `srvr.ErrConfig` | `Addr` |
| `Addr` | `not an address` | an error wrapping `srvr.ErrConfig` | `Addr` |
| `ShutdownTimeout` | `-1s` | an error wrapping `srvr.ErrConfig` | `ShutdownTimeout` |
| `MaxHeaderBytes` | `0` via `WithMaxHeaderBytes(0)` | an error wrapping `srvr.ErrConfig` | `MaxHeaderBytes` |
| `WithListener` | `nil` | an error wrapping `srvr.ErrConfig` | `Listener` |
| `WithListener` | a typed-nil `*net.TCPListener` | an error wrapping `srvr.ErrConfig` | `Listener` |
| `WithListener` and `Addr` | a typed-nil `*net.TCPListener` and `:8080` | an error wrapping `srvr.ErrConfig`, without panicking | `Listener` |
| `WithListener` and `Addr` | a listener whose `Addr()` panics, and `:8080` | an error wrapping `srvr.ErrConfig`, without panicking | `Listener` |
| a nil `Option` | `srvr.New(nil)` | an error wrapping `srvr.ErrConfig` | `Option` |
| `WithListener` and `Addr` | both set | an error wrapping `srvr.ErrConfig` | `Listener` |
| `WithShutdownHook` | `srvr.BeforeDrain` with a nil function | an error wrapping `srvr.ErrConfig` | `ShutdownHook` |
| `WithShutdownHook` | phase `srvr.ShutdownPhase(9)` | an error wrapping `srvr.ErrConfig` | `ShutdownPhase` |

### Requirement: Safe default server limits
With no configuration the server SHALL listen on `:8080` and apply the default timeouts, header limit, request body limit and drain time. A zero config value SHALL mean the default; a negative timeout or body limit SHALL disable that limit.

#### Scenario: Effective limits
- **Given** a server configured with `<config>`
- **When** the server starts
- **Then** the effective `<setting>` is `<effective>`

##### Examples: defaults
| config | setting | effective |
|--------|---------|-----------|
| (none) | listen address | `:8080` |
| (none) | `ReadHeaderTimeout` | `10s` |
| (none) | `ReadTimeout` | `60s` |
| (none) | `WriteTimeout` | `60s` |
| (none) | `IdleTimeout` | `120s` |
| (none) | `MaxHeaderBytes` | `1048576` |
| (none) | `MaxRequestBodyBytes` | `4194304` |
| (none) | `ShutdownTimeout` | `15s` |
| (none) | `ShutdownHookTimeout` | `5s` |

##### Examples: overrides
| config | setting | effective |
|--------|---------|-----------|
| `WithReadHeaderTimeout(2*time.Second)` | `ReadHeaderTimeout` | `2s` |
| `Config{ReadHeaderTimeout: 0}` | `ReadHeaderTimeout` | `10s` |
| `Config{WriteTimeout: -1}` | `WriteTimeout` | no timeout |
| `Config{MaxRequestBodyBytes: -1}` | `MaxRequestBodyBytes` | unlimited |
| `Config{Addr: ":9090"}` | listen address | `:9090` |
| `Config{ShutdownHookTimeout: -1}` | `ShutdownHookTimeout` | no budget |

### Requirement: Options and config structs compose
Each config struct (`Config`, `LogConfig`, `AccessLogConfig`, `ServiceConfig`, `TraceConfig`) SHALL be accepted through a `With<Struct>` option and its fields through individual options. Options SHALL apply in argument order, the later one winning; a `With<Struct>` option SHALL set every field of its struct, a zero field meaning the default.

#### Scenario: Option and config precedence
- **Given** a server constructed with `<options>` in that order
- **When** the server starts
- **Then** the effective `<setting>` is `<effective>`

##### Examples:
| options | setting | effective |
|---------|---------|-----------|
| `WithConfig(Config{Addr: ":9090"})` | listen address | `:9090` |
| `WithConfig(Config{Addr: ":9090"})`, `WithAddr(":7070")` | listen address | `:7070` |
| `WithAddr(":7070")`, `WithConfig(Config{Addr: ":9090"})` | listen address | `:9090` |
| `WithAddr(":7070")`, `WithConfig(Config{})` | listen address | `:8080` |
| `WithLogConfig(LogConfig{Level: "debug"})`, `WithLogLevel(slog.LevelWarn)` | log level | `WARN` |
| `WithAccessLogSkipPaths("/healthz")`, `WithAccessLogConfig(AccessLogConfig{})` | access-log skip paths | (empty) |
| `WithServiceConfig(ServiceConfig{Name: "registration-api"})`, `WithServiceName("orders")` | `service.name` | `orders` |
| `WithTraceConfig(TraceConfig{IgnoreIncoming: true})` | incoming `traceparent` | ignored |

### Requirement: Start serves until its context ends
`Start(ctx)` SHALL bind the listener, signal readiness, serve requests and block until `ctx` is done, `Shutdown` is called or serving fails. When `ctx` is done it SHALL drain gracefully within the shutdown timeout and return nil.

#### Scenario: Start and stop by context
- **Given** a started server with one route `GET /ping`
- **When** `<trigger>` happens
- **Then** `Start` returns `<result>`
- **And** a request in flight at that moment `<in_flight>`

##### Examples:
| trigger | result | in_flight |
|---------|--------|-----------|
| the `Start` context is cancelled | `nil` | completes with status `200` |
| `Shutdown(ctx)` is called from another goroutine | `nil` | completes with status `200` |
| the `Start` context deadline passes | `nil` | completes with status `200` |

### Requirement: Readiness is observable
The server SHALL expose `Ready()` returning a channel closed once the listener is bound, and `Addr()` returning the bound address after readiness. Before readiness `Addr()` SHALL return the empty string.

#### Scenario: Ephemeral port
- **Given** a server configured with `Addr` `<addr>`
- **When** the consumer waits on `Ready()` after calling `Start`
- **Then** `Addr()` returns `<bound>`

##### Examples:
| addr | bound |
|------|-------|
| `127.0.0.1:0` | `127.0.0.1:<port>` where `<port>` is not `0` |
| `127.0.0.1:18080` | `127.0.0.1:18080` |

#### Scenario: Address before readiness
- **Given** a constructed server that `<state>`
- **When** the consumer calls `Addr()`
- **Then** it returns `<addr>`

##### Examples:
| state | addr |
|-------|------|
| has not been started | (empty) |
| failed to bind | (empty) |

### Requirement: Start failures are returned, not hidden
`Start` SHALL return an error, without signalling readiness, when the listener cannot be bound or route configuration fails. Route configuration SHALL run before binding so a broken configuration never accepts a connection. A `Start` context that is already done SHALL fail `Start` with its `Err()` value even when that value is a typed nil or has an `Error()` method that panics: srvr reads its text without panicking.

#### Scenario: Start fails
- **Given** a server where `<condition>`
- **When** `Start` is called
- **Then** `Start` returns an error matching `<error>`
- **And** `Ready()` `<ready>`

##### Examples:
| condition | error | ready |
|-----------|-------|-------|
| the address is already in use | `*net.OpError` | is never closed |
| a route configurer panics | `srvr.ErrRouteConfig` | is never closed |
| two routes register the same pattern | `srvr.ErrRouteConfig` | is never closed |
| the `Start` context is already cancelled | `context.Canceled` | is never closed |
| the `Start` context is done and its `Err()` returns an error whose `Error()` panics | that error | is never closed |
| the `Start` context is done and its `Err()` returns a typed-nil error | that error | is never closed |
| `Start(nil)` is called | `srvr.ErrConfig` | is never closed |

### Requirement: A passed listener is owned by the server
A listener passed with `WithListener` SHALL be owned by the server once `New` returns one (if `New` fails, the caller keeps it) and SHALL be closed when the server closes: after serving, on every `Start` failure after the server left its initial state, and on `Shutdown` before `Start`. `Start(nil)` and `ErrServerStarted` SHALL NOT close it; `ErrServerClosed` does not close it (it was closed when the server closed).

#### Scenario: A passed listener is owned by the server
- **Given** a server configured with `WithListener(ln)` where `<condition>`
- **When** `<call>` returns
- **Then** `ln` `<listener>`

##### Examples:
| condition | call | listener |
|-----------|------|----------|
| a route configurer panics | `Start`, with an error matching `srvr.ErrRouteConfig` | is closed and refuses connections |
| two routes register the same pattern | `Start`, with an error matching `srvr.ErrRouteConfig` | is closed and refuses connections |
| the `Start` context is already cancelled | `Start`, with an error matching `context.Canceled` | is closed and refuses connections |
| `Shutdown` was called before `Start` | `Shutdown`, with `nil` | is closed and refuses connections |
| the server served and was shut down | `Start`, with `nil` | is closed and refuses connections |
| `Start(nil)` is called | `Start`, with an error matching `srvr.ErrConfig` | still accepts connections (nothing changed; the server can still start) |
| `New` failed because another option is invalid | `New`, with an error wrapping `srvr.ErrConfig` | still accepts connections (the caller keeps it) |

### Requirement: A listener without a usable address fails Start
A typed-nil listener SHALL fail construction like a nil one. A listener whose `Addr()` returns nil (or a typed nil) or panics, or whose address's `String()` panics, SHALL fail `Start` with an error wrapping `srvr.ErrConfig` that names `Listener`, through the same path as any other `Start` failure (`server.failed`, `AfterDrain` hooks, the listener closed), never silently and never leaving the server unable to stop.

#### Scenario: A listener without a usable address
- **Given** a server configured with `WithListener(ln)` where `ln.Addr()` `<fault>`
- **When** `Start` is called
- **Then** `Start` returns an error wrapping `srvr.ErrConfig` whose message contains `Listener`, without panicking, and `Ready()` is never closed
- **And** `ln` is closed and refuses connections, and a `server.failed` record is written
- **And** a later `Shutdown` returns `nil`, `Addr()` returns the empty string and `Register` returns an error matching `srvr.ErrServerClosed`, each without blocking

##### Examples:
| fault |
|-------|
| returns `nil` |
| returns a typed-nil `*net.TCPAddr` |
| panics with `"addr"` |
| returns an address whose `String()` panics with a value whose `Error()` panics with itself |

### Requirement: Shutdown is graceful, bounded and idempotent
`Shutdown(ctx)` SHALL stop accepting connections, wait for in-flight requests until `ctx` is done, then close remaining connections and return the context error. Repeated calls SHALL be safe and return nil once shut down.

#### Scenario: Drain within and beyond the deadline
- **Given** a started server with a request in flight that needs `<work>` to finish
- **When** `Shutdown` is called with a deadline of `<deadline>`
- **Then** `Shutdown` returns `<result>`
- **And** the in-flight request `<outcome>`

##### Examples:
| work | deadline | result | outcome |
|------|----------|--------|---------|
| `10ms` | `1s` | `nil` | completes with status `200` |
| `5s` | `50ms` | an error matching `context.DeadlineExceeded` | has its connection closed |
| (none) | `1s` | `nil` | (none) |

#### Scenario: Shutdown misuse
- **Given** a server that `<state>`
- **When** `Shutdown` is called
- **Then** `Shutdown` returns `<result>`
- **And** a later `Start` returns `<start>`

##### Examples:
| state | result | start |
|-------|--------|-------|
| was never started | `nil` | an error matching `srvr.ErrServerClosed` |
| was already shut down | `nil` | an error matching `srvr.ErrServerClosed` |
| is being shut down by another goroutine | `nil` after that shutdown finishes | an error matching `srvr.ErrServerClosed` |

#### Scenario: Shutdown with a nil context
- **Given** a started server
- **When** `Shutdown(nil)` is called
- **Then** `Shutdown` returns `<result>`

##### Examples:
| result |
|--------|
| an error wrapping `srvr.ErrConfig` and the server keeps serving |

### Requirement: Shutdown hooks run once on every stop path
`WithShutdownHook(phase, fn)` SHALL run `fn` once when the server stops: `Start`'s context ending, `Shutdown`, or a `Start` failure after the server left its initial state (configure, route or bind failure, context already done). Only `AfterDrain` hooks run when nothing was served. `Start(nil)`, `ErrServerStarted` and `ErrServerClosed` run no hooks. `BeforeDrain` hooks run after the listener closes, in the drain budget; `AfterDrain` after draining, in `ShutdownHookTimeout`.

#### Scenario: Hook phases and order
- **Given** a started server with hooks `A` and `B` at `srvr.BeforeDrain`, hook `C` at `srvr.AfterDrain`, and a request in flight that needs `100ms`
- **When** the `Start` context is cancelled
- **Then** the observed order is `<order>`

##### Examples:
| order |
|-------|
| new connections refused, `A`, `B`, the in-flight request completes with `200`, `C`, `Start` returns `nil` |

#### Scenario: Hooks on every stop path
- **Given** a server with one `BeforeDrain` hook and one `AfterDrain` hook that `<state>`
- **When** `<trigger>`
- **Then** the `BeforeDrain` hook ran `<before>` times and the `AfterDrain` hook ran `<after>` times

##### Examples:
| state | trigger | before | after |
|-------|---------|--------|-------|
| is running | the `Start` context is cancelled | `1` | `1` |
| is running | the `Start` context deadline passes | `1` | `1` |
| is running | `Shutdown(ctx)` is called | `1` | `1` |
| is running | `Shutdown` is called twice, then the `Start` context is cancelled | `1` | `1` |
| is running | `Shutdown` and `Start` context cancellation happen concurrently | `1` | `1` |
| was never started | `Shutdown(ctx)` is called | `0` | `1` |
| cannot bind its address | `Start` is called | `0` | `1` |
| has a route configurer that panics | `Start` is called | `0` | `1` |
| has two routes with the same pattern | `Start` is called | `0` | `1` |
| is new | `Start` is called with an already cancelled context | `0` | `1` |
| is new | `Start(nil)` is called | `0` | `0` |
| is running | `Start` is called again and returns `srvr.ErrServerStarted` | `0` | `0` |
| was stopped by cancelling its `Start` context | `Start` is called again and returns `srvr.ErrServerClosed` | `1` | `1` |
| has no hooks registered | `Shutdown(ctx)` is called | `0` | `0` |

### Requirement: Faulty shutdown hooks never stop the shutdown
A hook that returns an error (a typed nil or one whose `Error()` panics included), panics (with any value) or outlives its budget SHALL be logged without panicking and SHALL NOT prevent the remaining hooks, the drain or the server closing. The call that stopped the server (`Start` or `Shutdown`) SHALL return an error wrapping `srvr.ErrShutdownHook` joined with each hook's error and any drain error.

#### Scenario: Faulty hooks
- **Given** a running server whose first `AfterDrain` hook `<fault>` and whose second `AfterDrain` hook succeeds
- **When** the `Start` context is cancelled
- **Then** `Start` returns `<result>`
- **And** the second hook ran
- **And** a later `Shutdown` returns `nil`

##### Examples:
| fault | result |
|-------|--------|
| returns `errors.New("flush failed")` | an error matching `srvr.ErrShutdownHook` whose message contains `flush failed` |
| panics with `"boom"` | an error matching `srvr.ErrShutdownHook` whose message contains `boom` |
| blocks and ignores its context, with `ShutdownHookTimeout` `50ms` | an error matching `srvr.ErrShutdownHook` and `context.DeadlineExceeded`, returned within `200ms` |
| returns a typed-nil error `(*hookErr)(nil)` | an error matching `srvr.ErrShutdownHook` |
| returns an error whose `Error()` panics | an error matching `srvr.ErrShutdownHook` |
| returns an error whose `Error()` panics with a value whose `Error()` panics | an error matching `srvr.ErrShutdownHook` |
| panics with a value whose `Error()` panics with itself | an error matching `srvr.ErrShutdownHook` whose message contains `<panic formatting value>` |
| returns `nil` | `nil` |

#### Scenario: Hostile hook errors when Shutdown is called before Start
- **Given** a server that was never started, whose `AfterDrain` hook `<fault>`
- **When** `Shutdown` is called
- **Then** `Shutdown` returns an error matching `srvr.ErrShutdownHook` without panicking
- **And** a second `Shutdown` returns `nil` and a later `Start` returns an error matching `srvr.ErrServerClosed`

##### Examples:
| fault |
|-------|
| returns a typed-nil error `(*hookErr)(nil)` |
| returns an error whose `Error()` panics |
| returns an error whose `Error()` panics with a value whose `Error()` panics |
| panics with a value whose `Error()` panics with itself |

#### Scenario: Faulty hook during Shutdown
- **Given** a running server whose `BeforeDrain` hook returns `errors.New("close failed")`
- **When** `Shutdown` is called with a deadline of `<deadline>` while a request needing `<work>` is in flight
- **Then** `Shutdown` returns `<result>`

##### Examples:
| deadline | work | result |
|----------|------|--------|
| `1s` | `10ms` | an error matching `srvr.ErrShutdownHook` |
| `50ms` | `5s` | an error matching both `srvr.ErrShutdownHook` and `context.DeadlineExceeded` |

#### Scenario: Blocking and re-entrant hooks
- **Given** a running server with `ShutdownTimeout` `<drain>` and `ShutdownHookTimeout` `<budget>` whose hook `<hook>`
- **When** the `Start` context is cancelled
- **Then** `Start` returns `<result>` within `<within>`
- **And** `<after>`

##### Examples:
| drain | budget | hook | result | within | after |
|-------|--------|------|--------|--------|-------|
| `50ms` | `5s` | at `BeforeDrain` blocks and ignores its context | an error matching `srvr.ErrShutdownHook` and `context.DeadlineExceeded` | `300ms` | a `server.shutdown_hook.failed` record is written and the `AfterDrain` hook ran once |
| `15s` | `100ms` | at `AfterDrain` calls `srv.Shutdown(context.Background())` | an error matching `srvr.ErrShutdownHook` and `context.DeadlineExceeded` | `500ms` | the hook's `Shutdown` call returned `nil` after the stop finished, with no deadlock |

### Requirement: A hostile Shutdown context never breaks the stop sequence
The error of a `BeforeDrain` hook abandoned at its budget carries the hook context's `Err()`, which comes from the consumer's `Shutdown` context. srvr SHALL read its text without panicking, even when it is a typed nil or its `Error()` panics, so the stop sequence always closes the server.

#### Scenario: A Shutdown context with a hostile Err
- **Given** a running server whose `BeforeDrain` hook blocks and ignores its context
- **When** `Shutdown` is called with a context that is done after `50ms` and whose `Err()` returns `<err>`
- **Then** `Shutdown` returns an error matching `srvr.ErrShutdownHook` and `<err>`, without panicking
- **And** a later `Shutdown` returns `nil`

##### Examples:
| err |
|-----|
| an error whose `Error()` panics with a value whose `Error()` panics |
| a typed-nil error `(*hookErr)(nil)` |

### Requirement: Start is single-use
A server SHALL be started at most once. A second or concurrent `Start` SHALL return an error without affecting the running server.

#### Scenario: Start twice
- **Given** a server that `<state>`
- **When** `Start` is called again
- **Then** that call returns an error matching `<error>`
- **And** the first server `<first>`

##### Examples:
| state | error | first |
|-------|-------|-------|
| is running | `srvr.ErrServerStarted` | keeps serving |
| is starting in another goroutine at the same time | `srvr.ErrServerStarted` | keeps serving |
| has stopped | `srvr.ErrServerClosed` | stays stopped |

### Requirement: Lifecycle events are logged
The server SHALL log `server.started` with the bound address, `server.stopping` when draining begins, `server.stopped` with the drain duration, `server.failed` with the error when `Start` fails, and `server.shutdown_hook.failed` for each failing shutdown hook. These records SHALL carry service and build attributes and no `trace_id`.

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
| the drain deadline passes | `server.stopped` | `WARN` | `srvr.drain.forced` |
| a shutdown hook returns an error | `server.shutdown_hook.failed` | `WARN` | `srvr.shutdown_hook.phase` |
| a shutdown hook panics | `server.shutdown_hook.failed` | `WARN` | `exception.message` |

### Requirement: Lifecycle error text is redacted and limited
The `exception.message` of `server.failed` and `server.shutdown_hook.failed`, and of any other srvr lifecycle record carrying consumer error text, SHALL be built like a request failure record's: the guarded error text passed through the `WithErrorRedactor` redactor (a redactor that panics SHALL cause `[redaction failed]`, never the unredacted text) and then limited to 8 KiB, instead of `MaxAttrBytes`.

#### Scenario: Lifecycle error text is redacted and limited
- **Given** a server with the default logger writing to a buffer, configured with `<redactor>`
- **When** `<failure>`
- **Then** the `<event>` record's `exception.message` is `<message>`

##### Examples:
| redactor | failure | event | message |
|----------|---------|-------|---------|
| (none) | an `AfterDrain` hook returns `errors.New("close db: secret")` and `Shutdown` is called before `Start` | `server.shutdown_hook.failed` | `close db: secret` |
| `WithErrorRedactor(secretMasker)` | an `AfterDrain` hook returns `errors.New("close db: secret")` and `Shutdown` is called before `Start` | `server.shutdown_hook.failed` | `close db: ***` |
| `WithErrorRedactor(panicky)` | an `AfterDrain` hook returns `errors.New("close db: secret")` and `Shutdown` is called before `Start` | `server.shutdown_hook.failed` | `[redaction failed]` |
| `WithErrorRedactor(secretMasker)` | an `AfterDrain` hook panics with `"secret"` and `Shutdown` is called before `Start` | `server.shutdown_hook.failed` | `panic: ***` |
| `WithErrorRedactor(secretMasker)` | `Start` is called with a done context whose `Err()` returns `errors.New("stop: secret")` | `server.failed` | `stop: ***` |
| `WithErrorRedactor(panicky)` | `Start` is called with a done context whose `Err()` returns `errors.New("stop: secret")` | `server.failed` | `[redaction failed]` |
| (none) | an `AfterDrain` hook returns an error of 5000 bytes of `x` and `Shutdown` is called before `Start` | `server.shutdown_hook.failed` | 5000 bytes of `x` |
| (none) | an `AfterDrain` hook returns an error of 9000 bytes of `x` and `Shutdown` is called before `Start` | `server.shutdown_hook.failed` | 8192 bytes of `x` followed by `…[truncated]` |
| `WithErrorRedactor(expander)` returning 9000 bytes of `x` | an `AfterDrain` hook returns `errors.New("close db: secret")` and `Shutdown` is called before `Start` | `server.shutdown_hook.failed` | 8192 bytes of `x` followed by `…[truncated]` |
| (none) | `Start` is called with a done context whose `Err()` returns an error of 9000 bytes of `x` | `server.failed` | 8192 bytes of `x` followed by `…[truncated]` |
