# Tasks

## 1. Consolidation: refresh plan against merged changes

- [x] 1.1 Sync with the main branch and list every change merged or archived since this change was planned (expected: `core-server`); verify they are named in the completion note
- [x] 1.2 Decide every open `notes.md` carry-over from earlier changes addressed to `gin-and-fiber-engines` or `unassigned` (adopt, defer or close); verify none is left undecided
- [x] 1.3 Refresh this change's proposal, specs, design and tasks against the current main specs and merged code: check every `core-server` assumption listed in proposal.md - Impact (error names, config struct names, event names, attribute keys, `trace_id` member, `Register` signature, record builder inputs) with gopls, and re-check gin, fiber, ginprblm and fiberprblm released versions and APIs; verify `openspec validate gin-and-fiber-engines --strict` passes
- [x] 1.4 Record the consolidation outcome at the top of `openspec/changes/gin-and-fiber-engines/notes.md`; verify the file lists what was refreshed or states none was needed
- [x] 1.5 Commit the consolidation as `docs(openspec): consolidate gin-and-fiber-engines against main`, or skip when 1.1-1.4 changed nothing; verify with `git log -1 --stat`

## 2. Slice: engine-neutral pieces extracted, net/http unchanged

Layering decided (owner, 2026-11-18, design D1 Option A): the config structs, `Option` (`func(*engine.Settings)`), `Trace`, `ShutdownPhase`, `ErrorHook` and the sentinels are declared in `engine` and aliased in `srvr`; every `With...` stays in `srvr`. Each move is a behaviour-preserving refactoring with the core suite green at every step.

- [x] 2.1 Shared configuration model (engine-support / Scenario: Same validation on every engine, `net/http` rows); verify `go test -race ./...` in the core passes unchanged and `go test -race -run TestEngineConfig ./engine/...` passes
  - RED: `TestEngineConfig` in package `engine` with the `net/http` rows driving `engine.ResolveConfig`, plus the rows of engine-support / Scenario: srvr names are the engine declarations; fails because the package does not exist
  - GREEN: move the public types into `engine` and alias them in `srvr` (design D1); `Option` becomes `func(*engine.Settings)` with exported, engine-only fields; config resolution and validation move to `engine.ResolveConfig`, which `srvr.New` calls; an opaque engine-specific option slot whose foreign entries fail construction (engine-support / Scenario: Engine-specific option placement, `net/http` row)
  - REFACTOR: Move Function and Remove Middle Man where core only forwards, then /simplify
- [x] 2.2 Shared lifecycle state machine (engine-support / Scenario: Lifecycle misuse on every engine, as rows run against net/http); verify `go test -race -count=20 -run TestEngineLifecycle ./engine/...` passes
  - RED: `TestEngineLifecycle` with a fake serve/drain adapter covering every state row; fails because the state machine is internal
  - GREEN: `engine.Lifecycle` with `Serve` and `Drain` callbacks, net/http engine uses it; the core's shutdown-hook runner (`BeforeDrain`/`AfterDrain`, once per server, `Start`-failure rule) moves into it unchanged, so the core's hook rows stay green
  - Also make `Start`'s "Serve returned because the stop closed the listener" branch deterministic through the fake adapter and cover it (adopted core-server carry-over)
  - REFACTOR: Extract Class for the adapter seam, then /simplify
- [x] 2.3 Shared request primitives (trace context middleware core, access and triage record builders, logging pipeline) and the design D10 guards moved to `engine` without behaviour change, with failure classification reading panics through `errors.As` to `*prblm.PanicError`; verify the `core-server` suite, the log schema golden test and `go test -race ./...` pass
  - RED: tests in `engine` for each moved builder copied from core tests first (fail: symbols missing)
  - GREEN: move code, core uses `engine`
  - REFACTOR: Inline Function for core wrappers left empty, then /simplify
- [x] 2.4 Per-request state moved to `engine.RequestState` (engine-support / Scenario: Request state after the chain, `net/http` rows; Scenario: No request state outside a request); verify `go test -race -run 'TestRequestState|TestStateFrom' ./engine/...` passes and the core suite stays green
  - RED: `TestStateFrom` with the background, nil and request-context rows, and `TestRequestState` with the net/http rows (including `GET /nope` failed `true`); fails because `engine.RequestState` does not exist
  - GREEN: move core D5's holder to `engine.RequestState` with route, matched, status, failure and trace context; core stages use `engine.StateFrom`
  - REFACTOR: Replace Temp with Query where stages re-derive values the state holds, then /simplify
- [x] 2.5 Core dependencies unchanged and API marked (engine-support / Scenario: Core dependencies unchanged; Requirement: The engine-support API is marked as such); verify `GOWORK=off go list -m all` lists only prblm as third party and `go doc ./engine` shows the engine-implementations warning
- [x] 2.6 Verify slice end to end: `go test -race ./...` in the core with `GOWORK=off` (full core suite, golden log test and problem tests) green; confirm moved builders, the request state and the state machine are covered in `go tool cover -func`; `go vet ./...` and `golangci-lint run ./...` clean with `GOWORK=off`; gopls diagnostics on changed files and find references on every moved symbol; run /simplify on the slice and commit `refactor(core): extract engine support package`

## 3. Slice: gin engine end to end

- [x] 3.1 Module setup (first task of the first slice that needs it): create `github.com/kartaladev/srvr/ginsrvr` (go 1.27, gin v1.12.0, ginprblm, srvr core), add to `go.work`, CI job with `GOWORK=off`; verify `make test lint` passes for the empty module
- [x] 3.2 Gin registration and start (gin-engine / Scenario: Gin registration, accepted and rejected rows); verify `go test -race -run TestGinRegister` passes
  - RED: table including nil, typed-nil, panicking configurer, conflicting `:id`/`:name` and a route with a `nil` handler (detected from `e.Routes()` after configure, gin accepts it); fails because `ginsrvr` does not exist
  - GREEN: `ginsrvr.New`, `Register`, configure phase under recover on a `gin.New()` engine, served by `engine.Lifecycle`
  - REFACTOR: Extract Function for configurer invocation shared with core where identical, then /simplify
- [x] 3.3 Gin lifecycle misuse and shutdown hooks (engine-support / Scenario: Lifecycle misuse on every engine, `gin` rows; Scenario: Hooks per engine and stop path, `gin` rows); verify `go test -race -run 'TestGinLifecycle|TestGinShutdownHooks'` passes
  - RED: tables; fail if any state row or recorded hook sequence differs from net/http
  - GREEN: wire the shared state machine
  - REFACTOR: /simplify
- [x] 3.4 Gin errors become problems (gin-engine / Scenario: Gin error to problem, all rows); verify `go test -race -run TestGinErrors` passes
  - RED: table; fails because ginprblm is not installed or the trace member is missing
  - GREEN: ginprblm middleware with the shared option list (srvr hook and `trace_id` enricher appended last)
  - REFACTOR: /simplify
- [x] 3.5 Gin 404 and 405 (gin-engine / Scenario: Gin no route or wrong method, all rows including `GET /users/` and `POST` registered before `GET`); verify `go test -race -run TestGinUnmatched` passes
  - RED: table; fails with gin's plain-text 404, no 405, `Allow` `POST, GET` and an HTML `301` for `GET /users/`
  - GREEN: `HandleMethodNotAllowed` on, `RedirectTrailingSlash` off, `ginprblm.Middleware` installed with `engine.Use` so it renders gin's recorded 404/405; the access stage rewrites `Allow` sorted before rendering (no `NoRoute`/`NoMethod` handlers)
  - REFACTOR: /simplify
- [x] 3.6 Gin panic recovery without gin output and the aborted-request record (gin-engine / Scenario: Gin panic; Scenario: Gin aborted request record, all rows); verify `go test -race -run 'TestGinPanic|TestGinAbortRecord'` passes
  - RED: tables; fail if `[GIN]`/`[Recovery]` lines appear, the value leaks, or the `http.ErrAbortHandler` request writes no record or a record with a `problem` group
  - GREEN: ginprblm recovery, no gin logger; the access stage's deferred recover writes the aborted record and re-raises `http.ErrAbortHandler`; gin's global mode and `DebugPrintRouteFunc` are not touched (test sets `gin.SetMode(gin.TestMode)` itself)
  - REFACTOR: /simplify
- [x] 3.7 Trace and logger through gin.Context (gin-engine / Scenario: Reading trace through gin.Context, all rows including `c.Copy()`); verify `go test -race -run TestGinTraceAccess` passes
  - RED: table; fails because `ContextWithFallback` is off
  - GREEN: set `ContextWithFallback`, srvr trace middleware stores into `c.Request` context
  - REFACTOR: /simplify
- [x] 3.8 Gin access record route and unmatched classification (gin-engine / Scenario: Gin route attribute, all rows; engine-support / Scenario: Request state after the chain, `gin` rows); verify `go test -race -run 'TestGinAccessRoute|TestGinRequestState'` passes
  - RED: table; fails on unmatched rows, which log `http.request.completed` because `c.Errors` is empty
  - GREEN: shared access builder fed `c.FullPath()` through `engine.RequestState`; an empty `c.FullPath()` with status 404 or 405 marks the state failed
  - REFACTOR: /simplify
- [x] 3.9 Gin body limit (gin-engine / Scenario: Gin body limit, all rows including `4194304`/`4194305`, `10`/`11` and `-1`); verify `go test -race -run TestGinBodyLimit` passes
  - RED: table posting bodies of exact sizes through httptest; fails because gin has no body limit
  - GREEN: install the shared `http.MaxBytesReader` middleware from `engine` on the gin engine
  - REFACTOR: Extract Function if net/http and gin wrap bodies differently, then /simplify
- [x] 3.10 Configuration warning event and gin debug mode (engine-support / Scenario: Configuration warning record, `gin` and `net/http` rows; gin-engine / Scenario: Gin mode at construction); verify `go test -race -run 'TestConfigWarning|TestGinDebugWarning'` passes
  - RED: tables setting gin's mode per row (restored after each case, cases not parallel because the mode is global); fails because no warning is written
  - GREEN: `engine` writes `server.config.warning` with `srvr.warning.code` and `srvr.warning.message`; ginsrvr emits `gin_debug_mode` when `gin.IsDebugging()`; gin's mode is never changed
  - REFACTOR: /simplify
- [x] 3.11 Consumer middleware around srvr's pipeline (gin-engine / Scenario: Consumer middleware behaviour, all rows); verify `go test -race -run TestGinMiddleware` passes
  - RED: table counting access records; fails on double or missing records
  - GREEN: pipeline order trace -> access -> ginprblm -> recovery -> groups
  - REFACTOR: /simplify
- [x] 3.12 Engine hook keeps safety settings (gin-engine / Scenario: Engine hook, all rows including `RedirectTrailingSlash = true`); verify `go test -race -run TestGinEngineHook` passes
  - RED: table including nil, panicking, `Use`-calling and route-registering hooks; fails because settings are not re-asserted and gin trusts every proxy by default
  - GREEN: `SetTrustedProxies(nil)` before the hook, `WithEngine`, re-assertion (including `RedirectTrailingSlash` off), reject hooks that add to `e.Handlers` or `e.Routes()`
  - REFACTOR: /simplify
- [x] 3.13 Gin config validation parity (engine-support / Scenario: Same validation on every engine, `gin` rows including option order; Scenario: Engine-specific option placement, `gin` rows); verify `go test -race -run TestGinConfig` passes
  - RED: table; fails if any error differs
  - GREEN: delegate to `engine.ResolveConfig`
  - REFACTOR: /simplify
- [x] 3.14 Godoc (including the global gin mode and `GIN_MODE=release`, and the warning that a cyclic error chain passed to `c.Error` never returns - design D2) and a runnable `Example` with a `RegistrationHandler` whose `Register` method is mounted on `*gin.RouterGroup` (as in gin-engine / Scenario: Gin registration); verify `go test -run Example` passes
- [x] 3.15 Gin consumer faults (engine-support / Scenario: Consumer faults on every engine, `gin` rows except the route-check row, which task 6.2 covers; Scenario: Hooks per engine and stop path, the `gin` re-entrant row); verify `go test -race -run 'TestGinConsumerFaults|TestGinShutdownHooks'` passes
  - RED: one table over httptest and real listeners; fails on any row whose guard is not wired (for example an unguarded `c.Errors` entry or a panic logged as a returned error)
  - GREEN: wire the `engine` guards (design D10): `c.Errors` guarded before ginprblm resolves them, listener through `guardedListener`, no lock held across consumer calls
  - REFACTOR: /simplify
- [x] 3.16 Verify slice end to end with an httptest round trip (register, start, problem with `trace_id`, one access record, shutdown); confirm gin pipeline branches are covered in `go tool cover -func`; `GOWORK=off` `go vet`, `golangci-lint run` and `go test -race` clean in `ginsrvr` and the core; gopls diagnostics; run /simplify on the slice and commit `feat(ginsrvr): serve gin routes with srvr lifecycle, problems and logs`

## 4. Slice: fiber engine end to end

- [x] 4.1 Module setup (first task of the slice): create `github.com/kartaladev/srvr/fibersrvr` (go 1.27, fiber v3.7.0, fiberprblm, srvr core), add to `go.work`, CI job with `GOWORK=off`; verify `make test lint` passes for the empty module
- [x] 4.2 Listener-driven serve/drain adapter in `engine` if Fiber needs one not already extracted (engine-support / Scenario: Lifecycle misuse on every engine, `fiber` rows; Scenario: Hooks per engine and stop path, `fiber` rows except the route-check row, which task 6.2 covers); verify `go test -race -run 'TestFiberLifecycle|TestFiberShutdownHooks'` passes over a real listener
  - RED: tables of fiber lifecycle rows and recorded hook sequences, including the forced drain and never-started rows; fail because no fiber engine exists
  - GREEN: `fibersrvr.New` with `app.Listener` on srvr's listener under `engine.Lifecycle`
  - REFACTOR: Extract Function only for what both adapters share, then /simplify
- [x] 4.3 Fiber registration and duplicate routes (fiber-engine / Scenario: Fiber registration, accepted and rejected rows including `:id`/`:name`, a repeated route and a cross-configurer duplicate); verify `go test -race -run TestFiberRegister` passes
  - RED: table including nil handler, panicking configurer and the duplicate rows; fails because fiber accepts duplicates silently
  - GREEN: configure phase under recover on `fiber.Router`; after it, read `app.GetRoutes(true)`, normalise parameter names and fail `Start` with `srvr.ErrRouteConfig` naming the pattern and configurers
  - REFACTOR: /simplify
- [x] 4.4 Fiber errors become problems (fiber-engine / Scenario: Fiber error to problem, all rows); verify `go test -race -run TestFiberErrors` passes using `app.Test`
  - RED: table; fails because the default error handler leaks `*fiber.Error` messages
  - GREEN: fiberprblm `ErrorHandler` with the shared option list; the access middleware calls it on a chain error and returns nil so the record has the client's status (count `OnError` calls to prove it runs once)
  - REFACTOR: /simplify
- [x] 4.5 Fiber 404 and 405 (fiber-engine / Scenario: Fiber no route or wrong method); verify `go test -race -run TestFiberUnmatched` passes
  - RED: table; fails if `Allow` is missing
  - GREEN: rely on fiberprblm routing errors, add `Allow` when Fiber does not
  - REFACTOR: /simplify
- [x] 4.6 Fiber panic recovery (fiber-engine / Scenario: Fiber panic, all rows including `http.ErrAbortHandler`); verify `go test -race -run TestFiberPanic` passes
  - RED: table; fails without recovery, and fails if `http.ErrAbortHandler` is re-raised (the process would crash) or logged with `srvr.response.aborted`
  - GREEN: `fiberprblm.Recovery` after srvr trace and access middleware
  - REFACTOR: /simplify
- [x] 4.7 Value copying under context reuse (fiber-engine / Scenario: Context reuse, both rows); verify `go test -race -count=5 -run TestFiberContextReuse` passes
  - RED: 1000 concurrent requests across routes comparing records to requests; fails if a pooled value leaks
  - GREEN: copy at the boundary
  - REFACTOR: /simplify
- [x] 4.8 Trace through fiber.Ctx and cancellable context (fiber-engine / Scenario: Reading trace through fiber.Ctx; Scenario: Forced shutdown cancels handlers); verify `go test -race -run 'TestFiberTraceAccess|TestFiberForcedCancel'` passes
  - RED: tables; fails because `c` never cancels and accessors do not find values
  - GREEN: per-request context from the server base context via `c.SetContext`, accessor fallback, base cancel on forced drain
  - REFACTOR: /simplify
- [x] 4.9 Fiber access record route (fiber-engine / Scenario: Fiber route attribute, all rows including `msg` and `level`; engine-support / Scenario: Request state after the chain, `fiber` rows); verify `go test -race -run 'TestFiberAccessRoute|TestFiberRequestState'` passes
  - RED: table; fails because middleware route is reported
  - GREEN: read `c.Route().Path` after `c.Next()` only when `c.Matched()` into `engine.RequestState`; otherwise omit `http.route`; the access stage is the only one that renders a chain error and records the failure in the state
  - REFACTOR: /simplify
- [x] 4.10 Fiber drain closes lingering connections (fiber-engine / Scenario: Fiber drain, all rows); verify `go test -race -run TestFiberDrain` passes
  - RED: table counting open connections after `Shutdown`; fails because Fiber leaves them open
  - GREEN: tracking listener, close after drain or deadline
  - REFACTOR: /simplify
- [x] 4.11 Fiber config derived and hook guarded (fiber-engine / Scenario: Fiber config hook, all rows; engine-support / Scenario: Same validation on every engine, `fiber` rows; Scenario: Engine-specific option placement, `fiber` rows); verify `go test -race -run 'TestFiberConfig|TestFiberConfigHook'` passes
  - RED: tables including nil hook, nulled error handler and the body-limit boundary and unlimited rows; body rows run over a real listener because `app.Test` returns a transport error for an oversized body; fails
  - GREEN: derived `fiber.Config` (unlimited body as `math.MaxInt`), `WithFiberConfig`, re-assertion, srvr-built `ListenConfig` without prefork
  - REFACTOR: /simplify
- [x] 4.12 Fiber header limit (fiber-engine / Scenario: Fiber header limit, all rows; engine-support / Scenario: Configuration warning record, `fiber` rows); verify `go test -race -run TestFiberHeaderLimit` passes
  - RED: table over a real listener (`app.Test` returns a transport error for protocol rejections), asserting `ReadBufferSize`, status and warning count; fails with fiber's 8 KiB default
  - GREEN: `ReadBufferSize = min(MaxHeaderBytes, 65536)`; `fiber_header_limit_capped` warning only for a consumer-set value above the cap
  - REFACTOR: /simplify
- [x] 4.13 Fiber protocol rejections logged with their problem (fiber-engine / Scenario: Protocol rejection record, all rows); verify `go test -race -run TestFiberProtocolRejection` passes over a real listener
  - RED: table over a real `net.Listener` (not `app.Test`: these paths come from fasthttp's `serverErrorHandler`), counting access records and asserting `problem.status`/`problem.title`; fails because the replayed chain's nil error with a pre-set status writes no `problem` group (or renders twice)
  - GREEN: the access stage reads the status before calling `c.Next()`; a status of 400 or more marks a replay - no rendering, `problem` group from that status and `http.StatusText`, `OnError` without request state skips
  - REFACTOR: Extract Function for the replay detection shared with the unmatched classification, then /simplify
- [x] 4.14 Godoc (pooled context, use `c.Context()` for long work, disconnect limitation, header-limit cap, no header timeout, plain `501` for unknown methods, `http.ErrAbortHandler` recovered as an ordinary panic, protocol rejections logged from the pre-set status, path-scoped `Use` invisible to route checks) and a runnable `Example` on `fiber.Router`; verify `go test -run Example` passes
- [x] 4.15 Fiber consumer faults (engine-support / Scenario: Consumer faults on every engine, `fiber` rows; Scenario: Hooks per engine and stop path, the `fiber` re-entrant row; fiber-engine / Scenario: Fiber config hook, the panicking hook row); verify `go test -race -run 'TestFiberConsumerFaults|TestFiberConfigHook|TestFiberShutdownHooks'` passes
  - RED: one table, handler rows through `app.Test`, listener rows over a real listener; fails on any row whose guard is not wired (for example a cyclic chain reaching `fiberprblm.ErrorHandler`)
  - GREEN: wire the `engine` guards (design D10): `guardChain` before the error handler, `guardedListener` under the tracking listener, `WithFiberConfig` under recover with `valueText`
  - REFACTOR: /simplify
- [x] 4.16 Verify slice end to end over a real listener (register, start, problem with `trace_id`, one access record, forced drain); confirm fiber pipeline, copy and drain branches are covered in `go tool cover -func`; `GOWORK=off` `go vet`, `golangci-lint run` and `go test -race` clean in `fibersrvr` and the core; gopls diagnostics; run /simplify on the slice and commit `feat(fibersrvr): serve fiber v3 routes with srvr lifecycle, problems and logs`

## 5. Slice: cross-engine parity

- [x] 5.1 Module setup (first task of the slice): create test-only module `internal/paritytest` in `go.work`, never released; verify `go test ./...` runs in it with all three engines
- [x] 5.2 Problem parity (engine-parity / Scenario: Problem parity, all rows); verify `go test -race -run TestParity_Problem` passes
  - RED: one table, rows per engine, comparing decoded JSON exactly; fails on any drift found
  - GREEN: fix drift in the offending engine (each fix proven by its failing row)
  - REFACTOR: /simplify
- [x] 5.3 Log parity (engine-parity / Scenario: Log parity, all rows including unmatched `GET /nope`, `DELETE /items/7` and the net/http and gin `http.ErrAbortHandler` rows); verify `go test -race -run TestParity_Log` passes
  - RED: table comparing `msg`, `level`, `http.route` and exact key sets against net/http; fails on drift
  - GREEN: fix drift in the shared builders or the engine feeding them
  - REFACTOR: /simplify
- [x] 5.4 Pin documented exceptions (engine-parity / Scenario: Documented differences, all rows including gin `Allow` without `HEAD`, fiber `FOO`, fiber `http.ErrAbortHandler`, fiber `413` without route, fiber header cap, fiber header timeout and gin cyclic chain passed to `c.Error`); verify `go test -race -run TestParity_Exceptions` passes and each exception appears in that engine's godoc
  - RED: one test per row asserting the engine's actual behaviour; fails where the behaviour differs from the documented one
  - The gin cyclic-chain row asserts that `c.Error` has not returned after a bounded wait; the spinning goroutine cannot be stopped, so the test runs it once, uses no `t.Parallel` and says so in a comment
  - GREEN: correct either the documentation or the engine, recording the decision
  - REFACTOR: /simplify
- [x] 5.5 Write `notes.md` carry-overs: public `engine` package stability before v1 (Target: `unassigned`), metrics/tracing hooks needed in both engines (Target: `otel-observability`), scrty middleware placement per engine (Target: `scrty-integration`); verify entries exist with targets
- [x] 5.6 Verify slice end to end: from each module's directory (core, `ginsrvr`, `fibersrvr`, `internal/paritytest`) `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` clean, with `GOWORK=off` for the released modules; `go test -coverprofile=c.out ./... && go tool cover -func=c.out` in `internal/paritytest` and each engine module confirms the parity-exercised pipeline branches are covered; gopls diagnostics; run /simplify on the slice and commit `test(parity): guard identical problems and logs across engines`

## 6. Slice: registered routes listed and checked on every engine

- [x] 6.1 Route listing with configurer attribution (engine-support / Scenario: Route listing, all rows including net/http `Mount` and method-less patterns, gin `Any` and `Static`, fiber `All` and path-scoped `Use`); verify `go test -race -run TestRouteListing` passes in the core, `ginsrvr` and `fibersrvr`
  - RED: table per engine with a recording check; fails because no listing exists
  - GREEN: `engine.Route{Method, Pattern, Configurer}` and `engine.Routes`; the net/http registrar records patterns, gin and fiber diff `e.Routes()` / `app.GetRoutes(true)` around each `Configure`; exclude `Use` entries (path-scoped too) and fiber's implicit `HEAD`; collapse a pattern one configurer registered for the engine's whole any-method set into one entry with `Method` `""`
  - REFACTOR: Extract Function for the before/after diff shared by gin and fiber, then /simplify
- [x] 6.2 Route checks fail `Start` (engine-support / Scenario: Route check outcomes, all rows; Scenario: Hooks per engine and stop path, the fiber route-check row); verify `go test -race -run 'TestRouteCheck|TestFiberShutdownHooks'` passes in each engine module
  - RED: table including error, panic, nil, mutating, context-honouring and context-ignoring blocking check rows, and the engine-support "Consumer faults on every engine" route-check row (hostile error text, re-entrant `Addr`); fails because `srvr.WithRouteCheck` does not exist
  - GREEN: `srvr.WithRouteCheck(func(context.Context, engine.Routes) error)` runs checks in order under recover after configure and before bind, each with `Start`'s context and its own `slices.Clone` of the listing; `Start` returns the context error when it ends first and abandons the check; fiber duplicate detection becomes the first internal check
  - REFACTOR: Replace the fiber-specific duplicate call with the check mechanism (Substitute Algorithm), then /simplify
- [x] 6.3 Godoc on `engine.Route`, `srvr.WithRouteCheck` and a runnable `Example` asserting a route table at startup; verify `go test -run Example` passes
- [x] 6.4 Verify slice end to end: listing parity rows added to `internal/paritytest`; route-listing and check branches covered in `go tool cover -func`; from each module's directory `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` clean with `GOWORK=off` for released modules; gopls diagnostics and find references on changed definitions; run /simplify on the slice and commit `feat(engine): list registered routes and run route checks`

## 7. Slice: verification fixes

Findings and owner decisions of the verification pass on `32e2566`: see notes.md "Verification fixes 2026-11-18". Each behaviour runs RED (run the test and confirm it fails for the stated reason), GREEN, REFACTOR.

- [x] 7.1 Default logger only after a successful construction (engine-support / Scenario: Default logger after construction, all rows); verify `go test -race -run TestDefaultLoggerAfterConstruction` passes in the core, `ginsrvr` and `fibersrvr`
  - RED: one non-parallel table per module (`slog.SetDefault` is global; the previous default is restored in `t.Cleanup`), recording `slog.Default()` before `New` and checking that a record written through it afterwards reaches the server's handler only when `New` succeeded; fails on every failing-construction row because `ResolveConfig` calls `slog.SetDefault` before the later checks
  - GREEN: remove `slog.SetDefault` from `engine.ResolveConfig`; add `Core.SetDefaultLogger`, called as the last statement before the successful return of `srvr.New`, `ginsrvr.New` and `fibersrvr.New`
  - REFACTOR: Move Statement to Callers, then update the `WithSetDefaultLogger` godoc (applied once construction succeeds)
- [x] 7.2 Fiber duplicates compared as fiber matches them (fiber-engine / Scenario: Fiber duplicate normalisation, the rows without a sub-app); verify `go test -race -run TestFiberDuplicateNormalisation` passes in `fibersrvr`
  - RED: table over `Start`, with the `CaseSensitive` and `StrictRouting` opt-ins set through `WithFiberConfig`; fails because `Get("/users")` with `Get("/users/")` or `Get("/Users")` starts, and the opt-in rows are proved reachable through `app.Test`
  - GREEN: the duplicate key mirrors fiber's `normalizeRoutePattern` with the app's final config, then drops parameter names (design D3)
  - REFACTOR: Extract Function `matchKey`, pinned by a table of fiber's normalisation cases, then /simplify
- [x] 7.3 Mounted sub-app routes listed and checked (engine-support / Scenario: Sub-app route listing, all rows; fiber-engine / Scenario: Fiber duplicate normalisation, the sub-app rows); verify `go test -race -run 'TestSubAppRouteListing|TestFiberDuplicateNormalisation|TestMountFields'` passes in `fibersrvr`
  - RED: listing table recording the route check's input, and the sub-app duplicate rows; fail because the check receives `[]` and a sub-app route colliding with a parent route starts; one row also asserts `GET /api/users` answers `200` so the listed path is the served one
  - GREEN: implement the design D8 sub-app walk and feed its routes to the listing and the duplicate check
  - Guard test `TestMountFields` pins the three unexported fields and their types, like `TestRouteFlagFields`
  - REFACTOR: Extract Function for the walk, then /simplify; update the `fibersrvr` godoc beside the path-scoped `Use` note
- [x] 7.4 Streamed body size as a documented difference (engine-parity / Scenario: Documented differences, the fiber `SendStream` row); verify `go test -race -run TestParity_Exceptions` passes in `internal/paritytest`
  - RED: subtest asserting the client gets `hello` and the record has `http.response.body.size` `0`; run it to confirm it pins the current behaviour (it passes: a pin, not a defect - say so in the commit)
  - GREEN: none in code; `fibersrvr` godoc lists the difference under "Documented differences from net/http"
- [x] 7.5 Remove `t.Parallel()` from the gin cyclic `c.Error` parity subtest, as task 5.4 requires, and say why in its comment; verify `go test -race -run 'TestParity_Exceptions/gin_never'` passes
- [x] 7.6 Pin design D3's never-served shutdown fact: `fiber.New().ShutdownWithContext(ctx)` returns `nil`; verify `go test -race -run TestFiberShutdownNeverServed` passes in `fibersrvr`
- [x] 7.7 Verify slice end to end: from the core, `ginsrvr`, `fibersrvr` (with `GOWORK=off`) and `internal/paritytest`, `go vet ./...`, `golangci-lint run ./...`, `go test -race ./...` and coverage of the new branches (`SetDefaultLogger`, duplicate key, mount walk) in `go tool cover -func`; gopls diagnostics on changed files; no stray `.test` processes; write follow-ups to `notes.md`; run /simplify and commit as `fix(...)` (at most two commits: the fiber route listing, and the rest)

## 8. Slice: fiber mount internals fail safely

A consumer's fiber upgrade that renames or retypes `Route.group`, `Group.app` or `App.stack` silently dropped mounted routes or panicked in `Start` (design D8 decision 2026-11-18; notes.md "Fiber mount internals not fail-safe after a consumer's fiber upgrade"). Each behaviour runs RED (run the test and confirm it fails for the stated reason), GREEN, REFACTOR.

- [ ] 8.1 Walk reports unreadable internals (engine-support / Scenario: Fiber mount internals unreadable, renamed and retyped rows at the walk); verify `go test -race -run TestWalkUnreadable` passes in `fibersrvr`
  - RED: internal table calling the walk on stand-in app types with a renamed stack (`trees [][]*fiber.Route`), a retyped stack (`stack []*fiber.Route`) and a method stack holding non-pointers; fails because the walk lists nothing with no way to report it, and the retyped row panics
  - GREEN: the walk returns an error wrapping `srvr.ErrRouteConfig` naming the field and the fiber version; it checks the kind of every value it reads, and `addMounted` runs it under recover, turning a panic into the same error
- [ ] 8.2 Field check before the walk (engine-support / Scenario: Fiber mount internals unreadable, all rows); verify `go test -race -run 'TestMountFieldCheck|TestMountFields'` passes in `fibersrvr`
  - RED: internal table over `addMounted` with stand-in field lists (renamed, retyped, as pinned) and with and without a recorded mount; fails because no check exists
  - GREEN: with at least one mount, check the three fields' names and types once with `reflect.TypeFor`; on a mismatch return `srvr.ErrRouteConfig` naming the field and the fiber version from `runtime/debug.ReadBuildInfo` (`unknown` otherwise); with no mounts, no check; `configure` returns the error so `Start` fails
  - REFACTOR: `TestMountFields` checks the same field list the runtime check uses (Remove Duplicate Code); then /simplify
- [ ] 8.3 `fibersrvr` godoc: a sub-app must not be shared between servers that configure concurrently (fiber's own race, notes.md "A fiber sub-app shared between servers races in fiber")
- [ ] 8.4 Verify the slice: from `fibersrvr` with `GOWORK=off`, `go vet ./...`, `golangci-lint run ./...`, `go test -race ./...` and coverage of the new branches; no stray `.test` processes; close the notes.md entry; run /simplify and commit `fix(fibersrvr): fail Start when fiber's mount internals are unreadable`

## Workflow follow-up

- Re-read `notes.md` and make sure every open item has a target or is `unassigned`.
- Tag core `v0.2.0`, `ginsrvr/v0.1.0`, `fibersrvr/v0.1.0` after review; archive the change; verify the archived result.
