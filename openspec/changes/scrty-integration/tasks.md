# Tasks

## 1. Consolidation: refresh plan against merged changes

- [ ] 1.1 Sync with the main branch and list every change merged or archived since this change was planned (expect `core-server`, `gin-and-fiber-engines`, `otel-observability`); verify they are named in the completion note
- [ ] 1.2 Decide every open `notes.md` carry-over from earlier changes addressed to `scrty-integration` or `unassigned` (adopt, defer or close), in particular prblm hook composability and anything about `WriteError`, `engine.Routes`, `engine.RequestState`, shutdown hooks or body limits; verify none is left undecided
- [ ] 1.3 Refresh this change's proposal, specs, design and tasks against the current main specs and merged code. Check: pipeline order and the observer position; `OnError` ownership and `WithErrorHook`; `srvr.WithRouteCheck` and the contents of `engine.Routes`; `srvr.WithShutdownHook`, its `BeforeDrain`/`AfterDrain` phases, their budgets and how hook errors reach the stop call; `IncludeClientAddress` and the log keys; `engine.Routes` (with configurer attribution), `engine.RequestState` and the `server.config.warning` event as merged; gin's body limit and fiber's `BodyLimit`; the engine option names and how core options are rejected on gin and fiber; whether `error-responses`, `structured-logging` or engine capabilities need delta specs for `WriteError`, `SecurityLayer` and `WithSecurity`; and the latest scrty release, re-reading `httpsec.StatusForError` with gopls for the D3 rule. Verify `openspec validate scrty-integration --strict` passes
- [ ] 1.4 Record the consolidation outcome at the top of `openspec/changes/scrty-integration/notes.md`; verify the file lists what was refreshed or states none was needed
- [ ] 1.5 Commit the consolidation as `docs(openspec): consolidate scrty-integration against main`, or skip when 1.1-1.4 changed nothing; verify with `git log -1 --stat`

## 2. Slice: scrty refusals render as problems on net/http

- [ ] 2.1 Create module `github.com/kartaladev/srvr/security` (go 1.27, package `security`) in `go.work`, requiring scrty v0.1.1+, prblm and srvr core; add it to CI with `GOWORK=off`; verify `go build ./...` in `security` passes and `go list -m all` shows no gin or fiber module in `security`'s graph
- [ ] 2.2 Build a bridge (security-bridge / Scenario: Building the bridge, accepted and rejected rows; Scenario: Configuration sources, all rows); verify `go test -race -run 'TestNew|TestConfigSources'` passes in `security`
  - RED: `TestNew` table with nil chain, a second bridge, `WithChallengeToken()`, `WithOwnChainHandlers()`, an invalid collision mode and `WithConfig(Config{CollisionCheck: "loud"})`; `TestConfigSources` table over every Configuration sources row including both argument orders; fails because `security.New` does not exist
  - GREEN: `Bridge`, `Config` (with `json`/`yaml`/`env` tags), `WithConfig` and one option per field, `ErrConfig`; case-insensitive `CollisionCheck` parsing
  - REFACTOR: Extract Function for option validation, then /simplify
- [ ] 2.3 `engine.SecurityLayer`, `srvr.WithSecurity` and pipeline placement (security-bridge / Scenario: Engine options, net/http rows; Scenario: Placement, net/http rows); verify `go test -race -run 'TestWithSecurity|TestSecurityPlacement'` passes in core and `security`
  - RED: tables including nil layer, nil middleware, `WithSecurity` twice, unknown path refused (`401`, `http.request.failed`) vs passed (`404`, `http.request.failed`), built-in endpoint answered; fails because the option does not exist
  - GREEN: `SecurityLayer` interface in `srvr/engine`; `WithSecurity` composes the middleware between recovery and router, around the unmatched-route interception, and marks itself net/http-only
  - REFACTOR: Introduce Parameter Object for pipeline layers, then /simplify
- [ ] 2.4 `srvr.WriteError` and `bridge.ChainOptions()` (security-bridge / Scenario: Chain logging and rendering, all rows; Scenario: Rendering outside a srvr server); verify `go test -race -run 'TestChainOptions|TestWriteError'` passes
  - RED: tables including the consumer's own handler, a doubled error handler, no options (bare status), and use outside srvr; fails because neither exists
  - GREEN: request-context problem writer in core, `WriteError` with a bare-status fallback, a lazy forwarding slog handler
  - REFACTOR: Extract Class for the forwarding handler, then /simplify
- [ ] 2.5 Unused chain options warning (security-bridge / Scenario: Chain options check, all rows); verify `go test -race -run TestChainOptionsCheck` passes
  - RED: table with options used, never called (0 and 3 refusals), `WithOwnChainHandlers()`, and the chain's own bare `405`; fails because no warning is written
  - GREEN: the bridge records `ChainOptions()` calls; `SecurityLayer.ChainOptionsUsed()`; `Start` writes one `server.config.warning` with `srvr.warning.code` `security_chain_options_unused`
  - REFACTOR: /simplify
- [ ] 2.6 Status mapping and classification (security-error-mapping / Scenario: Refusal statuses, net/http rows; Scenario: Mapper scope, all net/http rows); verify `go test -race -run 'TestRefusalStatus|TestMapperScope'` passes
  - RED: tables over every net/http row, including the issuing-failure and `ErrChallengeUnavailable` 500 rows, `oidcprovider.ErrDependency`, `recovery.ErrDependency` and the `srvr.security.refused` column; fails because no mapper is registered
  - GREEN: prblm `Mapper` matching `StatusForError(err) != 500`, `*ChallengeError`, or the three exported 500-class sentinels, with status from `StatusForError`, added through `SecurityLayer.ProblemOptions()`
  - REFACTOR: Decompose Conditional into a named `isSecurityError`, then /simplify
- [ ] 2.7 Godoc for `security.New`, `ChainOptions`, `WithOwnChainHandlers`, `srvr.WithSecurity` and `WriteError`, plus a runnable net/http `Example` that builds the chain with `ChainOptions()`; verify `go test -run Example` passes
- [ ] 2.8 Verify slice end to end (an unauthenticated request gets a 401 problem with `trace_id` and one access record), confirm the mapper's match and no-match paths, `WriteError` fallback, the chain-options check and placement are covered in `go tool cover -func`, run gopls diagnostics on changed files, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from core and `security`, run /simplify on the slice and commit `feat(security): render scrty refusals as problems on net/http`

## 3. Slice: challenges, headers and log safety on net/http

- [ ] 3.1 Challenge members (security-error-mapping / Scenario: Challenge problems, all rows; Scenario: Challenge members opt-out); verify `go test -race -run 'TestChallenge'` passes
  - RED: tables including empty and nil methods, every kind and an unknown kind `100`, plus token and session absence; fails because members are missing
  - GREEN: the mapper sets `challenge` and `mfa_methods` on the problem it returns (scrty's `*ChallengeError` cannot be a prblm `Describer`), with the opt-out option and config
  - REFACTOR: Extract Function for kind naming, then /simplify
- [ ] 3.2 Challenge token opt-in (security-error-mapping / Scenario: Challenge token, all rows); verify `go test -race -run TestChallengeToken` passes
  - RED: table with the default, `WithChallengeToken()`, the config field, an empty token and the members opt-out, asserting `access_token`, `Cache-Control: no-store` and a log scan for `tok-123` at `DEBUG`; fails because the member is never set
  - GREEN: `WithChallengeToken`, `Config.ChallengeToken`; the mapper adds `access_token` only for a non-empty `Token` and sets `no-store`
  - REFACTOR: /simplify
- [ ] 3.3 Preserved headers (security-error-mapping / Scenario: Authentication headers kept, net/http rows); verify `go test -race -run TestHeadersKept` passes
  - RED: table with Basic realm, Bearer and `Cache-Control`; fails if rendering drops `WWW-Authenticate` or adds one scrty did not set
  - GREEN: `WriteError` merges into the existing header map
  - REFACTOR: /simplify
- [ ] 3.4 Refusal records without secrets (security-error-mapping / Scenario: Refusal record, all rows); verify `go test -race -run TestRefusalRecord` passes
  - RED: table scanning every log line for the bearer credential, token (with and without `WithChallengeToken()`) and session id; fails because `srvr.security.refused` is missing
  - GREEN: the refusal flag in the triage record; challenge fields never formatted
  - REFACTOR: /simplify
- [ ] 3.5 Panics inside the chain (security-bridge / Scenario: Panicking interceptor, net/http row); verify `go test -race -run TestChainPanic` passes
  - RED: a consumer interceptor panicking with `"boom"`; fails if the panic escapes or leaks
  - GREEN: confirm recovery placement; fix the layer order if needed
  - REFACTOR: /simplify
- [ ] 3.6 Chain mounted twice (security-bridge / Scenario: Chain mounted twice); verify `go test -race -run TestChainTwice` passes
  - RED: a server-level and a group-level mount with an attempt counter; fails if attempts are `2`
  - GREEN: no srvr change expected; if scrty counts twice, write a failing test, record the defect with proof in `notes.md` (Target: `unassigned`) and document it
  - REFACTOR: /simplify
- [ ] 3.7 Client address and browser headers (security-bridge / Scenario: Client address, net/http rows; Scenario: No duplicate browser headers); verify `go test -race -run 'TestClientAddress|TestNoDuplicateHeaders'` passes
  - RED: tables with a trusted proxy, an untrusted peer, a refused request, a resolved `Client.IP` of `""`, `IncludeClientAddress` off, and a CORS preflight; fails because srvr reads the peer
  - GREEN: add `ClientAddress` to `engine.RequestState`; a capture step between the chain and the router copies `httpsec.ClientFromContext` into `engine.StateFrom(ctx)`; the access record omits `client.address` and sets `srvr.client.unresolved` when nothing was captured or the IP is empty
  - REFACTOR: /simplify
- [ ] 3.8 Principal opt-in (security-bridge / Scenario: Principal in records, all rows); verify `go test -race -run TestPrincipalInLogs` passes
  - RED: table including anonymous and refused callers; fails because `enduser.id` is never written
  - GREEN: add `EndUserID` to `engine.RequestState`; `WithPrincipalInLogs`, `Config.LogPrincipal`, lookup through `identity.PrincipalFromContext` in the same capture step
  - REFACTOR: /simplify
- [ ] 3.9 Oversized login body on net/http (security-bridge / Scenario: Oversized login body, net/http row); verify `go test -race -run TestOversizedLogin` passes in core
  - RED: a 2048-byte body to the login endpoint with `MaxRequestBodyBytes` `1024`; fails if the status is not 413 or the body is not a problem
  - GREEN: confirm the core body limit error reaches srvr's 413 mapping and the security mapper does not match it
  - REFACTOR: /simplify
- [ ] 3.10 Golden log records: add `srvr.security.refused`, `srvr.client.unresolved` and `enduser.id` to the golden-record test for `http.request.failed` and `http.request.completed`, with `srvr.log.schema` unchanged; verify `go test -race -run TestGoldenRecords` passes
- [ ] 3.11 Verify slice end to end, confirm challenge, token, header-merge, client-capture and principal branches are covered in `go tool cover -func`, run gopls diagnostics, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from core and `security`, run /simplify on the slice and commit `feat(security): render challenges and keep refusal logs safe`

## 4. Slice: endpoint collisions and shutdown hooks on net/http

- [ ] 4.1 Collision matching (security-endpoint-collisions / Scenario: Collision outcome, both tables; Scenario: Engine equivalence, net/http rows); verify `go test -race -run TestCollisions` passes
  - RED: table over exact, segment and subtree endpoints, both method policies, base path and a parameter route covering a segment endpoint; fails because no check exists
  - GREEN: an `EndpointInfo` conversion in the bridge and a matcher over `engine.Routes`, supplied as a route check that `WithSecurity` registers with `srvr.WithRouteCheck`; its joined error fails `Start` with `ErrRouteConfig` naming route, configurer, `OperationID` and `PlacedBy`
  - REFACTOR: Replace Conditional with Polymorphism per match kind, then /simplify
- [ ] 4.2 Partial shadowing (security-endpoint-collisions / Scenario: Shadowing routes, all rows; Scenario: Engine equivalence, net/http `{method}` row); verify `go test -race -run TestShadowing` passes
  - RED: table with an SPA catch-all under pass-through and refused method policies, a wildcard, and mounted handlers at `/` and `/sign-in/`, asserting the warning count and who answers the probe; fails because shadowing fails start
  - GREEN: the full-versus-partial classification; one `server.config.warning` with `srvr.warning.code` `security_endpoint_shadowed` whose message lists every shadowed pair
  - REFACTOR: /simplify
- [ ] 4.3 All collisions at once (security-endpoint-collisions / Scenario: Several collisions); verify `go test -race -run TestCollisions_All` passes
  - RED: two colliding routes; fails because only the first is reported
  - GREEN: `errors.Join` in registration order
  - REFACTOR: /simplify
- [ ] 4.4 Collision modes (security-endpoint-collisions / Scenario: Collision modes, all rows); verify `go test -race -run TestCollisionModes` passes
  - RED: table including the config string and an invalid mode, counting `security_endpoint_collision` and `security_endpoint_shadowed` records; fails because modes do not exist
  - GREEN: `CollisionError`/`CollisionWarn`/`CollisionOff`; warn mode writes one `security_endpoint_collision` warning, off mode registers no route check
  - REFACTOR: /simplify
- [ ] 4.5 Shutdown hooks (security-bridge / Scenario: Shutdown hooks, all rows; Scenario: Shutdown before start; Scenario: Start fails after construction, all rows); verify `go test -race -run TestSecurityShutdown` passes
  - RED: table with signals disabled, a flush error on both stop paths, a panicking flush, close blocking past the deadline, a second shutdown, `Start` context cancellation, a never-started server, a collision or bind failure in `Start` (AfterDrain hook once, then once in total after `Shutdown`) and `ErrServerStarted`/`Start(nil)` (no hook), counting each scrty call and asserting `CloseSessionSignals` runs before drain ends; fails because hooks are not registered
  - GREEN: `SecurityLayer.ShutdownHooks()` returning a `BeforeDrain` hook (`CloseSessionSignals`) and an `AfterDrain` hook (`FlushRefusalLogs`, then `CloseSessionSignals` if not yet run), registered by `WithSecurity` through `srvr.WithShutdownHook`; `ErrSessionSignalsDisabled` treated as success; each call guarded by `sync.Once`
  - REFACTOR: Extract Function per hook, then /simplify
- [ ] 4.6 Golden log records: add `server.config.warning` records with the `security_chain_options_unused`, `security_endpoint_collision` and `security_endpoint_shadowed` codes to the golden-record test, with `srvr.log.schema` unchanged; verify `go test -race -run TestGoldenRecords` passes
- [ ] 4.7 Write `notes.md` carry-overs: background running of `chain.ExpiryTasks()`, naming spans and metrics by `OperationID` (Target: `otel-observability` if not yet archived, else `unassigned`); verify the entries exist
- [ ] 4.8 Verify slice end to end, confirm every matcher branch, the collision/shadowing split and each hook error path are covered in `go tool cover -func`, run gopls diagnostics, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from core and `security`, run /simplify on the slice and commit `feat(security): check endpoint collisions and close the chain on shutdown`

## 5. Slice: gin engine

- [ ] 5.1 `ginsrvr.WithSecurity` and placement (security-bridge / Scenario: Engine options, gin rows including core `srvr.WithSecurity` rejected; Scenario: Placement, gin rows; Scenario: Panicking interceptor, gin row); verify `go test -race -run 'TestWithSecurity|TestSecurityPlacement|TestChainPanic'` passes in `ginsrvr`
  - RED: tables with httptest; fails because the option does not exist and the core option is accepted
  - GREEN: `WithSecurity(layer, gin.HandlerFunc)`, added as the first `engine.Use` after ginprblm recovery and middleware, with the layer's problem options merged into gin's prblm options; `ginsrvr.New` rejects the net/http-only core option
  - REFACTOR: /simplify
- [ ] 5.2 gin refusal rendering and headers (security-error-mapping / Scenario: Refusal statuses, gin rows; Scenario: Authentication headers kept, gin row; Scenario: Unmatched-path 404 on gin, gin row); verify `go test -race -run 'TestRefusalStatus|TestHeadersKept|TestUnmatchedRefusal'` passes in `ginsrvr`
  - RED: tables; fails if ginprblm does not render `c.Errors` from ginsec, drops `WWW-Authenticate`, or the unmatched-path 404 record is not `http.request.failed` at `WARN`
  - GREEN: fix only srvr wiring; the empty committed 404 is the accepted exception; prove any ginprblm or ginsec defect with a failing test and record it in `notes.md`
  - REFACTOR: /simplify
- [ ] 5.3 gin client address and collisions (security-bridge / Scenario: Client address, gin rows; security-endpoint-collisions / Scenario: Engine equivalence, gin rows); verify the tests pass in `ginsrvr`
  - RED: tables including the refused-request row and the `:method` shadowing row; fails because gin routes are not normalised
  - GREEN: gin template normalisation of `engine.Routes` feeding the shared matcher
  - REFACTOR: /simplify
- [ ] 5.4 Body limit on gin (security-bridge / Scenario: Oversized login body, gin row); verify `go test -race -run TestOversizedLogin` passes in `ginsrvr`
  - RED: a 2048-byte body to the login endpoint; fails if the status is not 413
  - GREEN: confirm the gin body limit from `gin-and-fiber-engines` (`http.MaxBytesReader`, `413` problem) applies before the chain reads the login body
  - REFACTOR: /simplify
- [ ] 5.5 Godoc and a runnable gin `Example` with `ginsec.Middleware(chain)`; verify `go test -run Example` passes in `ginsrvr`
- [ ] 5.6 Verify slice end to end, confirm the gin rendering and template branches are covered in `go tool cover -func`, run gopls diagnostics, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from `ginsrvr` and `security`, run /simplify on the slice and commit `feat(security): wire scrty into the gin engine`

## 6. Slice: fiber v3 engine and cross-engine parity

- [ ] 6.1 `fibersrvr.WithSecurity` and placement (security-bridge / Scenario: Engine options, fiber rows including core `srvr.WithSecurity` rejected; Scenario: Placement, fiber rows; Scenario: Panicking interceptor, fiber row); verify `go test -race -run 'TestWithSecurity|TestSecurityPlacement|TestChainPanic'` passes in `fibersrvr` using `app.Test`
  - RED: tables; fails because the option does not exist and the core option is accepted
  - GREEN: `WithSecurity(layer, fiber.Handler)`, added as the first `app.Use` after fiberprblm recovery, with the layer's problem options merged into fiberprblm's; `fibersrvr.New` rejects the net/http-only core option
  - REFACTOR: /simplify
- [ ] 6.2 fiber refusal rendering through `RefusalError` (security-error-mapping / Scenario: Refusal statuses, fiber rows; Scenario: Mapper scope, fiber row; Scenario: Authentication headers kept, fiber row; Scenario: Unmatched-path 404 on gin, fiber row); verify the tests pass in `fibersrvr`
  - RED: tables; fails if the mapper does not see through `RefusalError`'s two unwrapped errors
  - GREEN: no change expected beyond option merging; prove any defect with a failing test
  - REFACTOR: /simplify
- [ ] 6.3 Body limit (security-bridge / Scenario: Oversized login body, fiber rows); verify `go test -race -run TestOversizedLogin` passes in `fibersrvr`
  - RED: the 2048-byte and 512-byte rows; fails if `BodyLimit` is not aligned
  - GREEN: confirm `fibersrvr`'s `BodyLimit` mapping applies before the chain
  - REFACTOR: /simplify
- [ ] 6.4 fiber client address and collisions (security-bridge / Scenario: Client address, fiber rows; security-endpoint-collisions / Scenario: Engine equivalence, fiber rows); verify the tests pass in `fibersrvr`
  - RED: tables including the refused-request row and the `:method` shadowing row; fails because fiber routes are not normalised
  - GREEN: fiber route-path normalisation of `engine.Routes`
  - REFACTOR: /simplify
- [ ] 6.5 Cross-engine parity (security-error-mapping / Scenario: Engine parity, all rows; Scenario: Unmatched-path 404 on gin, all rows); verify `go test -race -run TestSecurityParity` passes in `internal/paritytest`
  - RED: three servers with equivalent chains compared member by member except `trace_id`, with the gin unmatched-path 404 asserted as the listed exception; fails on any other difference
  - GREEN: fix srvr wiring only; record upstream differences with proof in `notes.md`
  - REFACTOR: Extract Function for response normalisation, then /simplify
- [ ] 6.6 Godoc and a runnable fiber `Example` with `fibersec.Middleware(chain)`; verify `go test -run Example` passes in `fibersrvr`
- [ ] 6.7 Verify slice end to end on all three engines, confirm the fiber rendering, body limit and template branches are covered in `go tool cover -func`, run gopls diagnostics, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from `fibersrvr`, `security` and core, re-read `notes.md` so every open item has a target, run /simplify on the slice and commit `feat(security): wire scrty into the fiber engine with parity tests`

## Workflow follow-up

- Tag `security/v0.1.0` and the minor releases of core, `ginsrvr` and `fibersrvr` after review.
- Archive the change and verify the archived result.
