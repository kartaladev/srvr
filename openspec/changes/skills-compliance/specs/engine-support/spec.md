# Spec Delta

## MODIFIED Requirements

### Requirement: Engine-specific options share the option list
Engine-specific options (`ginsrvr.WithGinEngine`, `fibersrvr.WithFiberConfig`) SHALL be `srvr.Option` values passed in the same list as the core options and read only by the engine they belong to; one given to any other engine SHALL fail construction with `srvr.ErrConfig` naming the option.

#### Scenario: Engine-specific option placement
- **Given** engine `<engine>` constructed with `<config>`
- **When** construction runs
- **Then** it returns `<result>`

##### Examples:
| engine | config | result |
|--------|--------|--------|
| `gin` | `WithLogLevel(slog.LevelDebug)` and `ginsrvr.WithGinEngine(hook)` in one list | a server logging at `DEBUG` whose hook ran |
| `fiber` | `WithLogLevel(slog.LevelDebug)` and `fibersrvr.WithFiberConfig(fn)` in one list | a server logging at `DEBUG` whose `fn` ran |
| `net/http` | an option that belongs to another engine (for example `ginsrvr.WithGinEngine(hook)`) | an error wrapping `srvr.ErrConfig` naming that option |
| `gin` | an option that belongs to another engine (for example `fibersrvr.WithFiberConfig(fn)`) | an error wrapping `srvr.ErrConfig` naming that option |
| `fiber` | an option that belongs to another engine (for example `ginsrvr.WithGinEngine(hook)`) | an error wrapping `srvr.ErrConfig` naming that option |

### Requirement: srvr re-exports the shared types
The public types every engine shares (`Config`, `LogConfig`, `AccessLogConfig`, `TraceConfig`, `ServiceConfig`, `Trace`, `ShutdownPhase`, `ErrorHook`, `Option`, `ConfigError`, `RouteCheck`, `Routes`, `Route` and the sentinel errors) SHALL be declared in the engine-support package and re-exported by `srvr` as aliases, so code written against `srvr` compiles unchanged, `errors.Is` against `srvr` sentinels matches errors created in `engine`, and `errors.As` into a `*srvr.ConfigError` reads the offending field and value.

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

#### Scenario: Configuration errors name their field
- **Given** construction with `<config>`
- **When** the consumer calls `errors.As(err, &ce)` with `ce` a `*srvr.ConfigError`
- **Then** it returns `<found>` with `ce.Field` `<field>`

##### Examples:
| config | found | field |
|--------|-------|-------|
| `LogConfig{Level: "loud"}` | `true` | `Level` |
| `WithLogOutput(nil)` | `true` | `Output` |
| `WithRouteCheck(nil)` | `true` | `RouteCheck` |
| two invalid fields `LogConfig{Level: "loud", Format: "xml"}` | `true` | `Level` (the first; both are named in `err.Error()`) |
| (none) | `false` (no error) | (none) |

### Requirement: Registered routes are listed with their configurer
After the configure phase and before binding, every engine SHALL build `engine.Routes`, one `engine.Route{Method, Pattern, Configurer}` per route a configurer declared (pattern in the engine's syntax, configurer named by its Go type), excluding middleware and implicit routes, and SHALL pass it to every `srvr.WithRouteCheck` check as its own copy; a route served for every method is listed once with `Method` `""`. A check error or panic SHALL fail `Start` wrapping `srvr.ErrRouteConfig`; srvr SHALL read a check's error without walking a chain that cycles or whose methods panic.

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
| `net/http` | returns an error whose `Unwrap` returns the error itself | `Start` returns an error wrapping `srvr.ErrRouteConfig` within `1s` |
| `gin` | returns an error whose `Unwrap` panics | `Start` returns an error wrapping `srvr.ErrRouteConfig`, and no panic escapes `Start` |
| `fiber` | returns an error whose `As` method panics | `Start` returns an error wrapping `srvr.ErrRouteConfig`, and no panic escapes `Start` |
| `net/http` | returns a typed-nil `*MyError` | `Start` returns an error wrapping `srvr.ErrRouteConfig` |
