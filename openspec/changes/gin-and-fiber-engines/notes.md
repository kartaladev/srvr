# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Verification fixes 2026-11-18 (task group 7)
- **Status:** closed: task group 7 applied (W1, W2, W3, W4, S1 fixed or pinned; S2 unconfirmed and pinned by `TestFiberShutdownNeverServed`)
- **Target:** gin-and-fiber-engines
- **Context:** The verification pass on `32e2566` found: W1 a failed construction still replaces `slog.Default()` (`engine.ResolveConfig` calls `slog.SetDefault` before later checks can fail); W2 fiber duplicates differing only by a trailing slash or letter case pass `Start` (owner: normalise like fiber, respecting `CaseSensitive`/`StrictRouting`); W3 routes of a mounted fiber sub-app are served but missing from the listing and the duplicate check (owner: list them); W4 a streamed fiber body logs its declared length (keep, document); S1 the gin cyclic `c.Error` parity subtest runs with `t.Parallel()` against task 5.4; S2 design D3 says `ShutdownWithContext` on a never-served app returns `nil`, claimed to return `fiber.ErrNotRunning`. Refreshed: engine-support new requirements "A failed construction changes no process-wide state" and "Mounted fiber sub-app routes are listed"; fiber-engine new requirement "Fiber duplicate routes are compared as fiber matches them" (the registration requirement points to it); engine-parity streamed body-size row; design D1, D3, D4, D8 (sub-app approach and the alternatives rejected), Defaults, Hot Paths, Risks; tasks group 7. `openspec validate gin-and-fiber-engines --strict` passes.
- **Evidence:** S2 did not reproduce: a probe in `fibersrvr` (`fiber.New().ShutdownWithContext(context.Background())`, `GOWORK=off go test -run TestZZProbe -v .`) logged `<nil>` and `errors.Is(err, fiber.ErrNotRunning)` `false`, also after serving and on a second call; fiber v3.7.0 `app.go:1436-1438` returns `ErrNotRunning` only when `app.server` is nil, which `fiber.New` never leaves. D3's wording stays, clarified, and task 7.6 pins it. W3 probe: fiber v3.7.0 does not fire `OnRoute` for a mount (`router.go:1516-1519`, `if !route.mount`), so the mount must be found in `app.Stack()`.
- **Suggested action:** apply group 7.

### Fiber mount internals not fail-safe after a consumer's fiber upgrade
- **Status:** closed: fixed in task group 8 (2026-11-18); `fibersrvr/routeflag_internal_test.go` `TestWalkUnreadable`, `TestReadInternals`, `TestSubAppUnreadable`, `TestMountFieldCheck`, `TestModuleVersion`
- **Target:** gin-and-fiber-engines
- **Context:** The sub-app walk (design D8) reads `Route.group`, `Group.app` and `App.stack` by reflection; `TestMountFields` pins them only in srvr's CI. After a consumer's fiber upgrade a renamed field silently dropped mounted routes from the listing and the duplicate check (D8 accepted "degrade to listing nothing"), and a retyped field panicked in `Start`, because only `engine.Configure` recovers. Owner decision 2026-11-18 (rule 7): fail `Start` with `srvr.ErrRouteConfig` naming the field and fiber version.
- **Evidence:** scratch tests in the `fibersrvr` internal package: `TestZZWalkRenamedStackIsVisible` (walk over a struct with `trees [][]*fiber.Route`) listed nothing with no way to report it; `TestZZWalkRetypedStackDoesNotPanic` (struct with `stack []*fiber.Route`) panicked with `reflect: call of reflect.Value.Len on ptr to non-array Value`.
- **Suggested action:** task group 8; engine-support "Fiber mount internals unreadable".

### Fiber route flag fields are still read with a silent fallback
- **Status:** open
- **Target:** unassigned (owner decision needed)
- **Context:** Task group 8 guards `Route.group`, `Group.app` and `App.stack` once a mount is recorded, but mounts are found through the unexported bool `Route.mount` (`recordMounts`, `boolField`), and `declared` reads `Route.use` and `Route.autoHead` the same way. `boolField` returns `false` for a missing or retyped field, so after a consumer's fiber upgrade that renames `mount`, no mount is recorded, the group-8 check never runs and mounted routes drop out of the listing silently; a renamed `use` or `autoHead` lists middleware or implicit `HEAD` routes as declared routes, which can fail the duplicate check falsely. `TestRouteFlagFields` catches this only in srvr's own CI. Found by the /simplify altitude review of task group 8.
- **Evidence:** unconfirmed end to end (a consumer's fiber with renamed fields cannot be built in this repository); by reading `fibersrvr/server.go` `boolField` (`f.IsValid() && f.Kind() == reflect.Bool && f.Bool()`).
- **Suggested action:** owner decides whether the three bool flags are checked (`reflect.TypeFor[fiber.Route]`) whenever any route was recorded, failing `Start` with `srvr.ErrRouteConfig` like task group 8; that would change group 8's "with no mounts, nothing is checked" for apps without sub-apps.

### A fiber sub-app shared between servers races in fiber
- **Status:** open
- **Target:** unassigned
- **Context:** Mounting one sub-app in two servers that configure concurrently makes fiber write the sub-app's mount path without synchronisation (fiber v3.7.0 `mount.go:195`, `mountFields.mountPath`), which `-race` reports. This is fiber's own data race; srvr cannot fix it without copying the sub-app. `fibersrvr` godoc says a sub-app must not be shared between servers that configure concurrently (task 8.3).
- **Evidence:** a scratch race test in `fibersrvr` (two servers mounting the same `fiber.New()` sub-app and starting concurrently) reported `WARNING: DATA RACE` at `mount.go:195`; the test was dropped because the race is upstream and cannot pass under `-race`.
- **Suggested action:** report upstream (fiber: synchronise or document mount path assignment on a shared sub-app); revisit if fiber fixes it.

### A fiber sub-app's own merged duplicate is not reported
- **Status:** open
- **Target:** unassigned
- **Context:** fiber merges a registration identical to the last route of a method stack into that route (v3.7.0 `router.go` `mergesIntoLastLocked`), so `sub.Get("/x", h1)` followed by `sub.Get("/x", h2)` on a mounted sub-app leaves one route in the sub-app's stack. srvr sees parent registrations through `OnRoute`, which fires for the merge, but reads sub-apps from their stacks, so this duplicate inside a sub-app is not reported (one served by `h1` unless it calls `Next`).
- **Evidence:** design D8 failure modes; fiber v3.7.0 `router.go` (`latestID` documents the merge).
- **Suggested action:** read `Route.id` and `Route.latestID` (unexported) in the sub-app walk and report a route whose `latestID` differs from its `id`, pinning both fields in `TestMountFields`; or ask fiber to fire `OnRoute` of the parent for mounted routes.

### Domain-mounted fiber sub-apps are not covered by the route listing tests
- **Status:** open
- **Target:** unassigned
- **Context:** fiber v3.7.0 keeps sub-apps mounted through `Domain(host).Use(prefix, sub)` in `domainAppList`, wrapped by a domain router. The sub-app walk (design D8) was only verified for plain and group mounts; whether a domain mount appears in the parent's stack as a `mount` route, and with which path, is unverified.
- **Evidence:** fiber v3.7.0 `mount.go` `domainMountedApp`, `domain.go`; no test in `fibersrvr/subapp_test.go` uses `Domain`.
- **Suggested action:** add a listing row for a domain mount; if it is missing, list it or document it next to the path-scoped `Use` note.

### One construction helper could own the order of New's final steps
- **Status:** open
- **Target:** unassigned
- **Context:** the default-logger fix (task 7.1) makes every engine call `Core.SetDefaultLogger` as the last step of a successful `New`; a future engine must remember it. A review suggested an `engine` helper that resolves the configuration, runs the engine's build step and applies the default logger only on success, making the order structural.
- **Evidence:** `engine/core.go` `SetDefaultLogger`; `server.go`, `ginsrvr/server.go`, `fibersrvr/server.go` `New`; `TestDefaultLoggerAfterConstruction` in each module guards today's engines.
- **Suggested action:** decide together with the `engine` package stability carry-over before v1.

### Owner decisions 2026-11-18
- **Status:** closed: recorded in the plan
- **Target:** gin-and-fiber-engines
- **Context:** The owner decided both open entries: layering Option A (types in `engine`, aliases and `With...` in `srvr`, engine-only exported `engine.Settings`, engine-specific options as `srvr.Option` in an opaque slot, `engine.ResolveConfig`) and the gin cyclic `c.Error` Option A (documented engine difference only, upstream issue left to the owner). Refreshed: design D1, D2, D10, Risks, Migration Plan; engine-support (two new requirements: engine-specific options in one list, srvr aliases); engine-parity (gin documented difference); proposal What Changes; tasks 2 header, 2.1, 3.13, 3.14, 4.11, 5.4. `openspec validate gin-and-fiber-engines --strict` passes.
- **Evidence:** the owner's message of 2026-11-18 relayed by the coordinator.
- **Suggested action:** none; slice 2 starts.

### Consolidation 2026-11-17 (task group 1)
- **Status:** closed: consolidation done; the open decision that blocked slice 2 was decided 2026-11-18 (see its entry)
- **Target:** gin-and-fiber-engines
- **Context:** 1.1: `main` at `97c88a3`, in sync with `origin/main`; one change merged and archived since planning, `core-server` (`openspec/changes/archive/2026-11-17-core-server`, specs synced to `openspec/specs`: error-responses, route-registration, server-lifecycle, structured-logging, trace-context, triage-error-records). 1.2: every open carry-over addressed to this change or `unassigned` is decided in "Carry-over decisions 2026-11-17" below. 1.3: each `core-server` assumption in proposal.md - Impact holds in the merged code (sentinels in `lifecycle.go` and `hooks.go`; `Register` returns `error` and `Configure` runs in `Start`; the five config structs and their `With<Struct>` entry points; core D5's holder is `request` in `trace.go`; unmatched 404/405 answered through prblm by `answerUnmatched`, so they log `http.request.failed` at `WARN`; event names and keys in `logkeys.go`, `srvr.log.schema` `1`; the `trace_id` enricher; srvr owns prblm's `OnError`; fingerprint inputs method, route and type chain; `TraceFromContext`/`LoggerFromContext`). Released versions are unchanged and still the latest: prblm, ginprblm and fiberprblm `v0.1.0`, gin `v1.12.0`, fiber `v3.7.0` (fasthttp `v1.78.0`). Refreshed: design Context (archived core, the verification guards, adapter facts), D1 (layering pending), new D10 (engines reuse the core's guards), Hot Paths; engine-support new requirement "Consumer faults are contained on every engine" (21 rows covering typed nils, recursively panicking `Error()`, cyclic chains, panicking log handlers and redactors, faulty listeners, hostile route-check errors and re-entrant calls from consumer code) and two re-entrant hook rows; gin-engine nil-handler row; fiber-engine panicking `WithFiberConfig` row (kept only there); proposal Impact and Modified Capabilities comment; tasks 2.2, 2.3, 3.2, 6.2 extended, new tasks 3.15 and 4.15 (the verify tasks are now 3.16 and 4.16), and a blocking note on group 2. `openspec validate gin-and-fiber-engines --strict` passes.
- **Evidence:** `git fetch` then `git log -1 origin/main` (`97c88a3`); `go list -m -versions` for the five modules; module sources read from the cache (`ginprblm@v0.1.0/middleware.go`, `recovery.go`; `fiberprblm@v0.1.0/handler.go`, `recovery.go`; gin `context.go:252-268`); probes below.
- **Suggested action:** owner decides the two open entries below, then slice 2 starts.

### Carry-over decisions 2026-11-17
- **Status:** closed: decided at consolidation
- **Target:** gin-and-fiber-engines
- **Context:** Open entries of `core-server` notes.md (archived; not edited) addressed to this change or `unassigned`, plus this change's own: "A cyclic error chain hangs prblm's resolution" (suggested action targets this change) - adopted: design D10, engine-support consumer-fault rows, tasks 2.3, 3.15, 4.15. "Start's 'Serve returned because the stop closed the listener' branch is covered only by timing" - adopted: task 2.2 covers it deterministically through the fake serve/drain adapter. "Ask prblm for a composable OnError and an exported status-only sentinel" - deferred, stays `unassigned` (gin keeps classifying from `c.Errors`, design D2). Deferred, staying `unassigned`, because they are engine-neutral and this change only inherits whatever the shared lifecycle or pipeline does: "prblm type discovery is not served by srvr", "AfterDrain hooks share one budget", "Shutdown waits up to 5s for a connection that never sent a request" (gin inherits it from `http.Server`; fiber's fasthttp closes idle connections itself), "A panicking consumer error hook is contained silently", "A srvr.problem.note event", "A Start or Shutdown context whose methods panic", "A connection from a WithListener listener whose methods panic crashes the process" (same on fiber, whose fasthttp reads connections on its own goroutines), "Errors and panics from a passed listener's Close are not logged", "Stack capture at error creation", "TLS termination", "HTTP/2 cleartext (h2c)", "Health endpoints". This change's "Unverified assumptions from audit 2" stays open; its item (2) is confirmed by reading the merged code (`group.go` `Mount` adds method `""` with `joinPattern("/static", "/")` = `/static/`) and is pinned by task 6.1.
- **Evidence:** `openspec/changes/archive/2026-11-17-core-server/notes.md`; `group.go` `Mount`, `add`, `joinPattern`.
- **Suggested action:** none.

### Where srvr's public configuration types live once engine exists
- **Status:** closed: owner decision 2026-11-18, Option A (design D1 "Layering - Decision"; engine-support new requirements "Engine-specific options share the option list" and "srvr re-exports the shared types"; proposal What Changes; tasks group 2 header, 2.1, 3.13, 4.11). Engine-specific options given to another engine fail construction with `srvr.ErrConfig` (implementer's reading of rule 7, recorded in D1).
- **Target:** gin-and-fiber-engines
- **Context:** `srvr.WithRouteCheck(func(context.Context, engine.Routes) error)` makes package `srvr` import `engine`, so `engine` cannot import `srvr`. Yet `engine` must resolve the `srvr.Option` values every engine accepts and return `srvr.ErrConfig`, `srvr.ErrRouteConfig`, `srvr.ErrServerStarted` and `srvr.ErrServerClosed` (engine-support "Engines share one configuration model", "Engines share one lifecycle state machine"), and `engine.RequestState` holds the request's `srvr.Trace`. Today `Option` is `func(*settings)` over an unexported struct in `srvr`, so only `srvr` can apply options. The plan names `engine.ResolveConfig` but leaves its input, and where these public types are defined, open. Every slice-2 task depends on it, and it decides the shape of the core's public API and godoc, which AGENTS rule 7 treats as a commitment. Engine-specific options (`ginsrvr.WithEngine`, `fibersrvr.WithFiberConfig`) have the same question: whether they are `srvr.Option` values passed to `ginsrvr.New(opts ...srvr.Option)` or a separate option type.
- **Evidence:** design D1/D8 and tasks 2.1-2.4 as planned; `config.go` (`type Option func(*settings)`); probe `scratchpad/probe-internal` (module `example.com/core/sub` importing `example.com/core/internal/x`, `GOWORK=off go build ./...` -> `BUILD-OK`): a nested module can import the core's `internal/` packages, so AGENTS.md's premise "separate modules cannot import core internal/" does not hold at the toolchain level (it holds as a versioning policy).
- **Suggested action:** Owner picks one. (A, recommended) The types move down: `engine` defines `Config`, `LogConfig`, `AccessLogConfig`, `TraceConfig`, `ServiceConfig`, `Trace`, `ShutdownPhase`, `ErrorHook`, the sentinels and `Option` as `func(*engine.Settings)`; `srvr` re-exports them as aliases (`type Config = engine.Config`, `var ErrConfig = engine.ErrConfig`) and keeps every `With...` function, with its godoc, as a closure over `engine.Settings` fields that are exported but documented as engine-only; `engine.ResolveConfig(opts []Option)` validates. Engine-specific options are `srvr.Option` values stored in an opaque `engine.Settings` slot that only the owning engine reads, so `ginsrvr.New(opts ...srvr.Option)` takes one list. Cost: srvr's struct godoc moves to `engine` behind aliases; `engine.Settings` is visible API (unstable before v1). (B) `srvr` keeps its types and exports one bridge, `srvr.EngineSettings(opts ...Option) (*engine.Resolved, error)`, returning resolved values `engine` defines; the sentinels move to `engine` and are re-exported. Smaller godoc change, but a new public function on `srvr` and two struct families to keep in step. (D) Break the cycle at its source: `Route`/`Routes` (plain data) are defined in `srvr` and `engine` imports `srvr`, so the public types stay where they are; `engine` then needs access to unexported `srvr` state, which means an `internal/` bridge or new exported `srvr` functions, and `engine.RequestState`/`engine.Routes` become aliases of `srvr` types. (C) No public `engine` package: shared code lives in `srvr/internal/...`, which the engine modules import by path. Least public API, but the engine modules then depend on unversioned internals, against design D1.

### gin's own c.Error never returns for a cyclic error chain
- **Status:** closed: owner decision 2026-11-18, Option A documented only, no `ginsrvr.Error` helper (design D2 "Cyclic chain passed to `c.Error`", D10; engine-parity "Documented differences" gin row; tasks 3.14, 5.4). The upstream issue is not filed by this change: the owner files or approves it separately (draft below).
- **Target:** gin-and-fiber-engines
- **Context:** `(*gin.Context).Error` calls `errors.As(err, &parsedError)` on the consumer's error before appending it to `c.Errors` (gin v1.12.0 `context.go:257-258`). For an error whose `Unwrap` returns itself, `errors.As` never returns, so the handler goroutine spins and the request never completes. This happens inside gin, before any srvr stage sees the error, so the `guardChain` precedent (net/http, fiber: a `500` problem) cannot be applied. A panicking `Unwrap` makes `errors.As` panic in the handler, which ginprblm recovery answers as a panic `500` (contained).
- **Evidence:** probe `scratchpad/apicheck/probe_test.go` `TestProbeGinCErrorCyclic` (gin v1.12.0, `ginprblm.Middleware`, `ginprblm.Recovery`, a handler calling `c.Error(&selfErr{})`), `GOWORK=off go test -count=1 -run TestProbeGinCErrorCyclic -v .`: `c.Error(cyclic) did not return within 2s (gin's errors.As loops)`, `FAIL`.
- **Suggested action:** Owner decides. (A, recommended) Engine-parity exception: a gin row in "Documented differences" pinning that the request does not complete, godoc on `ginsrvr`, and an upstream gin issue asking for a bounded `errors.As`. (B) A helper `ginsrvr.Error(c, err)` that guards the chain before calling `c.Error`, documented as the way to report errors, plus (A) for direct `c.Error` calls. (C) Accept without a row (not recommended: AGENTS rule 3 asks for every failure to be specified).
- **Draft upstream issue (for the owner to file in gin-gonic/gin; not filed):**
  > **Title:** `(*Context).Error` never returns for an error whose `Unwrap` chain is cyclic
  >
  > **Version:** gin v1.12.0, Go 1.27.
  >
  > **What happens:** `Context.Error(err)` calls `errors.As(err, &parsedError)` (`context.go:257-258`) before appending to `c.Errors`. `errors.As` walks `Unwrap` without a bound, so for an error whose `Unwrap()` returns itself (or any cycle) the call never returns: the handler goroutine spins at 100% CPU and the request never completes. Middleware after the handler and recovery never run.
  >
  > **Reproduction:** `type selfErr struct{}; func (e *selfErr) Error() string { return "self" }; func (e *selfErr) Unwrap() error { return e }`; a route whose handler calls `c.Error(&selfErr{})`; send one request with `httptest` - it does not return.
  >
  > **Expected:** `c.Error` returns in bounded time (for example a type assertion `err.(*gin.Error)` instead of `errors.As`, or a bounded walk), leaving the cyclic error for the application's error handling.
  >
  > **Why it matters:** a buggy error type in application code turns into a stuck goroutine per request instead of a 500.

### Adapter panic wrappers would log panics as returned errors
- **Status:** closed: confirmed for gin and fiber, fixed in task 2.3
- **Target:** gin-and-fiber-engines
- **Context:** The core classifies a failure as a panic with a type assertion on `*prblm.PanicError` (`access.go` `failure.level`, `triage.go` `triageAttrs`). `ginprblm.Recovery` and `fiberprblm.Recovery` hand prblm their own unexported wrappers (`panickedError`, `recoveredError`), each unwrapping to `*prblm.PanicError`, so the core's checks would see a returned error: `ERROR` level by status still, but `error.type` the wrapper's type and no stack. The shared builders read panics with `errors.As`.
- **Evidence:** gin confirmed 2026-11-18: `ginsrvr/panic_test.go` `TestGinPanic/boom`, run with `newFailure` temporarily reverted to the pre-2.3 `err.(*prblm.PanicError)` assertion, `GOWORK=off go test -count=1 -run 'TestGinPanic/boom' ./...` in `ginsrvr`: `--- FAIL: TestGinPanic/boom ... record map[... error.chain:[ginprblm.panickedError *prblm.PanicError] ...]` (logged as a returned error, not `error.kind` `panic`); with the fix (`engine/triage.go` `panicOf`, a bounded walk) it passes. Engine-level RED: `engine/panicwrap_internal_test.go` `TestFailure_PanicThroughWrapper` failed with `level = INFO, want ERROR` before the fix. fiber confirmed 2026-11-18 the same way: `fibersrvr/panic_test.go` `TestFiberPanic/boom` with the pre-2.3 assertion: `--- FAIL ... record map[... error.chain:[fiberprblm.recoveredError *prblm.PanicError] ... exception.message:Internal Server Error ...]` (no `error.kind`), passing with the fix.
- **Suggested action:** none.


### Gin handler signature contradicts config.yaml
- **Status:** closed: user accepted plain gin.HandlerFunc with c.Error (planning, 2026-11-17); config.yaml updated
- **Target:** gin-and-fiber-engines
- **Context:** Specs keep plain gin.HandlerFunc reporting errors through c.Error (matches the user's original sketch); openspec/config.yaml says handlers return error on every engine.
- **Evidence:** unverified: planning-time inconsistency found when the five changes were drafted in parallel
- **Suggested action:** Apply the user's decision on handler signature to specs and config.yaml before slice work.

### Public srvr/engine package is new core API
- **Status:** closed: user accepted public srvr/engine, unstable before v1 (planning, 2026-11-17); integration of later changes tracked in their own notes.md
- **Target:** gin-and-fiber-engines
- **Context:** Separate modules cannot import core internal/, so shared pipeline pieces become public srvr/engine. Later changes (observer, security layer, debug buffer) were planned without knowing this package.
- **Evidence:** unverified: planning-time inconsistency found when the five changes were drafted in parallel
- **Suggested action:** Confirm the package name and surface; make sure otel-observability, scrty-integration and triage-debug-buffer plug into it.

### Gin prints [GIN-debug] lines to stdout in its default mode
- **Status:** adopted by gin-and-fiber-engines (owner decision D19, 2026-11-17: one `server.config.warning` record with `srvr.warning.code` `gin_debug_mode` at construction; gin's globals untouched; gin-engine "Gin debug mode is reported", design D2/D9, task 3.10)
- **Target:** gin-and-fiber-engines
- **Context:** gin's mode is process-global (gin v1.12.0 `mode.go:48`, `debug.go:22-42`); there is no per-engine release mode, and `DebugPrintRouteFunc` is a package variable. Since srvr never calls `gin.SetMode` (owner decision), a zero-config ginsrvr in debug mode prints gin's warning and one line per route to stdout, mixed into srvr's JSON log stream. The audit removed the proposal/design claims of "release mode on srvr's engine".
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/gin/main_test.go` `TestDebugOutputAndDefaults`, `go test -count=1 -v -run TestDebugOutputAndDefaults .`: `mode: debug`, `[GIN-debug] [WARNING] Running in "debug" mode...`, `[GIN-debug] GET /x --> ... (1 handlers)`
- **Suggested action:** Owner decides: accept and document `GIN_MODE=release`; or log one `WARN` record at construction when `gin.IsDebugging()`; or (global, rejected so far) set `gin.DebugPrintRouteFunc`/`gin.DefaultWriter`.

### Fiber header limit and header timeout differ from MaxHeaderBytes and ReadHeaderTimeout
- **Status:** adopted by gin-and-fiber-engines (owner decision D20, 2026-11-17: `ReadBufferSize = min(MaxHeaderBytes, 65536)`, `fiber_header_limit_capped` warning for a consumer-set value above the cap; `ReadHeaderTimeout` and the cap are engine-parity exceptions; fiber-engine "Fiber header limit is derived and capped", task 4.12)
- **Target:** gin-and-fiber-engines
- **Context:** fasthttp has no header-read timeout (no such field in fasthttp v1.78.0 `server.go`), and a request header must fit `ReadBufferSize` (fiber default 8192, `app.go:662`), answered with 431. srvr's defaults (`ReadHeaderTimeout` 10s, `MaxHeaderBytes` 1 MiB) cannot map one to one; `ReadBufferSize` = 1 MiB would allocate that per connection. The proposal now flags this as pending.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/fib/real_test.go` `TestReal`, `go test -count=1 -v -run TestReal .`: `9000B header: 418 "EH:Request Header Fields Too Large ..."` with default config
- **Suggested action:** Owner decides per setting: map `MaxHeaderBytes` to `ReadBufferSize` (memory cost), cap it, or list both as engine-parity exceptions with spec rows; same for `ReadHeaderTimeout` (fasthttp `ReadTimeout` covers header and body).

### Fiber accepts duplicate and conflicting routes silently
- **Status:** adopted by gin-and-fiber-engines (owner decision D21, 2026-11-17: detect through `app.GetRoutes(true)` with normalised parameter names and fail `Start` with `srvr.ErrRouteConfig`; fiber-engine registration rows, design D3/D8, tasks 4.3 and 6.2)
- **Target:** gin-and-fiber-engines
- **Context:** core-server fails `Start` with `srvr.ErrRouteConfig` for conflicting patterns and gin panics on them, but fiber v3.7.0 registers `GET /users/:id`, `GET /users/:name` and a second `GET /users/:id` without error; the first wins. fiber-engine has no row for this.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/fib/main_test.go` `TestDupAndNil`, `go test -count=1 -v -run TestDupAndNil .`: `dup served body: a`, `dup panic: <nil>`
- **Suggested action:** Owner decides: detect duplicates after the configure phase from `app.GetRoutes()` (normalising parameter names) and return `srvr.ErrRouteConfig`, or add an engine-parity exception row.

### ginprblm reports status-only aborts to OnError with an unexported error
- **Status:** closed: tracked in core-server notes.md (one prblm issue, D26)
- **Target:** unassigned
- **Context:** `ginprblm.Middleware` turns `c.AbortWithStatus(401)` into a problem and calls the `OnError` hook with `errStatusOnly` (unexported, ginprblm `middleware.go`). srvr cannot match it with `errors.Is`, so classifying failed requests from hook calls would log `http.request.failed` where gin-engine expects `http.request.completed`. design D2 now classifies from `c.Errors` and recovered panics instead.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/gin/hook_test.go` `TestHookOnStatusOnly`, `go test -count=1 -v -run TestHookOnStatusOnly .`: `OnError called: err="ginprblm: response aborted with an error status and no error" type=*prblm.statusError`
- **Suggested action:** Ask prblm to export the sentinel (for example `ginprblm.ErrStatusOnly`) so the hook can tell it apart; until then keep the `c.Errors`-based classification and its test. Owner decision D26 (2026-11-17): one prblm issue covers this and the composable `OnError` request; core-server's notes.md holds the combined entry. This entry stays open until prblm ships the sentinel.

### Gin Allow order and trailing-slash redirects differ from net/http
- **Status:** adopted by gin-and-fiber-engines (owner decision D22, 2026-11-17: `Allow` sorted, `RedirectTrailingSlash` off and re-asserted after `WithEngine`; gin-engine unmatched and hook rows, design D2, tasks 3.5 and 3.12)
- **Target:** gin-and-fiber-engines
- **Context:** gin builds `Allow` in the order method trees were created, not sorted (`gin.go:738-753`), and `gin.New()` has `RedirectTrailingSlash` on, answering `GET /users/` with a `301` and an HTML body instead of a problem. Neither is pinned by engine-parity.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/gin/main_test.go` `TestUnmatched`, `go test -count=1 -v -run TestUnmatched .`: `DELETE /users -> 405 ... allow="POST, GET"` (POST registered first), `GET /users/ -> 301 ct="text/html; charset=utf-8" loc="/users"`
- **Suggested action:** Owner decides: sort `Allow` and disable `RedirectTrailingSlash` (re-asserted like the other safety settings), or add both as engine-parity exception rows.

### Fiber answers unknown methods before any middleware
- **Status:** adopted by gin-and-fiber-engines (owner decision D23, 2026-11-17: accepted as an engine-parity exception, no `RequestMethods` override; engine-parity row, design D3, task 5.4)
- **Target:** gin-and-fiber-engines
- **Context:** A method fiber does not know (for example `FOO`) gets a plain-text `501` from `defaultRequestHandler` (`router.go:1016-1020`) before middleware runs: no problem, no access record, no trace. Added as an engine-parity exception row; whether to accept it is a decision.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/fib/real_test.go` `TestReal`: `FOO method: 501 ct="text/plain; charset=utf-8" "Not Implemented" mwRan=0`
- **Suggested action:** Owner accepts the exception, or srvr sets `fiber.Config.RequestMethods` so unknown methods reach the chain and get a 405/501 problem.

### Adopted: otel-observability carry-over on Fiber route and status after Next
- **Status:** adopted by gin-and-fiber-engines
- **Target:** gin-and-fiber-engines
- **Context:** The open entry "Fiber unmatched route reads as "/" from middleware" in `openspec/changes/otel-observability/notes.md` targets this change. design D4 now reads the route only when `c.Matched()` and has the access middleware call the error handler; fiber-engine route rows omit `http.route` when unmatched. That entry's status lives in another change's file and was not edited here.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/fib/main_test.go` `TestRouteAfterNext`: `GET /nope -> resp=404 | mw: route="/" matched=false statusInMW=200`, `DELETE /users/42 -> resp=405 | mw: route="/users" matched=false statusInMW=200`
- **Suggested action:** Task 1.2 marks the otel-observability entry `adopted by gin-and-fiber-engines`.

### Planning audit 2026-11-17 decisions
- **Status:** closed: applied to proposal, specs, design and tasks during planning (2026-11-17)
- **Target:** gin-and-fiber-engines
- **Context:** The cross-change audit and the owner's decisions D8, D13 and D19-D23 changed this change. Applied: unmatched 404/405 logged as `http.request.failed` at `WARN` on every engine with parity rows (D8); config-struct entry points `srvr.WithConfig`, `WithLogConfig`, `WithAccessLogConfig`, `WithServiceConfig` applied in argument order (D13); D19-D23 as recorded on their entries. Audit gaps closed here because scrty-integration depends on them (Gall's Law): gin body limit through `http.MaxBytesReader` (design D2 no longer claims gin inherits it from `http.Server`); route listing `engine.Routes` with configurer attribution and `srvr.WithRouteCheck` (design D8, slice 6). One per-request holder `engine.RequestState` / `engine.StateFrom(ctx)` (design D7); this change adds route, matched, status, failure and trace context, and otel-observability and scrty-integration add their fields (trace source, client address, principal) to the same struct. New `server.config.warning` event (design D9), additive under the `srvr.log.schema` policy. Parity row corrected: gin, not net/http, is the engine whose `Allow` lacks `HEAD`. Consolidation task 1.5 added (D25); module setup was already the first task of each slice.
- **Evidence:** unverified: planning decisions; earlier probes are cited on the individual entries
- **Suggested action:** none; consolidation 1.3 re-checks these against merged core-server code (names of the config entry points and the request-state fields).

### Planning audit 2 2026-11-17: applied fixes
- **Status:** closed: applied during planning
- **Target:** gin-and-fiber-engines
- **Context:** The second fresh audit and the owner's A1-A4 defaults changed this change. Applied: option rows use `WithLogLevel(slog.LevelDebug)` and `srvr.WithTraceConfig` is listed (M1); new requirement "Shutdown hooks run on every engine's stop paths" with gin and fiber rows, the A4 `Start`-failure rule and tasks 2.2, 3.3, 4.2, 6.2 (M5); fiber protocol rejections (`408`, `413`, `431`) recorded from the pre-set status with a `problem` group, design D4/D7 reworded, task 4.13 over a real listener (M8); route listing lists any-method routes once with `Method` `""` (net/http `Mount` and method-less patterns, gin `Any`, fiber `All`), gin `Static` per method, path-scoped fiber `Use` excluded (M9); `WithRouteCheck(func(context.Context, engine.Routes) error)` with `Start`'s context and a copy per check, mutating and blocking rows (L7); gin aborted-request record and fiber `http.ErrAbortHandler` parity exception (A3); tasks 2.6 and 5.6 (L1); ginprblm entry closed (L5).
- **Evidence:** fiber v3.7.0 source in the module cache: `app.go:1793-1877` (`serverErrorHandler` seeds the status, replays the `Use` chain with route handlers skipped, then calls `app.ErrorHandler`), `router.go:763` (the replayed chain returns `false, nil`); prblm source: `ginprblm/recovery.go:58` passes `http.ErrAbortHandler` on, `fiberprblm/recovery.go:23` recovers it like any panic; gin v1.12.0 `routergroup.go:18-23` (`anyMethods`). No probes were run in this pass.
- **Suggested action:** none beyond the RED tests named in the tasks.

### Unverified assumptions from audit 2
- **Status:** closed: all three confirmed by running tests (2026-11-18)
- **Target:** gin-and-fiber-engines
- **Context:** Three rows rest on source reading, not a running test: (1) a header read stalled past fasthttp's `ReadTimeout` reaches `serverErrorHandler` as a timeout and is answered `408` (Protocol rejection record row 3); (2) net/http `Mount("/")` in group `/static` registers the method-less pattern `/static/`, which depends on how core-server implements `Mount`; (3) fiber `All` registers exactly `RequestMethods`, so the any-method collapse recognises it.
- **Evidence:** (1) `fibersrvr/protocol_test.go` `TestFiberProtocolRejection/stalled_headers` (real listener, `ReadTimeout` 100ms, headers left unfinished) passes: a `408` problem titled `Request Timeout` and one failed record. (2) `routelisting_test.go` `TestRouteListing/Mount_in_a_group` lists `{"" /static/}`. (3) `fibersrvr/routelisting_test.go` `TestRouteListing/All` lists `{"" /x}` through `engine.CollapseMethods` over `app.Config().RequestMethods`.
- **Suggested action:** Tasks 4.13 and 6.1 write these rows first as RED tests; if one fails for a reason other than the missing feature, correct the row and record the actual behaviour here. Consolidation 1.3 re-checks (2) against merged core code.

### Gin unmatched requests were already failed before task 3.8's GREEN
- **Status:** closed: plan assumption corrected during apply (2026-11-18)
- **Target:** gin-and-fiber-engines
- **Context:** Task 3.8 expected its RED to fail on the unmatched rows because `c.Errors` is empty. It passed at once: `ginprblm.Middleware` reports the status-only 404/405 answer to srvr's `OnError` hook, which records the failure. The GREEN step needed instead is the opposite case: ginprblm reports `c.AbortWithStatus(401)` the same way, so `ginsrvr` drops that failure for a matched route without `c.Errors` or panic (`ginsrvr/server.go` `classify`, task 3.11, gin-engine "Consumer middleware behaviour" row `c.AbortWithStatus(401)` -> `http.request.completed`).
- **Evidence:** `ginsrvr/access_test.go` `TestGinAccessRoute` and `state_internal_test.go` `TestGinRequestState` green before any classification code; `TestGinMiddleware/AbortWithStatus_401` RED (`http.request.failed`) until `classify` was added.
- **Suggested action:** none.

### Engine modules build against the in-repository core until it is tagged
- **Status:** open
- **Target:** gin-and-fiber-engines (release step in "Workflow follow-up")
- **Context:** `ginsrvr/go.mod` (and `fibersrvr/go.mod`) require `github.com/kartaladev/srvr v0.0.0-00010101000000-000000000000` with `replace github.com/kartaladev/srvr => ../`, because package `engine` exists only in the unreleased core. `GOWORK=off` verification therefore uses the local core. A replace directive is ignored by consumers, so a released engine module must require a tagged core.
- **Evidence:** unverified: release procedure, not automatable before tagging.
- **Suggested action:** when tagging, release core `v0.2.0` first, then set the engine modules' `require` to it, drop the `replace` lines, run `GOWORK=off go mod tidy` and the verification, and tag `ginsrvr/v0.1.0` and `fibersrvr/v0.1.0`.

### Engine modules duplicate the core's test helpers
- **Status:** open
- **Target:** unassigned
- **Context:** `ginsrvr/helpers_test.go` repeats about 150 lines of the core's `helpers_test.go` and `logging_test.go` (JSON log buffer, request/response helper with problem parsing, start and readiness helpers), and fibersrvr and `internal/paritytest` will need the same. Separate modules cannot share `_test.go` files, so removing the copies needs a public test-support package (for example `srvr/engine/enginetest`), which is new API and a design decision outside this change's plan. Found by the /simplify reuse review of task group 3.
- **Evidence:** unverified: duplication observed by reading the files, not a defect.
- **Suggested action:** decide whether a test-support package is worth its API cost before v1; until then keep the copies in step.

### Fiber RED assumptions that did not hold
- **Status:** closed: plan assumptions corrected during apply (2026-11-18)
- **Target:** gin-and-fiber-engines
- **Context:** Task 4.5 expected `Allow` to be missing on fiber's 405; fiber v3.7.0 sets it itself, sorted and with `HEAD` (`GET, HEAD, POST`), so no code was added. Task 4.3: fiber does not list an identical repeated registration in `GetRoutes` (it merges it into the earlier route, `router.go` `addRoute` `mergesIntoLastLocked`), so duplicate detection records registrations through the app's `OnRoute` hook, which fires for merged ones too; Use, mount and automatic HEAD registrations are told apart by fiber's unexported route flags read through reflection (`fibersrvr/server.go` `routeFlag`, pinned by the "Use twice and Get at the root" registration row). Task 4.7: route paths and method strings are not taken from pooled buffers in fiber v3.7.0, so the context-reuse test passes even without srvr's copies; it stays as a guard. Fiber's `ShutdownWithContext` on an app that never served returns `fiber.ErrNotRunning`, not `nil` as design D3 says; srvr never calls it then, because the shared lifecycle closes a never-started server without the runner.
- **Evidence:** `fibersrvr/unmatched_test.go` `TestFiberUnmatched` green without code; `TestFiberRegister/twice` RED with a `GetRoutes` diff, green with the hook; fiber v3.7.0 `app.go:1432-1434` (`ErrNotRunning`).
- **Suggested action:** none.

### RequestState.Status is not live on fiber
- **Status:** open
- **Target:** otel-observability
- **Context:** `engine.RequestState.Status()` reads net/http's response recorder live, but on fiber the status is only stored when `WriteAccess` runs (`engine/access.go` `WriteAccess`), so a stage reading it during the request sees 200. Nothing reads it mid-request in this change; otel-observability's observer stage will. Found by the /simplify altitude review of task group 4.
- **Evidence:** unverified: no reader exists yet; the state tests read it after the access record.
- **Suggested action:** when a mid-request reader arrives, let the fiber stage give the state a live status source (for example a `func() int` over `c.Response().StatusCode()`), with a RED test from that reader.

### Fiber rejection records build their problem apart from the client's
- **Status:** open
- **Target:** unassigned
- **Context:** For 408, 413 and 431 the access record's `problem` group comes from `engine.Core.RecordRejection` (`prblm.New(status)` and `http.StatusText`), while the client's problem is rendered later by fiberprblm with the consumer's `WithProblemOptions`. With a mapper or namespace that changes these statuses' type or title, the record and the response can differ. Design D4 specifies the status-and-standard-title record. Found by the /simplify altitude review of task group 4.
- **Evidence:** unverified: no consumer option in this change alters these statuses; a RED test would set a namespace or mapper and compare `problem.type`.
- **Suggested action:** resolve the rejection problem through the server's problem handler (`Core.Problems().Resolve`) so the record matches the response.

### Fiber access stage buffered streamed bodies (fixed)
- **Status:** closed: fixed in task group 4 (2026-11-18)
- **Target:** gin-and-fiber-engines
- **Context:** The fiber access stage measured the body with `c.Response().Body()`, which in fasthttp v1.78.0 reads a body stream whole into memory before the response is written: every `SendStream`/`SendFile` response was buffered, and an endless stream (server-sent events) never reached the client. Found by the /simplify efficiency review. A streamed body now reports its declared `Content-Length` (0 when unknown), never read by srvr.
- **Evidence:** `fibersrvr/stream_test.go` `TestFiberStreamNotBuffered`, `GOWORK=off go test -count=1 -run TestFiberStreamNotBuffered ./...` in `fibersrvr` before the fix: `--- FAIL ... no response while the stream is open: ... context deadline exceeded`; passes after.
- **Suggested action:** none; `http.response.body.size` of a streamed fiber response is its declared length, a difference from net/http's bytes written (to pin with task 5.4's exception rows if parity needs it).

### Public engine package stability before v1
- **Status:** open
- **Target:** unassigned
- **Context:** `github.com/kartaladev/srvr/engine` is public because the engine modules are separate Go modules; its godoc marks it as for srvr's own engines and unstable before v1. It now holds the shared types srvr aliases (`Config`, `Option`, `Settings`, sentinels), `Core`, `Lifecycle[C]`, `RequestState`, `BeginRequest`/`WriteAccess`/`AccessInfo`, `RecordRejection`, `Warn` with `WarningCode`, `RouteError`, `Configure`, `ForEngine`/`EngineOptions`, `FuncLocation`/`FuncPointer`, `GuardChain`, `ValueText`. Every symbol is a compatibility decision once v1 is tagged.
- **Evidence:** unverified: API review item, not a defect.
- **Suggested action:** before v1, review the surface (unexport what only one engine needs, or move it under `internal/` with the engine modules pinned to matching core versions) and decide whether `Settings`' exported fields stay.

### Metrics and tracing hooks needed in both engines
- **Status:** open
- **Target:** otel-observability
- **Context:** The observer stage (design pipeline: trace, access, observer, recovery) must sit at the same point on every engine: net/http between `LogAccess` and the body limit, gin between `requestStage` and `ginprblm.Middleware` (or around the gin engine as an `http.Handler` like the access stage), fiber inside `fibersrvr`'s `requestStage` after the chain returns and the error was rendered (fiber's status is only final there, design D4). Fiber's request context is `c.Context()` (D5); `RequestState.Status()` is not live on fiber during the request (see "RequestState.Status is not live on fiber").
- **Evidence:** unverified: planning carry-over from task 5.5.
- **Suggested action:** add the observer as an `engine` stage hook that each engine calls at those points, with a parity row per engine.

### scrty middleware placement per engine
- **Status:** open
- **Target:** scrty-integration
- **Context:** scrty's chain must run outside the router and after srvr's trace and access stages. net/http: wrap the router inside `Server.stages`. gin: srvr's stages wrap the gin engine as an `http.Handler` (`ginsrvr/server.go` `handler`), so the httpsec chain can go between `LimitBody` and the engine, or ginsec as the first consumer-independent global middleware after `requestStage`. fiber: fibersec after `requestStage` and `fiberprblm.Recovery()` in `app.Use`, before routes; fasthttp rejections (408, 413, 431) never reach it. Route listings for scrty's collision check come from task group 6.
- **Evidence:** unverified: planning carry-over from task 5.5.
- **Suggested action:** expose one insertion point per engine (an `engine`-level middleware slot) rather than per-engine options, with parity rows.

### Fiber duplicate detection runs before the route checks, not as one
- **Status:** closed: implementation choice during apply (2026-11-18)
- **Target:** gin-and-fiber-engines
- **Context:** Task 6.2 planned to turn fiber's duplicate-route detection into the first internal route check (Substitute Algorithm). It stays a direct call in `fibersrvr`'s configure phase, run before `Core.CheckRoutes`: it needs the registration records fiber's `OnRoute` hook keeps (merged duplicates never reach the listing), which a check receiving `engine.Routes` cannot see. The observable order is the one D3/D8 specify: duplicates fail `Start` before any consumer check runs.
- **Evidence:** `fibersrvr/register_test.go` duplicate rows and `routelisting_test.go` `TestRouteCheck` pass.
- **Suggested action:** none.
