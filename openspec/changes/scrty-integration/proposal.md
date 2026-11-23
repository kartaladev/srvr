# Proposal

## Why

Services built on srvr need authentication and authorization, and the organisation's security library is scrty. Wiring scrty by hand into each engine is error-prone. The chain has to sit outside the router, refusals must render as the same RFC 9457 problems as every other srvr error, built-in endpoints can silently shadow application routes, and the chain's shutdown hooks are easy to forget. This change adds one opt-in security option per engine so a consumer wires scrty once, on any engine, and gets consistent errors, logs and lifecycle.

## What Changes

- Three new opt-in modules, nested so the core and engine modules never import scrty:
  - `github.com/kartaladev/srvr/security` (net/http and the shared code): scrty `httpsec` and `httpsec/prblmsec` (v0.2.0), prblm, srvr core. It does not import gin or fiber.
  - `github.com/kartaladev/srvr/security/ginsecurity`: adds scrty `ginsec` and gin.
  - `github.com/kartaladev/srvr/security/fibersecurity`: adds scrty `fibersec` and fiber v3.
- Core gains `Config.ProblemNamespace` / `srvr.WithProblemNamespace(uri)`: srvr, as the coordinator, owns the problem namespace and applies it to its prblm handler and so to every library integration that registers relative problem types, such as scrty's. Wiring security on a server with no namespace fails construction.
- One engine option takes scrty's own options and builds the chain inside (owner decision 2026-11-22, option-based design):
  - net/http: `security.WithSecurity(chainOpts []httpsec.Option, opts ...security.Option)`
  - gin: `ginsecurity.WithSecurity(chainOpts, opts...)`, mounting `ginsec.Middleware(chain, ginsec.WithRefusalsRendered())`
  - fiber: `fibersecurity.WithSecurity(chainOpts, opts...)`, mounting `fibersec.Middleware(chain)`

  srvr appends its logger on every engine and, on net/http only, its error handler (rendering through `srvr.WriteError`), then calls `httpsec.New`. A consumer's own `httpsec.WithErrorHandler` or `httpsec.WithLogger` is refused by scrty at build time and reported naming the field. The middleware is built from the same chain, so the two cannot mismatch, and there is no bridge object, no `ChainOptions()`, no start-time "unused chain options" warning and no `WithOwnChainHandlers`. `security.WithChainReady(func(*httpsec.Chain))` hands the built chain back for `PublishSignal`, `ReportCredentialChange` and `ExpiryTasks`.
- What the option wires, through an internal, engine-neutral `srvr.SecurityLayer` the engines consume:
  - scrty's own problem integration, `prblmsec`, registered with the server's problem handler ahead of every other mapper: every refusal `httpsec.LookupStatus` recognises becomes a typed problem (`security/<name>` under the server's namespace) with scrty's status and `prblmsec`'s members (`challenge`, `methods`, `max_age`, `rule`). Session handles and error text never appear; the challenge token (`access_token`, `token_type`) appears only by opt-in, srvr giving `prblmsec.WithoutChallengeToken()` otherwise.
  - with a consumer `ErrorRenderer`, scrty's error reaches the renderer unchanged; a renderer delegating to `srvr.DefaultErrorRenderer()` gets the same problem.
  - a startup check of `chain.Endpoints()` against the application's routes from `srvr.Routes`, registered with `srvr.WithRouteCheck`: full collisions fail start, partial shadowing by wildcard, catch-all or mounted routes warns.
  - two core shutdown hooks: `CloseSessionSignals` before drain and `FlushRefusalLogs` after drain, registered with `srvr.WithShutdownHook` and run on every stop path.

  srvr places the middleware after panic recovery and before the router, so the chain sees every request, including unmatched ones, while logging and metrics still record refusals. Each security option is an engine-only option, so another engine's option fails construction naming the right one.
- The internal `srvr/internal/engine` package (aliased in `srvr`) gains the `SecurityLayer` interface and an engine-only middleware slot. Core gains `srvr.WriteError(w, r, err) bool`, which renders an error through the server that is handling the request (its renderer, or prblm) and reports false, writing nothing, outside a srvr request.
- When security is wired, scrty owns CORS, CSRF, security headers and the client address. srvr ships none of these. When `IncludeClientAddress` is on, access records read `client.address` from `httpsec.ClientFromContext` through the shared the internal `engine.RequestState`; a request the chain refused or could not resolve omits it and carries `srvr.client.unresolved` `true` instead of the proxy's address.
- An opt-in adds the authenticated principal's id to access and error records (`enduser.id`).

## Defaults and Customization

Zero configuration beyond the engine's `WithSecurity(chainOpts)` and a `ProblemNamespace` gives:
- Every scrty refusal is answered as `prblmsec`'s RFC 9457 problem with the status scrty chose, a `trace_id`, and no error text, token, session id or internal detail. A `WWW-Authenticate` header that scrty set is kept; srvr adds none where scrty set none.
- The chain runs for every request, after recovery and before routing. A refusal still produces one access record with the problem status.
- Startup fails if an application route is fully answered by a scrty built-in endpoint; a catch-all or wildcard route that an endpoint only partly shadows gets one `WARN` record.
- Session signals close before the drain so it can finish, and sampled refusal logs flush after it, whether the server stops through `Shutdown` or through `Start`'s context.
- scrty logs through srvr's logger, so its records carry `trace_id`. The principal id is not logged.

Opt-ins:
- `WithPrincipalInLogs()` / `WithConfig(Config{LogPrincipal: true})` adds `enduser.id` to access and error records. **This loosens a privacy default.**
- `WithChallengeToken()` / `WithConfig(Config{ChallengeToken: true})` returns a login challenge's pending-session token as the `access_token` member (and `token_type`), with `Cache-Control: no-store`, so bearer-token clients can continue an MFA login. The token is never logged. **This puts a credential in a response body.**
- `WithCollisionCheck(security.CollisionWarn)` turns a startup collision error into a `WARN` record. **This loosens a safety default.**
- `security.WithConfig(security.Config)` sets every srvr security setting (all but `WithChainReady`) from a config struct that any loader can fill; options apply in argument order, the later one winning.
- `WithChainReady(fn)` hands the built chain to the consumer during construction.
- srvr always owns the chain's error handler and logger; a consumer who needs their own builds and mounts the chain without srvr's security option.

## Capabilities

### New Capabilities
- `security-bridge`: wiring scrty into a srvr server on each engine through one option that builds the chain: placement in the pipeline, logging, client address, principal logging, and lifecycle hooks at shutdown.
- `security-error-mapping`: rendering scrty refusals and challenges as RFC 9457 problems, the same way on every engine.
- `security-endpoint-collisions`: detecting at startup that application routes collide with scrty's built-in endpoints.

### Modified Capabilities
- `error-responses`: srvr owns the problem namespace (`ProblemNamespace`), and middleware renders through the server with `srvr.WriteError`.

Consolidation 2026-11-22 decided that `structured-logging` and the engine capabilities need no delta: the new log keys are additive and specified in `security-bridge` and `security-error-mapping`, and `WithSecurity` per engine is specified in `security-bridge`.

## Scope by Gall's Law

The simplest version that works end to end is a net/http srvr server with `security.WithSecurity(chainOpts)`. A request without credentials to a guarded route gets a `401` problem with `trace_id`, one access record is written, and shutdown flushes refusal logs. `ginsecurity` and then `fibersecurity` are added in later slices, each a new module. Each reuses the same layer, mapper and collision check.

Deliberately left for later:
- srvr helpers around scrty's guards (`RequireAuthenticated` and the others). Consumers use `ginsec`/`fibersec` guards directly.
- Running `chain.ExpiryTasks()` in the background. Scheduling is the application's concern (carry-over, `unassigned`).
- Naming spans and metrics after `Endpoint.OperationID`. That belongs to `otel-observability` once both changes are merged (carry-over).

## Impact

- Three new modules with scrty's dependency graph (about 24 modules for `httpsec`, plus gin or fiber for the engine modules). Consumers who don't import them are unaffected.
- **Compatibility decision:** this adds public API to the core (`WithProblemNamespace`, `WriteError`, and the `SecurityLayer` alias for the security modules) and new modules `security`, `security/ginsecurity` and `security/fibersecurity`; `ginsrvr` and `fibersrvr` gain only an engine-only middleware slot.
- **Compatibility decision:** this adds new log attributes (`enduser.id` opt-in, `srvr.security.refused`, `srvr.client.unresolved`, `srvr.warning.code` values `security_endpoint_collision`, `security_endpoint_shadowed`) and new problem extension members (`challenge`, `mfa_methods`, opt-in `access_token`). Added keys keep `srvr.log.schema`'s version; the golden-record test gains them.
- Builds on `core-server`: the `Server` lifecycle, `srvr.WithShutdownHook` with `BeforeDrain` and `AfterDrain` phases, the request pipeline order, the shared prblm options and `OnError` ownership, the `trace_id` enricher, `IncludeClientAddress` and the log keys. It also builds on `gin-and-fiber-engines`: `ginsrvr`/`fibersrvr` constructors and options, `srvr.Routes` with configurer attribution and `srvr.WithRouteCheck`, the internal `engine.RequestState` (this change adds its `ClientAddress` and `EndUserID` fields), the `server.config.warning` event, gin's body limit, ginprblm middleware handling `c.Errors`, and fiberprblm as the app `ErrorHandler`. The consolidation group must check all of these names.
- Known gaps owned by scrty, recorded as `unassigned` carry-overs: no `WWW-Authenticate` on some 401s, and no resolved client for refused requests. gin's unmatched-path 404 is rendered since scrty v0.2.0 (`ginsec.WithRefusalsRendered`, and ginprblm's deferred header).
- Version pins: scrty v0.2.0 (every scrty module in lockstep), Go 1.27. ginsec requires gin v1.12.0. fibersec and scrty require Fiber v3.7.0, as fiberprblm does.
- Consolidation 2026-11-22 (owner decisions): delegate rendering to `prblmsec` (D3), keep the challenge token opt-in (D7), srvr owns the problem namespace (D9), a custom `ErrorRenderer` receives scrty's error unchanged (D2); one engine option takes `httpsec` options and builds the chain, with per-engine modules (D1, D10).
