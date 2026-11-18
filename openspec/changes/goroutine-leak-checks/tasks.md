# Tasks

## 1. Consolidation: refresh plan against merged changes

- [x] 1.1 Sync with the main branch (`git fetch` and rebase or merge the main branch into the working branch) and list every change archived or merged since this change was planned (`openspec list --json`, `openspec/changes/archive/`, `git log` on the main branch); verify by naming them in the task's completion note
- [x] 1.2 Read every `notes.md` carry-over from earlier changes (active and archived) and decide for each open entry addressed to this change or to `unassigned`: adopt it into this change's artifacts, defer it (re-address it in this change's own `notes.md`), or close it with a reason; verify no open entry is left undecided
- [x] 1.3 Compare this change's proposal, specs, design and tasks with the current main specs (`openspec list --specs`, `openspec show <id> --type spec`) and the merged code (test harnesses, `engine` lifecycle and hooks, `otelsetup` shutdown, fasthttp version); fix every stale assumption in this change's artifacts (use `/opsx:update`); verify with `openspec validate goroutine-leak-checks --strict` passing
- [x] 1.4 Record the outcome of 1.1-1.3 (what was refreshed, what was adopted from carry-overs) at the top of this change's `notes.md`; verify the file exists and lists the refreshed items or states that none were needed
- [x] 1.5 Commit the consolidation as one conventional commit, `docs(openspec): consolidate goroutine-leak-checks against main`, containing only planning files (`openspec/`); skip the commit when 1.1-1.4 changed nothing; verify with `git log -1 --stat`

## 2. Slice: core module tests fail on leaked goroutines

- [x] 2.1 Amend `AGENTS.md` rule 7 and its "Repository layout" core line, and `openspec/config.yaml` context's core dependency line, to say the core depends only on the standard library and prblm at runtime and that test-only dependencies such as goleak are allowed; add `go.uber.org/goleak v1.3.0` to the core `go.mod` as its tests' only new requirement; verify `GOWORK=off go test -run TestCoreHasNoOTel .` passes and `GOWORK=off go list -deps . ./engine ./internal/tracectx | grep -c goleak` prints `0`
- [x] 2.2 Leak check in every core test binary (test-quality / Scenario: A goroutine left running fails the module's tests, core rows and boundary rows); verify `GOWORK=off go test -race ./...` passes in the core and `go test -run TestLeakCheck .` passes
  - RED: first show the check catches a leak: add `TestMain` with `goleak.VerifyTestMain` to `srvr` (`leak_test.go`), `engine` and `internal/tracectx`, run `GOWORK=off go test -race ./...` and record that `engine` fails naming `TestCheckRoutes.func5` (a real leak); in a throwaway edit, leak a goroutine in `srvr` and in `tracectx` and record each binary's goleak failure. Then `TestLeakCheck` (non-parallel, `goleak.Find` with `leakOptions` and `IgnoreCurrent`), rows: never closed -> reported naming the blocked function; closed at cleanup -> not reported; none -> not reported; ends after 20ms -> not reported; `time.Sleep` with no srvr frame -> reported. Fails to compile until `leakOptions` exists
  - GREEN: `leakOptions` (empty in the core), the three `TestMain`s; fix `engine` `TestCheckRoutes/context ends` to block until a channel the test closes in `t.Cleanup`
  - REFACTOR: Extract Function for the blocked-goroutine fixture shared by the rows, then /simplify
- [x] 2.3 net/http lifecycle leaves no goroutine (test-quality / Scenario: Lifecycle leaves no goroutine behind, net/http rows); verify `GOWORK=off go test -race -run TestLifecycle_Goroutines .` passes
  - RED: `TestLifecycle_Goroutines` (non-parallel), one case per net/http row, using `checkGoroutines(t)` for the "no" rows and `goleak.Find` for the never-`Shutdown` row (reported, naming srvr's serve goroutine). Fails to compile until `checkGoroutines` exists; then run every row and record its result. A "no" row that fails is a srvr defect: keep the failing goleak output as the rule 2 proof and fix it under D6, or record it in `notes.md` and pause
  - GREEN: `checkGoroutines(t)` in the core's `leak_test.go` (first `t.Cleanup`, `IgnoreCurrent` captured at the call); production change only for a proven srvr leak
  - REFACTOR: Parameterize Function for the per-row lifecycle driver, then /simplify
- [x] 2.4 Abandoned hooks and route checks are the only goroutines left (test-quality / Scenario: Abandoned consumer code is the only goroutine left, all rows); verify `GOWORK=off go test -race -run 'TestAbandoned' ./...` passes in the core
  - RED: `TestAbandonedHook_Goroutines` (core, non-parallel; `BeforeDrain` and `AfterDrain` rows) and `TestAbandonedCheck_Goroutines` (`engine`, non-parallel; route check rows): before release `goleak.Find` reports exactly one goroutine whose stack holds the test's hook or check function; after release nothing is reported
  - GREEN: none expected (srvr already abandons and the goroutine ends when the consumer code returns); a failure is a srvr defect handled as in 2.3
  - REFACTOR: Extract Function for the "only this function leaked" assertion, then /simplify
- [x] 2.5 Verify slice end to end: in the core run `GOWORK=off go vet ./...`, `GOWORK=off golangci-lint run ./...`, `GOWORK=off go test -race ./...`, and `GOWORK=off go test -coverprofile=c.out ./... && go tool cover -func=c.out`, confirming coverage of `engine` lifecycle and hooks is unchanged or higher than before the slice; gopls diagnostics on changed files; after each test command, `pgrep -fl '\.test'` shows no binary of this repository
- [x] 2.6 Run /simplify on the slice's changes, re-run the core tests, and commit `test(core): fail tests on leaked goroutines`; verify with `git log -1 --stat`

## 3. Slice: gin and fiber tests fail on leaked goroutines

- [x] 3.1 Leak check in `ginsrvr` and `fibersrvr` (test-quality / Scenario: A goroutine left running fails the module's tests, ginsrvr and fibersrvr rows); verify `GOWORK=off go test -race ./...` passes in both modules
  - RED: add `goleak` and `TestMain` (`leak_test.go`, empty `leakOptions`) to both modules; run and record `fibersrvr` failing on fasthttp's `(*workerPool).Start.func2` and `updateServerDate.func1`; add `TestLeakCheck` rows per module
  - GREEN: `fibersrvr` `leakOptions` with the two `IgnoreAnyFunction` ignores, each commented with its reason and fasthttp v1.78.0
  - REFACTOR: Consolidate Duplicate Conditional Fragments between the two modules' fixtures where a file-local helper suffices, then /simplify
- [x] 3.2 fasthttp ignores are needed and narrow (test-quality / Scenario: Each ignore is needed and narrow, fibersrvr rows of both tables); verify `GOWORK=off go test -race -run TestLeakIgnores .` passes in `fibersrvr`
  - RED: `TestLeakIgnores` (non-parallel): after a fiber server's `Start`/`Shutdown`, `goleak.Find` without options reports each ignored function, with `leakOptions` reports neither; a fiber server never shut down is reported with `leakOptions`, naming srvr's serve goroutine. Run once with one ignore removed and record the failure
  - GREEN: the ignores from 3.1
  - REFACTOR: Extract Function for "stack names function", then /simplify
- [x] 3.3 gin and fiber lifecycles leave no goroutine (test-quality / Scenario: Lifecycle leaves no goroutine behind, gin and fiber rows); verify `GOWORK=off go test -race -run TestLifecycle_Goroutines .` passes in both modules
  - RED: `TestLifecycle_Goroutines` per module with `checkGoroutines(t)` (keep-alive request then `Shutdown` -> no) and `goleak.Find` (never `Shutdown` -> reported); add `checkGoroutines` to the existing non-parallel tests that start servers (`TestDefaultLoggerAfterConstruction` in both, `TestGinDebugWarning*`)
  - GREEN: `checkGoroutines` in each module's `leak_test.go`; production change only for a proven srvr leak (D6)
  - REFACTOR: Parameterize Function, then /simplify
- [x] 3.4 Verify slice end to end: in `ginsrvr` and `fibersrvr` run `GOWORK=off` vet, golangci-lint, `go test -race ./...` and coverage (`engine` adapters and lifecycle unchanged or higher); gopls diagnostics on changed files; `pgrep -fl '\.test'` after each test command shows no binary of this repository
- [x] 3.5 Run /simplify on the slice's changes, re-run both modules' tests, and commit `test(engines): fail gin and fiber tests on leaked goroutines`; verify with `git log -1 --stat`

## 4. Slice: otel module tests fail on leaked goroutines

- [x] 4.1 Leak check in `otelsrvr` and `otelsetup` (test-quality / Scenario: A goroutine left running fails the module's tests, otelsrvr and otelsetup rows); verify `GOWORK=off go test -race ./...` passes in both modules
  - RED: add `goleak`, `TestMain` and `TestLeakCheck` to both; record the run (the probe found no leak: the leak-check rows' never-closed case is the RED)
  - GREEN: empty `leakOptions` unless a dependency goroutine is proven deliberate (then an ignore per D3 and a row in the spec through `/opsx:update`)
  - REFACTOR: /simplify
- [x] 4.2 otel lifecycles leave no goroutine (test-quality / Scenario: Lifecycle leaves no goroutine behind, otel rows); verify `GOWORK=off go test -race -run TestLifecycle_Goroutines .` passes in both modules
  - RED: `TestLifecycle_Goroutines` in `otelsrvr` (traced server, SDK tracer provider shut down) and `otelsetup` (Prometheus listener with a scrape, OTLP HTTP to the test collector, OTLP gRPC to an unreachable endpoint, `New` failing on a bound address, never `Shutdown` -> reported naming the metrics server's serve goroutine); add `checkGoroutines` to the existing non-parallel tests that start listeners or exporters (`TestExportFailure`, `TestFaultyLogger`, `TestSetGlobal`, `TestConfigPrecedence`, `TestConfigPrecedence_OTLP`, `TestScrapeDefaultAddr`, `TestTracingSwitch`, `TestPropagation`, `TestDurationProvider`) where they do
  - GREEN: `checkGoroutines` per module; production change only for a proven srvr leak (D6)
  - REFACTOR: Parameterize Function, then /simplify
- [x] 4.3 Verify slice end to end: in `otelsrvr` and `otelsetup` run `GOWORK=off` vet, golangci-lint, `go test -race ./...` and coverage (`Setup.Shutdown`, `listen`, `stopListener` unchanged or higher); `TestNoSDKDependency` still passes; gopls diagnostics; `pgrep -fl '\.test'` after each test command shows no binary of this repository
- [x] 4.4 Run /simplify on the slice's changes, re-run both modules' tests, and commit `test(otel): fail otelsrvr and otelsetup tests on leaked goroutines`; verify with `git log -1 --stat`

## 5. Slice: parity tests, every-package guard and project rules

- [ ] 5.1 Leak check in `internal/paritytest` (test-quality / Scenario: A goroutine left running fails the module's tests, paritytest rows; Scenario: Each ignore is needed and narrow, paritytest rows); verify `go test -race ./...` passes in `internal/paritytest`
  - RED: add `goleak`, `TestMain` and `TestLeakCheck`; record the run failing on fasthttp and on the gin cyclic handler; `TestLeakIgnores` with the fasthttp rows and the "gin handler blocked on a channel never closed -> reported" row
  - GREEN: fasthttp ignores (as in `fibersrvr`, D3); move the gin cyclic subtest to `internal/paritytest/gincycle` (D4) with its own helpers, a named handler and a `TestMain` ignoring only that handler
  - REFACTOR: Move Function for the moved subtest's helpers, then /simplify
- [ ] 5.2 The gin cyclic ignore is needed and narrow (test-quality / Scenario: Each ignore is needed and narrow, gincycle row); verify `go test -race ./gincycle` passes in `internal/paritytest`
  - RED: in `gincycle`, after the cyclic request `goleak.Find` without options reports the named handler and with the package's options reports nothing; run once without the ignore and record the binary's failure
  - GREEN: the ignore from 5.1
  - REFACTOR: /simplify
- [ ] 5.3 Every test package runs the leak check (test-quality / Scenario: Every test package runs the leak check, all rows); verify `go test -race -run TestEveryTestPackageChecksLeaks .` passes in `internal/paritytest`
  - RED: `TestEveryTestPackageChecksLeaks` with the accepted rows (the repository walk finds all nine packages) and the rejected rows on synthetic sources (no `TestMain`, `TestMain` without `goleak.VerifyTestMain`, no test files, unparsable file); fails until the decision function exists
  - GREEN: the walk and the decision function (D5)
  - REFACTOR: Split Phase (walk, then decide), then /simplify
- [ ] 5.4 Add to `AGENTS.md`'s testing rules (rule 1) that every module's tests run under goleak (`TestMain` calling `goleak.VerifyTestMain`, exceptions only as named, commented ignores) and that a new goroutine started by srvr needs a test proving it ends; add `goroutine-leak-checks` before `scrty-integration` in the change order of `AGENTS.md` and `openspec/config.yaml`; verify by reading the diff
- [ ] 5.5 Verify the change end to end: from every module's directory (`.`, `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup` with `GOWORK=off`; `internal/paritytest`) run vet, golangci-lint, `go test -race ./...` and coverage (unchanged or higher than at `52fdbfc`); then run every module's suite 5 times in the foreground (`go test -race -count=1 ./...`) and record that all passed; `pgrep -fl '\.test'` after each test command shows no binary of this repository; `openspec validate goroutine-leak-checks --strict` passes
- [ ] 5.6 Re-read `notes.md` and make sure every open entry has a target; run /simplify on the slice's changes, re-run the parity tests, and commit `test(paritytest): fail parity tests on leaked goroutines and guard every package`; verify with `git log -1 --stat`

## Workflow follow-up

- Archive the change after the project's review requirements are satisfied (`/opsx:archive`), syncing `test-quality` into `openspec/specs/`.
