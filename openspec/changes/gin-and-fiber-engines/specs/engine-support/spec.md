# Spec Delta

## Purpose

Defines the engine-neutral contract the core module exposes so every engine module shares one lifecycle, configuration, trace context and logging behaviour instead of re-implementing it.

## ADDED Requirements

### Requirement: Engines share one configuration model
Every engine module SHALL accept the core's option functions, including the config-struct entry points (`srvr.WithConfig`, `srvr.WithLogConfig`, `srvr.WithAccessLogConfig`, `srvr.WithServiceConfig`, `srvr.WithTraceConfig`), apply them in argument order (a later option wins), resolve them with the core's defaults and validation, and return the same `srvr.ErrConfig` errors naming the same fields.

#### Scenario: Same validation on every engine
- **Given** engine `<engine>` constructed with `<config>`
- **When** construction runs
- **Then** it returns `<result>`

##### Examples: accepted
| engine | config | result |
|--------|--------|--------|
| `net/http` | (none) | a server |
| `gin` | (none) | a server |
| `fiber` | (none) | a server |
| `gin` | `WithLogConfig(LogConfig{Level: "debug"})` | a server |
| `fiber` | `WithConfig(Config{MaxRequestBodyBytes: 10})` | a server |
| `gin` | `WithLogConfig(LogConfig{Level: "warn"})` then `WithLogLevel(slog.LevelDebug)` | a server logging at `DEBUG` |
| `fiber` | `WithLogLevel(slog.LevelDebug)` then `WithLogConfig(LogConfig{Level: "warn"})` | a server logging at `WARN` |
| `gin` | `WithTraceConfig(TraceConfig{IgnoreIncoming: true})` | a server that ignores incoming `traceparent` |

##### Examples: rejected
| engine | config | result |
|--------|--------|--------|
| `net/http` | `WithLogConfig(LogConfig{Level: "verbose"})` | an error wrapping `srvr.ErrConfig` naming `Level` |
| `gin` | `WithLogConfig(LogConfig{Level: "verbose"})` | an error wrapping `srvr.ErrConfig` naming `Level` |
| `fiber` | `WithLogConfig(LogConfig{Level: "verbose"})` | an error wrapping `srvr.ErrConfig` naming `Level` |
| `gin` | `WithConfig(Config{ShutdownTimeout: -1s})` | an error wrapping `srvr.ErrConfig` naming `ShutdownTimeout` |
| `fiber` | `WithListener(nil)` | an error wrapping `srvr.ErrConfig` naming `Listener` |

### Requirement: A failed construction changes no process-wide state
Every engine SHALL apply `srvr.WithSetDefaultLogger` (`slog.SetDefault`) only as the last step of a construction that succeeds; a construction that returns an error SHALL leave `slog.Default()` unchanged, whichever check fails (core validation, an option of another engine, or an engine hook that is nil or panics).

#### Scenario: Default logger after construction
- **Given** the process default logger recorded before construction
- **When** engine `<engine>` is constructed with `WithSetDefaultLogger()`, `WithLogHandler(slog.NewJSONHandler(buf, nil))` and `<config>`
- **Then** construction returns `<result>`
- **And** a record written through `slog.Default()` afterwards `<default>`

##### Examples:
| engine | config | result | default |
|--------|--------|--------|---------|
| `net/http` | (nothing else) | a server | reaches `buf` |
| `gin` | (nothing else) | a server | reaches `buf` |
| `fiber` | (nothing else) | a server | reaches `buf` |
| `net/http` | `WithProblemOptions(prblm.WithNamespace("not a uri"))` | an error wrapping `srvr.ErrConfig` naming `ProblemOptions` | goes to the recorded default, not `buf` |
| `net/http` | an option of another engine (`ginsrvr.WithEngine(hook)`) | an error wrapping `srvr.ErrConfig` naming that option | goes to the recorded default, not `buf` |
| `gin` | `ginsrvr.WithEngine(nil)` | an error wrapping `srvr.ErrConfig` naming `WithEngine` | goes to the recorded default, not `buf` |
| `gin` | a `ginsrvr.WithEngine` hook that panics with `boom` | an error wrapping `srvr.ErrConfig` containing `boom` | goes to the recorded default, not `buf` |
| `fiber` | `fibersrvr.WithFiberConfig(nil)` | an error wrapping `srvr.ErrConfig` naming `WithFiberConfig` | goes to the recorded default, not `buf` |
| `fiber` | a `fibersrvr.WithFiberConfig` hook that panics with `boom` | an error wrapping `srvr.ErrConfig` containing `boom` | goes to the recorded default, not `buf` |

### Requirement: Engine-specific options share the option list
Engine-specific options (`ginsrvr.WithEngine`, `fibersrvr.WithFiberConfig`) SHALL be `srvr.Option` values passed in the same list as the core options and read only by the engine they belong to; one given to any other engine SHALL fail construction with `srvr.ErrConfig` naming the option.

#### Scenario: Engine-specific option placement
- **Given** engine `<engine>` constructed with `<config>`
- **When** construction runs
- **Then** it returns `<result>`

##### Examples:
| engine | config | result |
|--------|--------|--------|
| `gin` | `WithLogLevel(slog.LevelDebug)` and `ginsrvr.WithEngine(hook)` in one list | a server logging at `DEBUG` whose hook ran |
| `fiber` | `WithLogLevel(slog.LevelDebug)` and `fibersrvr.WithFiberConfig(fn)` in one list | a server logging at `DEBUG` whose `fn` ran |
| `net/http` | an option that belongs to another engine (for example `ginsrvr.WithEngine(hook)`) | an error wrapping `srvr.ErrConfig` naming that option |
| `gin` | an option that belongs to another engine (for example `fibersrvr.WithFiberConfig(fn)`) | an error wrapping `srvr.ErrConfig` naming that option |
| `fiber` | an option that belongs to another engine (for example `ginsrvr.WithEngine(hook)`) | an error wrapping `srvr.ErrConfig` naming that option |

### Requirement: Engines share one lifecycle state machine
Every engine SHALL implement `Start(ctx)`, `Shutdown(ctx)`, `Ready()` and `Addr()` with the core's states and errors: single-use `Start`, idempotent `Shutdown`, `srvr.ErrServerStarted` and `srvr.ErrServerClosed` on misuse, readiness only after the listener is bound.

#### Scenario: Lifecycle misuse on every engine
- **Given** a `<engine>` server that `<state>`
- **When** the consumer calls `<call>`
- **Then** the call returns `<result>`

##### Examples:
| engine | state | call | result |
|--------|-------|------|--------|
| `gin` | is running | `Start(ctx)` | an error matching `srvr.ErrServerStarted` |
| `fiber` | is running | `Start(ctx)` | an error matching `srvr.ErrServerStarted` |
| `gin` | was never started | `Shutdown(ctx)` | `nil` |
| `fiber` | was never started | `Shutdown(ctx)` | `nil` |
| `gin` | has stopped | `Start(ctx)` | an error matching `srvr.ErrServerClosed` |
| `fiber` | has stopped | `Start(ctx)` | an error matching `srvr.ErrServerClosed` |
| `gin` | is running | `Register(c)` | an error matching `srvr.ErrServerStarted` |
| `fiber` | has stopped | `Register(c)` | an error matching `srvr.ErrServerClosed` |
| `fiber` | is running | `Shutdown(nil)` | an error wrapping `srvr.ErrConfig` and the server keeps serving |

### Requirement: Shutdown hooks run on every engine's stop paths
Every engine SHALL run the core's `srvr.WithShutdownHook` hooks exactly as net/http does: `BeforeDrain` hooks after accepting stops and before draining, `AfterDrain` hooks after draining, once per server on every stop path. A `Start` failure after the server left its initial state SHALL run only `AfterDrain` hooks; `Start(nil)`, `srvr.ErrServerStarted` and `srvr.ErrServerClosed` SHALL run none.

#### Scenario: Hooks per engine and stop path
- **Given** a `<engine>` server with one `BeforeDrain` hook and one `AfterDrain` hook, each recording its call
- **When** `<stop>`
- **Then** the recorded calls are `<calls>`

##### Examples:
| engine | stop | calls |
|--------|------|-------|
| `gin` | the `Start` context is cancelled while serving | `BeforeDrain`, `AfterDrain` |
| `fiber` | the `Start` context is cancelled while serving | `BeforeDrain`, `AfterDrain` |
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
| `gin` | the `Start` context is cancelled while serving and the `AfterDrain` hook calls `srv.Shutdown(context.Background())` | `BeforeDrain`, `AfterDrain`; the hook's `Shutdown` returns `nil` after the stop finished, with no deadlock |
| `fiber` | the `Start` context is cancelled while serving and the `AfterDrain` hook calls `srv.Shutdown(context.Background())` | `BeforeDrain`, `AfterDrain`; the hook's `Shutdown` returns `nil` after the stop finished, with no deadlock |

### Requirement: Consumer faults are contained on every engine
Every engine SHALL contain faulty consumer code exactly as the net/http engine does, reusing the core's guards: consumer values are formatted without panicking, cyclic error chains reaching srvr get the safe `500` problem, failing log handlers and listeners never affect responses or the lifecycle, and no srvr lock is held while consumer code runs, so it may call the server's own methods.

#### Scenario: Consumer faults on every engine
- **Given** engine `<engine>` with `<fault>`
- **When** the server is constructed, started and, where the fault is in a handler, serves `GET /items/7`
- **Then** `<result>`

##### Examples:
| engine | fault | result |
|--------|-------|--------|
| `gin` | a configurer that panics with a value whose `Error()` panics with itself, `main.selfPanic` | `Start` returns an error wrapping `srvr.ErrRouteConfig` containing `<panic in Error(): main.selfPanic>`; a later `Shutdown` returns `nil` |
| `fiber` | a configurer that panics with a value whose `Error()` panics with itself, `main.selfPanic` | `Start` returns an error wrapping `srvr.ErrRouteConfig` containing `<panic in Error(): main.selfPanic>`; a later `Shutdown` returns `nil` |
| `gin` | a `WithEngine` hook that panics with a value whose `Error()` panics with itself | construction returns an error wrapping `srvr.ErrConfig` containing `<panic in Error(): main.selfPanic>` |
| `gin` | a handler calling `c.Error(e)`, `e` a typed-nil `*main.MyErr` returned as `error` | a `500` problem; the record has `error.type` `*main.MyErr` and `exception.message` `<nil *main.MyErr>` |
| `fiber` | a handler returning `e`, a typed-nil `*main.MyErr` returned as `error` | a `500` problem; the record has `error.type` `*main.MyErr` and `exception.message` `<nil *main.MyErr>` |
| `gin` | a handler calling `c.Error(e)`, `e`'s `Error()` panicking with a value whose `Error()` panics | a `500` problem; the record has `exception.message` `<panic in Error(): T>`, `T` its Go type |
| `fiber` | a handler returning an error whose `Error()` panics with a value whose `Error()` panics | a `500` problem; the record has `exception.message` `<panic in Error(): T>`, `T` its Go type |
| `fiber` | a handler returning an error whose `Unwrap()` returns itself | a `500` problem within `1s`; the record has an `error.chain` of at most 32 entries and `srvr.error.truncated` `true` |
| `gin` | a handler that panics with a value whose `Error()` panics with itself | a `500` problem; the record has `error.kind` `panic` and `exception.message` `<panic in Error(): main.selfPanic>` |
| `fiber` | a handler that panics with a value whose `Error()` panics with itself | a `500` problem; the record has `error.kind` `panic` and `exception.message` `<panic in Error(): main.selfPanic>` |
| `gin` | `WithLogHandler(h)` where `h.Enabled` panics | status `200`, the record is skipped and `srvr.LogWriteErrors()` increases |
| `fiber` | `WithLogHandler(h)` where `h.WithAttrs` panics | status `200`, the record is skipped and `srvr.LogWriteErrors()` increases |
| `gin` | gin in debug mode and `WithLogHandler(h)` where `h.Handle` panics | construction succeeds; the `gin_debug_mode` warning is skipped and `srvr.LogWriteErrors()` increases |
| `gin` | `WithErrorRedactor(fn)` where `fn` panics and a handler calls `c.Error(errors.New("boom"))` | a `500` problem; the record has `exception.message` `[redaction failed]` |
| `fiber` | `WithListener(ln)` where `ln.Accept` panics | `Start` returns an error wrapping `srvr.ErrConfig` naming `Listener` and the process keeps running |
| `gin` | `WithListener(ln)` where `ln.Close` panics | `Shutdown` returns `nil`, `server.stopped` has no `srvr.drain.forced`, and a later `Shutdown` returns `nil` |
| `fiber` | `WithListener(ln)` where `ln.Close` returns `errors.New("close failed")` | `Shutdown` returns `nil`, `server.stopped` has no `srvr.drain.forced`, and a later `Shutdown` returns `nil` |
| `fiber` | `WithListener(ln)` where `ln.Addr()` returns `nil` | `Start` returns an error wrapping `srvr.ErrConfig` naming `Listener`; `ln` is closed |
| `gin` | a configurer whose `Configure` calls `srv.Addr()` and `srv.Register(c2)` | `Addr()` returns `""`, `Register` returns an error matching `srvr.ErrServerStarted`, and the server becomes ready |
| `fiber` | a configurer whose `Configure` calls `srv.Addr()` and `srv.Register(c2)` | `Addr()` returns `""`, `Register` returns an error matching `srvr.ErrServerStarted`, and the server becomes ready |
| `gin` | a route check that calls `srv.Addr()` and returns an error whose `Error()` panics with itself | `Start` returns an error wrapping `srvr.ErrRouteConfig` containing `<panic in Error(): main.selfPanic>`, without deadlock |

### Requirement: The engine-support API is marked as such
The core SHALL expose the shared pieces in a package whose name and documentation state it is for engine implementations, and the core module's dependency set SHALL remain the standard library plus prblm.

#### Scenario: Core dependencies unchanged
- **Given** the core module after this change
- **When** `go list -m all` runs in the core module with `GOWORK=off`
- **Then** the third-party modules listed are `<modules>`

##### Examples:
| modules |
|---------|
| `github.com/kartaladev/prblm` only |

### Requirement: srvr re-exports the shared types
The public types every engine shares (`Config`, `LogConfig`, `AccessLogConfig`, `TraceConfig`, `ServiceConfig`, `Trace`, `ShutdownPhase`, `ErrorHook`, `Option` and the sentinel errors) SHALL be declared in the engine-support package and re-exported by `srvr` as aliases, so code written against `srvr` compiles unchanged and `errors.Is` against `srvr` sentinels matches errors created in `engine`.

#### Scenario: srvr names are the engine declarations
- **Given** the error `<err>` produced by `<source>`
- **When** the consumer calls `errors.Is(err, <sentinel>)`
- **Then** the result is `true`

##### Examples:
| err | source | sentinel |
|-----|--------|----------|
| an invalid log level | `engine.ResolveConfig` | `srvr.ErrConfig` |
| an invalid log level | `srvr.New` | `engine.ErrConfig` |
| `Start` on a running server | `engine.Lifecycle` | `srvr.ErrServerStarted` |
| `Start` on a stopped server | `engine.Lifecycle` | `srvr.ErrServerClosed` |

### Requirement: Configuration warnings are logged once
An engine SHALL report a configuration that works but is likely unintended as one `WARN` record with `msg` `server.config.warning`, a stable snake_case `srvr.warning.code` and a human-readable `srvr.warning.message`, written once per server: at construction for engine settings, at `Start` for route-dependent ones. A warning SHALL NOT fail construction or `Start`.

#### Scenario: Configuration warning record
- **Given** engine `<engine>` where `<condition>`
- **When** the server is constructed and started
- **Then** `<count>` records with `msg` `server.config.warning` and `srvr.warning.code` `<code>` are written

##### Examples:
| engine | condition | count | code |
|--------|-----------|-------|------|
| `gin` | gin's global mode is `debug` | `1` | `gin_debug_mode` |
| `gin` | gin's global mode is `release` | `0` | (none) |
| `gin` | gin's global mode is `test` | `0` | (none) |
| `fiber` | `WithMaxHeaderBytes(2097152)` | `1` | `fiber_header_limit_capped` |
| `fiber` | (none) | `0` | (none) |
| `net/http` | (none) | `0` | (none) |

### Requirement: Engines share one per-request state
Every engine SHALL keep one `engine.RequestState` per request, reachable through `engine.StateFrom(ctx)` from any stage inside srvr's pipeline, holding the matched route, whether a route matched, the response status, the failure (returned error or recovered panic) and the trace context. Later stages SHALL read and extend this one state instead of adding their own per-request holders.

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

### Requirement: Registered routes are listed with their configurer
After the configure phase and before binding, every engine SHALL build `engine.Routes`, one `engine.Route{Method, Pattern, Configurer}` per route a configurer declared (pattern in the engine's syntax, configurer named by its Go type), excluding middleware and implicit routes, and SHALL pass it to every `srvr.WithRouteCheck` check as its own copy; a route served for every method is listed once with `Method` `""`. A check error or panic SHALL fail `Start` wrapping `srvr.ErrRouteConfig`.

#### Scenario: Route listing
- **Given** engine `<engine>` with configurer `*main.UserRoutes` declaring `<routes>`
- **When** the server starts with a route check that records its input
- **Then** the check receives exactly `<listed>`

##### Examples:
| engine | routes | listed |
|--------|--------|--------|
| `net/http` | `GET /users/{id}` and `POST /users` | `{GET /users/{id} *main.UserRoutes}`, `{POST /users *main.UserRoutes}` |
| `gin` | `GET /users/:id` and `POST /users` | `{GET /users/:id *main.UserRoutes}`, `{POST /users *main.UserRoutes}` |
| `fiber` | `Get("/users/:id")` and `Post("/users")` | `{GET /users/:id *main.UserRoutes}`, `{POST /users *main.UserRoutes}` (no implicit `HEAD`) |
| `gin` | `group.Use(audit)` and `GET /x` | `{GET /x *main.UserRoutes}` |
| `fiber` | `router.Use(audit)` and `Get("/x")` | `{GET /x *main.UserRoutes}` |
| `gin` | sub-group `/v1` with `GET /users/:id` | `{GET /v1/users/:id *main.UserRoutes}` |
| `net/http` | group `/static` with `Mount("/", files)` | `{"" /static/ *main.UserRoutes}` |
| `net/http` | the method-less pattern `/health` | `{"" /health *main.UserRoutes}` |
| `gin` | `Any("/x")` | `{"" /x *main.UserRoutes}` |
| `fiber` | `All("/x")` | `{"" /x *main.UserRoutes}` |
| `gin` | `Static("/assets", dir)` | `{GET /assets/*filepath *main.UserRoutes}`, `{HEAD /assets/*filepath *main.UserRoutes}` |
| `fiber` | `router.Use("/assets", static.New(dir))` | (none: a path-scoped `Use` is middleware) |

#### Scenario: Route check outcomes
- **Given** engine `<engine>` configured with route check `<check>`
- **When** `Start` is called
- **Then** the result is `<result>`

##### Examples:
| engine | check | result |
|--------|-------|--------|
| `gin` | returns `nil` | the server becomes ready |
| `net/http` | returns `errors.New("collides")` | `Start` returns an error wrapping `srvr.ErrRouteConfig` containing `collides` and `Ready()` is never closed |
| `gin` | returns `errors.New("collides")` | `Start` returns an error wrapping `srvr.ErrRouteConfig` containing `collides` and `Ready()` is never closed |
| `fiber` | returns `errors.New("collides")` | `Start` returns an error wrapping `srvr.ErrRouteConfig` containing `collides` and `Ready()` is never closed |
| `fiber` | panics with `boom` | `Start` returns an error wrapping `srvr.ErrRouteConfig` containing `boom` |
| `gin` | `WithRouteCheck(nil)` | construction returns an error wrapping `srvr.ErrConfig` naming `RouteCheck` |
| `net/http` | a first check that deletes every entry from its input, then a second check that records its input | the second check receives the full listing and the server becomes ready |
| `gin` | blocks until its context is done, with a `Start` context whose deadline is `50ms` | `Start` returns an error matching `context.DeadlineExceeded`, `Ready()` is never closed and `AfterDrain` hooks run once |
| `fiber` | ignores its context and blocks forever, with a `Start` context whose deadline is `50ms` | `Start` returns an error matching `context.DeadlineExceeded` at the deadline; the check's goroutine is abandoned |

### Requirement: Mounted fiber sub-app routes are listed
The fiber engine SHALL list the routes of a sub-app mounted with `Use(prefix, subApp)`, at any depth, with their full mounted path, attributed to the configurer that mounted the sub-app, excluding the sub-app's middleware and implicit routes.

#### Scenario: Sub-app route listing
- **Given** a fiber server with configurer `*main.UserRoutes` that `<mounts>`
- **When** the server starts with a route check that records its input
- **Then** the check receives exactly `<listed>`

##### Examples:
| mounts | listed |
|--------|--------|
| sub-app `sub` with `Get("/users")`, mounted with `router.Use("/api", sub)` | `{GET /api/users *main.UserRoutes}` |
| sub-app `sub` with `Get("/users")`, mounted with `router.Use("/api", sub)`, then `sub.Post("/users")` | `{GET /api/users *main.UserRoutes}`, `{POST /api/users *main.UserRoutes}` |
| sub-app `inner` with `Get("/x")` mounted on sub-app `outer` with `outer.Use("/v2", inner)`, and `outer` mounted with `router.Use("/api", outer)` | `{GET /api/v2/x *main.UserRoutes}` |
| sub-app `sub` with `sub.Use(audit)` and `Get("/users")`, mounted with `router.Use("/api", sub)` | `{GET /api/users *main.UserRoutes}` |
| sub-app `sub` with `Get("/users")`, mounted with `router.Use("/", sub)` | `{GET /users *main.UserRoutes}` |
| sub-app `sub` with `Get("/users")`, mounted with `router.Group("/v1").Use("/api", sub)` | `{GET /v1/api/users *main.UserRoutes}` |
