# Proposal

## Why

Services built on srvr need authentication and authorization, and the organisation's security library is scrty. Wiring scrty by hand into each engine is error-prone. The chain has to sit outside the router, refusals must render as the same RFC 9457 problems as every other srvr error, built-in endpoints can silently shadow application routes, and the chain's shutdown hooks are easy to forget. This change adds an opt-in bridge so a consumer wires scrty once, on any engine, and gets consistent errors, logs and lifecycle.

## What Changes

- New module `github.com/kartaladev/srvr/security` (package `security`). Dependencies: scrty `httpsec` core (v0.1.1 or later), prblm core and srvr core. It does not import gin or fiber.
- `security.New(chain *httpsec.Chain, opts...) (*security.Bridge, error)`. The bridge offers:
  - a prblm mapper that turns every scrty refusal into a problem. It matches when `httpsec.StatusForError` gives a status other than 500, on a `*httpsec.ChallengeError`, or on scrty's three exported 500-class sentinels; the status comes from `StatusForError`. `*httpsec.ChallengeError` adds `challenge` and `mfa_methods` extension members. Session handles and error text never appear; the challenge token appears only by opt-in.
  - chain options for the consumer to pass to `httpsec.New`: an error handler that renders through the running srvr server, and srvr's logger. `Start` warns once when they were never used.
  - a startup check of `chain.Endpoints()` against the application's routes from `engine.Routes`, registered with `srvr.WithRouteCheck`: full collisions fail start, partial shadowing by wildcard, catch-all or mounted routes warns.
  - two core shutdown hooks: `CloseSessionSignals` before drain and `FlushRefusalLogs` after drain, registered with `srvr.WithShutdownHook` and run on every stop path.
- Each engine gets a typed option that takes the bridge and that engine's own scrty middleware:
  - net/http: `srvr.WithSecurity(bridge, chain.Middleware())`
  - Gin: `ginsrvr.WithSecurity(bridge, ginsec.Middleware(chain))`
  - Fiber: `fibersrvr.WithSecurity(bridge, fibersec.Middleware(chain))`

  srvr places the middleware after panic recovery and before the router, so the chain sees every request, including unmatched ones, while logging and metrics still record refusals. Passing middleware built for another engine does not compile, and the core `srvr.WithSecurity` given to the gin or fiber constructor fails construction.
- The public `srvr/engine` package gains an engine-neutral `engine.SecurityLayer` interface, which the bridge implements. Core gains `srvr.WriteError(w, r, err)`, which renders an error through the server that is handling the request.
- When security is wired, scrty owns CORS, CSRF, security headers and the client address. srvr ships none of these. When `IncludeClientAddress` is on, access records read `client.address` from `httpsec.ClientFromContext` through the shared `engine.RequestState`; a request the chain refused or could not resolve omits it and carries `srvr.client.unresolved` `true` instead of the proxy's address.
- An opt-in adds the authenticated principal's id to access and error records (`enduser.id`).

## Defaults and Customization

Zero configuration beyond `security.New(chain)` and the engine option gives:
- Every scrty refusal is answered as an RFC 9457 problem with the status scrty chose, a `trace_id`, and no error text, token, session id or internal detail. A `WWW-Authenticate` header that scrty set is kept; srvr adds none where scrty set none.
- The chain runs for every request, after recovery and before routing. A refusal still produces one access record with the problem status.
- Startup fails if an application route is fully answered by a scrty built-in endpoint; a catch-all or wildcard route that an endpoint only partly shadows gets one `WARN` record.
- Session signals close before the drain so it can finish, and sampled refusal logs flush after it, whether the server stops through `Shutdown` or through `Start`'s context.
- scrty logs through srvr's logger, so its records carry `trace_id`. The principal id is not logged.

Opt-ins:
- `WithPrincipalInLogs()` / `WithConfig(Config{LogPrincipal: true})` adds `enduser.id` to access and error records. **This loosens a privacy default.**
- `WithChallengeMembers(false)` leaves the `challenge` and `mfa_methods` members out of problem bodies. The members are on by default because clients need them to continue a login.
- `WithChallengeToken()` / `WithConfig(Config{ChallengeToken: true})` returns a login challenge's pending-session token as the `access_token` member, with `Cache-Control: no-store`, so bearer-token clients can continue an MFA login. The token is never logged. **This puts a credential in a response body.**
- `WithCollisionCheck(security.CollisionWarn)` turns a startup collision error into a `WARN` record. **This loosens a safety default.**
- `security.WithConfig(security.Config)` sets every bridge setting from a config struct that any loader can fill; options apply in argument order, the later one winning.
- `WithOwnChainHandlers()` declares that the consumer gave their own `httpsec.WithErrorHandler` and `httpsec.WithLogger`, which silences the start-time warning about unused chain options.
- A consumer may still set their own `httpsec.WithErrorHandler` and `httpsec.WithLogger`. They then own rendering and logging, which the bridge documents.

## Capabilities

### New Capabilities
- `security-bridge`: wiring a scrty chain into a srvr server on each engine: placement in the pipeline, logging, client address, principal logging, and lifecycle hooks at shutdown.
- `security-error-mapping`: rendering scrty refusals and challenges as RFC 9457 problems, the same way on every engine.
- `security-endpoint-collisions`: detecting at startup that application routes collide with scrty's built-in endpoints.

### Modified Capabilities
<!-- None at planning time. The consolidation group re-checks whether `error-responses`, `structured-logging` and the engine capabilities from `gin-and-fiber-engines` need delta specs for `srvr.WriteError`, `SecurityLayer` and `WithSecurity` once those changes are merged. -->

## Scope by Gall's Law

The simplest version that works end to end is a net/http srvr server with `srvr.WithSecurity(bridge, chain.Middleware())`. A request without credentials to a guarded route gets a `401` problem with `trace_id`, one access record is written, and shutdown flushes refusal logs. Gin and then Fiber are added in later slices. Each reuses the same mapper and collision check.

Deliberately left for later:
- srvr helpers around scrty's guards (`RequireAuthenticated` and the others). Consumers use `ginsec`/`fibersec` guards directly.
- Running `chain.ExpiryTasks()` in the background. Scheduling is the application's concern (carry-over, `unassigned`).
- Naming spans and metrics after `Endpoint.OperationID`. That belongs to `otel-observability` once both changes are merged (carry-over).

## Impact

- New module with scrty's dependency graph (about 24 modules for `httpsec`). Consumers who don't import `security` are unaffected.
- **Compatibility decision:** this adds public API to the core (`WithSecurity`, `WriteError`), to `srvr/engine` (`SecurityLayer`) and to `ginsrvr` and `fibersrvr` (`WithSecurity`).
- **Compatibility decision:** this adds new log attributes (`enduser.id` opt-in, `srvr.security.refused`, `srvr.client.unresolved`, `srvr.warning.code` values `security_chain_options_unused`, `security_endpoint_collision`, `security_endpoint_shadowed`) and new problem extension members (`challenge`, `mfa_methods`, opt-in `access_token`). Added keys keep `srvr.log.schema`'s version; the golden-record test gains them.
- Builds on `core-server`: the `Server` lifecycle, `srvr.WithShutdownHook` with `BeforeDrain` and `AfterDrain` phases, the request pipeline order, the shared prblm options and `OnError` ownership, the `trace_id` enricher, `IncludeClientAddress` and the log keys. It also builds on `gin-and-fiber-engines`: `ginsrvr`/`fibersrvr` constructors and options, `engine.Routes` with configurer attribution and `srvr.WithRouteCheck`, `engine.RequestState` (this change adds its `ClientAddress` and `EndUserID` fields), the `server.config.warning` event, gin's body limit, ginprblm middleware handling `c.Errors`, and fiberprblm as the app `ErrorHandler`. The consolidation group must check all of these names.
- Known gaps owned by scrty, recorded as `unassigned` carry-overs: no `WWW-Authenticate` on some 401s, no resolved client for refused requests, gin's committed unmatched-path 404, and no accessor for "error handler configured".
- Version pins: scrty v0.1.1 (v0.1.0 is retracted), Go 1.27. ginsec requires gin v1.12.0. fibersec requires Fiber v3.6.1, which MVS raises to v3.7.0 alongside fiberprblm.
