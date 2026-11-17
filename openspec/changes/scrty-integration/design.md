# Design

## Context

See proposal.md - Why. This change assumes the following from earlier changes. The consolidation group checks each assumption against the merged code.
- From `core-server`:
  - The core module has one prblm handler per server. srvr appends its own `OnError` last, so it always wins; consumer hooks run through `WithErrorHook`.
  - The pipeline order is trace context, access log, recovery, router.
  - `IncludeClientAddress` exists.
  - The log keys `client.address`, `error.kind`, `srvr.*` are defined.
  - The lifecycle states and the drain bounded by `ShutdownTimeout` exist.
  - `srvr.WithShutdownHook(phase, fn)` with phases `srvr.BeforeDrain` (inside the drain deadline) and `srvr.AfterDrain` (own budget). Hooks run once per server on every stop path: `Shutdown`, cancellation of `Start`'s context, and `Shutdown` of a never-started server, where only `AfterDrain` hooks run. Hook errors and panics are joined into the stop call's result.
  - Unmatched 404/405 requests log `http.request.failed` on every engine.
  - The log schema policy: added keys keep `srvr.log.schema`; a golden-record test pins every event's keys.
- From `gin-and-fiber-engines`:
  - `ginsrvr` and `fibersrvr` follow the same pipeline order.
  - `ginsrvr` installs `ginprblm.Middleware` and `ginprblm.Recovery`. `fibersrvr` sets `fiberprblm.ErrorHandler` as the app `ErrorHandler`.
  - The public `srvr/engine` package holds the shared pipeline pieces: `engine.Routes`, one `engine.Route{Method, Pattern, Configurer}` per declared route (no `Use` entries, no fiber implicit `HEAD`), inspected through `srvr.WithRouteCheck(check)` checks that run after configure, where an error or panic from a check fails `Start` with `srvr.ErrRouteConfig`; the per-request `engine.RequestState` (`engine.StateFrom(ctx)`), holding route, matched, status, failure and trace context; and the `server.config.warning` event (`WARN`, `srvr.warning.code`, `srvr.warning.message`), written once per server.
  - gin applies `MaxRequestBodyBytes` with `http.MaxBytesReader` and answers `413` with a problem; fiber maps it to `BodyLimit`.
- From `otel-observability`: the `Observer` hook sits between access log and recovery.

scrty facts, checked against v0.1.1 source:
- `(*Chain).Middleware() func(http.Handler) http.Handler`, `ginsec.Middleware(*Chain) gin.HandlerFunc` and `fibersec.Middleware(*Chain) fiber.Handler`. The gin and fiber versions panic on a nil chain. Mounting the same chain twice runs it once.
- `httpsec.StatusForError(error) int` (`httpsec/errors.go:502`) maps only scrty's own errors to statuses other than 500: an issuing failure (the unexported `*issueFailure`, `httpsec/complete.go:100`), `ErrChallengeUnavailable`, `oidcprovider.ErrDependency` and `recovery.ErrDependency` are 500; challenges are 401 or 403; `statusTable` covers the sentinels; anything else is 500. `oidcprovider` and `recovery` are packages of the root scrty module, so they add no module. `*ChallengeError{Kind policy.ChallengeKind; Session *session.Session; Token string; Methods []MFAMethod}`, with `MFAMethod{Name; Channel factor.Channel; Begins}`. `factor.Channel` is `type Channel string` with no `String()` method (`authenticate/factor/factor.go:54`). A login challenge carries the pending session's token only in `ChallengeError.Token` (`httpsec/complete.go:700`, `httpsec/login.go:435`); see notes.md. The challenge kinds are `ChallengeMFA`, `ChallengePasswordChange`, `ChallengeMFAEnrolment` and `ChallengeAccountRecovery`.
- `(*Chain).Endpoints() []Endpoint`. Each `Endpoint` has `Methods`, `Path` (base path applied), `Match` (`MatchExact`, `MatchSegment`, `MatchSubtree`), `OtherMethods` (`OtherMethodsPassThrough`, `OtherMethodsRefused`), `OperationID` and `PlacedBy`.
- `(*Chain).FlushRefusalLogs() error` and `(*Chain).CloseSessionSignals(ctx) error`. The latter returns `ErrSessionSignalsDisabled` when signals are off.
- `httpsec.WithErrorHandler` and `httpsec.WithLogger` can be given only at `httpsec.New` time; giving one twice is a config error. scrty exposes no accessor to tell whether a chain has an error handler.
- Basic authentication sets `WWW-Authenticate` on the writer before refusing, but only for a request that carried an `Authorization: Basic` header (`httpsec/basic.go:114-125`); a request with no credentials is refused later without it. Bearer authentication sets none.
- Refusals reach each engine differently:
  - net/http: through the error handler.
  - gin: through `c.Errors`, with the status set but not committed, except a `404` refusal on a path gin matched no route for, which ginsec commits with an empty body (`httpsec/ginsec/middleware.go:154-166`).
  - fiber: as a `*fibersec.RefusalError` returned to the app `ErrorHandler`. It unwraps to the refusal and a `*fiber.Error`.
- Client address comes only from the chain's trusted-proxy configuration, never from gin's or fiber's proxy settings.
- The chain publishes `httpsec.ClientFromContext` and the principal only on the context it hands downstream. Layers outside the chain, and the net/http error handler (which receives the incoming request), do not see them (proof in notes.md). `Client.IP` can be `""`.
- `CloseSessionSignals` ends long-lived session-signal streams; while they stay open, the HTTP drain cannot finish.

## Goals / Non-Goals

**Goals:**
- scrty refusals render identically on every engine, through the same prblm handler as every other srvr error.
- Core, `ginsrvr` and `fibersrvr` never import scrty.

**Non-Goals:**
- Guard helpers, expiry-task scheduling, or naming observability by `OperationID`. These are recorded as carry-overs.
- Re-implementing CORS, CSRF, headers or proxy trust in srvr.

## Slices

1. **net/http bridge end to end:**
   - The `security` module, `security.New`, the mapper (status and title only, matched by the D3 rule), `bridge.ChainOptions()` (logger and error handler), `engine.SecurityLayer`, `srvr.WithSecurity` and `srvr.WriteError`.
   - Pipeline placement, one access record per refusal, and the start-time check for unused chain options.
   - Specs satisfied: security-bridge (A bridge is built from a chain; Each engine takes its own scrty middleware, net/http rows; The chain runs after recovery and before routing, net/http rows; scrty logs through srvr's logger by default; Unused chain options are reported at start), security-error-mapping (Refusals use scrty's status, net/http rows; Non-scrty errors keep their own mapping).
   - Hot paths: the mapper's match and no-match paths, `WriteError` lookup from context, the bare-status fallback, and the chain's position in the pipeline.
2. **Challenges, headers and log safety on net/http:**
   - Challenge members and their opt-out, the opt-in challenge token, preserved headers, refusal records without secrets, panics inside the chain, principal opt-in, client address from scrty with refused requests unresolved.
   - Specs satisfied: security-error-mapping (Challenges expose only what a client needs; The challenge token is returned only by opt-in; Headers scrty sets are preserved; Refusals are logged without secrets), security-bridge (Panics inside the chain are recovered; Principal id is logged only by opt-in; scrty owns client address and browser protections, net/http rows; Chain mounted twice; Body limits apply before the chain, net/http row).
   - Hot paths: challenge rendering, token member and `no-store`, header merging, the redaction allow-list, principal and client capture into `engine.RequestState`.
3. **Collision check and shutdown hooks (net/http):**
   - Endpoint-versus-route matching over `engine.Routes`, collisions versus shadowing, collision modes, and the two core shutdown hooks.
   - Specs satisfied: security-endpoint-collisions (all except the gin and fiber rows), security-bridge (Shutdown closes the chain's background work).
   - Hot paths: the matcher for exact, segment and subtree endpoints, both method policies and the full-versus-partial classification; the hooks when they return errors, panic or hit deadlines, on each stop path.
4. **gin:**
   - `ginsrvr.WithSecurity` places `ginsec.Middleware` after `ginprblm.Recovery`/`Middleware` and before routes; core `srvr.WithSecurity` given to `ginsrvr.New` fails construction.
   - The mapper is added to gin's prblm options, so `c.Errors` from ginsec are rendered.
   - The collision check uses gin routes from `engine.Routes`; the gin body limit from `gin-and-fiber-engines` applies before the chain.
   - Specs satisfied: the gin rows of every scenario, including the unmatched-path 404 exception.
   - Hot paths: rendering from `c.Errors` and the gin template normaliser.
5. **fiber v3:**
   - `fibersrvr.WithSecurity` places `fibersec.Middleware` after `fiberprblm.Recovery` and before routes; core `srvr.WithSecurity` given to `fibersrvr.New` fails construction.
   - The mapper matches through `RefusalError`.
   - `BodyLimit` is checked against `MaxRequestBodyBytes`.
   - The collision check uses fiber routes from `engine.Routes`.
   - Specs satisfied: the fiber rows, security-error-mapping (Every engine renders a refusal identically), security-bridge (Body limits apply before the chain, fiber rows).
   - Hot paths: rendering through the fiber `ErrorHandler`, the body limit, and the fiber template normaliser.

Every slice can be released on its own. After slice 1, a net/http consumer has working, consistently rendered security.

## Decisions

### D1. An engine-neutral `engine.SecurityLayer` interface, with typed `WithSecurity` per engine
The public `srvr/engine` package (which holds the shared pipeline pieces) defines `type SecurityLayer interface { ProblemOptions() []prblm.Option; Endpoints() []EndpointInfo; ShutdownHooks() []ShutdownHook; ChainOptionsUsed() bool; ... }` using only standard-library and prblm types. `security.Bridge` implements it. Each engine has `WithSecurity(layer engine.SecurityLayer, mw <engine middleware type>)`. Passing the wrong middleware type is then a compile error, and no engine imports scrty.

`WithSecurity` also registers the bridge's collision check with `srvr.WithRouteCheck` (D5) and its shutdown hooks with `srvr.WithShutdownHook` (D6).

`ginsrvr.New` and `fibersrvr.New` accept core options, so `srvr.WithSecurity(bridge, chain.Middleware())` type-checks there. The core option therefore records itself as a net/http-only option, and the gin and fiber constructors reject it with an error wrapping `srvr.ErrConfig` that names the engine's own `WithSecurity` (spec rows).

Alternatives considered:
- One `security.With(chain)` option per engine inside the `security` module. Rejected: it would make `security` import gin and fiber.
- Per-engine bridge modules (`ginsrvr/security`). Rejected in exploration (S2) as three modules to release.

**Failure modes:**
- Nil layer or nil middleware: a construction error.
- `WithSecurity` given twice: a construction error, because two chains in one pipeline is ambiguous.
- Core `srvr.WithSecurity` on gin or fiber: a construction error.
- The same chain also used on a group: scrty itself runs it once (spec row).

### D2. Refusals render through the server handling the request
The core gains `srvr.WriteError(w, r, err)`. It finds the server's problem writer, stored in the request context by the outermost layer, and renders through it. Rendering therefore runs the same mappers and the `trace_id` enricher. srvr's `OnError` only collects the error and the refusal flag into `engine.RequestState`; the access stage writes the single `http.request.failed` record with the triage attributes.

`bridge.ChainOptions()` returns `httpsec.WithErrorHandler(srvr.WriteError)` and `httpsec.WithLogger(...)`. The logger is a lazy slog handler that forwards to the logger of the server handling the context, and to srvr's default JSON handler otherwise. This avoids a construction-order cycle: the chain is built before the server, but the server's middleware needs the chain.

Gin and fiber need no error handler. ginsec leaves errors in `c.Errors`, which ginprblm renders. fibersec returns errors that fiberprblm renders. The bridge's prblm mapper serves all three engines.

Alternatives considered:
- `security.New(srv, chain)`, which needs the server first. Rejected: it breaks the "chain built first" order that scrty's own `WithErrorHandler` requires.
- Having srvr wrap the chain's response writer to rewrite bare statuses. Rejected: it guesses at intent and would also rewrite responses the chain answers on purpose.

**Failure modes:**
- The consumer forgets `ChainOptions()`. On net/http, refusals then come back as bare statuses. The bridge records whether `ChainOptions()` was called, and `Start` writes one `server.config.warning` record with `srvr.warning.code` `security_chain_options_unused` when it was not. The check is deterministic: it does not inspect responses, so the bare `405` the chain's OpenID Provider endpoints write on purpose (`httpsec/oidc_authorize.go:146`) never triggers it. A consumer who supplies their own `httpsec.WithErrorHandler` and `httpsec.WithLogger` declares it with `WithOwnChainHandlers()` to silence the warning. A carry-over still asks scrty for an accessor, which would make the check exact.
- `WriteError` is called outside srvr: it falls back to `StatusForError`, a bare status and an empty body, which is scrty's default.
- The consumer gives their own `WithErrorHandler` along with `ChainOptions()`: scrty refuses the chain (`ErrConfig`).

### D3. The mapper is a prblm `Mapper` matched by one rule, and it builds the challenge members
The mapper matches an error when `httpsec.StatusForError(err) != 500`, or `errors.As` finds a `*httpsec.ChallengeError`, or `errors.Is` finds `httpsec.ErrChallengeUnavailable`, `oidcprovider.ErrDependency` or `recovery.ErrDependency`. The status always comes from `StatusForError`. This gives the same responses as a hand-kept sentinel list without a list that drifts: a sentinel scrty adds to its `statusTable` is matched automatically. The issuing failure is unexported, so srvr cannot classify it either way; it falls through to srvr's 500 mapping with the same response.

The mapper sets the challenge members on the problem it returns: `*httpsec.ChallengeError` is scrty's type, so it is not a prblm `Describer` and srvr cannot make it one. Challenge members are lowercase snake-case names of the kind. An unknown kind becomes `unknown`. `mfa_methods` is built only from `Name`, `string(Channel)` and `Begins`. `Session` is never read; `Token` is read only under `WithChallengeToken()` (D7).

Alternatives considered:
- A hand list of about 15 scrty sentinels plus a drift test that parses scrty's source. Rejected (planning audit D18): more code for the same responses.

**Failure modes:**
- A scrty upgrade adds a 500-class sentinel outside `StatusForError`'s non-500 range: not matched, answered 500 by srvr with the same body; only `srvr.security.refused` differs. The consolidation group re-reads `StatusForError` on each scrty upgrade.
- A fiber `RefusalError` unwraps to two errors: `errors.As` walks both, which the spec row tests.
- An empty `Channel`: rendered as `""`, never guessed.

### D4. Pipeline placement
The order, outermost first, is: trace context, access log, observer, recovery, scrty middleware, router.

On net/http the core composes the scrty middleware around its unmatched-route interception and mux, so the chain sees unmatched paths. On gin it is the first `engine.Use` after the prblm middleware and recovery. On fiber it is the first `app.Use` after recovery.

The chain decides 401 versus 404 for unknown paths, and srvr never reorders that decision (spec rows).

Because layers outside the chain cannot read what the chain resolved, srvr wraps the router, inside the chain, with a capture step. It copies `httpsec.ClientFromContext` and, when opted in, the principal into the shared per-request `engine.RequestState` (`engine.StateFrom(ctx)`) that the outermost layer created; this change adds no holder of its own. It adds two fields to `engine.RequestState`, `ClientAddress` and `EndUserID`, the first time it writes them, and the access-log stage reads them from there. A request the chain refuses or answers never reaches the capture step. With core `IncludeClientAddress` on, its record then omits `client.address` and sets `srvr.client.unresolved` `true`, as does a resolved `Client.IP` of `""`; with it off, neither key is written: the transport peer is usually the proxy, and logging it as the client would mislead triage (planning audit D4). A carry-over asks scrty to publish the resolved client to its error handler.

**Failure modes:**
- A panic inside the chain: recovered by the layer outside it (spec rows for each engine).
- gin's ordering trap: ginprblm must be registered before any middleware that aborts. Placing it before scrty satisfies this.

### D5. Collision matching: full collisions fail, partial shadowing warns
The check is a route check the bridge supplies and `WithSecurity` registers with `srvr.WithRouteCheck`, so it runs after configure and before bind on every engine. It compares endpoints with the routes in `engine.Routes`, normalised per engine:
- gin `:name` and fiber `:name` become `{name}`.
- A wildcard parameter becomes `{name...}`.

An endpoint answers a request when the method overlaps (or the endpoint's other methods are refused, scrty's own rule, `httpsec/collisions.go:118-125`) and the path matches its kind:
- exact: the paths are equal;
- segment: the endpoint's prefix plus exactly one segment;
- subtree: at or below the part of the endpoint's path before its first `{…}` segment.

Each route is then classified:
- **Collision:** every request the route matches is answered by an endpoint first (a concrete route, or a parameter route whose whole range an endpoint covers). The check returns every collision joined, and `Start` fails with `srvr.ErrRouteConfig`.
- **Shadowing:** only some of the route's matches are answered by an endpoint (a wildcard, a catch-all such as an SPA fallback at `GET /{path...}`, or a mounted handler). The check returns no error; `Start` writes one `server.config.warning` with `srvr.warning.code` `security_endpoint_shadowed` whose message lists every shadowed route-endpoint pair, and serves. Failing here would break every SPA or static-file setup once any endpoint is enabled.

`CollisionWarn` turns collisions into one `security_endpoint_collision` warning listing them; `CollisionOff` registers no check. Each warning code is written once per server, as the event defines.

**Failure modes:**
- A route wildcard overlapping an exact endpoint (`POST /sign-in/:method`): shadowing, a warning (spec row).
- An endpoint with an empty `OtherMethods` (the zero policy): treated as refused, the safe interpretation.
- A route with no configurer attribution (registered outside a configurer): named `(unknown)` in the message.

### D6. Shutdown through the core hook mechanism
The bridge exposes its hooks through `SecurityLayer.ShutdownHooks()`, and `WithSecurity` registers them with core's `srvr.WithShutdownHook`; this change adds no shutdown mechanism of its own (planning audit D5):
- `srvr.BeforeDrain`: `CloseSessionSignals(ctx)`, inside the drain deadline, so long-lived signal streams end and the drain can finish.
- `srvr.AfterDrain`: `FlushRefusalLogs()`, after the last request, so refusals during drain are flushed; then `CloseSessionSignals(ctx)` if the `BeforeDrain` hook did not run, which is the never-started path.

`ErrSessionSignalsDisabled` is treated as success. Each scrty call runs at most once per server (`sync.Once`). Core runs the hooks on every stop path and joins their errors into the stop call's result.

**Failure modes:**
- A hook that blocks: bounded by its phase's context; `context.DeadlineExceeded` is returned.
- A hook that panics: recovered by core, logged and joined as an error.

### D7. The challenge token is an opt-in member
A password login that needs a second factor carries the pending session's token only in `ChallengeError.Token`; scrty expects the error handler to deliver it. Cookie-based clients continue without it, but a bearer-token client cannot call `/mfa/verify/{method}`. With `WithChallengeToken()` (`Config.ChallengeToken`), the mapper adds `access_token` for a challenge with a non-empty `Token` and the response gets `Cache-Control: no-store`. The member is independent of `WithChallengeMembers(false)`. The token is never put in any log attribute: the triage record formats only the error chain's types and messages, and `ChallengeError.Error()` does not include it (verified by the log-scan rows).

Alternatives considered:
- On by default. Rejected: a credential in a response body should be a deliberate choice.
- A scrty-side responder. Rejected: scrty already delegates delivery to the error handler.

### D8. Body limits
`gin-and-fiber-engines` applies `MaxRequestBodyBytes` on gin (`http.MaxBytesReader`, `413` problem) and maps it to `fiber.Config.BodyLimit` on fiber. This change only adds tests that the limit applies before the chain reads a login body, because the chain's own login body limit applies after fasthttp has read the body.

## Defaults

| Behaviour | Default | Why | Override |
|-----------|---------|-----|----------|
| Refusal rendering | prblm problem with scrty's status, no detail | one error format, no leakage | consumer `httpsec.WithErrorHandler` in place of `ChainOptions()` |
| Challenge members | on | clients need them to continue a login | `WithChallengeMembers(false)`, `Config.ChallengeMembers` |
| Challenge token | off | a credential in a body must be deliberate | `WithChallengeToken()`, `Config.ChallengeToken` |
| Collision check | `CollisionError` for full collisions, warning for partial shadowing | a fully shadowed route is a silent bug; catch-alls are normal | `WithCollisionCheck(CollisionWarn or CollisionOff)`, `Config.CollisionCheck` |
| Unused chain options | one `server.config.warning` at `Start` | forgotten `ChainOptions()` gives bare refusals | `WithOwnChainHandlers()`, `Config.OwnChainHandlers` |
| Chain logging | srvr's logger, with `trace_id` | one log stream for triage | consumer `httpsec.WithLogger` |
| Principal in logs | off | privacy | `WithPrincipalInLogs()`, `Config.LogPrincipal` |
| Client address | scrty's resolved client, only with `IncludeClientAddress`; omitted with `srvr.client.unresolved` on refused or unresolved requests | one owner of proxy trust; the peer is usually the proxy | scrty's `EnableTrustedProxies` |
| Shutdown hooks | `CloseSessionSignals` before drain, `FlushRefusalLogs` after drain, on every stop path including a `Start` that fails after construction (only `AfterDrain`, which also closes signals) | release the chain's goroutines and sampled logs | none; scrty requires them |

Every row is also settable through `security.WithConfig(security.Config)`. Options apply in argument order and the later one wins; `WithConfig` sets every field, a zero field meaning the default (`ChallengeMembers` is a `*bool`, nil meaning on; `CollisionCheck` is a case-insensitive `error`, `warn` or `off`, empty meaning `error`). An invalid value fails `security.New` with an error wrapping `security.ErrConfig` naming the field and value. `IncludeClientAddress` is a core access-log setting, not a bridge field.

## Hot Paths

- Per refused request: the chain refuses; then, depending on the engine, `WriteError` (net/http), `c.Errors` with ginprblm (gin), or `RefusalError` with fiberprblm (fiber). Then the mapper's match path, challenge member building, header merging, `OnError` collecting the error and the refusal flag into `engine.RequestState`, and the access stage writing the single failed record.
- Per allowed request: the chain passes the request on, the mapper's no-match path runs for handler errors, and the principal lookup runs when opted in.
- Startup: the collision matcher across all match kinds, both method policies and the collision/shadowing split; the chain-options check.
- Shutdown: the two hooks with errors, panics and deadlines on each stop path.

## Risks / Trade-offs

- [The chain has no way to report whether it has an error handler] -> Warn at `Start` when `ChainOptions()` was never called, unless `WithOwnChainHandlers()`. Carry-over to scrty, `unassigned`.
- [scrty is pre-1.0 and adds sentinels] -> The D3 rule follows `StatusForError`, so new sentinels in its table are matched. The consolidation group re-reads `StatusForError` on each upgrade.
- [Refused requests have no client address in logs] -> `srvr.client.unresolved` marks them so triage knows the address is missing, not empty. Carry-over to scrty, `unassigned`.
- [scrty sends no `WWW-Authenticate` on a Bearer 401 or a Basic 401 without credentials (RFC 9110 requires one)] -> srvr keeps scrty's headers unchanged and adds no fallback, because scrty owns them. Carry-over to scrty, `unassigned`.
- [ginsec commits an empty `404` for a refusal on a path gin matched no route for] -> Accepted parity exception with a spec row. Carry-over to scrty for an option, `unassigned`.
- [The `access_token` member puts a credential in a response body] -> Opt-in, `no-store`, never logged.
- [The chain is shared mutable state across engines in tests] -> One chain per test server. Parity tests build three equivalent chains.
- [The lazy logger forwards to a different server when one chain is shared by two servers] -> It forwards by request context, so each request logs to its own server. Records written outside a request go to the first server that registered the layer, which is documented.
- [ginsec's fiber pin (3.6.1) is lower than fiberprblm's (3.7.0)] -> MVS selects 3.7.0. The fiber parity slice runs `go test` with `GOWORK=off`.

## Migration Plan

This is a new opt-in module, so there is nothing to migrate. It is released as `security/v0.1.0` after slice 5. The core and engine modules get minor releases for `SecurityLayer`, `WithSecurity` and `WriteError`.

## Open Questions

- None. Member names were settled by the owner as `challenge` and `mfa_methods`, on by default, and `access_token`, opt-in (RFC 9457 section 3.2: letters, digits and `_`, at least three characters). Planning audit decisions D3, D4, D5, D14-D18 (2026-11-17) are applied above.
- New log keys (`enduser.id`, `srvr.security.refused`, `srvr.client.unresolved`, `srvr.warning.code` values `security_*`) are additive, so `srvr.log.schema` keeps its version; the golden-record test gains them.
