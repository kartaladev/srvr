# Spec Delta

## Purpose

Defines how a consumer wires a scrty security chain into a srvr server on net/http, gin or fiber v3: where the chain runs in the request pipeline, how it logs, which concerns it owns, and how it is shut down with the server.

## ADDED Requirements

### Requirement: A bridge is built from a chain
`security.New(chain, opts...)` SHALL return a bridge for a non-nil chain and SHALL return an error wrapping `security.ErrConfig` for a nil chain or an invalid option. Building a bridge SHALL NOT change the chain's behaviour.

#### Scenario: Building the bridge
- **Given** `<chain>` and options `<options>`
- **When** the consumer calls `security.New`
- **Then** it returns `<result>`

##### Examples: accepted
| chain | options | result |
|-------|---------|--------|
| a chain from `httpsec.New()` | (none) | a bridge |
| a chain from `httpsec.New(httpsec.WithBasePath("/auth"))` | `WithPrincipalInLogs()` | a bridge |
| a chain from `httpsec.New()` | `WithChallengeToken()` | a bridge |
| a chain from `httpsec.New()` | `WithOwnChainHandlers()` | a bridge |
| the same chain used for a second bridge | (none) | a second bridge |

##### Examples: rejected
| chain | options | result |
|-------|---------|--------|
| `nil` | (none) | an error wrapping `security.ErrConfig` naming `chain` |
| a chain from `httpsec.New()` | `WithCollisionCheck(99)` | an error wrapping `security.ErrConfig` naming `CollisionCheck` |
| a chain from `httpsec.New()` | `WithConfig(Config{CollisionCheck: "loud"})` | an error wrapping `security.ErrConfig` naming `CollisionCheck` and `loud` |

### Requirement: Each engine takes its own scrty middleware
Each engine SHALL accept the bridge only together with that engine's scrty middleware type: `func(http.Handler) http.Handler` for net/http, `gin.HandlerFunc` for gin, `fiber.Handler` for fiber v3. Middleware for another engine SHALL fail to compile. A nil bridge or middleware, or the core `srvr.WithSecurity` given to the gin or fiber constructor, SHALL fail construction.

#### Scenario: Engine options
- **Given** an engine `<engine>` constructed with `<option>`
- **When** the server is constructed
- **Then** construction returns `<result>`

##### Examples: accepted
| engine | option | result |
|--------|--------|--------|
| net/http | `srvr.WithSecurity(bridge, chain.Middleware())` | a server |
| gin | `ginsrvr.WithSecurity(bridge, ginsec.Middleware(chain))` | a server |
| fiber v3 | `fibersrvr.WithSecurity(bridge, fibersec.Middleware(chain))` | a server |

##### Examples: rejected
| engine | option | result |
|--------|--------|--------|
| net/http | `srvr.WithSecurity(nil, chain.Middleware())` | an error wrapping `srvr.ErrConfig` |
| net/http | `srvr.WithSecurity(bridge, nil)` | an error wrapping `srvr.ErrConfig` |
| gin | `ginsrvr.WithSecurity(bridge, nil)` | an error wrapping `srvr.ErrConfig` |
| net/http | `srvr.WithSecurity` given twice | an error wrapping `srvr.ErrConfig` naming `WithSecurity` |
| gin | `ginsrvr.WithSecurity(bridge, fibersec.Middleware(chain))` | a compile error |
| gin | `srvr.WithSecurity(bridge, chain.Middleware())` | an error wrapping `srvr.ErrConfig` naming `WithSecurity` and `ginsrvr.WithSecurity` |
| fiber v3 | `srvr.WithSecurity(bridge, chain.Middleware())` | an error wrapping `srvr.ErrConfig` naming `WithSecurity` and `fibersrvr.WithSecurity` |

### Requirement: The chain runs after recovery and before routing
The chain SHALL run for every request, matched or not. It SHALL run inside the trace-context, access-log, observer and recovery layers and outside the router. A request the chain refuses or answers SHALL still produce exactly one access record with the request's `trace_id`.

#### Scenario: Placement
- **Given** a server on `<engine>` wired with a chain that `<chain_behaviour>` and a route `GET /orders`
- **When** a client sends `<request>`
- **Then** the response status is `<status>`
- **And** exactly one access record with `msg` `<msg>` and `http.response.status_code` `<status>` is written

##### Examples:
| engine | chain_behaviour | request | status | msg |
|--------|-----------------|---------|--------|-----|
| net/http | refuses unauthenticated requests | `GET /orders` | `401` | `http.request.failed` |
| gin | refuses unauthenticated requests | `GET /orders` | `401` | `http.request.failed` |
| fiber v3 | refuses unauthenticated requests | `GET /orders` | `401` | `http.request.failed` |
| net/http | refuses unauthenticated requests | `GET /unknown` | `401` | `http.request.failed` |
| gin | refuses unauthenticated requests | `GET /unknown` | `401` | `http.request.failed` |
| fiber v3 | refuses unauthenticated requests | `GET /unknown` | `401` | `http.request.failed` |
| net/http | allows anonymous requests | `GET /unknown` | `404` | `http.request.failed` |
| gin | allows anonymous requests | `GET /unknown` | `404` | `http.request.failed` |
| fiber v3 | allows anonymous requests | `GET /unknown` | `404` | `http.request.failed` |
| net/http | answers its built-in endpoint `POST /sign-in/password` with `204` | `POST /sign-in/password` | `204` | `http.request.completed` |
| net/http | allows authenticated requests | `GET /orders` with valid credentials | `200` | `http.request.completed` |

#### Scenario: Chain mounted twice
- **Given** a net/http server with `srvr.WithSecurity(bridge, chain.Middleware())` and a group that also uses `chain.Middleware()`
- **When** an authenticated client sends `GET /orders`
- **Then** the response status is `<status>`
- **And** the chain counts `<attempts>` authentication attempts for the request

##### Examples:
| status | attempts |
|--------|----------|
| `200` | `1` |

### Requirement: Panics inside the chain are recovered
A panic raised inside the chain, including a consumer interceptor registered on the chain, SHALL be recovered by srvr's recovery layer. It SHALL be answered with a safe `500` problem and logged with its stack. The server SHALL keep serving.

#### Scenario: Panicking interceptor
- **Given** a server on `<engine>` wired with a chain whose consumer interceptor panics with `"boom"`
- **When** a client sends `GET /orders`
- **Then** the response status is `500` and the body does not contain `boom`
- **And** an `http.request.failed` record has `error.kind` `panic` and the next request is served

##### Examples:
| engine |
|--------|
| net/http |
| gin |
| fiber v3 |

### Requirement: scrty logs through srvr's logger by default
`bridge.ChainOptions()` SHALL return the `httpsec` options that make the chain log through srvr's logger and render refusals through the server handling the request. scrty's records SHALL then carry `trace_id`. A consumer's own `httpsec.WithLogger` or `httpsec.WithErrorHandler` SHALL take precedence when used in place of these options.

#### Scenario: Chain logging and rendering
- **Given** a net/http chain built with `<chain_options>` and wired into a server
- **When** a request is refused and the chain writes a refusal log
- **Then** `<outcome>`

##### Examples:
| chain_options | outcome |
|---------------|---------|
| `bridge.ChainOptions()...` | the refusal record is written through srvr's handler with the request's `trace_id` and the response is a problem |
| `httpsec.WithLogger(consumerLogger)` and `httpsec.WithErrorHandler(consumerFn)` | the consumer's logger and handler are used and srvr still writes the access record |
| `bridge.ChainOptions()...` and `httpsec.WithErrorHandler(consumerFn)` | `httpsec.New` returns an error wrapping `httpsec.ErrConfig` because the error handler is given twice |
| (none) | the response is a bare `401` with an empty body |

### Requirement: Unused chain options are reported at start
When `bridge.ChainOptions()` was never called and `WithOwnChainHandlers()` is not set, `Start` SHALL write one `WARN` record with `msg` `server.config.warning`, `srvr.warning.code` `security_chain_options_unused` and a `srvr.warning.message` naming `ChainOptions`, then serve normally. srvr SHALL NOT guess at unrendered refusals at request time.

#### Scenario: Chain options check
- **Given** a net/http server wired with a bridge where `<setup>`
- **When** the server starts and serves `<requests>` refused requests
- **Then** `<count>` records with `srvr.warning.code` `security_chain_options_unused` are written

##### Examples:
| setup | requests | count |
|-------|----------|-------|
| `bridge.ChainOptions()` was passed to `httpsec.New` | `3` | `0` |
| `bridge.ChainOptions()` was never called | `0` | `1` |
| `bridge.ChainOptions()` was never called | `3` | `1` |
| `bridge.ChainOptions()` was never called and the bridge has `WithOwnChainHandlers()` | `3` | `0` |
| `bridge.ChainOptions()` was passed and the chain's OpenID Provider answers a bare `405` | `1` | `0` |

#### Scenario: Rendering outside a srvr server
- **Given** a chain built with `bridge.ChainOptions()` used in a plain `http.Server` without srvr
- **When** a request is refused with `<refusal>`
- **Then** the response is a bare `<status>` with an empty body

##### Examples:
| refusal | status |
|---------|--------|
| `httpsec.ErrAuthenticationRequired` | `401` |
| `httpsec.ErrCredentialsMissing` | `400` |

### Requirement: scrty owns client address and browser protections
When security is wired, srvr SHALL NOT set CORS, CSRF or security headers, or resolve the client address, itself. With `IncludeClientAddress` on, `client.address` SHALL come from scrty's resolved client. When the chain refused the request or resolved no address, `client.address` SHALL be omitted and `srvr.client.unresolved` SHALL be `true`; the transport peer SHALL NOT be logged in its place.

#### Scenario: Client address
- **Given** a server on `<engine>` with `<include>`, wired with a chain trusting `10.0.0.2` for `X-Forwarded-For`
- **When** peer `<peer>` sends `X-Forwarded-For: 203.0.113.9` and the chain `<outcome>`
- **Then** the access record's `client.address` is `<logged>` and `srvr.client.unresolved` is `<unresolved>`

##### Examples:
| engine | include | peer | outcome | logged | unresolved |
|--------|---------|------|---------|--------|------------|
| net/http | `IncludeClientAddress` on | `10.0.0.2` | allows the request | `203.0.113.9` | (none) |
| gin | `IncludeClientAddress` on | `10.0.0.2` | allows the request | `203.0.113.9` | (none) |
| fiber v3 | `IncludeClientAddress` on | `10.0.0.2` | allows the request | `203.0.113.9` | (none) |
| net/http | `IncludeClientAddress` on | `198.51.100.7` | allows the request | `198.51.100.7` | (none) |
| net/http | `IncludeClientAddress` on | `10.0.0.2` | refuses the request with `401` | (none) | `true` |
| gin | `IncludeClientAddress` on | `10.0.0.2` | refuses the request with `401` | (none) | `true` |
| fiber v3 | `IncludeClientAddress` on | `10.0.0.2` | refuses the request with `401` | (none) | `true` |
| net/http | `IncludeClientAddress` on | `10.0.0.2` | allows the request with a resolved `Client.IP` of `""` | (none) | `true` |
| net/http | `IncludeClientAddress` off | `10.0.0.2` | refuses the request with `401` | (none) | (none) |

#### Scenario: No duplicate browser headers
- **Given** a server wired with a chain using `EnableSecurityHeaders` and `EnableCORS`
- **When** a client sends a CORS preflight `OPTIONS /orders` from an allowed origin
- **Then** the response status is `<status>` and each of `<headers>` appears exactly once

##### Examples:
| status | headers |
|--------|---------|
| `204` | `Access-Control-Allow-Origin`, `X-Content-Type-Options` |

### Requirement: The bridge is configurable by options and by config struct
Every bridge setting SHALL be settable by its own option and through `security.WithConfig(security.Config)`. `WithConfig` SHALL set every field, a zero field meaning the default (`ChallengeMembers` nil means on; `CollisionCheck` empty means `error`, matched case-insensitively). Options SHALL apply in argument order, the later one winning.

#### Scenario: Configuration sources
- **Given** a bridge built with `<options>`
- **When** the bridge is used by a server
- **Then** the effective `<setting>` is `<effective>`

##### Examples:
| options | setting | effective |
|---------|---------|-----------|
| (none) | `LogPrincipal` | `false` |
| `WithPrincipalInLogs()` | `LogPrincipal` | `true` |
| `WithConfig(Config{LogPrincipal: true})` | `LogPrincipal` | `true` |
| `WithPrincipalInLogs()`, then `WithConfig(Config{})` | `LogPrincipal` | `false` |
| `WithConfig(Config{})`, then `WithPrincipalInLogs()` | `LogPrincipal` | `true` |
| `WithConfig(Config{})` | `ChallengeMembers` | `true` |
| `WithConfig(Config{ChallengeMembers: ptr(false)})`, then `WithChallengeMembers(true)` | `ChallengeMembers` | `true` |
| `WithConfig(Config{CollisionCheck: "WARN"})` | `CollisionCheck` | `CollisionWarn` |
| `WithConfig(Config{CollisionCheck: "warn"})`, then `WithCollisionCheck(security.CollisionError)` | `CollisionCheck` | `CollisionError` |
| `WithConfig(Config{OwnChainHandlers: true, ChallengeToken: true})` | `OwnChainHandlers` and `ChallengeToken` | `true` and `true` |

### Requirement: Principal id is logged only by opt-in
Access and error records SHALL NOT contain the authenticated principal's id by default. With `WithPrincipalInLogs()` they SHALL contain `enduser.id` from `identity.PrincipalFromContext`, and SHALL contain no `enduser.id` when no principal is present.

#### Scenario: Principal in records
- **Given** a bridge configured with `<config>`
- **When** `<caller>` sends `GET /orders`
- **Then** the access record's `enduser.id` is `<enduser>`

##### Examples:
| config | caller | enduser |
|--------|--------|---------|
| (none) | user `u-42` | (none) |
| `WithPrincipalInLogs()` | user `u-42` | `u-42` |
| `WithConfig(Config{LogPrincipal: true})` | user `u-42` | `u-42` |
| `WithPrincipalInLogs()` | an anonymous client | (none) |
| `WithPrincipalInLogs()` | a refused client | (none) |

### Requirement: Shutdown closes the chain's background work
The bridge SHALL register core shutdown hooks: `CloseSessionSignals` as a `srvr.BeforeDrain` hook, so long-lived signal streams end and drain can finish, and `FlushRefusalLogs` as a `srvr.AfterDrain` hook. Each runs once per server on every stop path; a never-started server, or a `Start` that fails after construction, still closes signals through the `AfterDrain` hook. `httpsec.ErrSessionSignalsDisabled` SHALL count as success; other errors SHALL be returned joined by the stop call.

#### Scenario: Shutdown hooks
- **Given** a running server wired with a chain where `<hooks>`
- **When** the server is stopped by `<stop>`
- **Then** the stop call returns `<result>`
- **And** `CloseSessionSignals` is called `<close_calls>` and `FlushRefusalLogs` is called `<flush_calls>`

##### Examples:
| hooks | stop | result | close_calls | flush_calls |
|-------|------|--------|-------------|-------------|
| session signals are not enabled | `Shutdown` with a `1s` deadline | `nil` | once | once |
| session signals are enabled and close promptly | `Shutdown` with a `1s` deadline | `nil` | once, before in-flight requests finish draining | once, after drain |
| session signals are enabled and close promptly | cancelling the `Start` context | `Start` returns `nil` | once | once |
| `FlushRefusalLogs` returns `errors.New("reporter down")` | `Shutdown` with a `1s` deadline | an error containing `reporter down` | once | once |
| `FlushRefusalLogs` returns `errors.New("reporter down")` | cancelling the `Start` context | `Start` returns an error containing `reporter down` | once | once |
| `CloseSessionSignals` blocks past the deadline | `Shutdown` with a `50ms` deadline | an error matching `context.DeadlineExceeded` | once | once |
| `FlushRefusalLogs` panics with `"boom"` | `Shutdown` with a `1s` deadline | an error containing `boom` | once | once |
| session signals are enabled | `Shutdown` called a second time | `nil` | once in total | once in total |

#### Scenario: Shutdown before start
- **Given** a server wired with security that was never started
- **When** `Shutdown` is called
- **Then** `Shutdown` returns `<result>`
- **And** `CloseSessionSignals` is called `<close_calls>` and `FlushRefusalLogs` is called `<flush_calls>`

##### Examples:
| result | close_calls | flush_calls |
|--------|-------------|-------------|
| `nil` | once, from the `srvr.AfterDrain` phase | once |

#### Scenario: Start fails after construction
- **Given** a server wired with security where `<failure>`
- **When** `Start` is called
- **Then** `Start` returns `<result>`
- **And** `CloseSessionSignals` is called `<close_calls>` and `FlushRefusalLogs` is called `<flush_calls>`

##### Examples:
| failure | result | close_calls | flush_calls |
|---------|--------|-------------|-------------|
| a route collides with `scrty.sign-in.password` | an error wrapping `srvr.ErrRouteConfig` | once, from the `srvr.AfterDrain` phase | once |
| a route collides with `scrty.sign-in.password` and `Shutdown` is called afterwards | an error wrapping `srvr.ErrRouteConfig` | once in total | once in total |
| the address is already in use | an error matching `*net.OpError` | once, from the `srvr.AfterDrain` phase | once |
| the server is already running | an error matching `srvr.ErrServerStarted` | never | never |
| `Start(nil)` is called | an error wrapping `srvr.ErrConfig` | never | never |

### Requirement: Body limits apply before the chain
The server's `MaxRequestBodyBytes` SHALL apply before the chain reads a login body on every engine: core's body limit on net/http, the gin engine's body limit from `gin-and-fiber-engines` on gin, and fiber's `BodyLimit` on fiber v3, because fasthttp reads the body before the chain's own limit applies. An oversized body SHALL get a `413` problem on every engine.

#### Scenario: Oversized login body
- **Given** a server on `<engine>` with `MaxRequestBodyBytes` `1024`, wired with a chain enabling form login
- **When** a client posts `<size>` bytes to `POST /sign-in/password`
- **Then** the response status is `<status>` with content type `application/problem+json`

##### Examples:
| engine | size | status |
|--------|------|--------|
| net/http | `2048` | `413` |
| gin | `2048` | `413` |
| fiber v3 | `2048` | `413` |
| fiber v3 | `512` | `401` |
