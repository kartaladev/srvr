# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (2026-11-23)
- **Status:** closed: task group 1 done
- **Target:** container-lifecycle
- **Context:** 1.1: `origin/main` at `efe3573` (merge of PR #32, this change's plan); nothing merged or archived since planning besides this plan itself: `pluggable-error-renderer` archived (`2026-11-22`), v0.4.0 released, `scrty-integration` active (0/45, PR #31). Working branch `feat/container-lifecycle` from `efe3573`. 1.2: archived and `scrty-integration` notes read; no open entry is addressed to `container-lifecycle`. Unassigned entries touching the lifecycle were decided: core-server "Health endpoints" deferred (this change adds a Go `HealthCheck` method only, an HTTP endpoint stays out of scope, proposal "Left out"); core-server "Start's 'Serve returned because the stop closed the listener' branch is covered only by timing" and "A Start or Shutdown context whose methods panic leaves the server unable to stop" deferred, unchanged by this change (only `HealthCheck` is specified against a panicking context); every other unassigned entry is unrelated to the lifecycle and stays unassigned. Entry for `scrty-integration` added below. 1.3: design Context rechecked against the code: `Lifecycle.Start` (`internal/engine/lifecycle.go:139`), the waiting `select` and stop (`:185-203`), `serve`/`serveOn` (`:332`, `:363`), `stop`/`stopRun` (`:395`, `:412`), `Shutdown` (`:429`), `joinServe` (`:377`), `guardedListener` (`:509`), the five state constants, sentinels in `internal/errchain/errchain.go:362-375` aliased in `types.go`; all match. MODIFIED blocks re-diffed against `openspec/specs/`: unchanged since planning. "Proposed defaults to confirm at review" kept as planned. `openspec validate container-lifecycle --strict` passes. Nothing in proposal, specs, design or tasks needed refreshing.
- **Evidence:** `git log origin/main --oneline -1`: `efe3573`; `openspec validate container-lifecycle --strict`: `Change 'container-lifecycle' is valid`.
- **Suggested action:** none.

### scrty-integration must move off the blocking Start
- **Status:** open
- **Target:** scrty-integration
- **Context:** `scrty-integration`'s specs, design and tasks were written against v0.4.0's blocking `Start` (shutdown-hook stop paths "the Start context is cancelled", examples `return srv.Start(ctx)`). After this change `Start` returns once serving and the blocking contract is `Run`.
- **Evidence:** unverified: planning documents, not code.
- **Suggested action:** In its consolidation (task group 1), replace the blocking-`Start` stop paths and examples with `Run` or `Start`+`Wait`, and add a `Run`-context row to its hook scenarios.

### Planning decisions (2026-11-23, owner)
- **Status:** adopted
- **Target:** container-lifecycle
- **Context:** Readiness for running srvr under doger (kartaladev/doger) means conformance only: neither imports the other. Owner chose shape 4 over a `Managed()` wrapper or a background-start option: `Start` returns once serving (breaking), plus `Run`, `Wait`, `HealthCheck`. Change placed after `pluggable-error-renderer` and before `scrty-integration`; release as v0.5.0 in this change.
- **Evidence:** doger `root_start.go:740-790` waits for every starter's `Start` to return; srvr `server.go:76-98` documents `Start` serving until stopped.
- **Suggested action:** none; recorded for consolidation.

### Proposed defaults to confirm at review
- **Status:** closed: confirmed unchanged at consolidation (task 1.3, 2026-11-23)
- **Target:** container-lifecycle
- **Context:** Chosen by default during planning, revisitable before slice 2: `Ready()` stays; `HealthCheck` returns `ErrServerNotStarted` while starting; `Wait` after a failed `Start` returns `nil` (design D4); no `RegisterAll` helper.
- **Evidence:** unverified: planning-time defaults
- **Suggested action:** Confirm or change in task 1.3.

### Triage with doger v0.3.0 (2026-11-23)
- **Status:** adopted (findings folded into proposal, specs, design and tasks on 2026-11-23)
- **Target:** container-lifecycle
- **Context:** Adversarial audit of this change's documents, then each claim tested directly against the latest doger release (v0.3.0, from the module proxy) with srvr `main` at `a833539` (v0.4.0 code), in a scratch module outside the repository (`GOWORK=off`, `replace` to the local srvr checkout).
- **Evidence:** `GOWORK=off go test -count=1 -v ./...` in the scratch module:
  - T1 `TestT1_BlockingStartHangsDogerStart`: FAIL `RootScope.Start did not return within 2s after the server was ready ... blocking Start hangs the container`: confirms the motivation.
  - T2 `TestT2_DogerCancelsStartContextAfterStart` (a starter that runs `srv.Start(ctx)` in a goroutine and returns at `Ready`): FAIL `GET /ping ... connect: connection refused`: doger cancels the `Start(ctx)` context when `RootScope.Start` returns; confirms design D2 is required.
  - T3 `TestT3_ServeReturnsWithoutStop` (listener whose `Accept` returns `net.ErrClosed` while open): net/http PASS (`Start` returned `use of closed network connection`), gin PASS (same), fiber FAIL `Start returned <nil> ... fiber: serving ended with no error and no stop requested`: confirms audit item 1 (design D1 must stop on a clean return) and a pre-existing silent stop on fiber.
  - P1 `TestP1_ShutdownDeadlineReachesService`: PASS: doger passes the `Shutdown` context deadline to `Shutdown(ctx) error`.
  - P2 `TestP2_HealthCheckDetectedByShape`: PASS: `HealthCheck(ctx) error` detected by shape; error text returned verbatim in the health map.
  - P3 `TestP3_StartDeadlineVsIgnoringStarter`: FAIL `StartWithContext returned doger: start aborted: context deadline exceeded after 1.504551542s` with a `100ms` deadline: a starter ignoring its context extends the deadline (documented doger behaviour, not a defect).
  - Unconfirmed, dropped: audit item 7 (client exposure of health text): doger's web UI shows only "Implements Healthchecker" (`doger/http/templates.go:126`), never the error text.
  - Audit items 2, 3, 4, 5, 6, 8, 10, 11 are document defects (untestable rows, a RED step allowed to pass, missing rows, a duplicate guard, missing baseline and release precondition), fixed in the documents; item 8 verified by `internal/paritytest/layout_test.go:163` (`TestCoreDependencies`). Item 9 (`Wait` from a hook deadlocks) concerns code that does not exist yet: hypothesis, kept as documented misuse because detection needs goroutine identity.
- **Suggested action:** T3 moves into `fibersrvr` as the RED test of task 2.5. Rerun T1 and T2 in task 4.4; both must pass after slice 2.

### Fiber treats a listener failure as a normal stop
- **Status:** adopted by container-lifecycle (task 2.5)
- **Target:** container-lifecycle
- **Context:** On v0.4.0, when a `WithListener` listener's `Accept` returns `net.ErrClosed` while nobody stopped the server, fiber's serve loop returns `nil`: `Start` returns `nil` and no `server.failed` is written, unlike net/http and gin.
- **Evidence:** triage T3, fiber row (above).
- **Suggested action:** Report it as "serving ended without a stop" (`srvr.ErrServerClosed`) through the supervisor, design D1.

### Suggestions for doger (outside srvr)
- **Status:** open (not srvr work; for the doger owner)
- **Target:** unassigned (doger repository)
- **Context:** Found while triaging; no doger defect was proven, behaviour matches its docs.
- **Evidence:** T1 (a starter that never returns hangs `RootScope.Start` with no diagnostic), P3 (a `100ms` start deadline took `1.5s` because the starter ignored its context).
- **Suggested action:** (1) log a warning naming a starter still running after a configurable duration, so a blocking starter is diagnosable; (2) optionally, an `InjectorOpts` start-deadline that returns `ErrStartTimeout` naming the stuck service instead of waiting (the goroutine stays, documented as abandoned, as srvr does for hooks); (3) document in `Starter` godoc that servers must return once listening, citing HTTP servers as the common trap.

### Baseline before slice 2 (task 1.6, 2026-11-23)
- **Status:** closed: recorded
- **Target:** container-lifecycle
- **Context:** `make test` (every module, `GOWORK=off`, `go test -race ./...`) on `feat/container-lifecycle` after the consolidation commit, before any code change.
- **Evidence:** exit 0; `ok` for core, `internal/benchgate`, `internal/engine`, `internal/errchain`, `internal/static`, `internal/tracectx`, `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup`, `internal/paritytest`, `internal/paritytest/gincycle`.
- **Suggested action:** none; compare against it in task 2.8.

### Slice 2 apply notes (2026-11-23)
- **Status:** closed: recorded
- **Target:** container-lifecycle
- **Context:** (1) Task 2.9's verify command `-run TestLeaks` matches no test; the leak rows live in `TestLifecycle_Goroutines` (core, `ginsrvr`, `fibersrvr`) and the new `TestLifecycle_GoroutinesWhileRunning` (core), run with `-run 'TestLifecycle_Goroutines|TestServeGoroutine|TestLeakCheck'`. (2) Task 2.5's fiber T3 test is the row "returns net.ErrClosed although the listener was not closed" of `fibersrvr`'s `TestServeFailure_AfterStart`, not a separate `TestServeEndsWithoutStop`; it passed on its first run because the shared supervisor was already in place, so its RED evidence is triage T3 on v0.4.0. (3) Rows of 2.4 (Wait misuse except "never started") and 2.6 (Run rows of hooks and Start twice) passed on first run: 2.3-2.5 had already wired them. The `Run` permanent-Accept-error row of "Wait after Start" is asserted in `TestServeFailure_AfterStart`. (4) `TestStart_Stop` became `TestRun_StopByContext`; `TestStart_ServeFails` was removed, superseded by `TestServeFailure_AfterStart`/`TestServeFailure_DuringRun`. (5) `/simplify` applied: dead `run.servedErr` and its test removed; `stop` and `stopFailed` merged into `stop(drainCtx, served)`; supervision runs at the end of `serveOn` instead of a second goroutine.
- **Evidence:** `make check` exit 0 after `/simplify`.
- **Suggested action:** none.

### /simplify findings deferred from slice 2
- **Status:** open; item (a) adopted by container-lifecycle task 5.5
- **Target:** unassigned
- **Context:** Skipped as outside the slice or behaviour-changing: (a) `ginsrvr`/`fibersrvr` `container_test.go` are near copies and the three `faultListener`s overlap `panicListener`/`faultyListener`; a shared parity driver (task 4.3 territory) would remove them. (b) Engine tests add `within`/`containerWait` beside `receive`. (c) `ErrServingEnded` could live in `fibersrvr`'s runner instead of the shared lifecycle; kept shared per design D1, as the contract "Serve returned without a stop" is engine-agnostic. (d) the supervisor keeps `context.WithoutCancel(startCtx)` for the server's lifetime by design D2 (service values in records). (e) pre-existing `wait(ctx, first bool)` control flag.
- **Evidence:** unverified: review findings, no behaviour defect.
- **Suggested action:** fold (a) and (b) into task 4.3's parity work; reconsider (c) if another engine needs it.

### Slice 3 apply notes (2026-11-23)
- **Status:** closed: recorded
- **Target:** container-lifecycle
- **Context:** (1) Task 3.3's RED could not fail to compile as planned: 3.1 had already added `HealthCheck` everywhere. The container deny check was proven instead by temporarily adding `gin-gonic/gin` to `containerModules` (FAIL `module ginsrvr depends on container module gin-gonic/gin`), then reverted. (2) The dependency rows live in `TestContainerDependencies` (gin, fiber); the net/http row is `TestCoreDependencies`' allowlist, so it is not checked twice. (3) `/simplify` applied: `health` returns bare `ErrServerClosed` with no serve failure; conformance driver bounded by its context only; `healthWithin` uses one goroutine. Deferred, added to "/simplify findings deferred from slice 2" (a): gin/fiber `TestHealthCheck` copies with a `switch` on row names, and sharing one `go list -deps` result across `internal/paritytest` tests.
- **Evidence:** `make check` exit 0.
- **Suggested action:** none.

### doger end-to-end check (task 4.4, 2026-11-23)
- **Status:** closed: passed
- **Target:** container-lifecycle
- **Context:** The original triage module was not kept, so T1 and T2 were rewritten in a scratch module outside the repository (`GOWORK=off`, `GOPROXY=off`, doger v0.3.0 from the module cache, `replace` to this checkout at slice 3). It wires the README section verbatim: `Provide` of two route configurers, a server provider collecting them with `InvokeAll[srvr.RouteConfigurer[*srvr.Group]]` and registered `AsStarter()`, `StartWithContext` with a 30s deadline. Not committed.
- **Evidence:** `TestDogerLifecycle` PASS (0.15s): T1 `StartWithContext` returned within 2s; T2 `GET /users` and `GET /orders` answered 200 after doger cancelled the `Start` context; `RootScope.HealthCheck` reported the server with a nil error; `ShutdownOnSignals(SIGUSR1)` stopped it; `srv.Wait()` returned nil and `srv.HealthCheck` an error afterwards.
- **Suggested action:** none.

### Slice 4 /simplify outcome (2026-11-23)
- **Status:** closed: recorded
- **Target:** container-lifecycle
- **Context:** Applied: `TestContainerConformance` reuses `lifecycleEngines`; duplicate comment dropped; module README "Lifecycle" sections reduced to a pointer to the root section. Skipped: reusing the harness `start()` in `TestLifecycleParity` (it only logs a non-nil `Run` error, the row asserts it); merging the per-package `lifecycle_example_test.go` files (Go examples are per package and stay self-contained for godoc).
- **Evidence:** `make check` exit 0.
- **Suggested action:** none.

### Review findings (2026-11-23)
- **Status:** open
- **Target:** container-lifecycle (group 5)
- **Context:** `/code-review high` on `feat/container-lifecycle`: (important) `serve` starts `serveOn` before writing `server.started` and closing `Ready`, and supervision now stops the server on that goroutine, so an immediate serve failure may log `server.failed`/`server.stopping`/`server.stopped` before `server.started` and close `Ready` after the server closed (`internal/engine/lifecycle.go` `serve`); (nits) stale blocking-`Start` docs in the `Server` type doc, `WithListener`, `WithShutdownTimeout`, `Config.ShutdownTimeout`, `ErrShutdownHook`, `LOGGING.md`, `otelsetup`; README `runManually` ignores a serve failure; `HealthCheck` godoc says not started until `Start` returns; gin `Start` godoc not re-wrapped. `/opsx:verify`: warning, design D1 describes a separate supervisor goroutine; warning, task 2.9's verify command matches no test (fixed in tasks.md); suggestions, gin/fiber test copies and two fiber hook rows. The security review excluded the concurrency changes by its rules; the code review reported only the ordering race among them, so task 5.1 reviews the concurrent paths explicitly.
- **Evidence:** unverified: review claims; task 5.1 reproduces the ordering claim with `TestLifecycleRecords_ImmediateServeFailure` before any fix.
- **Suggested action:** group 5 tasks 5.1-5.7.

### Security review (2026-11-23)
- **Status:** closed: no findings
- **Target:** container-lifecycle
- **Context:** `/security-review` (run twice on the same commits) found nothing at confidence 8 or above: new errors reach only Go callers, `HealthCheck` is not an HTTP endpoint, `server.failed` logs an internal serve error and no request data, no auth/crypto/injection surface was added. Concurrency was excluded by the review's rules (see "Review findings").
- **Evidence:** review report, no findings.
- **Suggested action:** none.

### Triage of the review findings (task 5.1, 2026-11-23)
- **Status:** closed: decided (fixes in tasks 5.2-5.5)
- **Target:** container-lifecycle
- **Context:** (1) Records order on an immediate serve failure: confirmed. (2) Concurrent paths reviewed line by line, no defect found: `Shutdown` while starting interrupts through `stepCtx` and `serve` re-checks `stateStopping` under the lock; supervision vs `Shutdown`/`Run` context end: `stop` claims under the lock (`stateRunning` only), the loser waits on `l.stopped` (pinned by `TestServeFailure_ShutdownDuringStop`, `TestRun_CancelDuringShutdown`); `Wait`/`HealthCheck` read `servedErr`/`hookErr` written under the lock or before `l.stopped` closes. (3) README `runManually` ignores a serve failure: confirmed by reading (it blocks on `<-ctx.Done()` only). (4) `HealthCheck` godoc "before Start returned": confirmed by reading (`serve` sets `stateRunning` before `Start` returns). (5) Stale docs and gin wrap: confirmed by reading. Security review: no findings. Verify warnings: design D1 and task 2.9 fixed in planning.
- **Evidence:** `TestLifecycleRecords_ImmediateServeFailure` (`container_test.go`), `go test -race -count=1000 -cpu=1,2,4,8 -run TestLifecycleRecords_ImmediateServeFailure .`: FAIL 3/4000, e.g. `records = [server.failed server.stopping server.stopped server.started], want [server.started server.failed server.stopping server.stopped]`; `-count=200` at default CPUs passed, so the race needs scheduling pressure.
- **Suggested action:** task 5.2: write `server.started` and close `Ready` before starting `serveOn`.

### Group 5 outcome (2026-11-23)
- **Status:** closed: done
- **Target:** container-lifecycle
- **Context:** 5.2 `serve` writes `server.started` and closes `Ready` before starting `serveOn`. 5.3 stale blocking-`Start` docs fixed (`Server` type, `WithListener`, `WithShutdownTimeout`, `Config.ShutdownTimeout`, `ErrShutdownHook`, `LOGGING.md`, `otelsetup` `ShutdownHook`, `HealthCheck`, gin `Start` wrap). 5.4 README `runManually` waits on its context and `Wait`; its test `TestReadme_RunManually_ServeFailure` was written together with the fix (no separate RED run; the old snippet blocked on its context only). 5.5 engine-neutral gin/fiber rows moved into `TestLifecycleParity` (plus `HealthCheck(nil)`); fiber hook row "Start fails because a route check returns an error" added (it was missing; passed at once, behaviour existed). `/simplify` applied: `bounded` instead of a nested goroutine, `messages()` reused, `errListener` beside `faultListener`, godoc rewrapped, README says to prefer `Run`. Skipped: describing the stop paths once instead of in three godocs, and tabling the fiber hook subtests (shapes differ).
- **Evidence:** `go test -race -count=1000 -cpu=1,2,4,8 -run TestLifecycleRecords_ImmediateServeFailure .` PASS after the fix (FAIL 3/4000 before); `make check` exit 0; `make cover` internal 99.8%, `serve`/`serveOn`/`superviseRun`/`stop` 100%.
- **Suggested action:** none.

### Release preparation (tasks 6.1-6.2, 2026-11-23)
- **Status:** closed: done
- **Target:** container-lifecycle
- **Context:** 6.1: PR #33 merged to `main` as `8b8c417`; `git diff v0.4.0..origin/main --stat -- security/` lists nothing (no `scrty-integration` code). The repository reports no CI checks, so `make check` is the gate. 6.2: on `release/prepare-v0.5.0` from `8b8c417`, `CHANGELOG.md` `[Unreleased]` moved under `## [v0.5.0] - YYYY-MM-DD`. Dry run of `RELEASING.md`: no `v0.5.0` or `<module>/v0.5.0` tag exists yet; `ginsrvr`, `fibersrvr`, `otelsrvr` require the core `v0.4.0` with one `replace`, `otelsetup` two, `internal/paritytest` builds through `replace`; `gh` is authenticated. Steps 3-6 (go get of the tagged core, dropping `replace`, tags, restore, release page) cannot run before the core tag exists.
- **Evidence:** `make check` exit 0 (govulncheck clean in 6 modules); `make cover` internal 99.8%.
- **Suggested action:** task 6.3, owner confirmation.

### Owner release decision (task 6.3, 2026-11-23)
- **Status:** closed: confirmed
- **Target:** container-lifecycle
- **Context:** Presented v0.5.0 (breaking `Start` semantics; new `Run`, `Wait`, `HealthCheck`, `ErrServerNotStarted`), the changelog section and the dry-run results. The owner confirmed "Yes, release v0.5.0": tag and push all five modules and publish the GitHub Release page.
- **Evidence:** owner answer in session, 2026-11-23.
- **Suggested action:** task 6.4.

### v0.5.0 released (task 6.4, 2026-11-23)
- **Status:** closed: released
- **Target:** container-lifecycle
- **Context:** Released in `RELEASING.md` order on `release/prepare-v0.5.0` (PR #34, merged as `cfabb5c`): core `v0.5.0`; `otelsrvr`, `ginsrvr`, `fibersrvr` each committed against the core `v0.5.0` without `replace` and tagged; `otelsetup` against core and `otelsrvr` `v0.5.0`, tagged; development `replace` lines restored with `internal/paritytest` now requiring the core `v0.5.0`, `make check` exit 0 after the restore. GitHub Release page published: https://github.com/kartaladev/srvr/releases/tag/v0.5.0 (intro, `## Modules` table, changelog section). The feature itself merged as PR #33 (`8b8c417`).
- **Evidence:** `proxy.golang.org/.../@v/v0.5.0.info` resolves for all five modules; a scratch consumer outside the repository (`GOWORK=off`, released modules only) passes a test running `Start`/`HealthCheck`/`Shutdown`/`Wait` and `Run` on net/http, gin and fiber; every tag is an ancestor of `main`.
- **Suggested action:** archive the change after review (`/opsx:archive`); `scrty-integration` consolidation takes "scrty-integration must move off the blocking Start".
