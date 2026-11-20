# Design

## Context

- `engine/` holds about 5,800 lines of production code in 15 files (plus about 3,000 lines of tests) in one public package. It is imported by the root package (`server.go`, `routing.go`, `options.go`, `types.go`, `static.go` and root tests), `ginsrvr`, `fibersrvr`, `otelsrvr` (`ValueText`, `IsNil`), `otelsetup` (`ValueText`, `IsNil`, `GuardChain`, `TypeName`, `LogSchemaVersion`, `RedactSecrets`), `internal/paritytest` and `internal/benchgate` (the package path `github.com/kartaladev/srvr/engine` as a string).
- Tooling names the path too: `Makefile` (`cover` awk filter on `/srvr/engine/`, benchmark output `bench/engine.*`), `.golangci.yml` (depguard comment), `docs/PERFORMANCE.md` (benchmark table, pprof examples), the leak-check guard in `internal/paritytest` and the `test-quality` spec's package list.
- Coupling inside `engine` (planning-time sketch; group 1's verified map and decisions are in D2):
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

Task groups: 1 consolidation, 2 benchmark baseline (no code), 3 to 5 the slices below, 6 comparison and docs, 7 release hand-off.

1. **Consolidation** (group 1): sync, carry-overs, the gopls-verified dependency map and the final package list (applying D3's merge criterion), owner confirmation of the change order.
2. **Move to `internal/engine`**: `git mv engine internal/engine`, imports updated in every module, tooling paths updated, AGENTS.md layout and godoc corrected. Works end to end: every module builds, every test passes, an outside module cannot import it. Releasable alone. Spec: engine-support "marked as such" and "re-exports"; test-quality leak-check rows; the import-direction guard with a one-entry order.
3. **Extract `internal/errchain`**: srvr's error values, chain walking, faulty-value guards, type and value text, redaction, fingerprint, top frame. The leaf every other package uses. Hot path: log record construction (chain walking, redaction).
4. **Extract `internal/static`**: static file resolution and its validation; `StaticResult.Failure` becomes `engine.StaticFailure`; the engines' static adapters import both. Hot path: static serving and its misses.
5. **Benchmark comparison, documentation, verification**.
6. **Release hand-off**: no tag here; the shared v0.4.0 release runs at the end of `pluggable-error-renderer` (owner decision, `notes.md`).

Each extraction slice may be dropped (D3) without affecting the others' order beyond the dependency map; the change is complete and releasable after any slice from 2 on.

## Decisions

### D1. Move first, split second

`git mv engine internal/engine` with only import changes is a slice of its own, so the visible break (the import path) and the internal reshuffle are reviewed and bisected separately. Alternative: split straight into new internal packages; rejected because one slice would then mix a public break with a large refactor.
Failure modes: an importer is missed (build fails: `make check` runs every module with `GOWORK=off`); a string path is missed (benchgate tests, Makefile cover summary, leak guard: each has a test or a visible output checked in tasks 3.2 to 3.5); released ginsrvr/fibersrvr v0.3.0 import `srvr/engine` and would break against a new core: D6.

### D2. Target package set (verified in group 1)

The planning target was 5 to 7 packages. Group 1's dependency map (go/types over the non-test files of `engine/`: 299 cross-file symbol edges, raw output summarised in `notes.md`) applied D3 to every candidate; the owner chose this set on 2026-11-20:

| package | responsibility | imports (srvr) |
|---------|----------------|----------------|
| `internal/tracectx` | (existing) W3C trace context parsing and generation | none |
| `internal/errchain` | srvr's error values (the sentinels, `ConfigError`/`NewConfigError`, `RouteErrorf`) and safe inspection of errors and faulty consumer values (chain walking, `GuardChain`, `IsNil`, `FuncPointer`, `ValueText`, `TypeName`, redaction, fingerprint, top frame) | none (std, prblm) |
| `internal/static` | resolving a request against an `fs.FS` mount, its options and validation | errchain |
| `internal/engine` | assembly: `Core`, `ResolveConfig`, the configuration model, `RequestState`, trace stage, access and triage records, the slog handler and debug buffer, the lifecycle, prblm handler and error hooks, client-closed mapping, unmatched limit, observers, route checks, and the static failure mapping (`StaticFailure`, formerly `StaticResult.Failure`) | all of the above |

Dropped candidates (D3), with the evidence:
- `config`: fails D3.1 and D3.2. `Settings` holds the declarative types of six concerns (the observer family `Observer`, `RequestInfo`, `ResultInfo`, `ErrorKind`, `HeaderReader`, `TraceSource`, `Trace`; `Route`, `Routes`, `RouteCheck`; `Hook`; `ServiceConfig`; `LogSettings`; `DebugBufferConfig`, `UnmatchedLimitConfig`), and `engine` reads unexported pieces that would all become exported (`Config.resolve`, `Config.httpServer`, `Settings.engineOpts`, `parseLevel`, `LogSettings.staticLevel`, `AccessLogConfig.suppressed`, `ServiceConfig.attrs` with the log keys, `listenerAddr`, the defaults).
- `logging`: fails D3.3. The handler reads `RequestState` (`requestFrom`, fields `buffer`, `trace`, `logger`, `core`), the debug buffer reads `aborted`, `failure`, `panic`, `problem`, and validation needs `Settings`; with the configuration model in `engine` it would import `engine` or need accessor interfaces invented to break the cycle.
- `lifecycle`: fails D3.3. It reads `Core` fields (`cfg`, `settings`, `log`, `serviceAttrs`, `unmatched`), the log handler, `checkRoutes`, `exceptionMessage` and 22 log keys.
- `static` as planned needed `NewConfigError`, `RouteErrorf` (in `engine`) and `MarkRouterAnswered` (`RequestState`). It passes after two moves: the error values go to `errchain` (one responsibility: srvr's errors) and `StaticResult.Failure`, the only piece touching request state, becomes `engine.StaticFailure` (4 call sites: `static.go`, `ginsrvr/static.go`, `fibersrvr/static.go`, plus tests).
Failure modes: a move uncovers a reference the map missed (the build fails; the slice stops at its last green state, D3).

### D3. Split criterion: merge if thin, stop if worse

A package is extracted only if all hold, checked in group 1 for every row of D2 and again at the start of its slice:
1. One responsibility, statable in one sentence.
2. A small exported API: the names other packages need, counted from the gopls reference map, are few (guide: under about 15) and are names a reader would expect; unexported cross-file coupling does not turn into many new exported names.
3. No import cycle and no interface or callback invented only to break one.
4. Not thin: a package with only a handful of declarations or under about 150 lines merges into its neighbour.
If a slice violates these while in progress, it returns to the last green state and the concern stays in `internal/engine`, recorded in `notes.md`. Group 1 applied these criteria: `config`, `logging` and `lifecycle` stay in `internal/engine` (D2).

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

(none: group 1 decided the package set, D2.)
