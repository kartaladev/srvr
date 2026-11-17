# Spec Delta

## Purpose

Defines how consumers contribute routes through typed route configurers, and how the net/http engine's group type joins prefixes, orders middleware and rejects broken registrations.

## ADDED Requirements

### Requirement: Route configurers are registered before start
A server SHALL accept route configurers through `Register(RouteConfigurer[*srvr.Group]) error` until `Start` is called, and SHALL invoke each configurer's `Configure` exactly once, in registration order, during `Start`.

#### Scenario: Registration outcome
- **Given** a server that `<state>`
- **When** the consumer registers `<configurer>`
- **Then** `Register` returns `<result>`

##### Examples: accepted
| state | configurer | result |
|-------|------------|--------|
| has not been started | a configurer adding `GET /ping` | `nil` |
| already has one configurer | a second configurer adding `GET /pong` | `nil` |
| already has the same configurer value | the same configurer again | `nil` and `Configure` runs twice |

##### Examples: rejected
| state | configurer | result |
|-------|------------|--------|
| has not been started | `nil` | an error wrapping `srvr.ErrRouteConfig` |
| has not been started | a typed-nil pointer configurer | an error wrapping `srvr.ErrRouteConfig` |
| is running | a configurer adding `GET /late` | an error matching `srvr.ErrServerStarted` |
| has stopped | a configurer adding `GET /late` | an error matching `srvr.ErrServerClosed` |

#### Scenario: Configure order
- **Given** configurers `<first>` and `<second>` registered in that order
- **When** the server starts
- **Then** `Configure` is called in the order `<order>`

##### Examples:
| first | second | order |
|-------|--------|-------|
| `A` | `B` | `A`, `B` |

### Requirement: Handlers return errors
Group routes SHALL take handlers of type `func(http.ResponseWriter, *http.Request) error`. A returned nil SHALL leave the response as written; a returned error SHALL be rendered as a problem, as defined by the error-responses capability.

#### Scenario: Handler result
- **Given** a route `GET /items` whose handler `<behaviour>`
- **When** a client sends `GET /items`
- **Then** the response status is `<status>`
- **And** the response content type is `<content_type>`

##### Examples:
| behaviour | status | content_type |
|-----------|--------|--------------|
| sets `Content-Type: application/json`, writes `200` with `{"ok":true}` and returns `nil` | `200` | `application/json` |
| returns `nil` without writing | `200` | (none) |
| returns `prblm.ErrNotFound` | `404` | `application/problem+json` |

### Requirement: Group paths are joined predictably
A group's prefix and a route's path SHALL be joined into one ServeMux pattern by collapsing duplicate slashes, keeping the route's trailing-slash meaning and keeping `{name}`, `{name...}` and `{$}` wildcards. Paths SHALL start with `/`, except an empty path, which SHALL mean the prefix itself.

#### Scenario: Joining prefix and path
- **Given** a group with prefix `<prefix>`
- **When** a route is registered for `GET` with path `<path>`
- **Then** the server routes `<request>` to it

##### Examples: joined
| prefix | path | request |
|--------|------|---------|
| (empty) | `/users` | `GET /users` |
| `/api` | `/users` | `GET /api/users` |
| `/api/` | `/users` | `GET /api/users` |
| `/api` | `/users/{id}` | `GET /api/users/42` |
| `/orgs/{org}` | `/members` | `GET /orgs/acme/members` |
| `/api` | (empty) | `GET /api` |
| `/api` | `/` | `GET /api/anything/below` |
| `/api` | `/{$}` | `GET /api/` |
| `/files` | `/{path...}` | `GET /files/a/b/c.txt` |

#### Scenario: Invalid paths
- **Given** a group with prefix `<prefix>`
- **When** a route is registered with path `<path>` and the server starts
- **Then** `Start` returns an error wrapping `srvr.ErrRouteConfig` that contains `<message>`

##### Examples:
| prefix | path | message |
|--------|------|---------|
| `/api` | `users` | `users` |
| `api` | `/users` | `api` |
| `/api` | `/users/{id` | `/users/{id` |
| `/api` | `/a/{x}/{x}` | `/a/{x}/{x}` |
| `/api` | `/{rest...}/more` | `/{rest...}/more` |
| `/api` | `example.com/users` | `example.com/users` |

### Requirement: Route registration conflicts fail start
Registering two routes whose patterns ServeMux treats as conflicting SHALL make `Start` return an error wrapping `srvr.ErrRouteConfig` that names both patterns and both configurers, instead of panicking.

#### Scenario: Conflicting routes
- **Given** configurer `<a>` registers `<first>` and configurer `<b>` registers `<second>`
- **When** the server starts
- **Then** `Start` returns `<result>`

##### Examples: conflict
| a | b | first | second | result |
|---|---|-------|--------|--------|
| `UsersAPI` | `AdminAPI` | `GET /users/{id}` | `GET /users/{name}` | an error naming `GET /users/{id}`, `GET /users/{name}`, `UsersAPI` and `AdminAPI` |
| `UsersAPI` | `UsersAPI` | `GET /users` | `GET /users` | an error naming `GET /users` |

##### Examples: no conflict
| a | b | first | second | result |
|---|---|-------|--------|--------|
| `UsersAPI` | `AdminAPI` | `GET /users/{id}` | `GET /users/me` | `nil` and the server is ready |
| `UsersAPI` | `AdminAPI` | `GET /users` | `POST /users` | `nil` and the server is ready |

### Requirement: Middleware applies in registration order
`Use` on a group SHALL apply middleware, outermost first, to routes registered on that group or its sub-groups after the call. Routes registered before `Use` SHALL NOT receive it. A sub-group SHALL inherit its parent's middleware at the time it is created.

#### Scenario: Middleware order and scope
- **Given** a group set up as `<setup>`
- **When** a client sends `<request>`
- **Then** the middleware run, in order, is `<run>`

##### Examples:
| setup | request | run |
|-------|---------|-----|
| `Use(A, B)` then `GET /x` | `GET /x` | `A`, `B` |
| `GET /x` then `Use(A)` | `GET /x` | (none) |
| `Use(A)`, sub-group `/v1` with `Use(B)`, `GET /v1/x` | `GET /v1/x` | `A`, `B` |
| sub-group `/v1` created, then parent `Use(A)`, then `GET /v1/x` | `GET /v1/x` | (none) |
| `Group("/v1", A)` then `GET /v1/x` | `GET /v1/x` | `A` |

#### Scenario: Faulty middleware
- **Given** a group with middleware that `<fault>` and route `GET /x`
- **When** a client sends `GET /x`
- **Then** the response status is `<status>`
- **And** `Start` had returned `<start>`

##### Examples:
| fault | status | start |
|-------|--------|-------|
| is `nil` | (none) | an error wrapping `srvr.ErrRouteConfig` |
| returns a `nil` handler | (none) | an error wrapping `srvr.ErrRouteConfig` |
| panics while handling | `500` | `nil` |
| never calls the next handler and writes `403` | `403` | `nil` |

### Requirement: A middleware constructor that panics fails Start
`Start` calls each middleware constructor once per route while it installs the routes. A constructor that panics SHALL fail `Start` like a panicking `Configure`: an error wrapping `srvr.ErrRouteConfig` that names the route and the configurer that registered it and contains the panic value, read without panicking. The server SHALL close as on any other `Start` failure.

#### Scenario: Middleware constructor panics
- **Given** a configurer of type `*main.API` registering `<route>` under group middleware whose constructor panics with `<value>`
- **When** `Start` is called
- **Then** `Start` returns an error wrapping `srvr.ErrRouteConfig` containing `<route>`, `*main.API` and `<text>`, without panicking, and `Ready()` is never closed
- **And** a later `Shutdown` returns `nil` without blocking

##### Examples:
| route | value | text |
|-------|-------|------|
| `GET /x` | `"mw"` | `mw` |
| `GET /x` | a value whose `Error()` panics with itself, of type `T` | `<panic in Error(): T>` |
| `Mount("/static/", files)` | `"mw"` | `mw` |

### Requirement: Plain handlers can be mounted
A group SHALL accept a plain `http.Handler` through `Mount(path, handler)`, routed for every method, receiving the group's middleware, with its panics recovered like other routes. The handler SHALL see the request path with the group prefix removed, as `http.StripPrefix` does.

#### Scenario: Mounted handler
- **Given** a group with prefix `/static` and `Mount` of `<handler>` at `/`
- **When** a client sends `<request>`
- **Then** the response status is `<status>`

##### Examples:
| handler | request | status |
|---------|---------|--------|
| `http.FileServerFS` over a file `a.txt` | `GET /static/a.txt` | `200` |
| `http.FileServerFS` over a file `a.txt` | `GET /static/missing.txt` | `404` |
| a handler that panics | `POST /static/x` | `500` |
| `nil` | (none) | (none); `Start` returns an error wrapping `srvr.ErrRouteConfig` |

### Requirement: A configurer failure names the configurer
When `Configure` panics, `Start` SHALL recover it and return an error wrapping `srvr.ErrRouteConfig` that names the configurer's type and contains the panic value, without binding the listener. The panic value's text SHALL be read without panicking; an error value whose `Error()` panics is shown as `<panic in Error(): T>` and a value whose `String()` panics as `<panic formatting value>`, and the server closes as on any other `Start` failure.

#### Scenario: Configure panics
- **Given** a registered configurer of type `<type>` whose `Configure` panics with `<value>`
- **When** the server starts
- **Then** `Start` returns an error containing `<type>` and `<value>`

##### Examples:
| type | value |
|------|-------|
| `*main.UsersAPI` | `boom` |
| `main.AdminAPI` | `runtime error: invalid memory address or nil pointer dereference` |
| `*main.HostileAPI` | a value whose `Error()` panics with itself, `main.selfPanic`, shown as `<panic in Error(): main.selfPanic>`; a later `Shutdown` returns `nil` |
