# Design

## Context

- `engine/` holds about 5,800 lines of production code in 15 files (plus about 3,000 lines of tests) in one public package. It is imported by the root package (`server.go`, `routing.go`, `options.go`, `types.go`, `static.go` and root tests), `ginsrvr`, `fibersrvr`, `otelsrvr` (`ValueText`, `IsNil`), `otelsetup` (`ValueText`, `IsNil`, `GuardChain`, `TypeName`, `LogSchemaVersion`, `RedactSecrets`), `internal/paritytest` and `internal/benchgate` (the package path `github.com/kartaladev/srvr/engine` as a string).
- Tooling names the path too: `Makefile` (`cover` awk filter on `/srvr/engine/`, benchmark output `bench/engine.*`), `.golangci.yml` (depguard comment), `docs/PERFORMANCE.md` (benchmark table, pprof examples), the leak-check guard in `internal/paritytest` and the `test-quality` spec's package list.
- Coupling inside `engine` (a planning-time grep sketch; group 1 replaces it with a gopls-verified map):
  - `*Core` has methods in nearly every file (`access.go`, `debugbuffer.go`, `lifecycle.go`, `observer.go`, `route.go`, `trace.go`, `triage.go`): it is the assembly point and stays in `internal/engine`.
  - `RequestState` (`trace.go`) holds fields of four concerns: `rec *responseRecorder` and `failure *failure` (access), `buffer *requestBuffer` (debug buffer), `ends []endFunc` (observers), plus `core *Core`. The log handler reads it (`requestOf`, `bufferFor`, `traceOf`). It cannot be a leaf package without exporting all of that.
  - `Settings` (`config.go`) embeds or holds `LogSettings`, `AccessLogConfig`, `TraceConfig`, `ServiceConfig`, `Hook`, `ErrorHook`, `RouteCheck`, `Observer`, `DebugBufferConfig`, `UnmatchedLimitConfig`: the config package must own these declarative types or sit above the packages that declare them.
  - `Lifecycle` (`lifecycle.go`) reaches `l.core.settings.Listener`, `l.core.cfg`, `l.core.logEvent`, `l.core.checkRoutes`, `l.core.runAfterDrain`, `l.core.exceptionMessage`.
  - Leaves with no `*Core` dependency: error-chain walking and guards (`walkChain`, `findInChain`, `GuardChain`, `TypeName`, `ValueText`, `IsNil`, `FuncPointer`, `RedactSecrets`, `fingerprint`, `topFrame`), and most of `static.go` (resolution; `StaticResult.Failure` uses prblm and the client-closed mapping).
- Owner decision 2026-11-20 (recorded in `notes.md`): do not oversplit; 5 to 7 cohesive packages; merge thin ones.

## Goals / Non-Goals

**Goals:**
- Hide the unstable pipeline API (`internal/engine`), with `srvr`'s aliases unchanged.
- Split it into a few packages, each with one clear responsibility and a small exported API, whose dependency direction is pinned by a test.
- No behaviour, log schema, default, dependency-set or performance change.

**Non-Goals:**
- No behavioural fix or cleanup found while moving code (recorded in `notes.md`).
- No public API for third-party engines.
- No renaming of public `srvr` names; no change to the root package's own layout beyond imports.
- Not every concern gets a package: per-request state, access and triage records, the prblm handler and hooks, observers and route checks stay together in `internal/engine`.

## Slices

Task groups: 1 consolidation, 2 benchmark baseline (no code), 3 to 8 the slices below, 9 comparison and docs, 10 release.

1. **Consolidation** (group 1): sync, carry-overs, the gopls-verified dependency map and the final package list (applying D3's merge criterion), owner confirmation of the change order.
2. **Move to `internal/engine`**: `git mv engine internal/engine`, imports updated in every module, tooling paths updated, AGENTS.md layout and godoc corrected. Works end to end: every module builds, every test passes, an outside module cannot import it. Releasable alone. Spec: engine-support "marked as such" and "re-exports"; test-quality leak-check rows; the import-direction guard with a one-entry order.
3. **Extract `internal/errchain`**: chain walking, faulty-value guards, type and value text, redaction, fingerprint, top frame. The leaf every other package uses. Hot path: log record construction (chain walking, redaction).
4. **Extract `internal/static`**: static file resolution and its validation; the engines' static adapters import it. Hot path: static serving and its misses.
5. **Extract `internal/config`**: `Config`, `Settings`, `Option`, `Only`, `ConfigError`, sentinels, validation and the declarative types `Settings` holds. Construction is its hot path.
6. **Extract `internal/logging`**: the slog handler, schema constants, truncation, guarded writer, `LogRecordsLost`, the debug buffer. Hot path: every log record.
7. **Extract `internal/lifecycle`**: the state machine, `Runner`, `HTTPRunner`, guarded listener, hooks runner. Hot path: `Start` and `Shutdown`.
8. **Benchmark comparison, documentation, verification**.
9. **Release** (owner STOP).

Each extraction slice may be dropped (D3) without affecting the others' order beyond the dependency map; the change is complete and releasable after any slice from 2 on.

## Decisions

### D1. Move first, split second

`git mv engine internal/engine` with only import changes is a slice of its own, so the visible break (the import path) and the internal reshuffle are reviewed and bisected separately. Alternative: split straight into new internal packages; rejected because one slice would then mix a public break with a large refactor.
Failure modes: an importer is missed (build fails: `make check` runs every module with `GOWORK=off`); a string path is missed (benchgate tests, Makefile cover summary, leak guard: each has a test or a visible output checked in tasks 3.2 to 3.5); released ginsrvr/fibersrvr v0.3.0 import `srvr/engine` and would break against a new core: D6.

### D2. Target package set (5 to 7 packages)

| package | responsibility | imports (srvr) |
|---------|----------------|----------------|
| `internal/errchain` | walking error chains safely, guarding faulty consumer values (`IsNil`, `FuncPointer`, `ValueText`, `TypeName`), redaction, fingerprint, top frame | none (std, prblm) |
| `internal/static` | resolving a request against an `fs.FS` mount, its options and validation | errchain |
| `internal/config` | the configuration model: `Config`, `Settings`, `Option`, validation, `ConfigError`, sentinels, and the declarative types `Settings` holds (log, access, trace, service, debug buffer, unmatched limit, hooks, route check, observer interfaces) | errchain |
| `internal/logging` | the slog handler, schema version and keys, truncation, guarded writer, debug buffer storage and policy | errchain, config, `internal/tracectx` |
| `internal/lifecycle` | the server state machine, runner, guarded listener, shutdown hook runner | errchain, config, logging |
| `internal/engine` | assembly: `Core`, `ResolveConfig`, `RequestState`, trace stage, access and triage records, prblm handler and error hooks, client-closed mapping, unmatched limit, observers, route checks | all of the above |

The prompt's sketch (`guard`, `errchain`, `config`, `reqstate`, `logging`, `problems`, `access`, `observe`, `routes`, `lifecycle`, `static`, `engine`) is reduced by the owner's decision: `guard` folds into `errchain` (the guards exist to format faulty values, which is what errchain does); `routes` and `observe` are mostly declarative types that `Settings` holds, so their types fold into `config` and their `*Core` behaviour stays in `engine`; `reqstate`, `access` and `problems` stay in `engine`, because `RequestState` carries their state and the prblm hook feeds the access record through a callback that `engine` wires (splitting them would export that coupling for no reuse).
Failure modes: the graph group 1 verifies differs (a cycle, e.g. config needing logging's handler type): D3 applies, the table is updated in this design and the test-quality guard order before slice 3.

### D3. Split criterion: merge if thin, stop if worse

A package is extracted only if all hold, checked in group 1 for every row of D2 and again at the start of its slice:
1. One responsibility, statable in one sentence.
2. A small exported API: the names other packages need, counted from the gopls reference map, are few (guide: under about 15) and are names a reader would expect; unexported cross-file coupling does not turn into many new exported names.
3. No import cycle and no interface or callback invented only to break one.
4. Not thin: a package with only a handful of declarations or under about 150 lines merges into its neighbour.
If a slice violates these while in progress, it returns to the last green state and the concern stays in `internal/engine`, recorded in `notes.md`. `logging` and `lifecycle` are the likely candidates: `logging` needs to read the request's trace and buffer from `RequestState` (an accessor interface defined in `logging` and implemented by `RequestState`, checked for allocations by the benchmarks); `lifecycle` needs `Core`'s logging and route checks (passed as a small struct of functions). If either costs more than it buys, it stays.

### D4. Behaviour-preserving moves

Every extraction is Move Function / Move Field / Extract Class from Fowler's catalog, in small steps: move the leaf declarations with their tests (internal tests move with them; external tests keep their assertions and change imports only), re-point callers with gopls rename/move, keep `internal/engine` compiling after each step, run `go test -race ./...` in every touched module after each step. No test assertion changes except import paths and qualified names; a needed assertion change is a behaviour change and goes to `notes.md`. TDD for a refactor: the RED step is the structure guard (D5) failing for the not-yet-extracted package; GREEN is the move.
Failure modes: an unexported identifier must become exported (counted against D3.2); an internal test relied on unexported state of two packages (split the test, or keep the state in one package); benchmarks move with their code and keep their names, so `benchgate`/`PERFORMANCE.md` comparisons still pair rows.

### D5. Import-direction guard

A test in the root module (`internal_layout_test.go`, or in `internal/paritytest` beside the leak guard) runs `go list -deps -f '{{.ImportPath}} {{.Imports}}'` (no new dependency; `golang.org/x/tools/go/packages` only if `go list` proves insufficient, and then only in a test module) over `./internal/...` and checks each import against a recorded order (D2's table). It fails naming both packages, and fails for an internal package missing from the order. Test-only imports are excluded. A second check (task 3.2) builds a throwaway module outside the path in `t.TempDir()` importing `internal/engine` with `GOWORK=off` and a `replace` to the working tree, and expects `use of internal package ... not allowed`; and a nested module (`internal/paritytest`) builds against it. Spec: test-quality "Internal packages depend in one direction", engine-support "Only srvr's modules import the shared pieces".
Failure modes: `go` not on PATH in the test (skip with a reason only under `-short`; `make check` always has it); a module proxy lookup in the throwaway build (`GOFLAGS=-mod=mod`, `GOPROXY=off`, `GONOSUMDB`, the replace covers prblm through the module cache; if it cannot run offline it is labelled unverified per AGENTS.md rule 2 and the check is run by hand in task 9.3).

### D6. Release all modules together

Released `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup` v0.3.0 import `github.com/kartaladev/srvr/engine`. A consumer that upgrades core to v0.4.0 but keeps ginsrvr v0.3.0 gets `no required module provides package github.com/kartaladev/srvr/engine`. This is the same coupling the engine modules already have; mitigation: release every module in one release in RELEASING.md order (core first), state in CHANGELOG that all srvr modules must be upgraded together, and require core `>= v0.4.0` in each module's `go.mod`.

### D7. Benchmark comparison

Baseline before slice 2 at the change's base commit, comparison after the last extraction slice: `BenchmarkPipeline` in the root module, `ginsrvr` and `fibersrvr`, plus the `engine` benchmarks (`BenchmarkLogHandler`, `BenchmarkCorrelation`, `BenchmarkRedactSecrets`, `BenchmarkDebugBuffer`) under their new packages, `-run '^$' -bench . -benchmem -count=1` alternating base and branch 10 times, `benchstat`. Stop for the owner on a regression over 5% `ns/op` with `p < 0.05`, or any extra `allocs/op` (test-quality "The refactor keeps the pipeline's performance"). Run locally only (GitHub CI is disabled by the owner); ask the owner before each run in case another benchmark session is active. Cross-package calls replacing same-package calls can lose inlining; if a hot function stops inlining (`-gcflags=-m` on the hot functions), that is the first suspect.

## Defaults

None change. No new option, no config field; there is no override because there is no new behaviour. The `srvr` aliases keep every type a consumer configures.

## Hot Paths

Unchanged code, moved: request pipeline per engine (trace stage, access stage, observer begin/end, prblm recovery and hook, unmatched limit), route registration and checks, `Start`/`Shutdown`, log record construction (handler `Handle`, truncation, chain walking, redaction, debug buffer flush), static resolution. Each keeps its existing tests through every slice; coverage is checked after each slice with `make cover` (whose summary now covers `internal/...`) so no moved function loses coverage.

## Risks / Trade-offs

- [Released engine modules break against a new core] → D6: release together, CHANGELOG, minimum versions.
- [Exported surface grows when unexported coupling crosses packages] → D3.2 counts it; a slice stops rather than export coupling.
- [Inlining lost across packages] → D7 benchmarks, `-gcflags=-m`.
- [Merge conflicts with `pluggable-error-renderer` and `scrty-integration`, planned against `engine/`] → apply this change first (owner confirmation 1.5); their consolidation groups re-point paths; a note in `pluggable-error-renderer/notes.md`.
- [Coverage summary silently drops packages] → Makefile `cover` filter changes from `/srvr/engine/` to `/srvr/internal/` (excluding `paritytest`, `benchgate`), verified by its printed total.
- [Someone outside imports `engine` despite the docs] → CHANGELOG names the break and the `srvr` aliases to use instead.

## Migration Plan

Consumers: no change unless they imported `github.com/kartaladev/srvr/engine`; they switch to the `srvr` names (`srvr.Config`, `srvr.ErrConfig`, ...). Upgrade every srvr module to the same release. Rollback: the previous release keeps working; the change is one commit per slice, so any slice can be reverted on its own while the earlier slices stay.

## Open Questions

- Whether `logging` and `lifecycle` pass D3 is decided with the gopls map in group 1 and re-checked at their slice; the outcome changes only whether those slices run, not the others.
