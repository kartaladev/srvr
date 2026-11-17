# Spec Delta

## Purpose

Defines how the Gin engine module serves consumer routes with srvr's lifecycle, RFC 9457 problems, trace correlation and access records, and how it neutralises Gin behaviour that would break them.

## ADDED Requirements

### Requirement: Gin route configurers receive a router group
`ginsrvr` SHALL accept `srvr.RouteConfigurer[*gin.RouterGroup]` through `Register` and SHALL call each `Configure` once, in registration order, during `Start`, with a group rooted at `/`, before the listener is bound.

#### Scenario: Gin registration
- **Given** a `ginsrvr` server and configurer `<configurer>`
- **When** the consumer registers it and starts the server
- **Then** `Register` returns `<register>` and `Start` `<start>`

##### Examples: accepted
| configurer | register | start |
|------------|----------|-------|
| adds `POST /registrations` with `group.Use(audit).POST("/registrations", h.Register)` | `nil` | becomes ready |
| adds sub-group `/v1` with `GET /users/:id` | `nil` | becomes ready |

##### Examples: rejected
| configurer | register | start |
|------------|----------|-------|
| `nil` | an error wrapping `srvr.ErrRouteConfig` | (none) |
| a typed-nil pointer | an error wrapping `srvr.ErrRouteConfig` | (none) |
| panics with `boom` in `Configure` | `nil` | returns an error wrapping `srvr.ErrRouteConfig` containing `boom` and the configurer type |
| registers `GET /users/:id` and `GET /users/:name` | `nil` | returns an error wrapping `srvr.ErrRouteConfig` naming `/users/:name` |

### Requirement: Gin handler errors become problems
An error recorded with `c.Error(err)` or `c.AbortWithError(code, err)` SHALL be rendered by ginprblm as an RFC 9457 problem with the `trace_id` member, using the server's prblm options; unmapped errors SHALL become `500` problems without detail.

#### Scenario: Gin error to problem
- **Given** a Gin route `GET /items/:id` whose handler `<behaviour>`
- **When** a client sends `GET /items/7`
- **Then** the response status is `<status>` with content type `<content_type>`
- **And** the body does not contain `<hidden>`

##### Examples:
| behaviour | status | content_type | hidden |
|-----------|--------|--------------|--------|
| calls `c.JSON(200, gin.H{"ok": true})` | `200` | `application/json; charset=utf-8` | (none) |
| calls `c.Error(prblm.ErrNotFound)` and returns | `404` | `application/problem+json` | (none) |
| calls `c.AbortWithError(409, errors.New("dup key alice"))` | `409` | `application/problem+json` | `alice` |
| calls `c.Error(errors.New("pq: auth failed"))` | `500` | `application/problem+json` | `pq:` |
| calls `c.AbortWithStatus(403)` | `403` | `application/problem+json` | (none) |
| calls `c.Error(err1)` then `c.Error(prblm.ErrConflict)` | `409` | `application/problem+json` | (none) |
| calls `c.Error(nil)` (gin panics with `err is nil`) | `500` | `application/problem+json` | `err is nil` |

### Requirement: Gin unmatched routes are problems
The Gin engine SHALL enable `HandleMethodNotAllowed`, disable `RedirectTrailingSlash`, and SHALL answer unmatched paths with a `404` problem and wrong methods with a `405` problem carrying `Allow` sorted alphabetically like net/http, never Gin's plain-text `404 page not found` or an HTML redirect.

#### Scenario: Gin no route or wrong method
- **Given** Gin routes `POST /users` registered before `GET /users`
- **When** a client sends `<request>`
- **Then** the response status is `<status>` with content type `application/problem+json`
- **And** the `Allow` header is `<allow>` and no `Location` header is sent

##### Examples:
| request | status | allow |
|---------|--------|-------|
| `GET /nope` | `404` | (none) |
| `DELETE /users` | `405` | `GET, POST` |
| `PATCH /nope` | `404` | (none) |
| `GET /users/` | `404` | (none) |

### Requirement: Gin panics are recovered by srvr
The Gin engine SHALL install ginprblm recovery instead of `gin.Recovery()` and SHALL NOT install `gin.Logger()`; a panic SHALL become a safe `500` problem and an `http.request.failed` record with the stack.

#### Scenario: Gin panic
- **Given** a Gin handler that panics with `<value>`
- **When** a client sends a request to it
- **Then** the client receives `<response>`
- **And** standard output contains no line starting with `[GIN]` or `[Recovery]`

##### Examples:
| value | response |
|-------|----------|
| `"boom"` | status `500` problem without `boom` |
| `http.ErrAbortHandler` | an aborted connection and no response |

#### Scenario: Gin aborted request record
- **Given** a Gin handler at `/items/:id` that panics with `http.ErrAbortHandler`
- **When** a client sends `GET /items/7`
- **Then** the access record has `<key>` `<value>`

##### Examples:
| key | value |
|-----|-------|
| `msg` | `http.request.failed` |
| `level` | `WARN` |
| `error.kind` | `panic` |
| `error.type` | `panic` |
| `srvr.response.aborted` | `true` |
| `problem` | (none) |
| `http.route` | `/items/:id` |

### Requirement: Gin request context carries srvr's trace and deadlines
The Gin engine SHALL set `ContextWithFallback` so `c.Value`, `c.Done` and `c.Deadline` reach the request context, letting `srvr.TraceFromContext(c)` and `srvr.LoggerFromContext(c)` work on `*gin.Context` directly.

#### Scenario: Reading trace through gin.Context
- **Given** a Gin handler that calls `<accessor>`
- **When** a request arrives with `traceparent` `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01`
- **Then** the accessor returns `<result>`

##### Examples:
| accessor | result |
|----------|--------|
| `srvr.TraceFromContext(c)` | trace id `4bf92f3577b34da6a3ce929d0e0e4736` and `true` |
| `srvr.TraceFromContext(c.Request.Context())` | trace id `4bf92f3577b34da6a3ce929d0e0e4736` and `true` |
| `srvr.LoggerFromContext(c)` | a logger whose records carry that trace id |
| `srvr.TraceFromContext(c.Copy())` in a goroutine | trace id `4bf92f3577b34da6a3ce929d0e0e4736` and `true` |

### Requirement: Gin access records use the route template
The Gin access record SHALL use `c.FullPath()` for `http.route`, omit `http.route` when no route matched, log an unmatched request as `http.request.failed` although `c.Errors` is empty, and SHALL be identical in keys and levels to the net/http engine's record.

#### Scenario: Gin route attribute
- **Given** Gin route `GET /users/:id`
- **When** a client sends `<request>`
- **Then** the access record has `msg` `<msg>`, `level` `<level>`, `http.route` `<route>` and `http.response.status_code` `<status>`

##### Examples:
| request | msg | level | route | status |
|---------|-----|-------|-------|--------|
| `GET /users/42` | `http.request.completed` | `INFO` | `/users/:id` | `200` |
| `GET /nope` | `http.request.failed` | `WARN` | (none) | `404` |
| `DELETE /users/42` | `http.request.failed` | `WARN` | (none) | `405` |

### Requirement: Gin enforces the request body limit
The Gin engine SHALL wrap every request body with `http.MaxBytesReader` at `MaxRequestBodyBytes` (gin does not inherit a body limit from `http.Server`); a handler that reads past the limit and reports the error with `c.Error` SHALL produce a `413` problem, and `-1` SHALL disable the limit.

#### Scenario: Gin body limit
- **Given** a Gin server configured with `<config>` and a handler that reads the whole body and calls `c.Error` on a read error
- **When** a client posts a body of `<size>` bytes
- **Then** the response status is `<status>`

##### Examples:
| config | size | status |
|--------|------|--------|
| (none) | `4194304` | `200` |
| (none) | `4194305` | `413` |
| `WithConfig(Config{MaxRequestBodyBytes: 10})` | `10` | `200` |
| `WithConfig(Config{MaxRequestBodyBytes: 10})` | `11` | `413` |
| `WithConfig(Config{MaxRequestBodyBytes: -1})` | `8388608` | `200` |

### Requirement: Gin debug mode is reported
Because gin's mode is process-global and srvr never changes it, the Gin engine SHALL write one `server.config.warning` record with `srvr.warning.code` `gin_debug_mode` at construction when gin is in debug mode, telling the consumer to set `GIN_MODE=release`, and SHALL NOT call `gin.SetMode` or change gin's package-level writers.

#### Scenario: Gin mode at construction
- **Given** gin's global mode is `<mode>`
- **When** a `ginsrvr` server is constructed
- **Then** `<warnings>` `gin_debug_mode` warning records are written and gin's mode is still `<mode>`

##### Examples:
| mode | warnings |
|------|----------|
| `debug` | `1` |
| `release` | `0` |
| `test` | `0` |

### Requirement: Gin middleware order is preserved around srvr's pipeline
srvr's pipeline (trace context, access record, recovery, problem rendering) SHALL run outside every consumer middleware, so a consumer middleware that aborts or writes still produces a problem for an error and exactly one access record.

#### Scenario: Consumer middleware behaviour
- **Given** a Gin group middleware that `<behaviour>` before route `GET /x`
- **When** a client sends `GET /x`
- **Then** the response status is `<status>`
- **And** exactly one access record is written with `msg` `<msg>`

##### Examples:
| behaviour | status | msg |
|-----------|--------|-----|
| calls `c.Next()` | `200` | `http.request.completed` |
| calls `c.AbortWithStatus(401)` | `401` | `http.request.completed` |
| calls `c.AbortWithError(401, errAuth)` | `401` | `http.request.failed` |
| panics | `500` | `http.request.failed` |
| writes `200` then calls `c.Error(errors.New("late"))` | `200` | `http.request.failed` |

### Requirement: Gin engine customisation cannot remove srvr's safety settings
srvr SHALL set Gin's trusted proxies to none (gin trusts every proxy by default), then run `ginsrvr.WithEngine(func(*gin.Engine))` before installing its pipeline, then re-assert `ContextWithFallback`, `HandleMethodNotAllowed`, `RedirectTrailingSlash` off and its recovery. A hook that registers routes or middleware SHALL be rejected, because they would run outside srvr's pipeline.

#### Scenario: Engine hook
- **Given** a `ginsrvr` server whose `WithEngine` hook `<hook>`
- **When** the server is constructed, started and handles a request
- **Then** the effective result is `<result>`

##### Examples:
| hook | result |
|------|--------|
| sets `e.ContextWithFallback = false` | `ContextWithFallback` is `true` |
| sets `e.HandleMethodNotAllowed = false` | `DELETE /users` returns `405` |
| sets `e.RedirectTrailingSlash = true` | `GET /users/` returns a `404` problem |
| calls `e.SetTrustedProxies([]string{"10.0.0.0/8"})` | `c.ClientIP()` honours `X-Forwarded-For` from `10.0.0.1` |
| (none) | `c.ClientIP()` ignores `X-Forwarded-For` |
| `nil` | construction returns an error wrapping `srvr.ErrConfig` |
| calls `e.Use(gin.Logger())` | construction returns an error wrapping `srvr.ErrConfig` naming the hook |
| calls `e.GET("/x", h)` | construction returns an error wrapping `srvr.ErrConfig` naming the hook |
| panics | construction returns an error wrapping `srvr.ErrConfig` containing the panic value |
