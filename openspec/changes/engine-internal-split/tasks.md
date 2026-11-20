# Tasks

## 1. Consolidation: refresh plan against merged changes

- [ ] 1.1 Sync with the main branch (`git fetch`, rebase or merge `origin/main` into the working branch) and list every change merged or archived since planning on 2026-11-20 (`openspec list --json`, `openspec/changes/archive/`, `git log origin/main`); verify they are named in the completion note (at planning: `main` at `25fa456`, v0.3.0 released, `static-files` complete and not archived, `pluggable-error-renderer` and `scrty-integration` planned and unapplied)
- [ ] 1.2 Read every `notes.md` carry-over (archived changes, `static-files`, `pluggable-error-renderer`, `scrty-integration`) and decide each open entry addressed to `engine-internal-split` or `unassigned` (adopt, defer or close); in particular close `pluggable-error-renderer` "engine could be internal/engine" as adopted by this change; verify none is left undecided
- [ ] 1.3 Dependency map with gopls (design Context, D2, D3): for every top-level declaration of `engine/*.go`, find references (LSP) and record which file and which target package uses it, and every use from another module; turn it into a package graph for D2's table, count the names each candidate package would have to export, and apply the D3 criteria including **merge if thin** (owner decision 2026-11-20: 5 to 7 packages; merge any package with a handful of declarations or under about 150 lines; keep in `internal/engine` anything whose split would export coupling or need a cycle-breaking callback). Record the verified graph and the final package list with the reason for every merge or drop in `design.md` (D2 table) and `notes.md`; verify the graph has no cycle and every final package states its responsibility in one sentence
- [ ] 1.4 Refresh this change's proposal, specs (the test-quality guard order and leak-check rows name the final packages), design (slices for dropped packages removed) and tasks against the main specs, the merged code and 1.3; re-diff the MODIFIED blocks of `engine-support` and `test-quality` against `openspec/specs/`; verify `openspec validate engine-internal-split --strict` passes
- [ ] 1.5 OWNER CONFIRMATION: the change order `engine-internal-split` -> `pluggable-error-renderer` -> `scrty-integration` (AGENTS.md and `openspec/config.yaml` currently end `... static-files -> scrty-integration`, and `pluggable-error-renderer` assumes it goes before `scrty-integration`). Ask the owner; record the answer in `notes.md`; the owner (not this change) updates AGENTS.md's order and `config.yaml` if confirmed; verify the answer is recorded
- [ ] 1.6 OWNER DECISION: release coordination with `pluggable-error-renderer`, which also proposes `v0.4.0` (one shared `v0.4.0` release, or this change as `v0.4.0` and the renderer as `v0.5.0`); record the answer in `notes.md`; verify group 10's tasks match it
- [ ] 1.7 Record the outcome of 1.1-1.6 at the top of this change's `notes.md` (replace the placeholder entry); verify the file lists what was refreshed and adopted, or states that nothing was needed
- [ ] 1.8 Commit the consolidation as `docs(openspec): consolidate engine-internal-split against main`, containing only `openspec/` files; skip it when 1.1-1.7 changed nothing; verify with `git log -1 --stat`

## 2. Benchmark baseline (before any code moves)

- [ ] 2.1 ASK THE OWNER whether another benchmark session is running on the machine and wait for the go-ahead (design D7); verify the go-ahead is recorded in `notes.md`
- [ ] 2.2 Record the baseline at the change's base commit in a worktree: `BenchmarkPipeline` in the root module, `ginsrvr` and `fibersrvr`, and the `engine` benchmarks (`BenchmarkLogHandler`, `BenchmarkCorrelation`, `BenchmarkRedactSecrets`, `BenchmarkDebugBuffer`), `-run '^$' -bench . -benchmem -count=1` run 10 times; keep the raw output in the session scratchpad and the commands, machine, Go version and summary in `notes.md`; verify `benchstat base.txt` prints every row with 10 samples

## 3. Slice: move `engine` to `internal/engine`

- [ ] 3.1 Load `golang-how-to` (and through it project layout, refactoring, testing, naming, documentation); verify the skills are loaded before touching Go code
- [ ] 3.2 Outside modules cannot import the shared pieces; srvr's modules can (engine-support / Scenario: Only srvr's modules import the shared pieces, every row); verify `go test -race -run TestInternalImport ./internal/paritytest/...` passes
  - RED: `TestInternalImport` (design D5), one case per Examples row: a throwaway module in `t.TempDir()` with a `replace` to the working tree, `GOWORK=off`, `GOPROXY=off`, importing the row's path; the srvr-module rows build the real modules. Expected failure: `example.com/consumer` importing `github.com/kartaladev/srvr/engine` builds, and `internal/engine` does not exist
  - GREEN: `git mv engine internal/engine`; update every import with gopls (root package and tests, `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup`, `internal/paritytest`); `internal/benchgate`'s `enginePkg`; no other edit
  - REFACTOR: none beyond import grouping (gofmt/goimports); then `/simplify`
- [ ] 3.3 The leak-check guard and the import-direction guard know the new path (test-quality / Scenario: Every test package runs the leak check, the `internal/engine` row; Scenario: Import direction guard, the slice-2 boundary row and the rejected rows); verify `go test -race -run 'TestLeakGuard|TestImportDirection' ./internal/paritytest/...` (or the root module, wherever D5 places it) passes
  - RED: `TestImportDirection` with the recorded order `[internal/engine]` plus `internal/tracectx`, rows for an unlisted package and an upward import built from fixture import lists; expected failure: undefined guard
  - GREEN: the guard over `go list -deps -f` output; leak guard row for `internal/engine`
  - REFACTOR: share the `go list` invocation with the leak guard if both parse package lists (Extract Function); then `/simplify`
- [ ] 3.4 srvr's aliases are unchanged (engine-support / Scenario: Re-exported names, srvr names are the engine declarations, Configuration errors name their field, every row including the new `ginsrvr.New`/`fibersrvr.New` rows); verify `go test -race -run 'TestAliases|TestSentinels|TestConfigError' . ./ginsrvr/... ./fibersrvr/...` passes
  - RED: add the `ginsrvr.New` and `fibersrvr.New` rows to the existing table tests (expected to pass already: the rows guard against regression; if they fail, that is a defect to record per AGENTS.md rule 2)
  - GREEN: none expected
  - REFACTOR: none
- [ ] 3.5 Tooling and docs follow the path: `Makefile` `cover` filter (`/srvr/internal/` excluding `paritytest` and `benchgate`, printing an internal total) and benchmark output names, `.golangci.yml` depguard comment, `docs/PERFORMANCE.md`, package godoc in `internal/engine/doc.go` (drop "public only because ..."; state it is internal to srvr's modules), AGENTS.md layout paragraph and coverage sentence (correct the wrong reason), `openspec/config.yaml` public-shape note, CHANGELOG `[Unreleased]` **BREAKING** entry naming the removed import path and the `srvr` names to use; verify `make cover` prints the internal total and `make bench` writes its files
- [ ] 3.6 Verify the slice end to end: `make check` (every module, `GOWORK=off`), `make cover`, `go mod tidy -diff` in every module, golden-record and log-safety suites unchanged, `go list -m all` in the core module lists only prblm (engine-support / Scenario: Core dependencies unchanged), gopls diagnostics clean on every changed file
- [ ] 3.7 Run `/simplify` on the slice's changes, apply what holds up, re-run the tests, then commit `refactor(engine)!: move engine to internal/engine`; verify with `git log -1 --stat`

## 4. Slice: extract `internal/errchain`

- [ ] 4.1 Precondition: 1.3 kept `errchain` (with the guards folded in); verify `design.md` D2 lists it, else skip this group and record why in `notes.md`
- [ ] 4.2 The guard pins `errchain` as a leaf (test-quality / Scenario: Import direction guard, the `internal/errchain` rows); verify `go test -race -run TestImportDirection` passes
  - RED: add `internal/errchain` below `internal/engine` in the recorded order and a row asserting the package exists; expected failure: the package is missing
  - GREEN: move `walkChain`, `findInChain`, `GuardChain`, `TypeName`, `ValueText`, `IsNil`, `FuncPointer`, `RedactSecrets`, `fingerprint`, `topFrame` and their tests, fuzz tests and `BenchmarkRedactSecrets` (or the set 1.3 decided) step by step with gopls, `go test -race ./...` in every touched module after each step; `otelsrvr` and `otelsetup` import `errchain`
  - REFACTOR: name smells from the move (a now-exported helper used once: Inline Function back into its caller's package); count exported names against D3.2; then `/simplify`
- [ ] 4.3 Verify: `make check`, `make cover` (no moved function loses coverage), the leak guard lists the package (test-quality leak-check rows), gopls diagnostics clean
- [ ] 4.4 Run `/simplify`, apply what holds up, re-run the tests, then commit `refactor(engine): extract internal/errchain`; verify with `git log -1 --stat`

## 5. Slice: extract `internal/static`

- [ ] 5.1 Precondition: 1.3 kept `static`; verify `design.md` D2 lists it, else skip and record why in `notes.md`
- [ ] 5.2 Move static resolution with its tests behind the guard (test-quality / Scenario: Import direction guard, the `internal/static` row); verify `go test -race -run TestImportDirection` and `go test -race ./...` in the root module, `ginsrvr`, `fibersrvr`, `internal/paritytest` pass
  - RED: add `internal/static` to the recorded order; expected failure: missing package
  - GREEN: move `Static`, `StaticConfig`, `StaticSettings`, `StaticOption`, `StaticResult`, `StaticPrefixes`, `ServeStaticFile` and helpers; `StaticResult.Failure`'s client-closed mapping stays reachable without an import of `internal/engine` (move the mapper's error construction to `errchain` or pass it, per 1.3)
  - REFACTOR: as 4.2; then `/simplify`
- [ ] 5.3 Verify: `make check`, `make cover`, static parity rows in `internal/paritytest` unchanged, gopls diagnostics clean
- [ ] 5.4 Run `/simplify`, apply what holds up, re-run the tests, then commit `refactor(engine): extract internal/static`; verify with `git log -1 --stat`

## 6. Slice: extract `internal/config`

- [ ] 6.1 Precondition: 1.3 kept `config`; verify `design.md` D2 lists it, else skip and record why in `notes.md`
- [ ] 6.2 Move the configuration model with its tests behind the guard (test-quality / Scenario: Import direction guard; engine-support / Scenario: Re-exported names and srvr names are the engine declarations stay green); verify `go test -race ./...` in every module passes
  - RED: add `internal/config` to the recorded order; expected failure: missing package
  - GREEN: move `Config`, `Settings`, `Option`, `Only`, `ConfigError`, `NewConfigError`, sentinels, validation and the declarative types `Settings` holds (D2), step by step; `srvr`'s aliases point at `config`
  - REFACTOR: validation functions that read another concern's settings (`validateLog`, `validateDebugBuffer`, `validateUnmatchedLimit`, `validateObservers`): keep them with the config they validate (Move Function) unless that needs an import of a higher package; then `/simplify`
- [ ] 6.3 Verify: `make check`, `make cover`, construction rejection rows of `engine-support` "Engines share one configuration model" unchanged on every engine, gopls diagnostics clean
- [ ] 6.4 Run `/simplify`, apply what holds up, re-run the tests, then commit `refactor(engine): extract internal/config`; verify with `git log -1 --stat`

## 7. Slice: extract `internal/logging`

- [ ] 7.1 Precondition: 1.3 kept `logging` and its access to the request's trace and buffer passes D3 (no cycle; an accessor that adds no allocation); verify `design.md` D2 lists it, else skip and record why in `notes.md`
- [ ] 7.2 Move the log handler, schema constants, truncation, guarded writer, `LogRecordsLost` and debug buffer storage with their tests and benchmarks behind the guard; verify `go test -race ./...` in every module and the golden-record test (`srvr.log.schema` `1`, every key) pass
  - RED: add `internal/logging` to the recorded order; expected failure: missing package
  - GREEN: move step by step; `RequestState` implements `logging`'s accessor; `otelsetup` imports `logging.LogSchemaVersion` (or the name 1.3 decided)
  - REFACTOR: as 4.2; then `/simplify`
- [ ] 7.3 Quick allocation check before committing (no owner gate needed: `-benchtime=1x` is not a timing run): `go test -run '^$' -bench 'LogHandler|DebugBuffer|Pipeline' -benchmem -benchtime=100x` shows the same `allocs/op` as the baseline in `notes.md`; if not, return to the last green state and keep logging in `internal/engine` (D3)
- [ ] 7.4 Verify: `make check`, `make cover`, log-safety and hostile-value suites unchanged, gopls diagnostics clean
- [ ] 7.5 Run `/simplify`, apply what holds up, re-run the tests, then commit `refactor(engine): extract internal/logging`; verify with `git log -1 --stat`

## 8. Slice: extract `internal/lifecycle`

- [ ] 8.1 Precondition: 1.3 kept `lifecycle` and its needs from `Core` fit a small struct of functions (D3); verify `design.md` D2 lists it, else skip and record why in `notes.md`
- [ ] 8.2 Move the state machine, `Runner`, `HTTPRunner`, guarded listener and hook runner with their tests behind the guard; verify `go test -race ./...` in every module passes, including the lifecycle misuse, shutdown-hook and leak tests of every engine
  - RED: add `internal/lifecycle` to the recorded order; expected failure: missing package
  - GREEN: move step by step; `internal/engine` builds the lifecycle's dependencies from `Core`
  - REFACTOR: as 4.2; then `/simplify`
- [ ] 8.3 Verify: `make check`, `make cover` (`Start` and `Shutdown` hot paths covered), goleak green in every module, gopls diagnostics clean
- [ ] 8.4 Run `/simplify`, apply what holds up, re-run the tests, then commit `refactor(engine): extract internal/lifecycle`; verify with `git log -1 --stat`

## 9. Benchmark comparison, documentation and end-to-end verification

- [ ] 9.1 ASK THE OWNER whether another benchmark session is running and wait for the go-ahead; then run the D7 comparison (test-quality / Scenario: Pipeline benchmark comparison): base worktree vs branch, 10 interleaved runs, `benchstat`; record the table in `notes.md`; STOP for the owner on a regression over 5% `ns/op` with `p < 0.05` or any extra `allocs/op`
- [ ] 9.2 Documentation: AGENTS.md layout paragraph lists the final internal packages (only the layout and coverage text; the change order is the owner's, task 1.5), `docs/PERFORMANCE.md` benchmark table packages, each package's godoc states its responsibility, README only if it mentions `engine` (verify with a search), CHANGELOG `[Unreleased]` complete; verify `go vet ./...` and `go test -run Example ./...` pass in every module
- [ ] 9.3 End-to-end verification locally (GitHub CI is disabled by the owner): `make check` and `make cover` from the root; every hot path in design Hot Paths covered (`go tool cover -func`); `govulncheck ./...` in every module; `openspec validate engine-internal-split --strict`; gopls diagnostics clean
- [ ] 9.4 Run `/simplify`, apply what holds up, re-run the tests, then commit `docs: document the internal package layout`; verify with `git log -1 --stat`

## 10. Release

- [ ] 10.1 Precondition: this change is reviewed and merged to `main`; verify `git log origin/main --oneline -1` contains the merge
- [ ] 10.2 Prepare: move `CHANGELOG.md` `[Unreleased]` under a version heading (placeholder), state that every srvr module must be upgraded together (design D6), run `make check` and `make cover` on `main`, dry-run the `RELEASING.md` steps without tagging; verify every step passes
- [ ] 10.3 STOP for the owner: present the proposed version (likely `v0.4.0`, breaking only for direct `engine` importers; coordinated with `pluggable-error-renderer` per 1.6), the changelog section, the benchmark table and the dry-run results; wait for explicit confirmation of the version and of pushing tags; record the decision in `notes.md`; do not continue without it
- [ ] 10.4 Release in `RELEASING.md` order (core, then `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup`, each requiring the new core), restore development `replace` lines, publish the GitHub Release page, verify every module through the proxy and with a scratch consumer on each engine (and that it cannot import `internal/engine`); record the result in `notes.md`

## Workflow follow-up

- Re-read `notes.md` and make sure every open item has a target or is `unassigned`.
- Archive the change after review; verify `openspec validate --specs --strict` passes after the archive sync.
