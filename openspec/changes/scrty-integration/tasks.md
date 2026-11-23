# Tasks

## 1. Consolidation: refresh plan against merged changes

- [x] 1.1 Sync with the main branch and list every change merged or archived since this change was planned (expect `core-server`, `gin-and-fiber-engines`, `otel-observability`); verify they are named in the completion note
- [x] 1.2 Decide every open `notes.md` carry-over from earlier changes addressed to `scrty-integration` or `unassigned` (adopt, defer or close), in particular prblm hook composability and anything about `WriteError`, `srvr.Routes`, the internal `engine.RequestState`, shutdown hooks or body limits; verify none is left undecided
- [x] 1.3 Refresh this change's proposal, specs, design and tasks against the current main specs and merged code. Check: pipeline order and the observer position; `OnError` ownership and `WithErrorHook`; `srvr.WithRouteCheck` and the contents of `srvr.Routes`; `srvr.WithShutdownHook`, its `BeforeDrain`/`AfterDrain` phases, their budgets and how hook errors reach the stop call; `IncludeClientAddress` and the log keys; `srvr.Routes` (with configurer attribution), the internal `engine.RequestState` and the `server.config.warning` event as merged; gin's body limit and fiber's `BodyLimit`; the engine option names and how core options are rejected on gin and fiber; whether `error-responses`, `structured-logging` or engine capabilities need delta specs for `WriteError`, `SecurityLayer` and `WithSecurity`; and the latest scrty release, re-reading `httpsec.StatusForError` with gopls for the D3 rule. Verify `openspec validate scrty-integration --strict` passes
- [x] 1.4 Record the consolidation outcome at the top of `openspec/changes/scrty-integration/notes.md`; verify the file lists what was refreshed or states none was needed
- [ ] 1.5 Commit the consolidation as `docs(openspec): consolidate scrty-integration against main`, or skip when 1.1-1.4 changed nothing; verify with `git log -1 --stat`

## 2. Slice: scrty refusals render as problems on net/http

- [ ] 2.1 Create module `github.com/kartaladev/srvr/security` (go 1.27, package `security`) in `go.work`, requiring scrty `httpsec` and `httpsec/prblmsec` v0.2.0, prblm and srvr core; allow `security` -> `internal/engine` in TestImportDirection and add a guard that `security` reaches neither gin nor fiber; add it to CI with `GOWORK=off`; verify `go build ./...` in `security` passes and `go list -m all` shows no gin or fiber module in `security`'s graph
- [ ] 2.2 Core problem namespace (error-responses / Scenario: Problem namespace, all rows); verify `go test -race -run TestProblemNamespace` passes in core, `ginsrvr` and `fibersrvr`
  - RED: table over every row; fails because `WithProblemNamespace` does not exist
  - GREEN: `Config.ProblemNamespace` with tags, `WithProblemNamespace`, validation naming the field, `prblm.WithNamespace` applied before the consumer's prblm options
  - REFACTOR: /simplify
- [ ] 2.3 `srvr.SecurityLayer`, the engine-only middleware slot and the core wiring helper (security-bridge / Scenario: Engine options, net/http rows without scrty errors); verify `go test -race -run TestSecurityLayer` passes in core
  - RED: core tests with a fake layer and fake middleware: no `ProblemNamespace`, security given twice, the slot given to the wrong engine, layer problem options placed first without counting as `WithProblemOptions`; fails because the interface and slot do not exist
  - GREEN: `SecurityLayer` in `internal/engine` (`ProblemOptions`, `Refused`, `RouteCheck`, `ShutdownHooks`, `CaptureState`), aliased `srvr.SecurityLayer`; middleware slot as `engine.Only`; one wiring helper every engine calls
  - REFACTOR: Introduce Parameter Object for pipeline layers, then /simplify
- [ ] 2.4 `security.WithSecurity` builds the chain (security-bridge / Scenario: Engine options, net/http rows; Scenario: Chain ready callback, all rows; Scenario: Configuration sources, all rows); verify `go test -race -run 'TestWithSecurity|TestChainReady|TestConfigSources'` passes in `security`
  - RED: tables including `nil` chain options, `httpsec.WithErrorHandler` or `httpsec.WithLogger` in `chainOpts`, a nil `chainOpts` entry, an invalid collision mode, `WithConfig(Config{CollisionCheck: "loud"})`, `WithChainReady(nil)` and a panicking callback, and both argument orders; fails because `security.WithSecurity` does not exist
  - GREEN: `WithSecurity(chainOpts []httpsec.Option, opts ...Option)`, `Option`, `Config` (`CollisionCheck`, `ChallengeToken`, `LogPrincipal`, with `json`/`yaml`/`env` tags), `WithConfig` and one option per field, `WithChainReady`, `ErrConfig` wrapping scrty's error naming the field; the shared build step in `security/internal/build`; prblmsec built with `WithoutChallengeToken()` unless opted in
  - REFACTOR: Extract Function for option validation, then /simplify
- [ ] 2.5 Pipeline placement on net/http (security-bridge / Scenario: Placement, net/http rows); verify `go test -race -run TestSecurityPlacement` passes in `security`
  - RED: table with unknown path refused (`401`, `http.request.failed`) vs passed (`404`), built-in endpoint answered, authenticated request; fails because the middleware is not composed
  - GREEN: compose the slot's middleware between recovery and router, around the unmatched-route interception
  - REFACTOR: /simplify
- [ ] 2.6 `srvr.WriteError` and srvr's chain handler and logger (security-bridge / Scenario: Chain logging and rendering, net/http row; Scenario: Rendering outside a srvr server; error-responses / Scenario: WriteError, all rows); verify `go test -race -run 'TestChainLogging|TestWriteError|TestOutsideSrvr'` passes
  - RED: tables; fails because neither exists
  - GREEN: `srvr.WriteError(w, r, err) bool` through `Core.HTTPErrors().Write` of the request's server; srvr's error handler falling back to `httpsec.StatusForError` and an empty body when it returns false; a forwarding slog handler over `srvr.LoggerFromContext`, added to `chainOpts` by `WithSecurity`
  - REFACTOR: Extract Class for the forwarding handler, then /simplify
- [ ] 2.7 Rendering and classification (security-error-mapping / Scenario: Refusal statuses, net/http rows; Scenario: Mapper scope, all net/http rows; Scenario: Refusal under a custom renderer); verify `go test -race -run 'TestRefusalStatus|TestMapperScope|TestCustomRendererRefusal'` passes
  - RED: tables over every net/http row, including the 500 rows, the `srvr.security.refused` column and both renderer rows; fails because prblmsec is not registered and nothing sets the flag
  - GREEN: `SecurityLayer.ProblemOptions()` returning `prblmsec`'s `Option()`; `SecurityLayer.Refused` from `httpsec.LookupStatus`; srvr's recording hook and `Core.Render` set `srvr.security.refused`
  - REFACTOR: /simplify
- [ ] 2.8 Godoc for `security.WithSecurity`, its options, `srvr.SecurityLayer` (for security modules only) and `WriteError`, plus a runnable net/http `Example` with `WithChainReady`; verify `go test -run Example` passes
- [ ] 2.9 Verify slice end to end (an unauthenticated request gets a 401 problem with `trace_id` and one access record), confirm the build step's error paths, the mapper's match and no-match paths, the `WriteError` fallback and placement are covered in `go tool cover -func`, run gopls diagnostics on changed files, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from core and `security`, run /simplify on the slice and commit `feat(security): render scrty refusals as problems on net/http`

## 3. Slice: challenges, headers and log safety on net/http

- [ ] 3.1 Challenge problems (security-error-mapping / Scenario: Challenge problems, all rows); verify `go test -race -run 'TestChallengeProblems'` passes
  - RED: table over every kind and an unknown kind `100`, plus token and session absence; fails if prblmsec's members do not reach the response through srvr
  - GREEN: no srvr code expected; prove any prblmsec defect with a failing test and record it in `notes.md`
  - REFACTOR: /simplify
- [ ] 3.2 Challenge token opt-in (security-error-mapping / Scenario: Challenge token, all rows); verify `go test -race -run TestChallengeToken` passes
  - RED: table with the default, `WithChallengeToken()`, the config field and an empty token, asserting `access_token`, `Cache-Control` and a log scan for `tok-123` at `DEBUG`; fails if the opt-in does not reach prblmsec
  - GREEN: `WithChallengeToken`, `Config.ChallengeToken` select whether `prblmsec.WithoutChallengeToken()` is given
  - REFACTOR: /simplify
- [ ] 3.3 Preserved headers (security-error-mapping / Scenario: Authentication headers kept, net/http rows); verify `go test -race -run TestHeadersKept` passes
  - RED: table with Basic realm and Bearer; fails if rendering drops `WWW-Authenticate` or adds one scrty did not set
  - GREEN: confirm `WriteHTTPError` merges into the existing header map
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
  - GREEN: add `ClientAddress` to the internal `engine.RequestState`; a capture step between the chain and the router calls `SecurityLayer.CaptureState`, which copies `httpsec.ClientFromContext`; the access record omits `client.address` and sets `srvr.client.unresolved` when nothing was captured or the IP is empty
  - REFACTOR: /simplify
- [ ] 3.8 Principal opt-in (security-bridge / Scenario: Principal in records, all rows); verify `go test -race -run TestPrincipalInLogs` passes
  - RED: table including anonymous and refused callers; fails because `enduser.id` is never written
  - GREEN: add `EndUserID` to the internal `engine.RequestState`; `WithPrincipalInLogs`, `Config.LogPrincipal`, lookup through `identity.PrincipalFromContext` in the same capture step
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
  - GREEN: an `EndpointInfo` conversion in the `security` module and a matcher over `srvr.Routes`, supplied as `SecurityLayer.RouteCheck()`, which the core wiring helper registers with `srvr.WithRouteCheck`; its joined error fails `Start` with `ErrRouteConfig` naming route, configurer, `OperationID` and `PlacedBy`
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
  - GREEN: `SecurityLayer.ShutdownHooks()` returning a `BeforeDrain` hook (`CloseSessionSignals`) and an `AfterDrain` hook (`FlushRefusalLogs`, then `CloseSessionSignals` if not yet run), registered by the core wiring helper through `srvr.WithShutdownHook`; `ErrSessionSignalsDisabled` treated as success; each call guarded by `sync.Once`
  - REFACTOR: Extract Function per hook, then /simplify
- [ ] 4.6 Golden log records: add `server.config.warning` records with the `security_endpoint_collision` and `security_endpoint_shadowed` codes to the golden-record test, with `srvr.log.schema` unchanged; verify `go test -race -run TestGoldenRecords` passes
- [ ] 4.7 Write `notes.md` carry-overs: background running of `chain.ExpiryTasks()`, naming spans and metrics by `OperationID` (Target: `otel-observability` if not yet archived, else `unassigned`); verify the entries exist
- [ ] 4.8 Verify slice end to end, confirm every matcher branch, the collision/shadowing split and each hook error path are covered in `go tool cover -func`, run gopls diagnostics, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from core and `security`, run /simplify on the slice and commit `feat(security): check endpoint collisions and close the chain on shutdown`

## 5. Slice: gin engine

- [ ] 5.1 Module `security/ginsecurity` with `ginsecurity.WithSecurity` and placement (security-bridge / Scenario: Engine options, gin rows including `security.WithSecurity` rejected; Scenario: Placement, gin rows; Scenario: Chain logging and rendering, gin row; Scenario: Panicking interceptor, gin row); verify `go test -race -run 'TestWithSecurity|TestSecurityPlacement|TestChainLogging|TestChainPanic'` passes in `security/ginsecurity`
  - RED: tables with httptest; fails because the module does not exist and `security.WithSecurity` is accepted by gin
  - GREEN: create the module (requires `security`, scrty `ginsec` v0.2.0, gin v1.12.0, `ginsrvr`; TestImportDirection edges; CI with `GOWORK=off`); `WithSecurity(chainOpts, opts...)` through `security/internal/build` with srvr's logger only and `ginsec.Middleware(chain, ginsec.WithRefusalsRendered())` in gin's middleware slot; `ginsrvr` adds the slot's handler as the first `engine.Use` after ginprblm recovery and middleware, through the shared core wiring helper
  - REFACTOR: /simplify
- [ ] 5.2 gin refusal rendering and headers (security-error-mapping / Scenario: Refusal statuses, gin rows; Scenario: Authentication headers kept, gin row; Scenario: Unmatched-path 404, both gin rows); verify `go test -race -run 'TestRefusalStatus|TestHeadersKept|TestUnmatchedRefusal'` passes in `ginsrvr`
  - RED: tables; fails if srvr's gin render path does not render `c.Errors` from ginsec, drops `WWW-Authenticate`, or the unmatched-path 404 is not a problem with one `http.request.failed` at `WARN`, with and without `ginsec.WithRefusalsRendered()`
  - GREEN: fix only srvr wiring; prove any ginsec defect with a failing test and record it in `notes.md`
  - REFACTOR: /simplify
- [ ] 5.3 gin client address and collisions (security-bridge / Scenario: Client address, gin rows; security-endpoint-collisions / Scenario: Engine equivalence, gin rows); verify the tests pass in `ginsrvr`
  - RED: tables including the refused-request row and the `:method` shadowing row; fails because gin routes are not normalised
  - GREEN: gin template normalisation of `srvr.Routes` feeding the shared matcher
  - REFACTOR: /simplify
- [ ] 5.4 Body limit on gin (security-bridge / Scenario: Oversized login body, gin row); verify `go test -race -run TestOversizedLogin` passes in `ginsrvr`
  - RED: a 2048-byte body to the login endpoint; fails if the status is not 413
  - GREEN: confirm the gin body limit from `gin-and-fiber-engines` (`http.MaxBytesReader`, `413` problem) applies before the chain reads the login body
  - REFACTOR: /simplify
- [ ] 5.5 Godoc and a runnable gin `Example` with `ginsecurity.WithSecurity` and `WithChainReady`; verify `go test -run Example` passes in `security/ginsecurity`
- [ ] 5.6 Verify slice end to end, confirm the gin rendering and template branches are covered in `go tool cover -func`, run gopls diagnostics, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from `ginsrvr`, `security` and `security/ginsecurity`, run /simplify on the slice and commit `feat(security): wire scrty into the gin engine`

## 6. Slice: fiber v3 engine and cross-engine parity

- [ ] 6.1 Module `security/fibersecurity` with `fibersecurity.WithSecurity` and placement (security-bridge / Scenario: Engine options, fiber rows including `ginsecurity.WithSecurity` rejected; Scenario: Placement, fiber rows; Scenario: Chain logging and rendering, fiber row; Scenario: Panicking interceptor, fiber row); verify `go test -race -run 'TestWithSecurity|TestSecurityPlacement|TestChainLogging|TestChainPanic'` passes in `security/fibersecurity` using `app.Test`
  - RED: tables; fails because the module does not exist and another engine's option is accepted
  - GREEN: create the module (requires `security`, scrty `fibersec` v0.2.0, Fiber v3.7.0, `fibersrvr`; TestImportDirection edges; CI with `GOWORK=off`); `WithSecurity(chainOpts, opts...)` with srvr's logger only and `fibersec.Middleware(chain)` in fiber's slot; `fibersrvr` adds it as the first `app.Use` after fiberprblm recovery, through the shared core wiring helper
  - REFACTOR: /simplify
- [ ] 6.2 fiber refusal rendering through `RefusalError` (security-error-mapping / Scenario: Refusal statuses, fiber rows; Scenario: Mapper scope, fiber row; Scenario: Authentication headers kept, fiber row; Scenario: Unmatched-path 404, fiber row); verify the tests pass in `fibersrvr`
  - RED: tables; fails if the mapper does not see through `RefusalError`'s two unwrapped errors
  - GREEN: no change expected; prove any defect with a failing test
  - REFACTOR: /simplify
- [ ] 6.3 Body limit (security-bridge / Scenario: Oversized login body, fiber rows); verify `go test -race -run TestOversizedLogin` passes in `fibersrvr`
  - RED: the 2048-byte and 512-byte rows; fails if `BodyLimit` is not aligned
  - GREEN: confirm `fibersrvr`'s `BodyLimit` mapping applies before the chain
  - REFACTOR: /simplify
- [ ] 6.4 fiber client address and collisions (security-bridge / Scenario: Client address, fiber rows; security-endpoint-collisions / Scenario: Engine equivalence, fiber rows); verify the tests pass in `fibersrvr`
  - RED: tables including the refused-request row and the `:method` shadowing row; fails because fiber routes are not normalised
  - GREEN: fiber route-path normalisation of `srvr.Routes`
  - REFACTOR: /simplify
- [ ] 6.5 Cross-engine parity (security-error-mapping / Scenario: Engine parity, all rows; Scenario: Unmatched-path 404, all rows); verify `go test -race -run TestSecurityParity` passes in `internal/paritytest`
  - RED: three servers with equivalent chains compared member by member except `trace_id`, the unmatched-path 404 included; fails on any difference
  - GREEN: fix srvr wiring only; record upstream differences with proof in `notes.md`
  - REFACTOR: Extract Function for response normalisation, then /simplify
- [ ] 6.6 Godoc and a runnable fiber `Example` with `fibersecurity.WithSecurity` and `WithChainReady`; verify `go test -run Example` passes in `security/fibersecurity`
- [ ] 6.7 Verify slice end to end on all three engines, confirm the fiber rendering, body limit and template branches are covered in `go tool cover -func`, run gopls diagnostics, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from `fibersrvr`, `security`, `security/fibersecurity` and core, re-read `notes.md` so every open item has a target, run /simplify on the slice and commit `feat(security): wire scrty into the fiber engine with parity tests`

## Workflow follow-up

- Tag `security/v0.1.0`, `security/ginsecurity/v0.1.0`, `security/fibersecurity/v0.1.0` and the minor releases of core, `ginsrvr` and `fibersrvr` after review.
- Archive the change and verify the archived result.
