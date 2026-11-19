# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (task group 1)
- **Status:** closed: consolidation done 2026-11-19
- **Target:** followups-hardening
- **Context:** 1.1: `git fetch`; the working branch `feat/followups-hardening` and `origin/main` are both at `0463c0f`. Since planning (`8eac64a`) `main` gained only this change's proposal (`d356f55`, merged as `0463c0f`); nothing else was merged or archived. Archived: `core-server`, `gin-and-fiber-engines`, `goroutine-leak-checks`, `otel-observability`, `skills-compliance`, `triage-debug-buffer` (2026-11-19, released as v0.1.0). Active: this change and `scrty-integration` (unmerged, applied after this one). 1.2: every open entry addressed to this change or `unassigned` in the archived changes' and `scrty-integration`'s `notes.md` was read. Re-addressed to this change: `otel-observability` "engine-support 'Engines share one per-request state' exceeds the 500-character requirement limit" (slice 5, D1) and `core-server` "Ask prblm for a composable OnError and an exported status-only sentinel" (part 1 done by prblm v0.2.0 `AddErrorHook` in `triage-debug-buffer`; part 2 is slice 6, D6); their Status and Target were updated in place. The nine `triage-debug-buffer` entries stay adopted as planned. Deferred unchanged, none touching a slice here: every other open `unassigned` entry of `core-server`, `gin-and-fiber-engines` and `otel-observability` (the nearest, "Unmatched 404 problems differ between net/http and gin/fiber", is about the problem body, not record limiting), and `scrty-integration`'s entries (owner decision). Not decided here because they are addressed to archived changes, not to this one or `unassigned`: `gin-and-fiber-engines` entries still open with targets `otel-observability`/`gin-and-fiber-engines`, and `skills-compliance` entries still open with target `triage-debug-buffer`; their stale targets are left for the owner. 1.3: no main spec or code changed since planning (`git diff 8eac64a origin/main` touches only this change, `AGENTS.md` and `openspec/config.yaml`). Every MODIFIED block keeps every scenario of its main requirement (scripted name comparison; added only "Problem-option errors are joined on every engine", "Level of a failure after the response started", "Failures consumer middleware reports are not limited", "Re-exported names"). Code facts in design Context rechecked with gopls and by reading: `engine.ResolveConfig` (`engine/core.go:41`) returns `newSettings`'s error before `newProblems`; `(*RequestState).unmatched` (`engine/unmatched.go:126`) derives from `!Matched && !aborted && Failed && !Panicked` and 404/405; `Server.answerUnmatched` (`server.go:183`); ginsrvr `classify` (`ginsrvr/server.go:196`) clears failures with no `c.Errors`, matched, not panicked; ginprblm v0.2.0 exports `ErrStatusOnly`; fiber v3.7.0 `router.go:834-836` returns `ErrMethodNotAllowed`/`ErrNotFound`; the otelsrvr recover sites flatten with `engine.ValueText` (`otelsrvr.go:200`, `:341`, `:353`; the sites at `:459` and `:602` discard the value and are out of D4's scope) and otelsetup's `validatePath` (`config.go:363`); `ConfigError.Unwrap` returns `[ErrConfig, Cause]` in both; `tracectx.New` reads `crypto/rand` once per trace. Nothing in the artifacts needed refreshing. `openspec validate followups-hardening --strict` passes. All facts were checked against prblm v0.2.0; the owner's planned prblm upgrade (2026-11-19, before slice 2) must recheck the prblm-dependent facts (D2's `newProblems` handling, D6's `ErrStatusOnly`).
- **Evidence:** `git rev-parse HEAD origin/main` → both `0463c0f`; `openspec validate followups-hardening --strict` → `Change 'followups-hardening' is valid`.
- **Suggested action:** none for this change; recheck D2 and D6 after the prblm upgrade.

### Planning 2026-11-19: entries adopted from triage-debug-buffer
- **Status:** closed: recorded
- **Target:** followups-hardening
- **Context:** the owner adopted these open entries of `openspec/changes/archive/2026-11-19-triage-debug-buffer/notes.md` on 2026-11-19 (their Status there now reads `adopted by followups-hardening`):
  - "engine-support main spec fails strict validation on requirement length": slice 5, design D1.
  - "TestShutdownHooks_Faulty 'blocks and ignores its context' is timing-sensitive under load": slice 5, D5.
  - "Problem-option errors are not joined with the other field errors": slice 2, D2.
  - "Unmatched classification of errors from consumer middleware": slice 3, D3.
  - "Recovered panic values that are errors are not kept as `Cause`": slice 4, D4.
  - "prblm v0.2.0 exports ginprblm.ErrStatusOnly": slice 6, D6.
  - "Trace ids cost a crypto/rand system call per request": slice 7, D7.
  - "Debug buffer efficiency items from slice 2 not measured": slice 7, D8.
  - "Fiber copies access values of records the unmatched limit suppresses" (with its `OmitUserAgent` read cost): slice 7, D9.
  - The suggested action of "Release v0.1.0" (RELEASING.md: tidy `internal/paritytest` in the restore commit; `main` CI red between module release commits): tasks 5.6 and 8.5.

  Planning findings to recheck in consolidation:
  - Read in gin's source, not tested: consumer group middleware (`RouterGroup.Use` on the root group) does not run for unmatched paths, because only `Engine.Use` rebuilds the no-route chains. So item 5 can only occur on fiber, whose router returns the shared `fiber.ErrNotFound`/`fiber.ErrMethodNotAllowed` values (v3.7.0). Task 3.1 tests this on every engine.
  - OpenSpec refuses a MODIFIED block that drops a scenario the main spec still has, so the over-long requirements are shortened rather than split (design D1).
  - A scratch-copy dry run of `openspec archive followups-hardening -y` at planning gave `openspec validate --specs --strict`: 16 passed, 0 failed.
  - The AGENTS.md and `openspec/config.yaml` change order now reads `... goroutine-leak-checks -> triage-debug-buffer -> followups-hardening -> scrty-integration` (owner ordering decision 2026-11-19; triage-debug-buffer had already been applied before scrty-integration).
- **Evidence:** `openspec validate followups-hardening --strict` passes at planning; the dry run above.
- **Suggested action:** none; consolidation rechecks the code facts in design Context.
