# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (task group 1)
- **Status:** closed: done 2026-11-21
- **Target:** pluggable-error-renderer
- **Context:**
  - 1.1: `git fetch`; `main` and `origin/main` at `78c8165`; branch `feat/pluggable-error-renderer` cut from it, nothing to rebase. Since planning (`25fa456`): #23 (this change's and `engine-internal-split`'s proposals, `53ff593`, `88b0437`), #25 `engine-internal-split` applied (`engine` -> `internal/engine`; `internal/errchain`, `internal/static` extracted; `StaticResult.Failure` -> `engine.StaticFailure`; core dependency set pinned), #26 archived it to `archive/2026-11-21-engine-internal-split` and synced `engine-support` and `test-quality` main specs. Active: `static-files` (61/61, not archived), this change, `scrty-integration` (0/45, unapplied). Nothing released since v0.3.0; `engine-internal-split` ships with this change as v0.4.0.
  - 1.2: carry-overs decided:
    - otel-observability "Unmatched 404 problems differ between net/http and gin/fiber": **defer** (owner deferred it 2026-11-19 at static-files; no new owner choice). The default renderer keeps v0.3.0 bodies byte for byte, so the gap stays on the default path; with a custom renderer every engine hands `srvr.ErrNotFound` to the renderer, so its answer is identical. Stays open/unassigned.
    - static-files "gin: a static mount replaces a WithGinEngine hook's NoRoute handlers": **defer**, unaffected. Slice 4 swaps the `e.Use` middleware (`ginprblm.Middleware`/`Recovery` -> srvr's error middleware and recovery) and never calls `NoRoute`/`NoMethod`; the mount's `s.engine.NoRoute(statics.serve)` (`ginsrvr/server.go`) is untouched. Stays open/unassigned.
    - gin-and-fiber-engines "Fiber rejection records build their problem apart from the client's": **defer**, narrowed. With a custom renderer slice 5 records the rejection from the renderer's `ErrorResponse` through `engine.Render`, so record and response match; the default path is unchanged (no behaviour change allowed) and keeps the gap. Stays open/unassigned.
    - core-server "A panicking consumer error hook is contained silently" and "A srvr.problem.note event ...": **defer**; `engine.Render` contains hook panics as today (design D4), adding an event is a schema addition out of scope.
    - Every other open `unassigned` entry in the archived changes, `static-files` and `scrty-integration` is unrelated to error rendering: stays unassigned, unchanged.
    - This change: "Planning 2026-11-20: owner decisions pending" closed (1.6-1.8 answered); "If engine-internal-split goes first ..." and "engine-internal-split applied: final internal packages" adopted (paths re-pointed, below); "scrty-integration must render through ErrorRenderer" already present, stays open for `scrty-integration`; "Later: one render path, configurable limits, net/http 431" stays open/unassigned.
  - 1.3: design Context facts rechecked in the code (gopls/grep): `Core.Problems`, `ProblemOptions`, `onError`, `newProblems`, `ErrorHook` (`internal/engine/core.go`); `failure`, `newFailure`, `prblmNotes`, `recordFailure`, `RecordRejection`, `addFailure` (`internal/engine/access.go`); `clientClosedMapper` (`internal/engine/clientclosed.go`); `traceIDEnricher` (`internal/engine/trace.go`); `GuardChain`, `panicOf` (`internal/engine/triage.go`); `StaticFailure` (`internal/engine/staticfailure.go`); `stages`, `wrapHandler`, `answerUnmatched` (`server.go`); gin `newEngine`, `requestStage`, `guardErrors`, `clearStatusOnly` (`ginsrvr/server.go`); fiber `ErrorHandler`/`Recovery` wiring, `requestStage`, `renderError` (`fibersrvr/server.go`); `otelsrvr` `*prblm.PanicError` lookup (`otelsrvr.go`). All hold except paths. New fact: fiber's static `notFound` (`fibersrvr/static.go`) returns `prblm.ErrNotFound` directly, so it must send `srvr.ErrNotFound` too (design Context, Slices, Hot Paths; task 2.4). The `error-responses` and `triage-error-records` main specs did not change since `25fa456`, so the MODIFIED blocks stand; the `test-quality` ADDED requirement does not collide with main's. `openspec validate pluggable-error-renderer --strict` passes.
  - Refreshed: design (Context paths and baseline, Goals, Slices 2, D1 location and its stale "cannot import internal" reason, typed errors in `internal/errchain`, Hot Paths), proposal (Approach, Public API, Code list, Builds on), error-rendering spec (test hook in `internal/engine`), tasks 2.4, 2.5, 3.2 (packages and paths).
  - 1.6-1.8: answered by the owner on 2026-11-20 (entries "Owner decisions 2026-11-20: change order and release" and "... D8 hook signature, D9 log schema").
- **Evidence:** `openspec validate pluggable-error-renderer --strict`: "Change 'pluggable-error-renderer' is valid"; `git diff 25fa456 origin/main --stat -- openspec/specs/` touches only `engine-support` and `test-quality`.
- **Suggested action:** none.

### Planning 2026-11-20: owner decisions pending
- **Status:** closed: answered 2026-11-20 (tasks 1.6-1.8)
- **Target:** pluggable-error-renderer
- **Context:** Owner chose Option A (pluggable renderer, prblm stays default) on 2026-11-20 and added the benchmark plan (design D10). Pending: change order before `scrty-integration` (task 1.6), `ErrorHook` signature (design D8, task 1.7), `srvr.log.schema` version (design D9, task 1.8).
- **Suggested action:** ask in group 1; benchmarks only after the owner confirms no other benchmark session runs on the machine.

### scrty-integration must render through ErrorRenderer
- **Status:** open
- **Target:** scrty-integration
- **Context:** scrty-integration was planned against prblm (scrty errors to prblm). Once this change lands, security refusals must reach the client through `engine.Render` so a custom renderer formats them too.
- **Suggested action:** its consolidation group refreshes its design against `ErrorRenderer` and the typed errors.

### Later: one render path, configurable limits, net/http 431
- **Status:** open
- **Target:** unassigned
- **Context:** design D2 keeps prblm's adapters for the default path; the 5 s render deadline and 1 MiB body limit are fixed; net/http answers oversized headers with its own plain-text 431 before srvr runs.
- **Suggested action:** revisit with parity and benchmark data after this change.

### engine could be internal/engine: AGENTS.md's reason does not hold
- **Status:** adopted by engine-internal-split (consolidation 2026-11-20)
- **Target:** engine-internal-split
- **Context:** AGENTS.md and `engine`'s package godoc say `engine` is public "because separate modules cannot import core `internal/`". Go's internal rule is by import path, not module: a nested module under `github.com/kartaladev/srvr/` (ginsrvr, fibersrvr, otelsrvr, internal/paritytest) may import `github.com/kartaladev/srvr/internal/...`; only modules outside that path are refused (golang.org/x/tools/gopls does the same with x/tools/internal). Moving `engine` to `internal/engine` would hide it from consumers (no accidental dependency on an unstable API), while `srvr` keeps re-exporting what consumers need as aliases. It does not change the version coupling between engine modules and core (the same as today; mitigated by releasing all modules together). Cost: import changes in every engine module and paritytest, AGENTS.md and godoc corrections, breaking for anyone importing `engine` directly (documented as unsupported before v1).
- **Evidence:** scratch probe (removed from the repo, in the session scratchpad): module `example.com/root` with `internal/x`; nested module `example.com/root/sub` importing `example.com/root/internal/x` with `GOWORK=off`: `go run .` prints `hi`; module `example.com/other`: `main.go:2:16: use of internal package example.com/root/internal/x not allowed`.
- **Suggested action:** owner decides whether and when to move `engine` to `internal/engine`, and AGENTS.md's layout paragraph is corrected either way.

### If engine-internal-split goes first, new types land in internal packages
- **Status:** adopted by consolidation 2026-11-21 (no `internal/problems`: renderer in `internal/engine`)
- **Target:** pluggable-error-renderer
- **Context:** `engine-internal-split` (planned 2026-11-20, proposed to apply before this change, owner confirmation pending) moves `engine` to `internal/engine` and splits it. Under its owner decision "do not oversplit", the prblm handler, error hooks, access and triage records stay together in `internal/engine`; a separate `internal/problems` package exists only if that change's group 1 finds it cohesive. This change's design and tasks name `engine/render.go`, `engine.Render`, `engine.DefaultRenderer` and other `engine/...` paths.
- **Suggested action:** if `engine-internal-split` is applied first, this change's consolidation group re-points every `engine/...` path: the renderer types and `Render` land in `internal/problems` if that package exists, otherwise in `internal/engine` beside the prblm handler; public names stay in `srvr` as aliases.

### Owner decisions 2026-11-20: change order and release
- **Status:** closed: recorded
- **Target:** pluggable-error-renderer
- **Context:** change order confirmed: `static-files` -> `engine-internal-split` -> `pluggable-error-renderer` -> `scrty-integration` (AGENTS.md and openspec/config.yaml updated). `engine-internal-split` and `pluggable-error-renderer` are released together as v0.4.0: the first change's release group does not tag; the release runs once, at the end of `pluggable-error-renderer`, after the owner confirms tags.
- **Suggested action:** group 1 ticks the order and release-coordination tasks against this entry; refresh the release groups accordingly.

### Owner decisions 2026-11-20: D8 hook signature, D9 log schema
- **Status:** closed: recorded (tasks 1.7, 1.8)
- **Target:** pluggable-error-renderer
- **Context:** D8: `ErrorHook` becomes `func(ctx, err error, sent ErrorReport)`, `ErrorReport` a value struct (not a pointer, not an interface), so no allocation and safe to retain; zero report = no answer sent. D9: `srvr.log.schema` stays `1`. Design "D8 and D9 outcome" records the reasons; slice 6 adds a benchmark row with an error hook installed and requires zero added allocs/op.
- **Suggested action:** none.

### engine-internal-split applied: final internal packages
- **Status:** adopted by consolidation 2026-11-21
- **Target:** pluggable-error-renderer
- **Context:** `engine-internal-split` (branch `refactor/engine-internal-split`) moved `engine` to `internal/engine` and extracted only `internal/errchain` (srvr's sentinels, `ConfigError`, `NewConfigError`, `RouteErrorf`, chain walking, guards, value text, redaction) and `internal/static` (static resolution; `engine.StaticFailure` maps its answers to errors). There is no `internal/problems`: the prblm handler, error hooks, access and triage records stay in `internal/engine`. Order pinned by `TestImportDirection` (`internal/paritytest/layout_test.go`): `tracectx` < `errchain` < `static` < `engine`. The two changes share the v0.4.0 release, which runs at the end of this change; `CHANGELOG.md` `[Unreleased]` already holds `engine-internal-split`'s entries.
- **Suggested action:** this change's consolidation re-points every `engine/...` path in its design and tasks to `internal/engine/...` (renderer types and `Render` beside the prblm handler); a new sentinel goes to `internal/errchain`; adds `internal/...` packages only through `internalOrder` and the leak-walk list.
