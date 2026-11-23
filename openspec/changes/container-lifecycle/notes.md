# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (placeholder)
- **Status:** open
- **Target:** container-lifecycle
- **Context:** Replaced by task 1.4 with what was refreshed and adopted.
- **Evidence:** (none yet)
- **Suggested action:** Run task group 1.

### Planning decisions (2026-11-23, owner)
- **Status:** adopted
- **Target:** container-lifecycle
- **Context:** Readiness for running srvr under doger (kartaladev/doger) means conformance only: neither imports the other. Owner chose shape 4 over a `Managed()` wrapper or a background-start option: `Start` returns once serving (breaking), plus `Run`, `Wait`, `HealthCheck`. Change placed after `pluggable-error-renderer` and before `scrty-integration`; release as v0.5.0 in this change.
- **Evidence:** doger `root_start.go:740-790` waits for every starter's `Start` to return; srvr `server.go:76-98` documents `Start` serving until stopped.
- **Suggested action:** none; recorded for consolidation.

### Proposed defaults to confirm at review
- **Status:** open
- **Target:** container-lifecycle
- **Context:** Chosen by default during planning, revisitable before slice 2: `Ready()` stays; `HealthCheck` returns `ErrServerNotStarted` while starting; `Wait` after a failed `Start` returns `nil` (design D4); no `RegisterAll` helper.
- **Evidence:** unverified: planning-time defaults
- **Suggested action:** Confirm or change in task 1.3.

### Triage with doger v0.3.0 (2026-11-23)
- **Status:** adopted (findings folded into proposal, specs, design and tasks on 2026-11-23)
- **Target:** container-lifecycle
- **Context:** Adversarial audit of this change's documents, then each claim tested directly against the latest doger release (v0.3.0, from the module proxy) with srvr `main` at `a833539` (v0.4.0 code), in a scratch module outside the repository (`GOWORK=off`, `replace` to the local srvr checkout).
- **Evidence:** `GOWORK=off go test -count=1 -v ./...` in the scratch module:
  - T1 `TestT1_BlockingStartHangsDogerStart`: FAIL `RootScope.Start did not return within 2s after the server was ready ... blocking Start hangs the container`: confirms the motivation.
  - T2 `TestT2_DogerCancelsStartContextAfterStart` (a starter that runs `srv.Start(ctx)` in a goroutine and returns at `Ready`): FAIL `GET /ping ... connect: connection refused`: doger cancels the `Start(ctx)` context when `RootScope.Start` returns; confirms design D2 is required.
  - T3 `TestT3_ServeReturnsWithoutStop` (listener whose `Accept` returns `net.ErrClosed` while open): net/http PASS (`Start` returned `use of closed network connection`), gin PASS (same), fiber FAIL `Start returned <nil> ... fiber: serving ended with no error and no stop requested`: confirms audit item 1 (design D1 must stop on a clean return) and a pre-existing silent stop on fiber.
  - P1 `TestP1_ShutdownDeadlineReachesService`: PASS: doger passes the `Shutdown` context deadline to `Shutdown(ctx) error`.
  - P2 `TestP2_HealthCheckDetectedByShape`: PASS: `HealthCheck(ctx) error` detected by shape; error text returned verbatim in the health map.
  - P3 `TestP3_StartDeadlineVsIgnoringStarter`: FAIL `StartWithContext returned doger: start aborted: context deadline exceeded after 1.504551542s` with a `100ms` deadline: a starter ignoring its context extends the deadline (documented doger behaviour, not a defect).
  - Unconfirmed, dropped: audit item 7 (client exposure of health text): doger's web UI shows only "Implements Healthchecker" (`doger/http/templates.go:126`), never the error text.
  - Audit items 2, 3, 4, 5, 6, 8, 10, 11 are document defects (untestable rows, a RED step allowed to pass, missing rows, a duplicate guard, missing baseline and release precondition), fixed in the documents; item 8 verified by `internal/paritytest/layout_test.go:163` (`TestCoreDependencies`). Item 9 (`Wait` from a hook deadlocks) concerns code that does not exist yet: hypothesis, kept as documented misuse because detection needs goroutine identity.
- **Suggested action:** T3 moves into `fibersrvr` as the RED test of task 2.5. Rerun T1 and T2 in task 4.4; both must pass after slice 2.

### Fiber treats a listener failure as a normal stop
- **Status:** adopted by container-lifecycle (task 2.5)
- **Target:** container-lifecycle
- **Context:** On v0.4.0, when a `WithListener` listener's `Accept` returns `net.ErrClosed` while nobody stopped the server, fiber's serve loop returns `nil`: `Start` returns `nil` and no `server.failed` is written, unlike net/http and gin.
- **Evidence:** triage T3, fiber row (above).
- **Suggested action:** Report it as "serving ended without a stop" (`srvr.ErrServerClosed`) through the supervisor, design D1.

### Suggestions for doger (outside srvr)
- **Status:** open (not srvr work; for the doger owner)
- **Target:** unassigned (doger repository)
- **Context:** Found while triaging; no doger defect was proven, behaviour matches its docs.
- **Evidence:** T1 (a starter that never returns hangs `RootScope.Start` with no diagnostic), P3 (a `100ms` start deadline took `1.5s` because the starter ignored its context).
- **Suggested action:** (1) log a warning naming a starter still running after a configurable duration, so a blocking starter is diagnosable; (2) optionally, an `InjectorOpts` start-deadline that returns `ErrStartTimeout` naming the stuck service instead of waiting (the goroutine stays, documented as abandoned, as srvr does for hooks); (3) document in `Starter` godoc that servers must return once listening, citing HTTP servers as the common trap.
