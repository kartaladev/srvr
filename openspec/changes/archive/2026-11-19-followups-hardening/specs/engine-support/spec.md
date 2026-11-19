# Spec Delta

## MODIFIED Requirements

### Requirement: srvr re-exports the shared types
The public types every engine shares and the sentinel errors SHALL be declared in the engine-support package and re-exported by `srvr` as aliases, so code written against `srvr` compiles unchanged, `errors.Is` against `srvr` sentinels matches errors created in `engine`, and `errors.As` into a `*srvr.ConfigError` reads the offending field and value.

#### Scenario: Re-exported names
- **Given** the `srvr` name `<name>`
- **When** the consumer compares its type with the engine-support declaration `<declaration>`
- **Then** both are the same type

##### Examples:
| name | declaration |
|------|-------------|
| `srvr.Config` | `engine.Config` |
| `srvr.LogConfig` | `engine.LogConfig` |
| `srvr.AccessLogConfig` | `engine.AccessLogConfig` |
| `srvr.TraceConfig` | `engine.TraceConfig` |
| `srvr.ServiceConfig` | `engine.ServiceConfig` |
| `srvr.Trace` | `engine.Trace` |
| `srvr.ShutdownPhase` | `engine.ShutdownPhase` |
| `srvr.ErrorHook` | `engine.ErrorHook` |
| `srvr.Option` | `engine.Option` |
| `srvr.ConfigError` | `engine.ConfigError` |
| `srvr.RouteCheck` | `engine.RouteCheck` |
| `srvr.Routes` | `engine.Routes` |
| `srvr.Route` | `engine.Route` |

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

### Requirement: Engines share one per-request state
Every engine SHALL keep one `engine.RequestState` per request, reachable through `engine.StateFrom(ctx)` from any stage inside srvr's pipeline, holding the matched route, whether a route matched, the response status, the failure (returned error or recovered panic) and the request's trace, chosen as request-observer's "Observers may supply the trace context" requires. Later stages SHALL read and extend this one state instead of adding their own per-request holders.

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
After the configure phase and before binding, every engine SHALL build `engine.Routes`, one `engine.Route{Method, Pattern, Configurer}` per route a configurer declared, and SHALL pass every `srvr.WithRouteCheck` check its own copy. A check's error or panic SHALL fail `Start` with an error wrapping `srvr.ErrRouteConfig`, read without walking a chain that cycles or whose methods panic.

#### Scenario: Route listing
- **Given** engine `<engine>` with configurer `*main.UserRoutes` declaring `<routes>`
- **When** the server starts with a route check that records its input
- **Then** the check receives exactly `<listed>`
- **And** each pattern is in the engine's syntax and each configurer is named by its Go type; middleware and implicit routes are not listed, and a route served for every method is listed once with `Method` `""`

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
