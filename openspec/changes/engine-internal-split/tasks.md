# Tasks

## 1. Consolidation: refresh plan against merged changes

- [x] 1.1 Sync with the main branch (`git fetch`, rebase or merge `origin/main` into the working branch) and list every change merged or archived since planning on 2026-11-20 (`openspec list --json`, `openspec/changes/archive/`, `git log origin/main`); verify they are named in the completion note (at planning: `main` at `25fa456`, v0.3.0 released, `static-files` complete and not archived, `pluggable-error-renderer` and `scrty-integration` planned and unapplied)
- [x] 1.2 Read every `notes.md` carry-over (archived changes, `static-files`, `pluggable-error-renderer`, `scrty-integration`) and decide each open entry addressed to `engine-internal-split` or `unassigned` (adopt, defer or close); in particular close `pluggable-error-renderer` "engine could be internal/engine" as adopted by this change; verify none is left undecided
- [x] 1.3 Dependency map with gopls (design Context, D2, D3): for every top-level declaration of `engine/*.go`, find references (LSP) and record which file and which target package uses it, and every use from another module; turn it into a package graph for D2's table, count the names each candidate package would have to export, and apply the D3 criteria including **merge if thin** (owner decision 2026-11-20: 5 to 7 packages; merge any package with a handful of declarations or under about 150 lines; keep in `internal/engine` anything whose split would export coupling or need a cycle-breaking callback). Record the verified graph and the final package list with the reason for every merge or drop in `design.md` (D2 table) and `notes.md`; verify the graph has no cycle and every final package states its responsibility in one sentence
- [x] 1.4 Refresh this change's proposal, specs (the test-quality guard order and leak-check rows name the final packages), design (slices for dropped packages removed) and tasks against the main specs, the merged code and 1.3; re-diff the MODIFIED blocks of `engine-support` and `test-quality` against `openspec/specs/`; verify `openspec validate engine-internal-split --strict` passes
- [x] 1.5 OWNER CONFIRMATION: the change order `engine-internal-split` -> `pluggable-error-renderer` -> `scrty-integration` (AGENTS.md and `openspec/config.yaml` currently end `... static-files -> scrty-integration`, and `pluggable-error-renderer` assumes it goes before `scrty-integration`). Ask the owner; record the answer in `notes.md`; the owner (not this change) updates AGENTS.md's order and `config.yaml` if confirmed; verify the answer is recorded
- [x] 1.6 OWNER DECISION: release coordination with `pluggable-error-renderer`, which also proposes `v0.4.0` (one shared `v0.4.0` release, or this change as `v0.4.0` and the renderer as `v0.5.0`); record the answer in `notes.md`; verify group 10's tasks match it
- [x] 1.7 Record the outcome of 1.1-1.6 at the top of this change's `notes.md` (replace the placeholder entry); verify the file lists what was refreshed and adopted, or states that nothing was needed
- [x] 1.8 Commit the consolidation as `docs(openspec): consolidate engine-internal-split against main`, containing only `openspec/` files; skip it when 1.1-1.7 changed nothing; verify with `git log -1 --stat`

## 2. Benchmark baseline (before any code moves)

- [x] 2.1 ASK THE OWNER whether another benchmark session is running on the machine and wait for the go-ahead (design D7); verify the go-ahead is recorded in `notes.md`
- [x] 2.2 Record the baseline at the change's base commit in a worktree: `BenchmarkPipeline` in the root module, `ginsrvr` and `fibersrvr`, and the `engine` benchmarks (`BenchmarkLogHandler`, `BenchmarkCorrelation`, `BenchmarkRedactSecrets`, `BenchmarkDebugBuffer`), `-run '^$' -bench . -benchmem -count=1` run 10 times; keep the raw output in the session scratchpad and the commands, machine, Go version and summary in `notes.md`; verify `benchstat base.txt` prints every row with 10 samples

## 3. Slice: move `engine` to `internal/engine`

- [x] 3.1 Load `golang-how-to` (and through it project layout, refactoring, testing, naming, documentation); verify the skills are loaded before touching Go code
- [x] 3.2 Outside modules cannot import the shared pieces; srvr's modules can (engine-support / Scenario: Only srvr's modules import the shared pieces, every row); verify `go test -race -run TestInternalImport ./internal/paritytest/...` passes
  - RED: `TestInternalImport` (design D5), one case per Examples row: a throwaway module in `t.TempDir()` with a `replace` to the working tree, `GOWORK=off`, `GOPROXY=off`, importing the row's path; the srvr-module rows build the real modules. Expected failure: `example.com/consumer` importing `github.com/kartaladev/srvr/engine` builds, and `internal/engine` does not exist
  - GREEN: `git mv engine internal/engine`; update every import with gopls (root package and tests, `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup`, `internal/paritytest`); `internal/benchgate`'s `enginePkg`; no other edit
  - REFACTOR: none beyond import grouping (gofmt/goimports); then `/simplify`
- [x] 3.3 The leak-check guard and the import-direction guard know the new path (test-quality / Scenario: Every test package runs the leak check, the `internal/engine` row; Scenario: Import direction guard, the slice-2 boundary row and the rejected rows); verify `go test -race -run 'TestLeakGuard|TestImportDirection' ./internal/paritytest/...` (or the root module, wherever D5 places it) passes
  - RED: `TestImportDirection` with the recorded order `[internal/engine]` plus `internal/tracectx`, rows for an unlisted package and an upward import built from fixture import lists; expected failure: undefined guard
  - GREEN: the guard over `go list -deps -f` output; leak guard row for `internal/engine`
  - REFACTOR: share the `go list` invocation with the leak guard if both parse package lists (Extract Function); then `/simplify`
- [x] 3.4 srvr's aliases are unchanged (engine-support / Scenario: Re-exported names, srvr names are the engine declarations, Configuration errors name their field, every row including the new `ginsrvr.New`/`fibersrvr.New` rows); verify `go test -race -run 'TestAliases|TestSentinels|TestConfigError' . ./ginsrvr/... ./fibersrvr/...` passes
  - RED: add the `ginsrvr.New` and `fibersrvr.New` rows to the existing table tests (expected to pass already: the rows guard against regression; if they fail, that is a defect to record per AGENTS.md rule 2)
  - GREEN: none expected
  - REFACTOR: none
- [x] 3.5 Tooling and docs follow the path: `Makefile` `cover` filter (`/srvr/internal/` excluding `paritytest` and `benchgate`, printing an internal total) and benchmark output names, `.golangci.yml` depguard comment, `docs/PERFORMANCE.md`, package godoc in `internal/engine/doc.go` (drop "public only because ..."; state it is internal to srvr's modules), AGENTS.md layout paragraph and coverage sentence (correct the wrong reason), `openspec/config.yaml` public-shape note, CHANGELOG `[Unreleased]` **BREAKING** entry naming the removed import path and the `srvr` names to use; verify `make cover` prints the internal total and `make bench` writes its files
- [x] 3.6 Verify the slice end to end: `make check` (every module, `GOWORK=off`), `make cover`, `go mod tidy -diff` in every module, golden-record and log-safety suites unchanged, `go list -m all` in the core module lists only prblm (engine-support / Scenario: Core dependencies unchanged), gopls diagnostics clean on every changed file
- [x] 3.7 Run `/simplify` on the slice's changes, apply what holds up, re-run the tests, then commit `refactor(engine)!: move engine to internal/engine`; verify with `git log -1 --stat`

## 4. Slice: extract `internal/errchain`

- [x] 4.1 Precondition: 1.3 kept `errchain` (with the guards folded in); verify `design.md` D2 lists it, else skip this group and record why in `notes.md`
- [x] 4.2 The guard pins `errchain` as a leaf (test-quality / Scenario: Import direction guard, the `internal/errchain` rows); verify `go test -race -run TestImportDirection` passes
  - RED: add `internal/errchain` below `internal/engine` in the recorded order and a row asserting the package exists; expected failure: the package is missing
  - GREEN: move srvr's error values (`ErrConfig`, `ErrServerStarted`, `ErrServerClosed`, `ErrRouteConfig`, `ErrShutdownHook`, `ConfigError`, `NewConfigError`, `RouteErrorf`) and `walkChain`, `findInChain`, `GuardChain`, `TypeName`, `ValueText`, `IsNil`, `FuncPointer`, `RedactSecrets`, `fingerprint`, `topFrame` with their helpers, tests, fuzz tests and `BenchmarkRedactSecrets` (design D2); `srvr`'s aliases point at `errchain` step by step with gopls, `go test -race ./...` in every touched module after each step; `otelsrvr` and `otelsetup` import `errchain`
  - REFACTOR: name smells from the move (a now-exported helper used once: Inline Function back into its caller's package); count exported names against D3.2; then `/simplify`
- [x] 4.3 Verify: `make check`, `make cover` (no moved function loses coverage), the leak guard lists the package (test-quality leak-check rows), gopls diagnostics clean
- [x] 4.4 Run `/simplify`, apply what holds up, re-run the tests, then commit `refactor(engine): extract internal/errchain`; verify with `git log -1 --stat`

## 5. Slice: extract `internal/static`

- [x] 5.1 Precondition: 1.3 kept `static`; verify `design.md` D2 lists it, else skip and record why in `notes.md`
- [x] 5.2 Move static resolution with its tests behind the guard (test-quality / Scenario: Import direction guard, the `internal/static` row); verify `go test -race -run TestImportDirection` and `go test -race ./...` in the root module, `ginsrvr`, `fibersrvr`, `internal/paritytest` pass
  - RED: add `internal/static` to the recorded order; expected failure: missing package
  - GREEN: move `Static`, `StaticConfig`, `StaticSettings`, `StaticOption`, `StaticResult`, `StaticPrefixes`, `ServeStaticFile` and helpers; `StaticResult.Failure` moves to `internal/engine` as `StaticFailure(ctx, res, allow)` (design D2; call sites in `static.go`, `ginsrvr/static.go`, `fibersrvr/static.go` and tests)
  - REFACTOR: as 4.2; then `/simplify`
- [x] 5.3 Verify: `make check`, `make cover`, static parity rows in `internal/paritytest` unchanged, gopls diagnostics clean
- [x] 5.4 Run `/simplify`, apply what holds up, re-run the tests, then commit `refactor(engine): extract internal/static`; verify with `git log -1 --stat`

## 6. Benchmark comparison, documentation and end-to-end verification

- [ ] 6.1 ASK THE OWNER whether another benchmark session is running and wait for the go-ahead; then run the D7 comparison (test-quality / Scenario: Pipeline benchmark comparison): base worktree vs branch, 10 interleaved runs, `benchstat`; record the table in `notes.md`; STOP for the owner on a regression over 5% `ns/op` with `p < 0.05` or any extra `allocs/op`
- [ ] 6.2 Documentation: AGENTS.md layout paragraph lists the final internal packages (only the layout and coverage text; the change order is the owner's, task 1.5), `docs/PERFORMANCE.md` benchmark table packages, each package's godoc states its responsibility, README only if it mentions `engine` (verify with a search), CHANGELOG `[Unreleased]` complete; verify `go vet ./...` and `go test -run Example ./...` pass in every module
- [ ] 6.3 End-to-end verification locally (GitHub CI is disabled by the owner): `make check` and `make cover` from the root; every hot path in design Hot Paths covered (`go tool cover -func`); `govulncheck ./...` in every module; `openspec validate engine-internal-split --strict`; gopls diagnostics clean
- [ ] 6.4 Run `/simplify`, apply what holds up, re-run the tests, then commit `docs: document the internal package layout`; verify with `git log -1 --stat`

## 7. Release hand-off (no tag in this change)

- [ ] 7.1 Prepare the hand-off (owner decision 2026-11-20: `engine-internal-split` and `pluggable-error-renderer` ship together as v0.4.0, released at the end of `pluggable-error-renderer`): CHANGELOG `[Unreleased]` holds this change's entries including the BREAKING import path and "upgrade every srvr module together" (design D6); `pluggable-error-renderer/notes.md` gets an entry naming the final internal packages and that its renderer types land in `internal/engine`; verify no tag was created (`git tag --contains HEAD` empty)

## Workflow follow-up

- Re-read `notes.md` and make sure every open item has a target or is `unassigned`.
- Archive the change after review; verify `openspec validate --specs --strict` passes after the archive sync.
