# server-lifecycle Specification

## Purpose

Defines how a srvr server is constructed, started, observed as ready and shut down, including safe defaults and the behaviour under lifecycle misuse.

## Requirements

### Requirement: Construction validates configuration
Constructing a server SHALL validate every option and config field and SHALL return one error naming every offending field and its value instead of a server when any is invalid, problem options included. Construction SHALL NOT open a listener. A `*slog.Logger` passed with `WithLogger` whose `Handler()` is nil or a typed nil SHALL fail construction naming `Logger`, since srvr could not write a single record through it.

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
| `WithLogger` | a zero `&slog.Logger{}`, whose `Handler()` is nil | an error wrapping `srvr.ErrConfig` | `Logger` |
| `WithLogger` | `slog.New(h)` where `h` is a typed-nil `slog.Handler` | an error wrapping `srvr.ErrConfig` | `Logger` |
| `LogConfig.Level` and `MaxHeaderBytes` | `loud`, and `-1` via `WithMaxHeaderBytes(-1)` | one error wrapping `srvr.ErrConfig`, matching `errors.As` for each field | `Level` and `MaxHeaderBytes` |
| `LogConfig.Format` and `ShutdownTimeout` | `xml` and `-1s` | one error wrapping `srvr.ErrConfig`, matching `errors.As` for each field | `Format` and `ShutdownTimeout` |
| `LogConfig.Level` and `LogConfig.Format` | `loud` and `xml` | one error wrapping `srvr.ErrConfig` | `Level` and `Format` |
| `WithProblemOptions` and `ShutdownTimeout` | `prblm.WithNamespace("not a uri")` and `-1s` | one error wrapping `srvr.ErrConfig`, matching `errors.As` for each field | `ProblemOptions` and `ShutdownTimeout` |
| `WithProblemOptions` and `LogConfig.Level` | `prblm.WithNamespace("not a uri")` and `loud` | one error wrapping `srvr.ErrConfig`, matching `errors.As` for each field | `ProblemOptions` and `Level` |
| `WithProblemOptions` and `MaxHeaderBytes` | an option that panics with `boom`, and `-1` via `WithMaxHeaderBytes(-1)` | one error wrapping `srvr.ErrConfig`, matching `errors.As` for each field, without panicking | `ProblemOptions` and `MaxHeaderBytes` |
| `WithProblemOptions` | `prblm.WithNamespace("not a uri")` and no other invalid field | an error wrapping `srvr.ErrConfig` | `ProblemOptions` |

#### Scenario: Problem-option errors are joined on every engine
- **Given** engine `<engine>` configured with `WithProblemOptions(prblm.WithNamespace("not a uri"))` and `ShutdownTimeout` `-1s`
- **When** the consumer constructs the server
- **Then** construction returns one error wrapping `srvr.ErrConfig`
- **And** `errors.As` finds a `*srvr.ConfigError` with `Field` `<field>`

##### Examples:
| engine | field |
|--------|-------|
| `net/http` | `ProblemOptions` |
| `net/http` | `ShutdownTimeout` |
| `gin` | `ProblemOptions` |
| `gin` | `ShutdownTimeout` |
| `fiber` | `ProblemOptions` |
| `fiber` | `ShutdownTimeout` |

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

### Requirement: A passed listener whose Accept panics stops serving
srvr SHALL call a passed listener's `Accept`, which net/http calls on a goroutine of its own, under `recover`. A panic SHALL stop serving instead of crashing the process: `Start` returns an error wrapping `srvr.ErrConfig` that names `Listener` and contains the panic value read without panicking, writes `server.failed`, and stops the running server in order (stop accepting, `BeforeDrain` hooks, drain, `AfterDrain` hooks, listener closed).

#### Scenario: A listener whose Accept panics
- **Given** a server configured with `WithListener(ln)` where `ln.Accept()` panics with `<value>` after `<accepted>`
- **When** `Start` is called
- **Then** `Start` returns an error wrapping `srvr.ErrConfig` whose message contains `Listener` and `<text>`, and the process does not crash
- **And** a `server.failed` record whose `exception.message` contains `<text>` is written, followed by `server.stopped`
- **And** `ln` is closed, and a later `Shutdown` returns `nil` without blocking

##### Examples:
| value | accepted | text |
|-------|----------|------|
| `"accept"` | no connection | `accept` |
| `"accept"` | one connection whose `GET /x` was answered `200` | `accept` |
| a value whose `Error()` panics with itself, of type `T` | no connection | `<panic in Error(): T>` |

### Requirement: A passed listener whose Close panics never blocks the stop
srvr SHALL call a passed listener's `Close` under `recover` and treat a panic like a `Close` error, which srvr ignores: the stop sequence completes, the server closes, the call that stopped the server returns exactly what it returns when `Close` succeeds, and no record is written for it. This holds for every caller of `Close`, `http.Server.Shutdown` included.

#### Scenario: A listener whose Close panics
- **Given** a server configured with `WithListener(ln)` where `ln.Close()` panics with `"close"`
- **When** `<call>` returns
- **Then** it returns `<result>`, without panicking
- **And** a later `Shutdown` returns `nil` and a later `Start` an error matching `srvr.ErrServerClosed`, each without blocking

##### Examples:
| call | result |
|------|--------|
| `Shutdown` before `Start` | `nil` |
| `Start` with an already-cancelled context | an error matching `context.Canceled` |
| `Start`, where a route configurer panics | an error matching `srvr.ErrRouteConfig` |
| `Shutdown` on a running server | `nil` |
| `Start`, after its context ends while running | `nil` |

#### Scenario: A listener whose Close returns an error
- **Given** a server configured with `WithListener(ln)` and `WithLogOutput(logs)`, where `ln.Close()` closes the listener it wraps and returns an error with the text `"close: secret-dsn"`
- **When** `<call>` returns
- **Then** it returns `<result>`
- **And** `logs` holds exactly one `server.stopped` record, at `INFO` and without `srvr.drain.forced`, and no record contains `secret-dsn`

##### Examples:
| call | result |
|------|--------|
| `Shutdown` on a running server | `nil` |
| `Start`, after its context ends while running | `nil` |

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
`WithShutdownHook(phase, fn)` SHALL run `fn` once when the server stops: `Run`'s context ending, `Shutdown`, serving failing after `Start`, or a `Start` failure after leaving the initial state (configure, route or bind failure, context already done). Only `AfterDrain` hooks run when nothing was served. `Start(nil)`, `ErrServerStarted` and `ErrServerClosed` run no hooks. `BeforeDrain` hooks run after the listener closes, in the drain budget; `AfterDrain` after draining, in `ShutdownHookTimeout`.

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
| panics with a value whose `Error()` panics with itself | an error matching `srvr.ErrShutdownHook` whose message contains `<panic in Error(): T>`, `T` the value's Go type |
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

### Requirement: A passed listener whose Close does not unblock Accept never blocks the stop
On every engine, when the listener passed with `WithListener` returns from `Close` without unblocking a pending `Accept`, `Shutdown` SHALL still return once its drain deadline passes, and a later `Shutdown` SHALL return `nil`.

#### Scenario: Stuck listener on every engine
- **Given** engine `<engine>` serving on a passed listener whose `Close` returns `nil` but leaves `Accept` blocked, and `ShutdownTimeout` `200ms`
- **When** `Shutdown` is called with a context without deadline
- **Then** `Shutdown` returns within `1s`
- **And** a later `Shutdown` returns `nil`

##### Examples:
| engine |
|--------|
| `net/http` |
| `gin` |
| `fiber` |

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

#### Scenario: Records order when serving fails at once
- **Given** engine `net/http` serving on a passed listener whose first `Accept` returns a permanent error `E`
- **When** `Start` returns and the server stops itself
- **Then** the records appear in the order `<order>`
- **And** `Ready()` closed before the server was closed

##### Examples:
| order |
|-------|
| `server.started`, `server.failed`, `server.stopping`, `server.stopped` |

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
`HealthCheck(ctx)` SHALL return `nil` while the server is serving and an error otherwise, without blocking and without sending anything to clients. The error SHALL match `srvr.ErrServerNotStarted` until serving begins (the listener is bound), `srvr.ErrServerClosed` while stopping or after the stop, and SHALL also wrap the serve failure when there was one.

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
