# fiber-engine Specification

## Purpose

Defines how the Fiber v3 engine module serves consumer routes with srvr's lifecycle, RFC 9457 problems, trace correlation and access records, and how it handles Fiber's pooled contexts, missing cancellation and shutdown behaviour.

## Requirements

### Requirement: Fiber route configurers receive a router
`fibersrvr` SHALL accept `srvr.RouteConfigurer[fiber.Router]` through `Register` and SHALL call each `Configure` once, in registration order, during `Start`, with a router rooted at `/`, before the listener is bound. Duplicate routes, which fiber accepts silently and of which it serves only the first, SHALL fail `Start` (see "Fiber duplicate routes are compared as fiber matches them").

#### Scenario: Fiber registration
- **Given** a `fibersrvr` server and configurer `<configurer>`
- **When** the consumer registers it and starts the server
- **Then** `Register` returns `<register>` and `Start` `<start>`

##### Examples: accepted
| configurer | register | start |
|------------|----------|-------|
| adds `POST /registrations` with `router.Use(audit).Post("/registrations", h.Register)` | `nil` | becomes ready |
| adds group `/v1` with `Get("/users/:id", h)` | `nil` | becomes ready |

##### Examples: rejected
| configurer | register | start |
|------------|----------|-------|
| `nil` | an error wrapping `srvr.ErrRouteConfig` | (none) |
| a typed-nil pointer | an error wrapping `srvr.ErrRouteConfig` | (none) |
| panics with `boom` in `Configure` | `nil` | returns an error wrapping `srvr.ErrRouteConfig` containing `boom` and the configurer type |
| registers a route with handler `nil` | `nil` | returns an error wrapping `srvr.ErrRouteConfig` |
| registers `Get("/users/:id")` and `Get("/users/:name")` | `nil` | returns an error wrapping `srvr.ErrRouteConfig` naming `/users/:name` |
| registers `Get("/users/:id")` twice | `nil` | returns an error wrapping `srvr.ErrRouteConfig` naming `/users/:id` |
| one configurer registers `Get("/a")`, another registers `Get("/a")` | `nil` | returns an error wrapping `srvr.ErrRouteConfig` naming `/a` and both configurer types |

### Requirement: Fiber duplicate routes are compared as fiber matches them
Two fiber routes SHALL conflict, failing `Start` with `srvr.ErrRouteConfig`, when they have the same method and the same path after normalising it as fiber matches it: parameter names ignored, letters lowercased unless the app's `CaseSensitive` is on, a trailing slash removed unless its `StrictRouting` is on (both read after `WithFiberConfig`), with the routes of mounted sub-apps compared at their full mounted path.

#### Scenario: Fiber duplicate normalisation
- **Given** a `fibersrvr` server and configurer `<configurer>`
- **When** the consumer registers it and starts the server
- **Then** `Register` returns `<register>` and `Start` `<start>`

##### Examples: accepted
| configurer | register | start |
|------------|----------|-------|
| registers `Get("/users")` and `Get("/Users")` with `WithFiberConfig(func(c *fiber.Config){ c.CaseSensitive = true })` | `nil` | becomes ready, and `GET /Users` reaches the second route |
| registers `Get("/users")` and `Get("/users/")` with `WithFiberConfig(func(c *fiber.Config){ c.StrictRouting = true })` | `nil` | becomes ready, and `GET /users/` reaches the second route |
| registers `Get("/users")` and mounts sub-app `sub` with `Get("/users")` with `Use("/api", sub)` | `nil` | becomes ready |

##### Examples: rejected
| configurer | register | start |
|------------|----------|-------|
| registers `Get("/users")` and `Get("/users/")` | `nil` | returns an error wrapping `srvr.ErrRouteConfig` naming `/users/` |
| registers `Get("/users")` and `Get("/Users")` | `nil` | returns an error wrapping `srvr.ErrRouteConfig` naming `/Users` |
| registers `Get("/users")` and `Get("/users/")` with `WithFiberConfig(func(c *fiber.Config){ c.CaseSensitive = true })` | `nil` | returns an error wrapping `srvr.ErrRouteConfig` naming `/users/` |
| registers `Get("/users")` and `Get("/Users")` with `WithFiberConfig(func(c *fiber.Config){ c.StrictRouting = true })` | `nil` | returns an error wrapping `srvr.ErrRouteConfig` naming `/Users` |
| mounts sub-app `sub` with `Get("/Users/")` with `Use("/api", sub)` and registers `Get("/api/users")` | `nil` | returns an error wrapping `srvr.ErrRouteConfig` naming `/api/Users/` and `/api/users` |
| one configurer mounts sub-app `sub` with `Get("/x")` with `Use("/", sub)`, another registers `Get("/x")` | `nil` | returns an error wrapping `srvr.ErrRouteConfig` naming `/x` and both configurer types |

### Requirement: Fiber handler errors become problems
An error returned by a Fiber handler or middleware SHALL be rendered by fiberprblm's error handler as an RFC 9457 problem with the `trace_id` member; a `*fiber.Error` code SHALL be a status hint and its message SHALL NOT be sent.

#### Scenario: Fiber error to problem
- **Given** a Fiber route `GET /items/:id` whose handler `<behaviour>`
- **When** a client sends `GET /items/7`
- **Then** the response status is `<status>` with content type `<content_type>`
- **And** the body does not contain `<hidden>`

##### Examples:
| behaviour | status | content_type | hidden |
|-----------|--------|--------------|--------|
| returns `c.JSON(fiber.Map{"ok": true})` | `200` | `application/json; charset=utf-8` | (none) |
| returns `prblm.ErrNotFound` | `404` | `application/problem+json` | (none) |
| returns `fiber.NewError(409, "dup key alice")` | `409` | `application/problem+json` | `alice` |
| returns `errors.New("pq: auth failed")` | `500` | `application/problem+json` | `pq:` |
| returns `fiber.ErrForbidden` | `403` | `application/problem+json` | (none) |

### Requirement: Fiber unmatched routes are problems
The Fiber engine SHALL answer unmatched paths with a `404` problem and wrong methods with a `405` problem carrying `Allow`.

#### Scenario: Fiber no route or wrong method
- **Given** Fiber routes `GET /users` and `POST /users`
- **When** a client sends `<request>`
- **Then** the response status is `<status>` with content type `application/problem+json`
- **And** the `Allow` header is `<allow>`

##### Examples:
| request | status | allow |
|---------|--------|-------|
| `GET /nope` | `404` | (none) |
| `DELETE /users` | `405` | `GET, HEAD, POST` |

### Requirement: Fiber panics are recovered by srvr
The Fiber engine SHALL install fiberprblm recovery outermost after srvr's trace and access middleware; a panic SHALL become a safe `500` problem and an `http.request.failed` record with the stack.

#### Scenario: Fiber panic
- **Given** a Fiber handler that panics with `<value>`
- **When** a client sends a request to it
- **Then** the client receives `<response>`
- **And** the server keeps serving the next request with status `200`

##### Examples:
| value | response |
|-------|----------|
| `"boom"` | status `500` problem without `boom` |
| `errors.New("db down")` | status `500` problem without `db down` |
| `http.ErrAbortHandler` | status `500` problem; the record has `error.kind` `panic` and no `srvr.response.aborted` (fiber has no abort sentinel; parity exception) |

### Requirement: Fiber values kept by srvr are copied
srvr SHALL copy every value it keeps beyond the handler's return (route, method, header values, trace ids, error text) out of Fiber's pooled buffers, so records stay correct when contexts are reused, without requiring `Immutable`.

#### Scenario: Context reuse
- **Given** a Fiber server with `Immutable` `<immutable>` receiving `<requests>` concurrent requests to different routes
- **When** all access records are written
- **Then** every record's `http.route` and `trace_id` match its own request

##### Examples:
| immutable | requests |
|-----------|----------|
| `false` | `1000` |
| `true` | `1000` |

### Requirement: Fiber handlers get a cancellable request context
Because `fiber.Ctx` never cancels, srvr SHALL attach trace and logger to `c.Context()`, and `srvr.TraceFromContext` and `srvr.LoggerFromContext` SHALL accept both `c` and `c.Context()`; the context SHALL be cancelled when the server's drain deadline forces shutdown.

#### Scenario: Reading trace through fiber.Ctx
- **Given** a Fiber handler that calls `<accessor>`
- **When** a request arrives with `traceparent` `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01`
- **Then** the accessor returns `<result>`

##### Examples:
| accessor | result |
|----------|--------|
| `srvr.TraceFromContext(c)` | trace id `4bf92f3577b34da6a3ce929d0e0e4736` and `true` |
| `srvr.TraceFromContext(c.Context())` | trace id `4bf92f3577b34da6a3ce929d0e0e4736` and `true` |
| `srvr.LoggerFromContext(c.Context())` | a logger whose records carry that trace id |

#### Scenario: Forced shutdown cancels handlers
- **Given** a Fiber handler blocked on `<-c.Context().Done()`
- **When** `Shutdown` is called with a deadline of `<deadline>`
- **Then** `Shutdown` returns `<result>` within `<bound>`

##### Examples:
| deadline | result | bound |
|----------|--------|-------|
| `50ms` | an error matching `context.DeadlineExceeded` | `1s` |

### Requirement: Fiber access records use the matched route
The Fiber access record SHALL read `http.route` from the matched route after the handler chain returns (not the middleware's own route), omit `http.route` and log `http.request.failed` when no route matched, and SHALL be identical in keys and levels to the net/http engine's record.

#### Scenario: Fiber route attribute
- **Given** Fiber route `GET /users/:id` behind group middleware
- **When** a client sends `<request>`
- **Then** the access record has `msg` `<msg>`, `level` `<level>`, `http.route` `<route>` and `http.response.status_code` `<status>`

##### Examples:
| request | msg | level | route | status |
|---------|-----|-------|-------|--------|
| `GET /users/42` | `http.request.completed` | `INFO` | `/users/:id` | `200` |
| `GET /nope` | `http.request.failed` | `WARN` | (none) | `404` |
| `DELETE /users/42` | `http.request.failed` | `WARN` | (none) | `405` |

### Requirement: Fiber shutdown closes lingering connections
On a drain deadline the Fiber engine SHALL close remaining connections (Fiber's `ShutdownWithContext` returns without closing them) and SHALL close idle keep-alive connections, so `Shutdown` never leaves sockets open after it returns.

#### Scenario: Fiber drain
- **Given** a running Fiber server with `<connections>`
- **When** `Shutdown` is called with a deadline of `<deadline>`
- **Then** `Shutdown` returns `<result>`
- **And** after it returns, open server connections number `0`

##### Examples:
| connections | deadline | result |
|-------------|----------|--------|
| one idle keep-alive connection | `1s` | `nil` |
| one request needing `10ms` | `1s` | `nil` |
| one request needing `5s` | `50ms` | an error matching `context.DeadlineExceeded` |

### Requirement: Fiber configuration is derived from srvr's
srvr SHALL derive Fiber's `ErrorHandler`, `BodyLimit` and timeouts from srvr's config, then apply `fibersrvr.WithFiberConfig(func(*fiber.Config))`, then re-assert the error handler. srvr SHALL own the `ListenConfig` (startup message off, prefork never enabled) and SHALL NOT expose it.

#### Scenario: Fiber config hook
- **Given** a `fibersrvr` server configured with `<config>`
- **When** the server is constructed and handles a request
- **Then** the result is `<result>`

##### Examples:
| config | result |
|--------|--------|
| (none) | a `4194304`-byte body is accepted |
| (none) | a `4194305`-byte body gets a `413` problem |
| `WithConfig(Config{MaxRequestBodyBytes: 10})` | a `10`-byte body is accepted |
| `WithConfig(Config{MaxRequestBodyBytes: 10})` | an `11`-byte body gets a `413` problem |
| `WithConfig(Config{MaxRequestBodyBytes: -1})` | an `8388608`-byte body is accepted |
| `WithFiberConfig(func(c *fiber.Config){ c.ErrorHandler = nil })` | errors still render as problems |
| `WithFiberConfig(func(c *fiber.Config){ c.Immutable = true })` | `Immutable` is `true` |
| `WithFiberConfig(nil)` | construction returns an error wrapping `srvr.ErrConfig` |
| `WithFiberConfig(func(c *fiber.Config){ panic("boom") })` | construction returns an error wrapping `srvr.ErrConfig` containing `boom` |

### Requirement: Fiber protocol rejections are logged with their problem
For a request fasthttp rejects before routing (`408`, `413`, `431`), fiber replays srvr's middleware with the status already set and renders through its error handler afterwards. The access stage SHALL detect this by a status of 400 or more set before the chain ran, SHALL NOT render the error itself, and SHALL write one `http.request.failed` record whose `problem` group carries that status and its standard title.

#### Scenario: Protocol rejection record
- **Given** a `fibersrvr` server with default limits serving `POST /upload` over a real listener
- **When** a client sends `<request>`
- **Then** the response is a `<status>` problem with title `<title>`
- **And** exactly one access record is written with `msg` `http.request.failed`, `level` `WARN`, `problem.status` `<status>`, `problem.title` `<title>` and no `http.route`

##### Examples:
| request | status | title |
|---------|--------|-------|
| a `4194305`-byte body | `413` | `Request Entity Too Large` |
| `70000` bytes of headers | `431` | `Request Header Fields Too Large` |
| headers that stall past `ReadTimeout` set to `100ms` | `408` | `Request Timeout` |

### Requirement: Fiber header limit is derived and capped
srvr SHALL set fiber's `ReadBufferSize` to `MaxHeaderBytes` capped at `65536` bytes, because fasthttp allocates that buffer per connection; capping a value the consumer set (not the default) SHALL write a `server.config.warning` with code `fiber_header_limit_capped`. Headers over the buffer SHALL get a `431` problem. `ReadHeaderTimeout` has no fasthttp equivalent and is a documented parity exception.

#### Scenario: Fiber header limit
- **Given** a `fibersrvr` server configured with `<config>`
- **When** a client sends a request whose headers total `<size>` bytes over a real listener
- **Then** fiber's `ReadBufferSize` is `<buffer>`, the response status is `<status>` and `<warnings>` `fiber_header_limit_capped` records were written

##### Examples:
| config | size | buffer | status | warnings |
|--------|------|--------|--------|----------|
| (none) | `60000` | `65536` | `200` | `0` |
| (none) | `70000` | `65536` | `431` | `0` |
| `WithMaxHeaderBytes(8192)` | `9000` | `8192` | `431` | `0` |
| `WithMaxHeaderBytes(8192)` | `4000` | `8192` | `200` | `0` |
| `WithMaxHeaderBytes(65536)` | `60000` | `65536` | `200` | `0` |
| `WithMaxHeaderBytes(2097152)` | `70000` | `65536` | `431` | `1` |
