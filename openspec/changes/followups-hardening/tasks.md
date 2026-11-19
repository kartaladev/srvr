# Tasks

## 1. Consolidation: refresh plan against merged changes

- [x] 1.1 Sync with the main branch (`git fetch`, rebase or merge `origin/main` into the working branch) and list every change merged or archived since this change was planned on 2026-11-19 (`openspec list --json`, `openspec/changes/archive/`, `git log origin/main`); verify they are named in the completion note (at planning: `main` = `origin/main` = `8eac64a`, `triage-debug-buffer` archived 2026-11-19, `scrty-integration` active and unmerged)
- [x] 1.2 Read every `notes.md` carry-over (archived changes and `scrty-integration`) and decide each open entry addressed to `followups-hardening` or `unassigned`; verify none is left undecided. Already adopted by owner decision 2026-11-19 (Status set at planning): the nine `triage-debug-buffer` entries this change implements. To close or re-address in this step: `otel-observability` "engine-support 'Engines share one per-request state' exceeds the 500-character requirement limit" (covered by slice 5) and `core-server` "Ask prblm for a composable OnError and an exported status-only sentinel" (part 1 done by prblm v0.2.0 in `triage-debug-buffer`; part 2 is slice 6). `scrty-integration`'s open entries stay for `scrty-integration` (owner decision); the remaining `unassigned` entries of `core-server`, `gin-and-fiber-engines` and `otel-observability` are deferred unless one touches a slice here
- [x] 1.3 Refresh this change's proposal, specs, design and tasks against the main specs and merged code: re-diff each MODIFIED block against `openspec/specs/<capability>/spec.md` (any requirement changed on main since planning must be re-copied with this change's edits), and recheck with gopls the code facts in design Context (`ResolveConfig`, `RequestState.unmatched`, `answerUnmatched`, gin `classify`, fiber request stage, otel recover sites, `tracectx.New`); verify `openspec validate followups-hardening --strict` passes
- [x] 1.4 Record the outcome of 1.1-1.3 at the top of this change's `notes.md` (replace the placeholder entry); verify the file lists what was refreshed and adopted, or states that nothing was needed
- [x] 1.5 Commit the consolidation as `docs(openspec): consolidate followups-hardening against main`, containing only `openspec/` files; skip it when 1.1-1.4 changed nothing; verify with `git log -1 --stat`

## 2. Slice: construction joins problem-option errors (item 4)

- [x] 2.1 Prove or drop the claim: one error names `ProblemOptions` together with the other invalid fields (server-lifecycle / Scenario: Valid and invalid configuration, the four `WithProblemOptions` rows); verify `go test -race -run 'TestNew_Config' .` passes
  - RED: add the four rows to `TestNew_Config` (`server_test.go`), asserting with `configFields` that every listed field is found; run it and record the failing output (expected: `fields ["ShutdownTimeout"], want "ProblemOptions" among them`). If every row passes, the defect is not confirmed: record that in `notes.md` (rule 2), keep the rows as regression rows, skip 2.2's GREEN, and commit 2.5 as `test(core): pin joined problem-option errors`
  - GREEN: design D2: `ResolveConfig` builds the problem handler even when `newSettings` reports errors and returns `errors.Join` of both, with the consumer-only fallback when srvr's own options cannot be built
  - REFACTOR: look for a duplicated error-collection path (Consolidate Duplicate Conditional Fragments) between `newSettings` and `ResolveConfig`; keep one join point
- [x] 2.2 The same join on gin and fiber (server-lifecycle / Scenario: Problem-option errors are joined on every engine); verify `go test -race -run TestProblemOptionErrors_Joined ./...` in `internal/paritytest` passes
  - RED: `TestProblemOptionErrors_Joined` (`internal/paritytest`), one case per Examples row (3 engines × 2 fields); fails on every engine like 2.1 if the defect is confirmed
  - GREEN: none beyond 2.1 expected (the engines share `ResolveConfig`); if an engine still fails, fix its construction order
  - REFACTOR: reuse the paritytest construction helpers instead of new ones (Extract Function only if three call sites repeat)
- [x] 2.3 Murphy check of the new branch: a consumer option that panics inside the joined path (row with `MaxHeaderBytes`), and a valid problem option with invalid settings that reports no `ProblemOptions` field; verify both rows pass in `TestNew_Config` and `go tool cover -func` shows every branch of `ResolveConfig` covered
- [x] 2.4 `CHANGELOG.md` `## [Unreleased]` "Fixed" entry (only if 2.1 confirmed the defect); `gopls` diagnostics on every changed file; verify `make check MODULES=". internal/paritytest"` (or `make check`) passes
- [x] 2.5 Run `/simplify` on the slice's changes, apply what holds up, re-run the tests, then commit `fix(core): join problem-option errors with the other configuration errors` (or the `test(core)` message from 2.1 when not confirmed); verify with `git log -1 --stat`

## 3. Slice: only the router's 404 and 405 are limited (item 5)

- [x] 3.1 Prove or drop the claim on every engine (structured-logging / Scenario: Failures consumer middleware reports are not limited); verify `go test -race -run TestUnmatchedLimit_ConsumerMiddleware .` in `internal/paritytest` passes
  - RED: `TestUnmatchedLimit_ConsumerMiddleware` (`internal/paritytest/unmatched_test.go`), one case per row of both Examples tables (fake clock as in `TestUnmatchedLimit`); record the failing output (expected on fiber: `1 http.request.failed records, want 3` for the `prblm.ErrNotFound` and wrapped `fiber.ErrNotFound` rows). The net/http and gin rows confirm that their middleware never runs for an unmatched path (design Context, read from gin's source, now tested). If every row passes, record "not confirmed" in `notes.md`, keep the rows, and commit 3.5 as `test(paritytest): pin unmatched classification of consumer middleware`
  - GREEN: design D3: a router-answered mark on `RequestState` set by net/http `answerUnmatched`, gin's request stage and fiber's request stage (identity check against `fiber.ErrNotFound`/`fiber.ErrMethodNotAllowed`); `unmatched` requires it
  - REFACTOR: replace the derived condition (Substitute Algorithm) and review `RequestState.result`'s similar classification; change it only behind a RED row, otherwise add a `notes.md` entry saying why it stays
- [x] 3.2 Existing limit rows still hold with the recorded origin (structured-logging / Scenario: Limiting unmatched failed records, and "Failures while the access log is suppressed"); verify `go test -race -count=5 -run 'TestUnmatchedLimit|TestFailedRecords' .` in `internal/paritytest` and `go test -race -run TestUnmatchedLimit ./engine` pass
- [x] 3.3 godoc of `WithUnmatchedRecordLimit`, `AccessLogConfig.UnmatchedLimit` and `LOGGING.md`'s "Unmatched-record limit" section state that only the router's answers count and name the `fiber.ErrNotFound` identity limitation; `CHANGELOG.md` "Fixed" entry; verify `go test -run 'TestLogSchemaDocumented|Example' .` passes
- [x] 3.4 Verify the slice end to end: `make check` passes; `go tool cover -func` (via `make cover`) shows `unmatched`, the mark's setters in each engine and `writeAccess` fully covered; `gopls` diagnostics on every changed file report nothing new
- [x] 3.5 Run `/simplify`, apply what holds up, re-run the tests, then commit `fix(engine): limit only unmatched records the router answered` (or the `test(paritytest)` message from 3.1); verify with `git log -1 --stat`

## 4. Slice: recovered panic errors are kept as Cause (item 6)

- [x] 4.1 otelsrvr keeps a recovered panic error as `Cause` (otel-metrics / Scenario: Typed configuration errors, the eight new panic rows); verify `go test -race -run TestConfigError ./...` in `otelsrvr` passes
  - RED: add the rows to `TestConfigError` (`otelsrvr/config_test.go`) with fakes whose `Meter`, `Tracer` or `Fields` panic; expected failure: `New error does not reach the expected cause` on the three `E` rows, the `Error`-panics row and the `sliceError` row; the `"boom"`, typed-nil and cyclic rows pass already (they pin the guard)
  - GREEN: design D4: an unexported cause helper (error, not typed nil, walkable chain, no `==` on values) used at the three recover sites; text unchanged (`ValueText`)
  - REFACTOR: one helper for the three sites (Extract Function); diff the message of every invalid case before and after, expecting `SAME`
- [x] 4.2 otelsetup keeps the error `http.ServeMux` panics with as `Cause` (telemetry-setup / Scenario: Typed configuration errors, the two `Prometheus.Path` rows); verify `go test -race -run TestConfigError ./...` in `otelsetup` passes
  - RED: the two rows in `TestConfigError` (`otelsetup/setup_test.go`), the expected text obtained by registering the same path on a throwaway `http.ServeMux` under recover in the test; fails with a nil `Cause`
  - GREEN: the same helper logic in `validatePath`
  - REFACTOR: reuse `configCauses`; no shared export in `engine` (design D4)
- [x] 4.3 Godoc of both `ConfigError.Cause` fields says recovered panic errors are kept; `CHANGELOG.md` "Changed" entry (a `Cause` that was nil is now set); verify `go doc ./otelsrvr ConfigError` and `go doc ./otelsetup ConfigError` show it
- [x] 4.4 Verify the slice end to end: `make check MODULES="otelsrvr otelsetup"` passes; `go tool cover -func` shows every branch of the helper and each recover site covered; `gopls` diagnostics clean
- [x] 4.5 Run `/simplify`, apply what holds up, re-run the tests, then commit `fix(otel): keep recovered panic errors as ConfigError.Cause`; verify with `git log -1 --stat`

## 5. Slice: spec and test hygiene (items 1, 2 and RELEASING.md)

- [x] 5.1 Trace every clause removed from the six shortened requirement descriptions to the scenario step, row or main-spec requirement that now carries it (design D1), and write the list into this change's `notes.md`; verify `openspec validate followups-hardening --strict` passes and every removed clause has a target in the list
- [x] 5.2 Re-exported names (engine-support / Scenario: Re-exported names); verify `go test -run TestReexportedNames .` passes
  - RED: `TestReexportedNames` (`types_test.go`), one case per row comparing `reflect.TypeFor[srvr.X]()` with `reflect.TypeFor[engine.X]()`; run it with one row deliberately pointing at a wrong type to see it fail, then restore it (aliases already exist, so no production RED is possible; record this in the task note)
  - GREEN: none expected
  - REFACTOR: none beyond /simplify
- [x] 5.3 Late-failure levels (structured-logging / Scenario: Level of a failure after the response started) map to `TestLateError`'s four rows; rename the test's comment to name the new scenario; verify `go test -race -run TestLateError .` passes
- [x] 5.4 Route-listing and hook-order steps moved into scenarios (engine-support / Scenario: Route listing; error-responses / Scenario: Consumer prblm error hooks) are asserted by the existing route-listing and prblm-hook tests; name each test in the task note and add an assertion only where a moved step is unchecked; verify the named tests pass with `-race`
- [x] 5.5 Make `TestShutdownHooks_Faulty` "blocks and ignores its context" deterministic (server-lifecycle shutdown-hook rows, unchanged); verify `go test -race -count=20 -run 'TestShutdownHooks_Faulty' .` passes, and passes again while `go test -race ./...` runs in `internal/paritytest` and `fibersrvr` at the same time
  - RED: none can fail on demand (timing flake, rule 2: the archived entry's failing output `Start returned after 205.696042ms, want within 200ms` is the evidence); instead, temporarily make the hook ignore the block channel's close to confirm the new assertion fails by the hang guard, then revert
  - GREEN: design D5: the hook signals entry and blocks on a channel closed after `Start` returns; assert `Start` returned, the hook was still blocked, and `errors.Is(err, context.DeadlineExceeded)`; hang guard 10s
  - REFACTOR: drop the `within` field if no row uses it any more (Remove Dead Code); close the channel in `t.Cleanup` so goleak sees the goroutine end
- [x] 5.6 `RELEASING.md`: the restore step says to run `go mod tidy` in `internal/paritytest` in the same commit, and a note says `main` CI is red between the module release commits (expected while released modules require the new core and others still require the pseudo-version); verify by reading the rendered file and checking each step against the "Release v0.1.0" notes entry
- [x] 5.7 Verify the slice end to end: `make check` passes; dry-run the archive sync in a scratch copy of the repository (`cp -R` to the scratchpad, `openspec archive followups-hardening -y` there) and run `openspec validate --specs --strict` in the copy: all 16 main specs pass; `gopls` diagnostics clean
- [x] 5.8 Run `/simplify`, apply what holds up, re-run the tests, then commit `test(core): pin shortened spec detail and make the blocking-hook row deterministic` (includes `RELEASING.md`); verify with `git log -1 --stat`

## 6. Slice: gin status-only classification with ginprblm.ErrStatusOnly (item 7)

- [x] 6.1 Pin today's gin behaviour (gin-engine / Scenario: Consumer middleware behaviour, the five new rows); verify `go test -race -run TestGinMiddleware ./...` in `ginsrvr` passes (use the existing test of this scenario; name it in the task note)
  - RED: the rows are characterisation guards and must pass on current code; if one fails, that is a defect claim: record the failing output in `notes.md` and stop for the owner before changing the row or the code
  - GREEN: none
  - REFACTOR: none
- [x] 6.2 Evaluate `errors.Is(err, ginprblm.ErrStatusOnly)` in `classify` against design D6's three criteria; prototype it, run the ginsrvr suite and `internal/paritytest` with `-race`, and record the decision with the diff size in `notes.md`; verify the prototype is either kept with every test green or reverted (`git diff --stat ginsrvr` empty)
- [x] 6.3 If adopted: update the `classify` comment and the package overview in `ginsrvr/doc.go`; no CHANGELOG entry (no visible change); verify `make check MODULES=ginsrvr` passes and `go tool cover -func` covers both branches of `classify`
- [x] 6.4 Close the archived entry's target in this change's `notes.md` outcome; run `/simplify`, apply what holds up, re-run the tests, then commit `refactor(ginsrvr): classify status-only aborts with ginprblm.ErrStatusOnly` if adopted, otherwise `test(ginsrvr): pin status-only abort records`; verify with `git log -1 --stat`

## 7. Slice: performance, measured (items 8, 9, 10)

- [x] 7.1 Guard rows for id generation (trace-context / Scenario: Concurrent generation, three rows); verify `go test -race -run TestTraceIDs_Concurrent .` (the existing test of this scenario, extended; name it in the task note) passes
  - RED: the new `clients`, span-id and format assertions pass on current code (guards before any change); confirm each would fail by breaking one generated id in a scratch copy, then revert
  - GREEN: none
  - REFACTOR: none
- [x] 7.2 Benchmark rows: `BenchmarkNewTrace` (serial and `RunParallel`, `internal/tracectx`); a `BenchmarkDebugBuffer` row with six or more attributes and a `slog.Any` value; `BenchmarkPipeline` rows on fiber for a suppressed unmatched record and on every engine for `OmitUserAgent`; verify `go test -run '^$' -bench . -benchmem -benchtime=1x ./...` passes in each touched module and `make bench` lists the new rows
- [x] 7.3 Measure trace-id cost (design D7) on darwin (`make profile MODULES=. BENCH=BenchmarkPipeline`) and on Linux (the CI `bench` job of this slice's pull request, or a local Linux container); compare candidates (a), (b), (c) with `benchstat -count=10` on `BenchmarkNewTrace` and `BenchmarkPipeline`; record the tables in `notes.md`, marking Linux unverified with the reason if no Linux run was possible; verify the notes entry holds both platforms' tables or the unverified label
- [x] 7.4 STOP for the owner if a trace-id candidate meets the gain on `BenchmarkPipeline`: present the numbers and design D7's security trade-off, and apply it only on explicit confirmation (record the decision in `notes.md`); with confirmation, implement it under the 7.1 guards with `-race -count=5`; verify the guards pass and the `trace-context` requirement's constraints hold (source seeded only from `crypto/rand`, all-zero rejection kept)
- [x] 7.5 Debug-buffer items (design D8): prototype `attrSize` single formatting, the avoided `Clone`, and a ring, one at a time; `benchstat -count=10` before and after each; apply only at the gain and revert the rest; verify `go test -race -run 'TestDebugBuffer' ./... .` passes in `engine` and the root after each applied item, and every item's table is in `notes.md`
- [x] 7.6 fiber copies and user-agent read (design D9): prototype the cached `admit` decision and the exposed omission; apply only at the gain; verify `go test -race -count=5 -run 'TestUnmatchedLimit|TestAccessLog_OmitUserAgent|TestFiberAccessSuppressed|TestAccessSuppressed' ./...` passes in `internal/paritytest`, `fibersrvr` and `engine`, and the tables are in `notes.md`
- [x] 7.7 Update `docs/PERFORMANCE.md` with the measured numbers and decisions; `CHANGELOG.md` "Changed" entries for applied items (performance only); verify the slice end to end: `make check` passes, `make cover` shows every applied hot-path change covered, `gopls` diagnostics clean
- [x] 7.8 Run `/simplify`, apply what holds up, re-run the tests and benchmarks, then commit `perf: <applied items>` (for example `perf(engine): batch trace-id randomness`), or `test(bench): measure trace ids, debug buffer and access copies` when nothing was applied; verify with `git log -1 --stat`

## 9. Slice: deterministic allocation gate (owner request 2026-11-19, during release)

Placed before group 8 because it runs between 8.3 and 8.4: the owner held the module tags until this lands ("fix the gate first, then finish the release"). Design D10.

- [x] 9.1 Root cause first (rule 2): reproduce the allocation variance of `BenchmarkLogHandler/root_logger_concurrent_requests` that failed pull request #14 (run 44486663813 attempt 1) with a scripted repeated run (`-count=10 -cpu=1,2,4,8 -benchtime=200ms`), and measure the `allocs/op` and `B/op` spread of every gated benchmark over 20 runs of `make bench` at `-cpu=4`; verify the evidence and the list of affected benchmarks are in `notes.md`
- [x] 9.2 The gate gates only reproducible units (test-quality / Scenario: Benchmark comparison in CI, the four new rows); verify `go test -race ./internal/benchgate` passes
  - RED: the four rows in `TestBenchmarkComparison` (`internal/benchgate/main_test.go`), the recorded run as fixture; record the failing output (expected: `FAIL ... allocs/op: main 5, branch 7 (+40.0%)` and `FAIL ... B/op: main 344.5, branch 418 (+21.3%)`)
  - GREEN: design D10 (c): a rise of a unit whose runs on either side spread more than 10% is reported as `noisy`, not failed
  - REFACTOR: boundary rows (spread exactly 10% gated, one past not, spread up from zero, one outlier run of six) in `TestBenchmarkComparison_Faults`; the even-runs row moved to runs within 10%
- [x] 9.3 The concurrent log benchmark is deterministic (design D10 (b)): `root logger interleaved requests` alternates two requests per goroutine; verify one `allocs/op` and one `B/op` value over `-count=10 -cpu=1,2,4,8` and over the 18 runs of the three `make bench` runs of 9.5
- [x] 9.4 The gate step of the CI `bench` job also appends its report to the job summary, so a noisy unit is seen (review finding); `docs/PERFORMANCE.md` "The CI benchmark job" says what the gate checks and what it no longer catches; verify `actionlint` reports nothing and the doc matches D10
- [x] 9.5 Verify the slice end to end: `make check` passes; gopls diagnostics clean on every changed file; the gate simulated locally on two `make bench` runs of identical code at least three times passes; `openspec validate followups-hardening --strict` passes
- [x] 9.6 Run `/simplify`, apply what holds up, re-run the tests, then commit `fix(ci): stop the allocation gate failing on scheduling noise`; verify with `git log -1 --stat`

## 8. Slice: release

Tasks 8.4 and later resume after group 9 lands on `main`.

- [x] 8.1 Precondition: this change is reviewed and merged to `main`; verify `git log origin/main --oneline -1` contains the merge
- [x] 8.2 Prepare: move the `CHANGELOG.md` `## [Unreleased]` entries under a version heading (version left as a placeholder), run `make check` and `make cover` on `main`, dry-run the release guard and the `RELEASING.md` steps without tagging; verify every step passes
- [x] 8.3 STOP for the owner: present the proposed version (`v0.2.0` if any visible behaviour change landed: the joined `ProblemOptions` error (slice 2), the counted unmatched records (slice 3), the set `Cause` (slice 4); `v0.1.1` if none did), the changelog section and the dry-run results; wait for explicit confirmation of the version and of pushing tags; record the decision in `notes.md`; do not continue without it
- [ ] 8.4 Release in `RELEASING.md` order: tag the core module and push the tag; in each released module (`ginsrvr`, `fibersrvr`, `otelsrvr`, then `otelsetup`) require that core version (and `otelsrvr`'s for `otelsetup`), drop the `replace`, run its checks with `GOWORK=off`, commit, tag `<module>/<version>` and push; verify the CI `release` job passes for every tag
- [ ] 8.5 Restore the development `replace` lines on `main` in one separate commit that also runs `go mod tidy` in `internal/paritytest` (lesson from the "Release v0.1.0" notes entry); verify `make check` passes after the restore and `main` CI is green
- [ ] 8.6 Publish the GitHub Release page (RELEASING.md step 6): write the notes (short intro, `## Modules` table with module, tag and `go get` line for every released module, then the version's `CHANGELOG.md` section) and run `gh release create <version> --verify-tag --title "srvr <version>" --notes-file <file>` on the core tag; verify with `gh release view <version>`
- [ ] 8.7 Verify through the module proxy: `GOPROXY=https://proxy.golang.org GOFLAGS=-mod=mod go list -m github.com/kartaladev/srvr@<version>` and the same for every released module, and build a scratch consumer outside the repository that imports each module at the tagged version; record the result in `notes.md`

## Workflow follow-up

- Re-read `notes.md` and make sure every open item has a target or is `unassigned`.
- Archive the change after review; verify `openspec validate --specs --strict` passes for all main specs after the archive sync.
