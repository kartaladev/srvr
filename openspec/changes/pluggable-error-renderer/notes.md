# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (task group 1)
- **Status:** open: placeholder, replaced by task 1.4
- **Target:** pluggable-error-renderer

### Planning 2026-11-20: owner decisions pending
- **Status:** open
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
- **Status:** open (owner decision)
- **Target:** unassigned (own change, or a slice of pluggable-error-renderer before its new types land in `engine`)
- **Context:** AGENTS.md and `engine`'s package godoc say `engine` is public "because separate modules cannot import core `internal/`". Go's internal rule is by import path, not module: a nested module under `github.com/kartaladev/srvr/` (ginsrvr, fibersrvr, otelsrvr, internal/paritytest) may import `github.com/kartaladev/srvr/internal/...`; only modules outside that path are refused (golang.org/x/tools/gopls does the same with x/tools/internal). Moving `engine` to `internal/engine` would hide it from consumers (no accidental dependency on an unstable API), while `srvr` keeps re-exporting what consumers need as aliases. It does not change the version coupling between engine modules and core (the same as today; mitigated by releasing all modules together). Cost: import changes in every engine module and paritytest, AGENTS.md and godoc corrections, breaking for anyone importing `engine` directly (documented as unsupported before v1).
- **Evidence:** scratch probe (removed from the repo, in the session scratchpad): module `example.com/root` with `internal/x`; nested module `example.com/root/sub` importing `example.com/root/internal/x` with `GOWORK=off`: `go run .` prints `hi`; module `example.com/other`: `main.go:2:16: use of internal package example.com/root/internal/x not allowed`.
- **Suggested action:** owner decides whether and when to move `engine` to `internal/engine`, and AGENTS.md's layout paragraph is corrected either way.

### If engine-internal-split goes first, new types land in internal packages
- **Status:** open
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
