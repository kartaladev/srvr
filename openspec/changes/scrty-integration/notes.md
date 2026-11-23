# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

## Redesign: option-based integration (2026-11-22, owner decision)

- **Decision:** no public bridge. Each engine has one option taking scrty's options and building the chain inside: `security.WithSecurity(chainOpts []httpsec.Option, opts ...security.Option)` (net/http), `ginsecurity.WithSecurity` and `fibersecurity.WithSecurity` in new nested modules `security/ginsecurity` and `security/fibersecurity` (design D1, D10).
- **Why:** found while writing configuration samples. The bridge design had four flaws: a circular build order on net/http (`bridge.ChainOptions()` needed before `httpsec.New`, the bridge needed the chain); the `security_chain_options_unused` warning firing on gin and fiber, which never need chain options; `WithOwnChainHandlers` detecting a forgotten call rather than a missing handler; and bridge and middleware passed separately, so they could come from different chains. scrty v0.2.0 accepts `WithErrorHandler`/`WithLogger` only at `httpsec.New` and documents the error handler as net/http-only (`httpsec/options.go:911`, `:1243-1257`; `chain.go:82`), so srvr building the chain removes all four by construction. The core cannot accept `httpsec` types (stdlib + prblm only), hence options living in the security modules.
- **Removed:** `security.New`, `Bridge`, `bridge.ChainOptions()`, `WithOwnChainHandlers`, `Config.OwnChainHandlers`, `SecurityLayer.ChainOptionsUsed()`, the `security_chain_options_unused` warning and its scenario, the "Unused chain options" requirement, `srvr.WithSecurity`, `ginsrvr.WithSecurity`, `fibersrvr.WithSecurity`.
- **Added:** `security.WithChainReady(func(*httpsec.Chain))` to hand the built chain back (`PublishSignal`, `ReportCredentialChange`, `ExpiryTasks`); engine-only middleware slot in `internal/engine`; shared build step in `security/internal/build`; requirements "One engine option builds and wires the chain", "The built chain is handed to the consumer", "scrty logs and renders through srvr".
- **Shapes settled by default (D11), revisit before task 2.4 if wanted:** two parameters rather than a variadic `any`; `WithChainReady` callback rather than a constructor returning the chain; option names as above.
- **Artifacts refreshed:** proposal, design (D1, D2, D10, D11, Defaults, Slices, Risks, Migration), specs `security-bridge`, `security-error-mapping`, `security-endpoint-collisions`, tasks (slice 2 rewritten as 2.1-2.9; 5.1, 5.5, 6.1, 6.6 and the release follow-up for the new modules). `openspec validate scrty-integration --strict` passes.
- **Reference:** configuration samples for all three engines are in the owner's doc "srvr scrty-integration" (section "Configuring srvr: option-based design (proposed)").
- **Next session:** commit the plan (task 1.5) after owner review, then start slice 2 with task 2.1.

## Consolidation outcome (2026-11-22)

- **Merged since planning (1.1):** `core-server`, `gin-and-fiber-engines`, `otel-observability`, `goroutine-leak-checks`, `skills-compliance`, `triage-debug-buffer`, `followups-hardening`, `engine-internal-split`, `pluggable-error-renderer` (archived), with srvr v0.4.0 released; `static-files` is still an open change in `openspec/changes/`. scrty v0.2.0 was released on 2026-11-22.
- **Refreshed (1.3):**
  - Paths: `srvr/engine` is now `srvr/internal/engine`; specs and proposal use the `srvr` aliases (`srvr.Routes`, `srvr.SecurityLayer`); TestImportDirection must allow `security` -> `internal/engine` (task 2.1).
  - Rendering (owner decisions): delegate to scrty's new `httpsec/prblmsec` instead of srvr's own mapper (design D3); keep the challenge token opt-in by passing `prblmsec.WithoutChallengeToken()` by default (D7); srvr owns the problem namespace through a new core `Config.ProblemNamespace` / `WithProblemNamespace`, required when security is wired (D9, new `error-responses` delta); a custom `ErrorRenderer` receives scrty's error unchanged, `srvr.DefaultErrorRenderer()` still includes prblmsec (D2).
  - security-error-mapping rewritten: typed `security/<name>` problems, prblmsec members (`challenge` with hyphenated vocabulary, `methods`, `access_token`, `token_type`); the `WithChallengeMembers` opt-out and `mfa_methods` dropped; `srvr.security.refused` from `httpsec.LookupStatus`; new custom-renderer scenario; `Cache-Control: no-store` now comes from scrty.
  - gin unmatched-path 404: no longer a parity exception (scrty v0.2.0 `ginsec.WithRefusalsRendered`); both gin rows are normal rows.
  - `srvr.WriteError` now returns `bool` (false outside a srvr request) so the bridge's handler can fall back to scrty's bare status; core cannot import scrty.
  - `srvr.WithSecurity` is an `engine.Only("net/http", ...)` option, so the merged `EngineOptions` check rejects it on gin and fiber; one core helper wires the layer's shared parts on every engine.
  - Checked unchanged: pipeline order with the observer between access log and recovery; `WithRouteCheck` and `ErrRouteConfig`; `WithShutdownHook` with `BeforeDrain`/`AfterDrain` and `ShutdownHookTimeout`; `Core.Warn` / `server.config.warning`; `IncludeClientAddress`; gin's body limit and fiber's `BodyLimit`; `structured-logging` and engine capabilities need no delta (new keys are additive).
  - tasks.md: new task 2.2 (problem namespace); slice 2 renumbered to 2.1-2.9; tasks 2.7, 3.1, 3.2, 3.3, 5.2, 5.5, 6.5 rewritten for prblmsec.
- **Carry-overs decided (1.2):** see the entries below, each with its status; open entries from earlier changes addressed to `unassigned` that do not touch `WriteError`, routes, request state, shutdown hooks or body limits are left with their owners unchanged. "AfterDrain hooks share one budget" (core-server, `unassigned`) affects D6's AfterDrain hook only through a consumer's blocking hook ordered before it: deferred, unchanged.


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
- **Status:** closed 2026-11-22: not needed. Under the option-based redesign srvr builds the chain and always sets its own error handler, so there is nothing to detect; the warning and `WithOwnChainHandlers()` are removed.
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
- **Status:** superseded 2026-11-22 by the option-based redesign: no warning exists any more (was planning audit D16)
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
- **Status:** closed (consolidation 2026-11-22): released in scrty v0.2.0 as `ginsec.WithRefusalsRendered()`; this change pins v0.2.0 and drops the D17 parity exception (Scenario "Unmatched-path 404", tasks 5.2 and 6.5)
- **Target:** scrty-integration
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
- **Status:** decided (consolidation 2026-11-22): accepted gap, stated in design D5; a fiber consumer who serves files through `app.Use` gets no `security_endpoint_shadowed` warning. Reopen if `srvr.Routes` ever lists path-scoped `Use` entries
- **Target:** scrty-integration
- **Context:** gin-and-fiber-engines excludes path-scoped fiber `Use` entries from `engine.Routes` (planning audit 2). A consumer serving files through `app.Use("/", static)` can shadow security endpoints without a `security_endpoint_shadowed` warning.
- **Evidence:** unverified: follows from the route-listing rule in gin-and-fiber-engines `engine-support` spec; no implementation yet.
- **Suggested action:** During consolidation (1.3), either add a fiber row stating the gap as an accepted exception, or ask gin-and-fiber-engines to list path-scoped `Use` entries with a marker the check can read.

### scrty-integration must render through ErrorRenderer (from pluggable-error-renderer)
- **Status:** adopted (consolidation 2026-11-22): design D2/D3, security-error-mapping "A custom renderer receives scrty's error unchanged", task 2.7
- **Target:** scrty-integration
- **Context:** pluggable-error-renderer notes.md: refusals must reach the client through the server's renderer, not prblm directly.
- **Evidence:** unverified: design-level; `WithErrorRenderer` and `WithProblemOptions` are exclusive (`internal/engine/config.go` `validateErrorRenderer`), so the layer's prblm options are added internally.
- **Suggested action:** implemented by tasks 2.4 and 2.7.

### scrty middleware placement per engine (from gin-and-fiber-engines)
- **Status:** adopted (consolidation 2026-11-22): design D1/D4; one core wiring helper for the shared parts, a typed `WithSecurity` per engine for the middleware
- **Target:** scrty-integration
- **Context:** gin-and-fiber-engines suggested one engine-level insertion point with parity rows.
- **Evidence:** unverified: design-level.
- **Suggested action:** implemented by tasks 2.4, 5.1 and 6.1.

### prblmsec owns srvr's security problem contract
- **Status:** open
- **Target:** unassigned (each change that bumps the scrty pin)
- **Context:** Since D3 (2026-11-22) problem types, titles and members of scrty refusals are scrty's `prblmsec` contract, not srvr's. A scrty release may change them.
- **Evidence:** unverified: release-process risk; prblmsec godoc v0.2.0.
- **Suggested action:** on every scrty bump, re-run the security golden and parity tests and record any body change in CHANGELOG.md as a srvr-visible change.
