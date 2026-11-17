# Spec Delta

## Purpose

Defines how a srvr server wired with scrty detects, at start, application routes that scrty's built-in endpoints would answer first: a route the endpoints answer completely fails start, and a wildcard, catch-all or mounted route that endpoints only partly shadow is reported as a warning.

## ADDED Requirements

### Requirement: Colliding routes fail start
Through a route check registered with `srvr.WithRouteCheck`, a server wired with security SHALL compare every route from `engine.Routes` with every endpoint from `chain.Endpoints()` before binding. A route whose every match an endpoint answers first is a collision: `Start` SHALL return an error wrapping `srvr.ErrRouteConfig` naming the route, its configurer, the endpoint's `OperationID` and its `PlacedBy`.

#### Scenario: Collision outcome
- **Given** a chain with base path `<base>` exposing `<endpoint>`
- **And** an application route `<route>` registered by configurer `AuthAPI`
- **When** the server starts
- **Then** `Start` returns `<result>`

##### Examples: collisions
| base | endpoint | route | result |
|------|----------|-------|--------|
| (none) | `POST /sign-in/password` exact, other methods pass through | `POST /sign-in/password` | an error naming `POST /sign-in/password`, `AuthAPI`, `scrty.sign-in.password` and its `PlacedBy` |
| `/auth` | `POST /auth/sign-in/password` exact | `POST /auth/sign-in/password` | an error naming `scrty.sign-in.password` |
| (none) | `GET /session/signals` exact, other methods refused | `POST /session/signals` | an error naming `POST /session/signals` and `scrty.session.signals` |
| (none) | `GET /sso/login/{provider}` segment match | `GET /sso/login/google` | an error naming `GET /sso/login/google` |
| (none) | `GET /sso/login/{provider}` segment match | `GET /sso/login/{name}` | an error naming `GET /sso/login/{name}` |
| (none) | `POST /mfa/verify/{method}` subtree match, other methods pass through | `POST /mfa/verify/totp/extra` | an error naming `POST /mfa/verify/totp/extra` and `scrty.mfa.verify` |

##### Examples: no collision
| base | endpoint | route | result |
|------|----------|-------|--------|
| (none) | `POST /sign-in/password` exact, other methods pass through | `GET /sign-in/password` | `nil` and the server is ready |
| `/auth` | `POST /auth/sign-in/password` exact | `POST /sign-in/password` | `nil` and the server is ready |
| (none) | `GET /sso/login/{provider}` segment match | `GET /sso/login` | `nil` and the server is ready |
| (none) | `GET /sso/login/{provider}` segment match | `GET /sso/login/google/extra` | `nil` and the server is ready |
| (none) | (no endpoints enabled) | `POST /sign-in/password` | `nil` and the server is ready |
| (none) | `POST /mfa/verify/{method}` subtree match, other methods pass through | `GET /mfa/verify/totp` | `nil` and the server is ready |

### Requirement: Partial shadowing is a warning
A wildcard, catch-all or mounted route that an endpoint answers for only some of its matches SHALL NOT fail start. `Start` SHALL write one `WARN` record with `msg` `server.config.warning`, `srvr.warning.code` `security_endpoint_shadowed` and a `srvr.warning.message` naming every shadowed route, its configurer and the endpoint's `OperationID`, then serve.

#### Scenario: Shadowing routes
- **Given** a chain exposing `<endpoints>`
- **And** an application route `<route>` registered by configurer `WebApp`
- **When** the server starts
- **Then** `Start` `<start>` and `<count>` `security_endpoint_shadowed` records are written naming `<pairs>` shadowed pairs
- **And** a request to `<probe>` is answered by `<answered_by>`

##### Examples:
| endpoints | route | start | count | pairs | probe | answered_by |
|-----------|-------|-------|-------|-------|-------|-------------|
| `POST /sign-in/password` exact | `GET /{path...}` | becomes ready | `0` | `0` | `GET /sign-in/password` | the route |
| `POST /sign-in/password` exact, other methods refused | `GET /{path...}` | becomes ready | `1` | `1` | `GET /sign-in/password` | the chain |
| `POST /sign-in/password` exact | `POST /sign-in/{method}` | becomes ready | `1` | `1` | `POST /sign-in/password` | the chain |
| `POST /sign-in/password` exact, `POST /sign-out` exact | a mounted handler at `/` for every method | becomes ready | `1` | `2` | `GET /assets/app.js` | the route |
| `POST /sign-in/password` exact | a mounted handler at `/sign-in/` for every method | becomes ready | `1` | `1` | `POST /sign-in/password` | the chain |

### Requirement: Every collision is reported at once
When several routes collide, `Start` SHALL report all of them in one joined error, ordered by route registration, so the consumer fixes them in one pass.

#### Scenario: Several collisions
- **Given** application routes `<routes>` that collide with scrty endpoints
- **When** the server starts
- **Then** the error lists `<count>` collisions in the order `<order>`

##### Examples:
| routes | count | order |
|--------|-------|-------|
| `POST /sign-in/password`, `POST /sign-out` | `2` | `POST /sign-in/password`, `POST /sign-out` |

### Requirement: Collision checking can be relaxed explicitly
With `WithCollisionCheck(security.CollisionWarn)` the server SHALL start despite collisions and SHALL write one `server.config.warning` record with `srvr.warning.code` `security_endpoint_collision` listing every collision. With `CollisionOff` the bridge SHALL register no route check. The default SHALL be `CollisionError`, under which a collision fails `Start` and no shadowing warning is written.

#### Scenario: Collision modes
- **Given** a bridge configured with `<mode>`, a route colliding with `scrty.sign-in.password` and a route `POST /sign-in/{method}` shadowed by it
- **When** the server starts
- **Then** `Start` `<start>`
- **And** `<collision_records>` `security_endpoint_collision` and `<shadow_records>` `security_endpoint_shadowed` records are written

##### Examples:
| mode | start | collision_records | shadow_records |
|------|-------|-------------------|----------------|
| (none) | returns an error wrapping `srvr.ErrRouteConfig` | `0` | `0` |
| `WithCollisionCheck(security.CollisionWarn)` | becomes ready | `1` | `1` |
| `WithConfig(Config{CollisionCheck: "warn"})` | becomes ready | `1` | `1` |
| `WithCollisionCheck(security.CollisionOff)` | becomes ready | `0` | `0` |
| `WithConfig(Config{CollisionCheck: "loud"})` | does not run because `security.New` returns an error wrapping `security.ErrConfig` | `0` | `0` |

### Requirement: Collisions are checked on every engine
The collision check SHALL use each engine's declared routes from `engine.Routes` (net/http patterns, gin full paths, fiber route paths, each with its configurer; no `Use` entries or fiber implicit `HEAD`) and SHALL give the same result on every engine for equivalent routes.

#### Scenario: Engine equivalence
- **Given** a server on `<engine>` with route `<route>` and a chain exposing `POST /sign-in/password`
- **When** the server starts
- **Then** `Start` `<outcome>`

##### Examples:
| engine | route | outcome |
|--------|-------|---------|
| net/http | `POST /sign-in/password` | returns an error naming `scrty.sign-in.password` |
| gin | `POST /sign-in/password` | returns an error naming `scrty.sign-in.password` |
| fiber v3 | `POST /sign-in/password` | returns an error naming `scrty.sign-in.password` |
| net/http | `POST /sign-in/{method}` | becomes ready with one `security_endpoint_shadowed` record |
| gin | `POST /sign-in/:method` | becomes ready with one `security_endpoint_shadowed` record |
| fiber v3 | `POST /sign-in/:method` | becomes ready with one `security_endpoint_shadowed` record |
