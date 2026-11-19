# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (task group 1)
- **Status:** open: placeholder until task 1.4
- **Target:** followups-hardening
- **Context:** to be replaced by the outcome of tasks 1.1-1.3: changes merged or archived since planning, carry-over decisions, and the artifacts refreshed.
- **Evidence:** unverified: consolidation has not run yet.
- **Suggested action:** task 1.4 records the outcome here.

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
