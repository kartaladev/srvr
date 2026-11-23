# Design

## Context

See proposal.md - Why. Consolidated on 2026-11-22 against main at v0.4.0 (after `engine-internal-split` and `pluggable-error-renderer`) and scrty v0.2.0, then redesigned the same day to the option-based integration (owner decision, D1 and D10); notes.md records what changed.

Merged srvr facts this change builds on:
- Layout: the shared pipeline lives in `srvr/internal/engine`; consumers see it through `srvr` aliases (`srvr.Routes`, `srvr.Route{Method, Pattern, Configurer}`, `srvr.RouteCheck`, `srvr.ShutdownPhase`). The `security`, `security/ginsecurity` and `security/fibersecurity` modules are nested under `github.com/kartaladev/srvr/`, so they may import `internal/engine`; `internal/paritytest` (TestImportDirection) pins the import order and must allow those edges and forbid `security` -> gin or fiber.
- Pipeline order, outermost first: trace context, access log, observer, recovery, router (net/http `Server.stages`; gin `requestStage` then the gin engine; fiber `requestStage` then `fiberprblm`/srvr recovery in `app.Use`).
- Error answers go through `engine.Core.Render` when a consumer `ErrorRenderer` is set, and through the server's prblm handler (built from `Core.ProblemOptions()`) otherwise. `WithErrorRenderer` and `WithProblemOptions` are mutually exclusive. `srvr.DefaultErrorRenderer()` answers with the request's server's prblm handler. `ErrorHook` receives an `ErrorReport`; srvr's own hook records the failure in `engine.RequestState` and the access stage writes one `http.request.failed`.
- net/http handler errors and panics are answered through `Core.HTTPErrors()` (`Write`, `Wrap`, `Recover`).
- `srvr.WithRouteCheck(check)` runs checks after configure and before bind; an error or panic fails `Start` wrapping `srvr.ErrRouteConfig`. `srvr.Routes` lists declared routes with configurer attribution.
- `srvr.WithShutdownHook(phase, fn)` with `srvr.BeforeDrain` (inside the drain deadline) and `srvr.AfterDrain` (own budget, `ShutdownHookTimeout`, default 5s, shared by all hooks of the phase). Hooks run once per server on every stop path; a never-started server and a `Start` that fails after construction run only `AfterDrain`; errors and panics are joined into the stop call's result.
- `Core.Warn(ctx, code, message)` writes `server.config.warning` (`WARN`, `srvr.warning.code`, `srvr.warning.message`) once per code.
- Engine-only options are `engine.Only(engine, name, value)`; `Core.EngineOptions(engine)` rejects another engine's option with `ErrConfig` naming it. Every engine calls it, so a net/http-only core option is rejected by gin and fiber for free.
- gin applies `MaxRequestBodyBytes` with `http.MaxBytesReader` and answers `413`; fiber maps it to `BodyLimit`.
- `IncludeClientAddress` and the log keys `client.address`, `error.kind`, `srvr.*`; the golden-record test pins every event's keys and `srvr.log.schema`.

scrty facts, checked against v0.2.0 source and godoc:
- Every scrty module is released in lockstep at v0.2.0. `httpsec/prblmsec` (new) requires prblm v0.3.0, which srvr already uses. fibersec and scrty require Fiber v3.7.0.
- `(*Chain).Middleware() func(http.Handler) http.Handler`, `ginsec.Middleware(*Chain, ...ginsec.Option) gin.HandlerFunc` (new `ginsec.WithRefusalsRendered()`) and `fibersec.Middleware(*Chain) fiber.Handler`.
- `httpsec.LookupStatus(err) (int, bool)` reports the status and whether scrty recognises the error; `StatusForError` is defined through it. Both walk at most 1,000 links and never panic or hang.
- `prblmsec.New(opts...)` returns `*Problems` with `Option()` (types plus mapper) and `Mapper()`. Types are relative (`security/<name>`) and need a prblm namespace. Members: `challenge` (fixed vocabulary), `methods`, `max_age`, `rule`, `access_token`, `token_type`. The token is delivered by default; `prblmsec.WithoutChallengeToken()` withholds it. The mapper never sets `WWW-Authenticate` and keeps headers the chain set.
- `httpsec` itself sets `Cache-Control: no-store` on a response refused with a `*ChallengeError` carrying a `Token`.
- `(*Chain).Endpoints() []Endpoint` (`Methods`, `Path`, `Match`, `OtherMethods`, `OperationID`, `PlacedBy`), `FlushRefusalLogs() error`, `CloseSessionSignals(ctx) error` (`ErrSessionSignalsDisabled` when off).
- `httpsec.WithErrorHandler` and `httpsec.WithLogger` are given only at `httpsec.New` time (`httpsec/options.go:1255`, `:911`); twice is a config error (`:1257`, `:914`); `Chain` has no setter (`chain.go:82`). The error handler is `func(w, r, err)` called with the incoming request (`nethttp.go:776`). scrty documents the error handler as governing the net/http chain only; a chain mounted by gin or fiber should not have one (`options.go:1243-1250`). The chain's default logger lives in `scrty/internal`, so srvr can log through it only with `WithLogger`.
- The chain publishes `httpsec.ClientFromContext` and the principal only on the context it hands downstream; the net/http error handler receives the incoming request. `Client.IP` can be `""`.
- Refusals reach each engine differently: net/http through the error handler; gin through `c.Errors` (an unrouted `404` included with `WithRefusalsRendered`, and per prblmsec's godoc also without it under ginprblm's deferred header); fiber as a `*fibersec.RefusalError` returned to the app `ErrorHandler`.
- Basic authentication sets `WWW-Authenticate` only for a request that carried `Authorization: Basic`; Bearer sets none.

## Goals / Non-Goals

**Goals:**
- scrty refusals render identically on every engine, through the same error path as every other srvr error, under the default prblm renderer or a consumer's renderer.
- Core, `ginsrvr` and `fibersrvr` never import scrty; the `security` module never imports gin or fiber.
- A consumer cannot wire a mismatched or half-configured chain: one option builds the chain and its middleware.
- srvr keeps no copy of scrty's mapping: scrty's `prblmsec` owns problem types and members.

**Non-Goals:**
- Guard helpers, expiry-task scheduling, or naming observability by `OperationID` (carry-overs).
- Re-implementing CORS, CSRF, headers or proxy trust in srvr.

## Slices

1. **net/http end to end:**
   - Core `ProblemNamespace`; the `security` module, `security.WithSecurity(chainOpts, opts...)` building the chain with srvr's logger and error handler, `WithChainReady`, `srvr.SecurityLayer` with its engine-only middleware slot, and `srvr.WriteError`.
   - prblmsec registration, pipeline placement, one access record per refusal, refusal classification (`srvr.security.refused`) and the custom renderer path.
   - Specs: error-responses (both requirements, net/http rows), security-bridge (One engine option builds and wires the chain, net/http rows; The built chain is handed to the consumer; The chain runs after recovery and before routing, net/http rows; scrty logs and renders through srvr, net/http rows; Configuration sources), security-error-mapping (Refusals render as scrty's problems, net/http rows; Non-scrty errors keep their own mapping, net/http rows; A custom renderer receives scrty's error unchanged).
2. **Challenges, headers and log safety on net/http:** challenge problems, the opt-in token, preserved headers, refusal records without secrets, panics inside the chain, chain mounted twice, client address and principal capture, body limit.
3. **Collision check and shutdown hooks (net/http).**
4. **gin**: the `security/ginsecurity` module, including the unmatched-path 404 as a normal parity row.
5. **fiber v3**: the `security/fibersecurity` module and cross-engine parity.

Every slice can be released on its own.

## Decisions

### D1. One engine option builds the chain (owner decision 2026-11-22)
The consumer passes scrty's own options to one srvr option per engine; srvr builds the chain:
- net/http: `security.WithSecurity(chainOpts []httpsec.Option, opts ...security.Option) srvr.Option`
- gin: `ginsecurity.WithSecurity(chainOpts, opts...)`; fiber: `fibersecurity.WithSecurity(chainOpts, opts...)`

Two parameters keep scrty's options and srvr's security options type-safe and visibly apart. During server construction the option validates srvr's settings, calls `httpsec.New(append(chainOpts, srvrOpts...)...)`, builds the engine's middleware from that chain (`chain.Middleware()`, `ginsec.Middleware(chain, ginsec.WithRefusalsRendered())`, `fibersec.Middleware(chain)`), calls `WithChainReady`'s callback, and hands core an internal `SecurityLayer` plus the middleware.

`internal/engine` defines `type SecurityLayer interface { ProblemOptions() []prblm.Option; Refused(err error) bool; RouteCheck() RouteCheck; ShutdownHooks() []ShutdownHook; CaptureState(ctx context.Context, s *RequestState) }`, aliased as `srvr.SecurityLayer` for the security modules (godoc: not for application use), using only standard-library, prblm and engine types; `ShutdownHook` is `{Phase ShutdownPhase; Fn func(context.Context) error}`. The middleware travels in an engine-only slot (`engine.Only(<engine>, <option name>, value)`); each engine type-asserts it to its own handler type, so the engines never import scrty. The existing `EngineOptions` check rejects another engine's option with `ErrConfig` naming the right option. One core wiring helper consumes the layer on every engine, so the three engines cannot drift.

**Why not a public bridge:** the core may not name `httpsec` types, so the scrty-aware object must live in a security module; exposing it made the consumer build the chain and the bridge separately, which caused a circular build order on net/http (`ChainOptions()` needed before the chain, the bridge needed the chain), a warning that fired wrongly on gin and fiber, a warning that could not detect a missing handler, and middleware that could come from a different chain. The option-based design removes all four by construction.

**Failure modes:**
- A missing `ProblemNamespace`, security given twice, another engine's option: construction errors naming the field or option.
- `chainOpts` containing `httpsec.WithErrorHandler` or `httpsec.WithLogger`: scrty refuses the duplicate; srvr wraps it in `security.ErrConfig` naming the field, keeping `httpsec.ErrConfig` in the chain.
- A nil entry in `chainOpts`, or any other `httpsec.New` error: construction fails with scrty's error kept.
- `WithChainReady(nil)`: construction error; a panic in the callback: construction error naming `WithChainReady`. The callback is not called when the chain was not built.
- The handed chain mounted again by the consumer: the "Chain mounted twice" scenario.

### D2. Refusals render through the server handling the request
Core gains `srvr.WriteError(w, r, err) bool`: inside a srvr request it answers through `Core.HTTPErrors().Write` of that request's server, so the renderer or prblm, every safety rule and the single failed record apply, and returns `true`; outside one it writes nothing and returns `false`.

On net/http srvr's error handler calls `srvr.WriteError` and, when it returns `false` (the handed chain used outside srvr), answers scrty's default: `httpsec.StatusForError(err)` with an empty body. srvr's logger is a slog handler that forwards to `srvr.LoggerFromContext(ctx)`'s handler and to the server's handler outside a request. On gin and fiber srvr adds the logger only: ginsec leaves errors in `c.Errors` and fibersec returns them, and both reach srvr's render path.

With a consumer `ErrorRenderer` the renderer receives scrty's error unchanged (owner decision 2026-11-22); `srvr.DefaultErrorRenderer()` still includes `prblmsec`, because the layer's prblm options are added to the server's problem handler internally, not through `WithProblemOptions`, so they do not trip the renderer/problem-options exclusion.

### D3. Rendering is delegated to scrty's `prblmsec` (owner decision 2026-11-22)
The security option builds `prblmsec.New(...)` and `SecurityLayer.ProblemOptions()` returns its `Option()`. Core places the layer's options first, ahead of the consumer's `WithProblemOptions` and srvr's own mappers, as prblmsec requires. srvr writes no mapper and no member: problem types, challenge members and the 500 rule are scrty's, and stay in step with scrty releases. The planned `WithChallengeMembers` opt-out is dropped: prblmsec has no such option and its members are what clients need to continue a login.

`SecurityLayer.Refused(err)` is `httpsec.LookupStatus`'s `ok`; srvr's recording hook sets `srvr.security.refused` from it on every engine and under any renderer.

**Failure modes:**
- A consumer mapper registered through `WithProblemOptions` runs after prblmsec, so it cannot steal a scrty refusal; replacing one scrty problem on purpose needs a custom renderer.
- A cyclic or panicking chain: prblm answers the safe 500 before any mapper; `LookupStatus` is bounded.

### D4. Pipeline placement
Outermost first: trace context, access log, observer, recovery, scrty middleware, capture step, router. On net/http the core composes the middleware around its unmatched-route interception and mux, so the chain sees unmatched paths. On gin it is the first `engine.Use` after srvr's recovery and error middleware; on fiber the first `app.Use` after recovery.

The capture step runs inside the chain and calls `SecurityLayer.CaptureState(ctx, state)`, which copies `httpsec.ClientFromContext` and, when opted in, the principal id into the request's `engine.RequestState` (new fields `ClientAddress`, `EndUserID`). A refused request never reaches it: with `IncludeClientAddress` on, its record omits `client.address` and sets `srvr.client.unresolved` `true`, as does a resolved `Client.IP` of `""`.

### D5. Collision matching: full collisions fail, partial shadowing warns
Unchanged from planning: `SecurityLayer.RouteCheck()` compares `chain.Endpoints()` with `srvr.Routes` (gin and fiber `:name` become `{name}`, wildcards `{name...}`); a full collision fails `Start` with `ErrRouteConfig` naming route, configurer (`(unknown)` without attribution), `OperationID` and `PlacedBy`, all joined; partial shadowing writes one `security_endpoint_shadowed` warning. `CollisionWarn` writes one `security_endpoint_collision` warning; `CollisionOff` registers no check. An empty `OtherMethods` is treated as refused. Path-scoped fiber `Use` handlers are not in `srvr.Routes`, an accepted gap (notes.md).

### D6. Shutdown through the core hook mechanism
`SecurityLayer.ShutdownHooks()` returns a `BeforeDrain` hook (`CloseSessionSignals`) and an `AfterDrain` hook (`FlushRefusalLogs`, then `CloseSessionSignals` if not yet run). `ErrSessionSignalsDisabled` is success; each scrty call runs at most once per server (`sync.Once`); core joins errors and recovers panics.

### D7. The challenge token stays opt-in (owner decision 2026-11-22)
prblmsec delivers the token by default; srvr's safe default is to withhold it, so the security option passes `prblmsec.WithoutChallengeToken()` unless `WithChallengeToken()` / `Config.ChallengeToken`. `Cache-Control: no-store` comes from scrty for any challenge carrying a token. The token is never logged: the triage record formats only the error chain, and `ChallengeError.Error()` does not include it (log-scan rows). srvr's own hook receives an `ErrorReport`, never the problem, so prblm's warning about hooks logging extensions does not apply to srvr's records.

### D8. Body limits
Unchanged: tests only, proving the engine limit applies before the chain reads a login body.

### D9. srvr owns the problem namespace (owner decision 2026-11-22)
srvr coordinates its sub-libraries, so the namespace is a srvr setting: `Config.ProblemNamespace` / `WithProblemNamespace`, validated with prblm's rules at construction and applied as `prblm.WithNamespace` before the consumer's prblm options (a consumer's later `prblm.WithNamespace` still wins, as prblm defines). The default stays empty so no existing answer changes. `WithSecurity` on a server without `ProblemNamespace` fails construction naming `ProblemNamespace`: scrty's relative types would otherwise be sent unresolved and reported to hooks as `prblm.ErrInvalidType`.

### D10. Module layout (owner decision 2026-11-22)
```
srvr/                        github.com/kartaladev/srvr            stdlib + prblm
  internal/engine/           SecurityLayer, wiring helper, middleware slot, RequestState fields
  ginsrvr/  fibersrvr/       no scrty
  security/                  .../security                          httpsec, prblmsec; no gin/fiber
    security.go              WithSecurity (net/http)
    options.go config.go     WithChallengeToken, WithPrincipalInLogs, WithCollisionCheck, WithChainReady, WithConfig, Config, ErrConfig
    layer.go handler.go problems.go collisions.go shutdown.go capture.go
    internal/build/          the shared build step the engine modules call (Go's internal rule admits the nested modules)
    ginsecurity/             .../security/ginsecurity              + ginsec, gin
    fibersecurity/           .../security/fibersecurity            + fibersec, fiber v3
```
The shared build step stays in `security/internal/...` so the layer is not public API. TestImportDirection gains the five edges and a guard that `security` reaches neither gin nor fiber.

### D11. Shapes settled for later (2026-11-22)
- Two parameters (`chainOpts []httpsec.Option`, then `...security.Option`), not one variadic `any`.
- `WithChainReady` callback, not a constructor returning the chain, so security stays a plain option in the constructor's list.
- Option names: `security.WithSecurity`, `ginsecurity.WithSecurity`, `fibersecurity.WithSecurity`. The gin and fiber guard helpers stay scrty's (`ginsec.NewGuards`, `httpsec.NewGuards`).

## Defaults

| Behaviour | Default | Why | Override |
|-----------|---------|-----|----------|
| Refusal rendering | prblmsec problem, scrty's status, no detail | one error format, no leakage, no copy of scrty's mapping | a consumer `ErrorRenderer` |
| Problem namespace | empty; required with security | no change for existing servers; scrty's types need one | `WithProblemNamespace`, `Config.ProblemNamespace` |
| Challenge token | off | a credential in a body must be deliberate | `WithChallengeToken()`, `Config.ChallengeToken` |
| Collision check | `CollisionError` for full collisions, warning for partial shadowing | a fully shadowed route is a silent bug; catch-alls are normal | `WithCollisionCheck`, `Config.CollisionCheck` |
| Chain logging and net/http error handler | srvr's, always | one log stream and one error path; scrty refuses a second handler | none: build and mount the chain without srvr's option |
| Principal in logs | off | privacy | `WithPrincipalInLogs()`, `Config.LogPrincipal` |
| Client address | scrty's resolved client, only with `IncludeClientAddress`; `srvr.client.unresolved` on refused or unresolved requests | one owner of proxy trust | scrty's `EnableTrustedProxies` |
| Shutdown hooks | `CloseSessionSignals` before drain, `FlushRefusalLogs` after, on every stop path | release the chain's goroutines and sampled logs | none |

Every security row is also settable through `security.WithConfig(security.Config)` (fields `CollisionCheck`, `ChallengeToken`, `LogPrincipal`, with `json`/`yaml`/`env` tags); `WithChainReady` is a function and has no field. Options apply in argument order, the later winning; `CollisionCheck` is a case-insensitive `error`, `warn` or `off`, empty meaning `error`. An invalid value fails server construction with an error wrapping `security.ErrConfig` naming the field and value.

## Hot Paths

- Per refused request: the chain refuses; `WriteError` (net/http), `c.Errors` (gin) or `RefusalError` (fiber); the server's render path with prblmsec; srvr's hook recording the failure and the refusal flag; the access stage.
- Per allowed request: the capture step; the prblmsec no-match path for handler errors.
- Construction: namespace validation, chain build and its error wrapping, the chain-ready callback.
- Startup: the collision matcher.
- Shutdown: the two hooks with errors, panics and deadlines on each stop path.

## Risks / Trade-offs

- [scrty is pre-1.0 and its problem types are its own contract] -> srvr pins scrty v0.2.0 and the golden and parity tests pin the bodies; each scrty upgrade re-runs them.
- [Consumers lose "build the chain yourself, then hand it to srvr"] -> `WithChainReady` gives the chain back; a consumer who must own the handler wires scrty without srvr's option.
- [Three modules to release instead of one] -> released together per slice; the engine modules are thin.
- [Refused requests have no client address] -> `srvr.client.unresolved`; carry-over to scrty.
- [No `WWW-Authenticate` on a Bearer 401] -> srvr adds none; carry-over to scrty.
- [A consumer mapper cannot override one scrty problem through `WithProblemOptions`] -> documented; a custom renderer can.
- [The handed chain mounted on a second server] -> its logger forwards by request context and its error handler renders through whichever srvr server handles the request; documented, not encouraged.

## Migration Plan

New opt-in modules `security`, `security/ginsecurity` and `security/fibersecurity`, each released as `v0.1.0` after its slice; core, `ginsrvr` and `fibersrvr` get minor releases for `ProblemNamespace`, `SecurityLayer`, the engine-only middleware slot and `WriteError`.

## Open Questions

- None. Owner decisions of 2026-11-17 and 2026-11-22 are applied.
