# Tasks

## 1. Consolidation: refresh plan against merged changes

- [x] 1.1 Sync with the main branch (`git fetch`, rebase or merge `origin/main` into the working branch) and list every change merged or archived since this change was planned on 2026-11-23 (`openspec list --json`, `openspec/changes/archive/`, `git log origin/main`); verify they are named in the completion note (at planning: `main` at `a833539`, v0.4.0 released, `pluggable-error-renderer` archived, `scrty-integration` active with PR #31 open)
- [x] 1.2 Read every `notes.md` carry-over (archived changes, `scrty-integration`) and decide each open entry addressed to `container-lifecycle` or `unassigned` (adopt, defer or close); add an entry addressed to `scrty-integration`: its specs and tasks assume the blocking `Start` (shutdown-hook stop paths, examples) and must move to `Run`/`Start`+`Wait` in its consolidation; verify none is left undecided
- [x] 1.3 Refresh this change's proposal, specs, design and tasks against the main specs and merged code: recheck with gopls the facts in design Context (`Lifecycle.Start`, `serve`, `serveOn`, `stop`, `stopRun`, `Shutdown`, `joinServe`, `guardedListener`, the state constants, `types.go` sentinels, every `Start` caller); re-diff the MODIFIED blocks of `server-lifecycle`, `engine-support` and `test-quality` against `openspec/specs/`; verify `openspec validate container-lifecycle --strict` passes
- [x] 1.4 Record the outcome of 1.1-1.3 at the top of this change's `notes.md` (replace the placeholder entry); verify the file lists what was refreshed and adopted, or states that nothing was needed
- [x] 1.5 Commit the consolidation as `docs(openspec): consolidate container-lifecycle against main`, containing only `openspec/` files and the change-order lines of `AGENTS.md`/`openspec/config.yaml` if not yet committed; skip it when 1.1-1.4 changed nothing; verify with `git log -1 --stat`
- [x] 1.6 Precondition for slice 2: run every module's tests once on the working branch before any change and record the baseline (all pass); verify the result is in `notes.md`

## 2. Slice: Start returns once serving on every engine, with Run and Wait

- [x] 2.1 Add `ErrServerNotStarted` (design D6) and `Wait` misuse on a never-started server (server-lifecycle / Scenario: Wait misuse, row "was never started"); verify `go test -race -run TestWait_Misuse .` passes
  - RED: `TestWait_Misuse` with the row "was never started"; fails to compile because `Wait` and `ErrServerNotStarted` do not exist
  - GREEN: sentinel in `internal/errchain`, alias in `types.go`; `Lifecycle.Wait` returning it in `stateNew`; `Server.Wait` delegating
  - REFACTOR: keep the sentinels' godoc grouped (Move Statement); then /simplify
- [x] 2.2 `Start` returns once serving and its context bounds startup only (server-lifecycle / Scenario: Start returns while serving, net/http rows; Scenario: Start bounded by its context); verify `go test -race -run 'TestStart_ReturnsWhileServing|TestStart_BoundedByContext' .` passes
  - RED: both tests with every net/http row, including cancelled and expired `Start` contexts and `Start(nil)`; fail because `Start` blocks
  - GREEN: `Lifecycle.Start` returns after `serve` closes `ready` (design D1, D2); `superviseRun` started from `serve` handles `r.stopping` only for now
  - REFACTOR: Extract Function for the old waiting `select` (kept for `Run`); then /simplify
- [x] 2.3 `Run` serves until its context ends and reports start failures (server-lifecycle / Scenario: Run and stop by context, net/http rows; Scenario: Run fails to start); verify `go test -race -run 'TestRun_' .` passes
  - RED: `TestRun_StopByContext` (cancel, `Shutdown`, deadline rows) and `TestRun_FailsToStart` (address in use, configurer panic, cancelled context, `Run(nil)`, `Shutdown` before `Run`)
  - GREEN: `Lifecycle.Run` = `Start` + waiting `select` + stop + `Wait` (design D3); `Server.Run` delegating
  - REFACTOR: remove duplication between `Run`'s stop and `Shutdown`'s drain-context construction (Extract Function); then /simplify
- [x] 2.4 `Wait` reports how serving ended (server-lifecycle / Scenario: Wait after Start, net/http rows; Scenario: Wait misuse, remaining rows); verify `go test -race -run 'TestWait_' .` passes
  - RED: rows for `Shutdown` returning `nil`, `3` concurrent waiters, repeated calls, a failing `AfterDrain` hook, a permanent `Accept` error; misuse rows for failed bind, `Shutdown` before `Start`, `Run` cancelled
  - GREEN: `l.servedErr`, a served-run flag written before `l.stopped` closes; `Wait` per design D4
  - REFACTOR: name the result composition (Extract Function `runResult`); then /simplify
- [x] 2.5 A serve failure after `Start` stops the server in order and is reported (server-lifecycle / Scenario: Listener fails while serving, net/http rows; MODIFIED Scenario: Lifecycle records, new row); verify `go test -race -run 'TestServeFailure_AfterStart|TestLifecycleRecords' .` passes
  - RED: listener whose `Accept` returns a permanent error, and one that panics, after `Start` returned; assert `Wait`, one `server.failed`, `AfterDrain` ran once, later `Shutdown` `nil`, later `Start` `ErrServerClosed`; add triage test T3 (notes.md) to `fibersrvr` as `TestServeEndsWithoutStop`: a listener whose `Accept` returns `net.ErrClosed` unclosed must yield an error matching `srvr.ErrServerClosed` and one `server.failed` (it fails on main today: `Start` returns `nil`); add the "Shutdown during the stop a serve failure began" and `Run` serve-failure rows
  - GREEN: `superviseRun` handles `r.done` with a served error, and a clean return with no stop requested as "serving ended without a stop": log, record, `stop` with a detached `ShutdownTimeout` context (design D1)
  - REFACTOR: Consolidate Conditional Expression between `Run`'s and the supervisor's served-error handling; then /simplify
- [x] 2.6 Stop paths, hooks and single use under the new methods (server-lifecycle MODIFIED / Scenario: Hook phases and order; Scenario: Hooks on every stop path; Scenario: Start twice); verify `go test -race -run 'TestShutdownHooks|TestStart_Twice' .` passes
  - RED: before touching 2.2-2.5's code paths further, add the MODIFIED rows not yet covered by 2.2-2.5 (`Run` misuse rows of "Start twice", the `Run`-context rows of "Hooks on every stop path", "Hook phases and order" under `Run`); run them and confirm each fails because `Run` misuse or the `Run` stop path is not yet wired to the shared single-use and hook checks; rows already made green by 2.2-2.5 are written in those tasks, not here
  - GREEN: wire the remaining paths; move every remaining core test that expects a blocking `Start` to `Run`
  - REFACTOR: shared test helper `startServer(t)` returning a stopper (Extract Function); then /simplify
- [x] 2.7 gin and fiber get `Run` and `Wait`, and their tests move to the new contract (engine-support MODIFIED / Scenario: Lifecycle misuse on every engine, non-`HealthCheck` rows; Scenario: Hooks per engine and stop path; server-lifecycle gin and fiber rows of "Start returns while serving", "Run and stop by context", "Wait after Start", "Listener fails while serving"); verify `make test MODULES=ginsrvr` and `make test MODULES=fibersrvr` pass
  - RED: add the gin and fiber rows to each module's lifecycle tests; fail to compile without `Run`/`Wait`
  - GREEN: delegate `Run` and `Wait` to the shared `Lifecycle`; move each module's blocking `Start` callers to `Run`
  - REFACTOR: Pull Up any duplicated delegation comments into the shared godoc reference; then /simplify
- [x] 2.8 Move `otelsrvr`, `otelsetup`, `internal/paritytest`, `example_test.go`, `readme_example_test.go` and the README snippets of core, `ginsrvr`, `fibersrvr` from blocking `Start` to `Run`; verify `make test` passes for every module
  - RED: run each module's tests and list the ones that now fail or hang because `Start` returns, compared with the 1.6 baseline; this list is the RED evidence
  - GREEN: replace those calls with `Run` (or `Start` + `Shutdown` where the test drives the stop)
  - REFACTOR: none beyond renames; /simplify
- [x] 2.9 Goroutine leaks (test-quality MODIFIED / Scenario: Lifecycle leaves no goroutine behind, `Start`, `Run`, `Wait` and serve-failure rows on all engines); verify `go test -race -run TestLeaks ./...` passes in core, `ginsrvr`, `fibersrvr`
  - RED: add the rows; the never-stopped rows must report srvr's serve goroutine
  - GREEN: fix any supervisor leak found (it must end with `stop`)
  - REFACTOR: name the supervisor goroutine function so reports name it; /simplify
- [x] 2.10 Godoc for `Start` (returns once serving, context bounds startup), `Run` (blocks; not for a container's run-once hook), `Wait` (never call from a shutdown hook), `ErrServerNotStarted`; `CHANGELOG.md` `[Unreleased]` **BREAKING** entry with a `Start` to `Run` snippet; verify `go vet ./...` passes in every module
- [x] 2.11 Verify the slice end to end: `make check`; gopls diagnostics clean on changed files; `go tool cover -func` shows `Lifecycle.Start`, `superviseRun`, `Run`, `Wait` and every error branch of design Hot Paths covered
- [x] 2.12 Run `/simplify` on the slice's changes, apply what holds up, re-run the tests, then commit `feat(core)!: return from Start once serving, add Run and Wait`; verify with `git log -1 --stat`

## 3. Slice: health check on every engine and container conformance

- [x] 3.1 `HealthCheck` across the lifecycle (server-lifecycle / Scenario: Health across the lifecycle, every row; engine-support `HealthCheck` rows); verify `go test -race -run TestHealthCheck ./...` passes in core, `ginsrvr`, `fibersrvr`
  - RED: every row, including starting with a waiting route check, draining after `Shutdown` began, failed bind, and the serve-failure join; fails to compile without `HealthCheck`
  - GREEN: `Lifecycle.HealthCheck` per design D5; delegations on the three servers
  - REFACTOR: Replace Conditional with a state-to-error switch kept next to the state constants (Move Function); then /simplify
- [x] 3.2 `HealthCheck` with hostile contexts (server-lifecycle / Scenario: Health with a hostile context); verify `go test -race -run TestHealthCheck_HostileContext .` passes
  - RED: rows `nil`, already cancelled, `Err()` panics
  - GREEN: nil check returning `ErrConfig`; no `ctx.Err()` call
  - REFACTOR: none expected; /simplify
- [x] 3.3 Container conformance (server-lifecycle / Scenario: Container-shaped lifecycle) in `internal/paritytest` per design D7; verify `go test -race -run TestContainerConformance ./internal/paritytest/...` passes
  - RED: compile-time interface assertions and the driver for each engine (fails to compile before 3.1 adds `HealthCheck` on gin and fiber, so write it first); extend `TestCoreDependencies`' module coverage with a container deny check for `ginsrvr` and `fibersrvr` (design D7), proven by temporarily requiring `github.com/kartaladev/doger` in a scratch copy, not committed
  - GREEN: nothing beyond 3.1 should be needed; fix any engine that fails
  - REFACTOR: share the driver across engines (Parameterize Function); /simplify
- [x] 3.4 Godoc for `HealthCheck` (states, no client exposure, safe for containers); `CHANGELOG.md` Added; verify `go test -run Example ./...` passes
- [x] 3.5 Verify the slice end to end: `make check`; gopls diagnostics clean; `go tool cover -func` shows every branch of `HealthCheck` covered
- [x] 3.6 Run `/simplify`, apply what holds up, re-run the tests, then commit `feat(core): add HealthCheck and pin container conformance`; verify with `git log -1 --stat`

## 4. Slice: documentation and end-to-end verification

- [ ] 4.1 README section "Running under a lifecycle container": registering route configurers and the server in doger v0.3.0 or later (`Provide`, `InvokeAll[srvr.RouteConfigurer[*srvr.Group]]`, `AsStarter`), bounding startup with `RootScope.StartWithContext` and a deadline (doger has no start timeout of its own) and the note that doger cancels that context when it returns, which srvr's `Start` ignores by design, the gin and fiber type swaps, `Run` for a plain `main`, `Start`+`Wait` for manual control; snippets compile in `readme_example_test.go` without importing doger (doger calls shown in prose or a non-compiled block); module READMEs updated; verify `go test -run 'Example|Readme' ./...` passes
- [ ] 4.2 `ExampleServer_Start` (with `Wait`) and `ExampleServer_Run` on each engine; verify `go test -run Example ./...` passes in core, `ginsrvr`, `fibersrvr`
- [ ] 4.3 Parity: `internal/paritytest` runs the lifecycle rows of this change on all three engines; verify `go test -race ./...` passes in `internal/paritytest`
- [ ] 4.4 Scratch check outside the repository (not committed): rerun the triage module (notes.md "Triage with doger v0.3.0"; T1 and T2 must now pass) and a throwaway module using the latest doger release with a srvr server registered `AsStarter` and two route configurers; `RootScope.Start` returns, a request is served, `RootScope.HealthCheck` reports the server healthy, `ShutdownOnSignals` stops it; record the result in `notes.md`
- [ ] 4.5 End-to-end verification locally: `make check`, `make cover`, `govulncheck`, `openspec validate container-lifecycle --strict`, gopls diagnostics clean; `CHANGELOG.md` `[Unreleased]` complete
- [ ] 4.6 Run `/simplify`, apply what holds up, re-run the tests, then commit `docs: document running srvr under lifecycle containers`; verify with `git log -1 --stat`

## 5. Slice: release v0.5.0

- [ ] 5.1 Precondition: this change is reviewed and merged to `main`, and no `scrty-integration` code has been merged since (only its planning files may be on `main`); verify `git log origin/main --oneline -1` contains the merge and `git diff v0.4.0..origin/main --stat -- security/` lists nothing
- [ ] 5.2 Prepare: move `CHANGELOG.md` `[Unreleased]` under a `v0.5.0` heading (date placeholder), run `make check` and `make cover` on `main`, dry-run the `RELEASING.md` steps without tagging; verify every step passes
- [ ] 5.3 STOP for the owner: present the proposed version (`v0.5.0`: breaking `Start` semantics, new `Run`, `Wait`, `HealthCheck`, `ErrServerNotStarted`), the changelog section and the dry-run results; wait for explicit confirmation of the version and of pushing tags; record the decision in `notes.md`; do not continue without it
- [ ] 5.4 Release in `RELEASING.md` order (core, then `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup`), restore development `replace` lines, publish the GitHub Release page with the migration note; verify every module resolves through the proxy at `v0.5.0` and a scratch consumer builds `Run` and `Start`+`Wait` on each engine; record the result in `notes.md`

## Workflow follow-up

- Re-read `notes.md` and make sure every open item has a target or is `unassigned`.
- Archive the change after review; verify `openspec validate --specs --strict` passes after the archive sync.
