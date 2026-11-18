# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (task group 1, 2026-11-18)
- **Status:** closed: recorded
- **Target:** goroutine-leak-checks
- **Context:** 1.1: `git fetch`; `main` was at `origin/main` plus this change's planning commit `ea23d3d`; nothing was merged or archived since planning (archived: `core-server`, `gin-and-fiber-engines`, `otel-observability`, all before planning; active: `scrty-integration`, `triage-debug-buffer`, unchanged). 1.2: every open entry addressed to `unassigned` or to this change was read; none is about goroutine lifetimes, so none is adopted. Deferred unchanged (they keep their targets): all open `unassigned` entries of `core-server`, `gin-and-fiber-engines` and `otel-observability` notes and those of the active changes. Two are related and stay `unassigned` with a remark: "Engine modules duplicate the core's test helpers" (this change adds one more small per-module copy, `leak_test.go`, for the same reason: modules cannot share `_test.go` files) and "otelsrvr's go.mod requires the OpenTelemetry SDK for its tests" (goleak's test-only requirement has the same module-graph effect, accepted by the owner for this change, proposal Defaults and Customization). 1.3: the artifacts were written against `52fdbfc` minutes before this consolidation, from a probe run of every module under goleak; no main spec, code or dependency version changed since, so nothing was refreshed; `openspec validate goroutine-leak-checks --strict` passes.
- **Evidence:** `git log --oneline origin/main..main` lists only `ea23d3d`; `openspec list --json`.
- **Suggested action:** none.

### srvr's serve goroutine is an anonymous closure
- **Status:** open
- **Target:** unassigned
- **Context:** the "never `Shutdown`" lifecycle rows identify srvr's serve goroutine by its frame `engine.(*Lifecycle[...]).serve.func1` (`engine/lifecycle.go:312`, `go func() { r.served <- runner.Serve(ln) }()`). The tests match the stable prefix `engine.(*Lifecycle[...]).serve`, but a refactor that moves the goroutine into another function would still break them without any behaviour change. Raised by the /simplify altitude review of group 2; a production change is out of scope for this test-only change.
- **Evidence:** `TestLifecycle_Goroutines/one_route,_Start_and_never_Shutdown` (`lifecycle_goroutines_test.go`) reports `github.com/kartaladev/srvr/engine.(*Lifecycle[...]).serve.func1`.
- **Suggested action:** give the serve goroutine a named method (for example `(*Lifecycle[C]).serveOn`) and match that name in the leak rows of every engine.

### fasthttp's idle-worker cleaner could end in time instead of being ignored
- **Status:** open
- **Target:** goroutine-leak-checks (owner decision; D3 kept as planned meanwhile)
- **Context:** the /simplify altitude review of group 3 noted that `(*workerPool).Start.func2` outlives `Stop` only because it sleeps `MaxIdleWorkerDuration` (10s by default). A test helper setting a short `MaxIdleWorkerDuration` on the fiber app's fasthttp server (reachable through `App().Server()` in `fibersrvr/export_test.go`) would let the cleaner end within goleak's retry window, so the cleaner ignore, its constant and its "needed" rows in fibersrvr and paritytest could go. That changes design D3 and the spec's ignore rows, so it was not applied.
- **Evidence:** fasthttp v1.78.0 `workerpool.go` (cleaner loop sleeps `MaxIdleWorkerDuration`); `TestLeakIgnores/fasthttp_idle-worker_cleaner` (`fibersrvr/leakignores_test.go`) fails with the ignore removed.
- **Suggested action:** the owner decides; if adopted, update D3 and the spec's ignore rows through `/opsx:update`, then drop the ignore.
