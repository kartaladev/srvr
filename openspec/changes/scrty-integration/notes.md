# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.


### Security layer must plug into srvr/engine
- **Status:** adopted by scrty-integration (planning audit 2026-11-17: design D1/D4, `engine.SecurityLayer`, `engine.Routes` through `srvr.WithRouteCheck`, `engine.RequestState` with added `ClientAddress`/`EndUserID`)
- **Target:** scrty-integration
- **Context:** SecurityLayer/WithSecurity/WriteError were planned as core API without knowing gin-and-fiber-engines' public srvr/engine package.
- **Evidence:** unverified: planning-time inconsistency found when the five changes were drafted in parallel
- **Suggested action:** Re-express SecurityLayer positioning through srvr/engine as merged; avoid duplicating pipeline-slot APIs.

### Shutdown hooks shared with otel-observability
- **Status:** closed: merged into otel-observability's entry; core mechanism decided (D5): `srvr.WithShutdownHook` with `BeforeDrain`/`AfterDrain`, used by design D6
- **Target:** scrty-integration
- **Context:** otel-observability adds core WithShutdownHook; this change needs scrty shutdown calls within the drain deadline.
- **Evidence:** unverified: planning-time inconsistency found when the five changes were drafted in parallel
- **Suggested action:** Use the single ordered shutdown-hook mechanism once merged.

### Ask scrty for an error-handler accessor
- **Status:** open (planning audit D16: the runtime heuristic is replaced by a start-time `security_chain_options_unused` warning; the accessor would make that check exact)
- **Target:** unassigned
- **Context:** scrty cannot report whether a chain has an error handler. Since D16 srvr warns deterministically at `Start` (`security_chain_options_unused`) when `bridge.ChainOptions()` was never called, unless `WithOwnChainHandlers()` is set. That detects a forgotten call, not a missing handler: a consumer who passes their own options but forgets `httpsec.WithErrorHandler` is not caught. An accessor would let srvr check for the handler itself.
- **Evidence:** unverified: planning-time inconsistency found when the five changes were drafted in parallel
- **Suggested action:** Open an issue on kartaladev/scrty proposing an accessor; when released, base the start-time warning on it and retire `WithOwnChainHandlers()`.

### Login challenge token never reaches the client
- **Status:** adopted by scrty-integration (planning audit D3: opt-in `WithChallengeToken()`, `access_token` member, `Cache-Control: no-store`, never logged; design D7, security-error-mapping "The challenge token is returned only by opt-in", task 3.2)
- **Target:** scrty-integration
- **Context:** A password login that needs a second factor returns `*httpsec.ChallengeError` and writes nothing itself (`httpsec/login.go:435-437`). The pending session's token exists only in `ChallengeError.Token` (`httpsec/complete.go:700`); scrty expects the consumer's error handler to deliver it (scrty `openspec/specs/http-error-propagation/spec.md:65`). The bridge never reads `Token`, so on every engine a bearer-token client gets `challenge`/`mfa_methods` but no credential for `/mfa/verify/{method}`, and the MFA login cannot continue.
- **Evidence:** unverified as a running test (needs a full MFA login fixture); established from the cited scrty source lines.
- **Suggested action:** Decided (D3): opt-in `WithChallengeToken()` adds `access_token` only for a non-empty `Token`, with `Cache-Control: no-store`, never logged; implemented by task 3.2.

### 401 without WWW-Authenticate
- **Status:** open (planning audit D15: srvr adds no fallback; raise an issue on kartaladev/scrty)
- **Target:** unassigned
- **Context:** RFC 9110 section 11.6.1 / 15.5.2 (formerly RFC 7235 section 3.1) requires a `WWW-Authenticate` challenge on every 401; RFC 6750 section 3 describes the `Bearer` challenge. scrty sets one only when a failing `Authorization: Basic` header was sent (`httpsec/basic.go:114-125`) or DPoP is on; Bearer sets none (`httpsec/bearer.go:80`).
- **Evidence:** probe `TestBasicChallengeWithoutCredentials` (`go test -v` in a scratch module with a `replace` to scrty v0.1.1 source): `Authorization="" -> status 401 WWW-Authenticate=""`; `Authorization="Basic Ym9iOndyb25n" -> status 401 WWW-Authenticate="Basic realm=\"orders\""`. The spec row "Authentication headers kept" was corrected to match.
- **Suggested action:** Decided (D15): no bridge fallback; open an issue on kartaladev/scrty asking for a `WWW-Authenticate` challenge on every 401, citing the probe.

### Client address and principal are invisible outside the chain
- **Status:** adopted by scrty-integration for srvr's side (planning audit D4: refused or unresolved requests omit `client.address` and set `srvr.client.unresolved`; design D4, Client address rows, task 3.7); the scrty ask is tracked in "Ask scrty to publish the resolved client to its error handler"
- **Target:** scrty-integration
- **Context:** The access-log layer sits outside the chain, but scrty publishes `ClientFromContext` and the principal only on the context it passes downstream; the net/http error handler receives the incoming request (`httpsec/nethttp.go:744`). design.md now adds an inner capture step for allowed requests. For refused requests (where triage most needs the real client) no layer can read scrty's resolved client, so `client.address` falls back to the transport peer, which is the proxy. The scrty `Client.IP` can also be `""` (`httpsec/client.go:22-25`).
- **Evidence:** probe `TestOuterSeesClient`: `status=200 client in outer ctx=false, in handler ctx=true`; `status=401 client in outer ctx=false, in handler ctx=false (handler ran=false), in error-handler ctx=false (ran=true)`.
- **Suggested action:** Decided (D4): omit `client.address` and set `srvr.client.unresolved` on refused or unresolved requests (only with `IncludeClientAddress` on); rows added; the scrty ask is a separate open entry.

### Unrendered-refusal warning has false positives
- **Status:** adopted by scrty-integration (planning audit D16: deterministic `security_chain_options_unused` warning at `Start`, `WithOwnChainHandlers()` to silence it; design D2, task 2.5)
- **Target:** scrty-integration
- **Context:** D2 detects a bare 4xx/5xx written while the chain ran. The chain itself writes bare answers that never go through the error handler, for example `405` from its OpenID Provider endpoints (`httpsec/oidc_authorize.go:146`, `oidc_token.go:129`), so the warning fires even when `ChainOptions()` was used.
- **Evidence:** unverified as a running test; established from the cited source lines.
- **Suggested action:** Decided (D16): deterministic `security_chain_options_unused` warning at `Start`, silenced by `WithOwnChainHandlers()`; implemented by task 2.5; the accessor carry-over stays open.

### gin commits unmatched-route 404 refusals unrendered
- **Status:** adopted by scrty-integration as a parity exception (planning audit D17: Scenario "Unmatched-path 404 on gin", tasks 5.2 and 6.5); the scrty option is tracked in "Ask scrty for an option to leave gin's unmatched-path 404 uncommitted"
- **Target:** scrty-integration
- **Context:** ginsec commits an empty-body 404 for a refusal on a path gin matched no route for (`httpsec/ginsec/middleware.go:154-166`), so ginprblm cannot render it. "Every engine renders a refusal identically" cannot hold for that case on gin (for example an unknown SSO provider under `/sso/login/{provider}`).
- **Evidence:** unverified as a running test; established from the cited source lines.
- **Suggested action:** Decided (D17): documented gin parity exception (tasks 5.2, 6.5); the scrty option is a separate open entry. Prove the behaviour with a probe before filing that issue.

### Catch-all routes collide with every endpoint by default
- **Status:** adopted by scrty-integration (planning audit D14: full collisions fail, partial shadowing writes `security_endpoint_shadowed`; design D5, Requirement "Partial shadowing is a warning", task 4.2)
- **Target:** scrty-integration
- **Context:** D5 treats a wildcard or mounted handler that overlaps an endpoint as a collision, and collisions fail `Start` by default. A common SPA fallback or static file server mounted at `/` (or `GET /{path...}`) would therefore fail every start once any built-in endpoint is enabled, although only the endpoint's own paths are shadowed.
- **Evidence:** unverified: design-level reasoning, no implementation yet.
- **Suggested action:** Decided (D14): full collisions fail `Start`; partial shadowing writes one `security_endpoint_shadowed` warning; implemented by tasks 4.1, 4.2 and 4.4.

### Sentinel matching could use scrty's own mapping
- **Status:** adopted by scrty-integration (planning audit D18: one `StatusForError` rule, hand list and drift test dropped; names checked against scrty v0.1.1 `httpsec/errors.go:502`, `oidcprovider/errors.go:17`, `recovery/grants.go:27`; design D3, task 2.6)
- **Target:** scrty-integration
- **Context:** D3 keeps a hand list of sentinels from about 15 scrty packages plus a source-parsing drift test. The issuing failure that `StatusForError` answers 500 is the unexported `*issueFailure` (`httpsec/complete.go:100`), so srvr cannot classify it either way. Matching "`StatusForError(err) != 500`, or a `*ChallengeError`, or one of the exported 500-class sentinels (`ErrChallengeUnavailable`, `oidcprovider.ErrDependency`, `recovery.ErrDependency`)" gives the same responses without a list to drift.
- **Evidence:** unverified: simplification proposal; status values confirmed by probe `TestStatusRows` (every spec row matched `StatusForError`).
- **Suggested action:** Decided (D18): the `StatusForError` rule replaces the hand list and the drift test; implemented by task 2.6; consolidation 1.3 re-reads `StatusForError` against the latest scrty release.

### Ask scrty to publish the resolved client to its error handler
- **Status:** open
- **Target:** unassigned
- **Context:** Planning audit D4. Refused requests log no `client.address` because scrty resolves the client only on the context it hands downstream. srvr marks them with `srvr.client.unresolved`. If scrty set the resolved client on the request its error handler receives, refused requests could log the real client.
- **Evidence:** probe `TestOuterSeesClient` (see "Client address and principal are invisible outside the chain")
- **Suggested action:** Open an issue on kartaladev/scrty; when released, capture the client in `WriteError` and drop the unresolved marker for that case.

### Ask scrty for an option to leave gin's unmatched-path 404 uncommitted
- **Status:** accepted by scrty (2026-11-18): planned in scrty change `problem-details-rendering` (design decision 10, MODIFIED framework-adapters requirement "gin refusals reach the error channel and fail closed", tasks 5.1, 5.2 and 6.1); not yet implemented or released
- **Target:** the srvr change that bumps the scrty pin past v0.1.1
- **Context:** Planning audit D17. ginsec commits an empty `404` for a refusal on a path gin matched no route for (`httpsec/ginsec/middleware.go:154-166`), so it cannot render as a problem on gin. Accepted as a parity exception.
- **Evidence:** unverified as a running test; established from the cited source lines.
- **Suggested action:** When a scrty release after v0.1.1 ships `ginsec.WithRefusalsRendered()` (`ginsec.Middleware(chain, opts ...ginsec.Option)`; it skips `WriteHeaderNow` on an unrouted 404 so the error middleware renders it): bump the pin, pass the option in srvr's gin wiring and docs (srvr always renders `c.Error`), drop the D17 parity exception (Scenario "Unmatched-path 404 on gin", tasks 5.2 and 6.5) and turn the gin row into a normal parity row with a RED test first.

### Planning audit 2026-11-17 decisions
- **Status:** closed: applied to proposal, specs, design and tasks during planning
- **Target:** scrty-integration
- **Context:** The owner accepted the cross-change audit defaults. Applied here: D3 (opt-in challenge token), D4 (refused requests unresolved), D5 (core shutdown hooks: `CloseSessionSignals` before drain, `FlushRefusalLogs` after, on every stop path), D6 (new keys additive, golden tests), D8 (unmatched refusals `http.request.failed`), D14 (collision vs shadowing), D15 (no `WWW-Authenticate` fallback), D16 (start-time chain-options warning), D17 (gin 404 exception), D18 (`StatusForError` rule), D25 (task 1.5, scaffolding folded into slice 1). Also: core `srvr.WithSecurity` is rejected by gin and fiber constructors; collisions run as a `srvr.WithRouteCheck` check over `engine.Routes`; client and principal live in `engine.RequestState`; gin's body limit is provided by gin-and-fiber-engines.
- **Evidence:** unverified: planning decisions, no code yet.
- **Suggested action:** none; consolidation (1.3) re-checks the names against merged code.

### Planning audit 2 2026-11-17
- **Status:** closed: applied to proposal, specs, design, tasks and notes during planning
- **Target:** scrty-integration
- **Context:** A second fresh-context audit found gaps after the first round of decisions. Applied here: `security.WithConfig(security.Config)` plus one option per field, with a "Configuration sources" scenario and a rejected `WithConfig` row (M3, D13); `OnError` only collects into `engine.RequestState` and the access stage writes the failed record (L2); `srvr.client.unresolved` only with `IncludeClientAddress` on, design aligned with the spec (L6); under `CollisionError` a collision fails `Start` without a shadowing warning, and a `Start` that fails after construction runs the `AfterDrain` hook once while `ErrServerStarted` and `Start(nil)` run none (L7, A4); stale "Owner decision" suggested actions on decided entries rewritten (L3). A refused request is never an aborted one, so A3 changes no row here.
- **Evidence:** unverified: planning decisions, no code yet.
- **Suggested action:** none; consolidation (1.3) re-checks the option names and the Start-failure hook rule against merged core-server code.

### Path-scoped fiber Use handlers are invisible to the shadowing check
- **Status:** open
- **Target:** scrty-integration
- **Context:** gin-and-fiber-engines excludes path-scoped fiber `Use` entries from `engine.Routes` (planning audit 2). A consumer serving files through `app.Use("/", static)` can shadow security endpoints without a `security_endpoint_shadowed` warning.
- **Evidence:** unverified: follows from the route-listing rule in gin-and-fiber-engines `engine-support` spec; no implementation yet.
- **Suggested action:** During consolidation (1.3), either add a fiber row stating the gap as an accepted exception, or ask gin-and-fiber-engines to list path-scoped `Use` entries with a marker the check can read.
