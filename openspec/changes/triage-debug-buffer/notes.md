# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (task group 1, 2026-11-18)
- **Status:** closed: recorded
- **Target:** triage-debug-buffer
- **Context:** 1.1: `git fetch`; `main` equals `origin/main` at `5d3b678`. Archived since this change was planned: `core-server` (2026-11-17), `gin-and-fiber-engines`, `otel-observability`, `goroutine-leak-checks` (2026-11-18). `scrty-integration` is still active and unmerged; this change is applied before it by owner choice and does not depend on it. 1.2: every open entry addressed to this change or `unassigned` in the archived changes' and `scrty-integration`'s `notes.md` was read. Adopted: the two goroutine-leak-checks carry-overs (named serve goroutine, build-tag guard) as new task group 7. Deferred unchanged, none being about request logging: all other open `unassigned` entries of `core-server`, `gin-and-fiber-engines`, `otel-observability` and `scrty-integration`. In this file, "Summary attributes belong in triage-error-records" is closed (kept here) and "Correlation keys may nest under WithGroup" is closed as verified. 1.3: checked with gopls against `5d3b678`: the wrapper is one handler, `engine.logHandler`, doing correlation, truncation (`truncator`, reusable by the buffer in the same package) and the guard, with `LogWriteErrors()`; `trace_id` stays top-level by step replay; `Core.writeAccess` is the single outcome point for net/http, gin (`LogAccess`) and fiber (`WriteAccess`), including the `http.ErrAbortHandler` path; `OnError` only records (`recordFailure`); fiber uses `SetContext`, not locals; `srvr.log.schema` is `1`; the golden test is `TestLogSchemaGolden` (`logconfig_test.go`); `triage-error-records` and `structured-logging` are main specs. Refreshed: proposal Impact (merged names), design Context, D2 (the buffer is a `RequestState` field; the marker is a top-level attribute added on replay through the entry's `logHandler`, replacing the separate marked chain; truncation at append reuses `truncator`; own records are never buffered; fiber call-time context), D3 (`writeAccess`, `failure.problem.Status`, aborted counts as panicked, seal before the access record, discard when the access record's level is disabled), D5, D6; spec: the flush requirement and a Suppressed access record row for a disabled access-record level; tasks 2.3, 4.3, 5.1 and new group 7. `openspec validate triage-debug-buffer --strict` passes.
- **Evidence:** `git log --oneline origin/main..main` is empty; `openspec list --json`; gopls document symbols and references on `engine/log.go`, `engine/access.go`, `engine/trace.go`, `fibersrvr/server.go`.
- **Suggested action:** none.


### Carried over from goroutine-leak-checks: srvr's serve goroutine is an anonymous closure
- **Status:** closed: fixed by skills-compliance (task 3.4: `(*Lifecycle[C]).serveOn`, leak rows of core, ginsrvr and fibersrvr match it; `internal/paritytest` has no serve row)
- **Target:** triage-debug-buffer (owner decision, 2026-11-18)
- **Context:** the "never `Shutdown`" leak rows of every engine identify srvr's serve goroutine by its frame `engine.(*Lifecycle[...]).serve.func1` (`go func() { r.served <- runner.Serve(ln) }()` in `engine/lifecycle.go`). The rows match the prefix `engine.(*Lifecycle[...]).serve`, so moving the goroutine into another function would break them with no behaviour change. Full entry: `openspec/changes/archive/2026-11-18-goroutine-leak-checks/notes.md`. Since commit 0470d34 the lifecycle goroutine tests live in each module's `leak_test.go`.
- **Evidence:** `TestLifecycle_Goroutines/one_route,_Start_and_never_Shutdown` reports `github.com/kartaladev/srvr/engine.(*Lifecycle[...]).serve.func1`.
- **Suggested action:** add a small slice: give the serve goroutine a named unexported method (for example `(*Lifecycle[C]).serveOn`) and match that name in the leak rows of every engine. Internal only: no public API or behaviour change.

### Carried over from goroutine-leak-checks: the every-package guard accepts a TestMain behind a build tag
- **Status:** adopted by triage-debug-buffer (consolidation 2026-11-18: task 7.2)
- **Target:** triage-debug-buffer (owner decision, 2026-11-18)
- **Context:** the guard `TestEveryTestPackageChecksLeaks` (`checkLeakCheck`, now in `internal/paritytest/leak_test.go` since commit 0470d34) parses every `_test.go` file regardless of build constraints, so a package whose only goleak `TestMain` sits behind `//go:build integration` is accepted although a plain `go test` runs without the leak check. No package does this today. Full entry: `openspec/changes/archive/2026-11-18-goroutine-leak-checks/notes.md`.
- **Evidence:** a temporary test, `TestGuardRejectsTestMainBehindBuildTag`, failed with "checkLeakCheck accepted a package whose only TestMain is behind //go:build integration" (not kept).
- **Suggested action:** add a small slice: evaluate each file's `//go:build` line with `go/build/constraint` against the default build context and ignore excluded files when looking for `TestMain`; restore the reproducing test as the RED step. Test-only change.

### Summary attributes belong in triage-error-records
- **Status:** closed: kept in this change's capability (consolidation 2026-11-18). `triage-error-records` is a main spec now, but its requirements describe the error of an `http.request.failed` record; the `srvr.debug_buffer.*` keys describe the buffer, exist only when buffering is enabled, and also appear on an `http.request.completed` record for a directly written `5xx`. A MODIFIED delta would restate a requirement none of whose rows changes.
- **Target:** triage-debug-buffer
- **Context:** srvr.debug_buffer.* and srvr.buffered were ADDED in this change's own capability because triage-error-records was not archived at planning time.
- **Evidence:** unverified: planning-time inconsistency found when the five changes were drafted in parallel
- **Suggested action:** If core-server is archived, move them to a MODIFIED delta of triage-error-records.

### Buffer must plug into srvr/engine
- **Status:** adopted by triage-debug-buffer (planning audit 2026-11-17: design D3/D6 and task 6.1 put buffer creation and the flush in the `srvr/engine` access stage, reading `engine.RequestState`)
- **Target:** triage-debug-buffer
- **Context:** Engine parity slice assumed core options; gin-and-fiber-engines introduces public srvr/engine.
- **Evidence:** unverified: planning-time inconsistency found when the five changes were drafted in parallel
- **Suggested action:** Re-express engine wiring through srvr/engine as merged.

### Audit 2026-11-17: slog probes behind the design fixes
- **Status:** closed: fixes applied to spec, design and tasks during planning audit
- **Target:** triage-debug-buffer
- **Context:** An adversarial planning audit found four slog behaviours the design got wrong (marker under groups, replay context, lazy/mutable values, per-record memory) and fixed D2-D5, the Derived loggers/Flush/Invalid limits scenarios and tasks 2.x, 3.1, 5.1.
- **Evidence:** probe `go run .` in the session scratchpad `probes/slogbuf` (Go 1.27.4): P1 `base.WithGroup("db").WithAttrs(table)` handling a clone with `srvr.buffered` added -> `{"db":{"table":"users","srvr.buffered":true}}`; fix probe `probes/slogbuf-fix` (`base.WithAttrs(marker).WithGroup("db")...`) -> `{"srvr.buffered":true,"db":{"table":"users"}}`. P2 a ctx-reading handler replayed with the request ctx prints `span_id":"root-span"` for a record logged with a child ctx (`child-span` when written immediately). P3 `slog.Any("user", &u)` retained, `u.Name` changed afterwards -> flushed `{"Name":"bob"}`. P4 JSONHandler at `INFO` writes a `DEBUG` record passed to `Handle` directly. P5 `unsafe.Sizeof(slog.Record{}) = 288`. Source: `log/slog/handler.go` "It will only be called when Enabled returns true"; `log/slog/record.go` "Copies of a Record share state"; fiber v3.7.0 `app.go:1549` `conn := new(testConn)` per `app.Test` call.
- **Suggested action:** none beyond the applied fixes; keep the probes' cases as RED tests in tasks 2.3, 5.1 and 3.1.

### Correlation keys may nest under WithGroup in core-server
- **Status:** closed: verified in consolidation 2026-11-18 - `engine.logHandler.target` (`engine/log.go`) adds `trace_id`/`span_id` with `WithAttrs` on the base handler before replaying the recorded steps; main spec structured-logging "Correlation keys stay top-level under groups" pins it
- **Target:** core-server
- **Context:** core-server D7 adds `trace_id`/`span_id` in the correlation handler. If that is done with `Record.AddAttrs` inside `Handle`, a `WithGroup("db")` logger renders `db.trace_id` (same mechanism as probe P1), breaking structured-logging "every record carries `trace_id`" and this change's Derived loggers rows.
- **Evidence:** unverified: core-server code is not written yet; mechanism proven by probe P1 above.
- **Suggested action:** Consolidation task 1.3 checks the merged correlation handler with a `WithGroup` test; if it nests, write a fix entry for core-server's engine pipeline and use the same "attrs before groups" chain as D2.

### Flush when the access record is suppressed
- **Status:** adopted by triage-debug-buffer (owner decision D24, 2026-11-17: discard; Scenario: Suppressed access record, task 4.3)
- **Target:** triage-debug-buffer
- **Context:** `AccessLogConfig` skip paths and `Disabled` suppress access records below `500`. With `FlushAtStatus(400)`, a `404` on a skipped path would flush records that precede no access record.
- **Evidence:** unverified: specification gap; no scenario defines it.
- **Suggested action:** Owner decides: flush anyway, or discard when the access record is suppressed (suggested: discard, add a row to Flush threshold).

### Panic with http.ErrAbortHandler
- **Status:** adopted by triage-debug-buffer (owner decision D24, 2026-11-17: flush; Unusual endings row, task 6.3)
- **Target:** triage-debug-buffer
- **Context:** core-server re-raises `http.ErrAbortHandler` to abort the connection; `httputil.ReverseProxy` uses it on upstream copy failures. The flush rule "recovered panic" does not say whether this counts.
- **Evidence:** unverified: specification gap.
- **Suggested action:** Owner decides; suggested: flush (it is an aborted, failed request) and add an Unusual endings row.

### Programmatic options for buffer level and limits
- **Status:** adopted by triage-debug-buffer (owner decision D13, 2026-11-17: `WithDebugBuffer(opts ...DebugBufferOption)` with `BufferLevel`, `FlushAtStatus`, `MaxRecords`, `MaxBytes`, `TotalBytes`, plus `WithDebugBufferConfig`; Configuration sources rows, task 4.1)
- **Target:** triage-debug-buffer
- **Context:** Only `WithDebugBuffer()` and `FlushAtStatus(n)` are named; `Level`, `MaxRecords`, `MaxBytes`, `TotalBytes` are set only through `DebugBufferConfig`. AGENTS.md rule 7 asks for both functional options and config structs.
- **Evidence:** unverified: API-shape gap in proposal/design Defaults table.
- **Suggested action:** Owner picks: `WithDebugBuffer(opts ...DebugBufferOption)` with `BufferLevel`, `MaxRecords`, `MaxBytes`, `TotalBytes` options, or `WithDebugBufferConfig(DebugBufferConfig)`; then add rows to Configuration sources.

### Tie-break order for flushed records
- **Status:** closed: owner decision D24, 2026-11-17 - no sequence key; recorded in design D3
- **Target:** triage-debug-buffer
- **Context:** Stream order is not time order (design D3); consumers and AI triage order a `trace_id` by `time`. Records logged in the same clock tick, or from concurrent goroutines, tie.
- **Evidence:** unverified: design consequence, no defect.
- **Suggested action:** Owner decides whether to add a per-request sequence key (for example `srvr.buffer.seq`) to flushed records - a log-schema addition.

### Buffer level validation with LevelVar or a consumer handler
- **Status:** closed: owner decision D24, 2026-11-17 - keep the current check (reject only a level above a static log level)
- **Target:** triage-debug-buffer
- **Context:** The audit resolved the conflict between "Level must be below the log level" and the Level routing row `DEBUG`/`DEBUG` by rejecting only a level above a static log level; with `WithLogLevelVar` or `WithLogHandler` no check is possible.
- **Evidence:** unverified: textual contradiction between Defaults table and Level routing row (fixed).
- **Suggested action:** Owner confirms, or prefers rejecting `Level` equal to the log level (then change the Level routing row).

### Oversized single record
- **Status:** closed: owner decision D24, 2026-11-17 - drop and count; recorded in design D4
- **Target:** triage-debug-buffer
- **Context:** A record larger than `MaxBytes` is dropped and counted. AWS Powertools and .NET log buffering instead emit it immediately, unbuffered.
- **Evidence:** unverified: design choice compared with external practice.
- **Suggested action:** Owner confirms drop (safer for volume) or switches to immediate write.

### Planning audit 2026-11-17 decisions
- **Status:** closed: applied to proposal, design, spec and tasks during planning
- **Target:** triage-debug-buffer
- **Context:** The cross-change audit's owner decisions applied here: D1 (core-server keeps `trace_id` top-level under `WithGroup` by replaying recorded steps; the marked chain reuses that rule), D7 (core-server's wrapper truncates every string attribute; this change truncates at buffer time with the same function so byte limits count truncated sizes, replacing the old "truncating `ReplaceAttr`" assumption), single access-record writer (`OnError` only records into `engine.RequestState`; the access stage flushes, then writes), D6 (additive keys keep `srvr.log.schema`; the earlier bump was removed), D13 (options and `WithDebugBufferConfig`), D24 (five buffer details), D25 (task 1.5 commits consolidation). D2 (`error.type` `panic` for panics) touches no row in this change. Slice-end tasks now run vet, golangci-lint and `-race` with `GOWORK=off`.
- **Evidence:** unverified: planning decisions, no code yet; the `WithGroup` defect is proven by probe `probes/xgroup` (see the core-server entry above).
- **Suggested action:** none; consolidation 1.3 checks these against the merged core-server and engines code.

### Planning audit 2 2026-11-17
- **Status:** closed: applied to spec, design and tasks during planning
- **Target:** triage-debug-buffer
- **Context:** The second audit's fixes applied here: M2 (`DebugBuffer` stays nested in `LogConfig`; a later `WithLogConfig` replaces it, pinned by two Configuration sources rows and godoc in task 4.5), M6/A3 (an `http.ErrAbortHandler` panic flushes before core-server's access record `http.request.failed` `WARN`, `error.kind`/`error.type` `panic`, `srvr.response.aborted` `true`, no `problem`; new scenario "Aborted request flush order", task 6.3), A2 (buffer-time truncation also cuts error values to their `Error()` text and the `msg`; new rows in Per-request limits and Redaction and truncation, tasks 3.1 and 5.5).
- **Evidence:** unverified: planning decisions; the `KindAny` gap is proven by probe `probes/audit2/errkind` `TestErrorAttrIsNotKindString`.
- **Suggested action:** none; consolidation 1.3 checks core-server's merged truncation function covers error values and `msg`.

### Items carried from skills-compliance (owner decision, 2026-11-18)
- **Status:** open
- **Target:** triage-debug-buffer (slices 8-11)
- **Context:** the owner added the items `skills-compliance` left out or deferred: performance benchmarking and profiling, with the deferred optimisations measured (slice 8; design D8); an opt-in unmatched-record limit and user-agent omission (slice 9; D7); `srvr.LogWriteErrors` renamed to `srvr.LogRecordsLost`, one joined construction error, typed otel configuration errors and the fiber stuck-listener row (slice 10; D9); the first release, tagged only after owner confirmation (slice 11; D10). The unmatched-limit mechanism was chosen after comparing journald's burst-per-interval limit, zap's sampler and `samber/slog-sampling` (D7). Each source entry in `openspec/changes/skills-compliance/notes.md` now targets its task here.
- **Evidence:** the source entries in `skills-compliance/notes.md`: "Unmatched-request failed records cannot be sampled", "The user agent is logged by default without an opt-out", "Slice 2 /simplify: efficiency items deferred to the benchmarks", "Configuration errors: ...", "Task 9.4: measured performance changes", "Target cache: patterns where it cannot hit", "Kept as is after the audit" (update) and "Slice 3: defects found beyond the audit" (fiber row).
- **Suggested action:** archive `skills-compliance` before this change (this change's `engine-support` delta restates a requirement that change modifies); close each source entry when its task here is done.


### engine-support main spec fails strict validation on requirement length
- **Status:** open
- **Target:** unassigned
- **Context:** `openspec validate --specs --strict` fails `engine-support`: three requirement texts exceed 500 characters - "Engines share one per-request state" (579, already in main before `skills-compliance` was archived), and "srvr re-exports the shared types" (522) and "Registered routes are listed with their configurer" (581), both as `skills-compliance` modified them. Found while archiving `skills-compliance` on 2026-11-18; the sync copied the delta text faithfully.
- **Evidence:** `openspec validate engine-support --type spec --strict`: `[WARNING] requirements[7]`, `[9]`, `[10]`: "Requirement text is very long (>500 characters)"; 14 of 15 main specs pass.
- **Suggested action:** a planning change (or this change's next consolidation) moves the examples out of each requirement text into its scenarios, or splits each into requirements stating one behaviour, without changing behaviour.

### Slice 2 (tasks 2.1-2.7): outcome
- **Status:** closed: recorded
- **Target:** triage-debug-buffer
- **Context:** 2.1 and 2.4 had no RED step that could fail. `TestDebugBuffer_Off` pins behaviour that already exists, and `TestDebugBuffer_Threshold`/`TestDebugBuffer_LevelRouting` passed with the 2.2 code already in place: that GREEN routes records by the call-time level and flushes on `panicked || status >= 500`. 2.5 asserts only the flushed numbers. The `srvr.debug_buffer.flushed`/`dropped` counts in the same rows are added to `TestDebugBuffer_DefaultLimits` in 3.3, together with the summary attributes. /simplify found that `logEvent` still called the buffer-aware `Enabled`, so one of srvr's own records logged with a request context below the log level was written while the buffer was open. The cause was this slice's own uncommitted change, not main. It is proven by `TestDebugBuffer_OwnRecordsFollowTheLevel` (`engine/debugbuffer_internal_test.go`), which failed with "own WARN record written at level ERROR", and fixed by having `logEvent` use `baseEnabled`. Other /simplify fixes applied: one `cut` helper for truncation, one `requestOf` lookup, one lock per buffered record, the `logHandler` built in one place (`LogSettings.wrap`), and the shared test filter `where`. Skipped: (a) three efficiency items that apply only with buffering on. These are `attrSize` formatting `KindAny` values a second time, a `Clone` discarded when `truncator.record` rewrites, and reslicing the entries instead of a ring. They are deferred to task 8.5, to be applied only if measurement shows a gain (design D8). (b) Sharing one outcome classification between `outcome()` and `shouldFlush`, which waits for 3.3, when the summary attributes reuse the same decision. (c) Dropping the `DebugBufferOption` parameter, which is kept because spec rows (Invalid limits, Configuration sources) require it.
- **Evidence:** `go test -race -count=1 -run TestDebugBuffer_OwnRecords ./engine` failed before the fix and passes after it. One full `go test -race ./...` run during /simplify reported `FAIL github.com/kartaladev/srvr` with no detail; 7 later runs (`-count=2`, `-count=5`) pass, so this is unconfirmed (AGENTS.md rule 2).
- **Suggested action:** none. Task 8.5 measures the deferred efficiency items.

### TestShutdownHooks_Faulty "blocks and ignores its context" is timing-sensitive under load
- **Status:** open
- **Target:** unassigned
- **Context:** this row starts a hook that blocks and ignores its context, sets `WithShutdownHookTimeout(50ms)`, and asserts that Start returns within 200ms. With `go test -race ./...` running the core, engine and tracectx packages at the same time, Start once returned after 205ms. The test was not changed by this change. Most likely it is also behind the unexplained root-package FAIL recorded in the slice 2 outcome. It does not affect the debug buffer, so it is not fixed here (AGENTS.md: follow-ups go to notes).
- **Evidence:** run 4 of 4 of `GOWORK=off go test -race -count=1 ./...` on 2026-11-19 failed with `shutdown_test.go:569: Start returned after 205.696042ms, want within 200ms` (`TestShutdownHooks_Faulty/blocks_and_ignores_its_context`). Runs 1-3, and every run of the package alone, passed.
- **Suggested action:** give the bound headroom for `-race` and CPU contention (for example the hook budget plus a margin derived from it, or a bound that fails only when Start waits for the blocking hook itself, such as several seconds). Keep the row's intent: Start must not wait for a hook that ignores its context.

### Task 3.6: first BenchmarkDebugBuffer numbers
- **Status:** closed: recorded
- **Target:** triage-debug-buffer (task 8.5 measures again with `benchstat` on the CI runner)
- **Context:** `BenchmarkDebugBuffer` (`engine/log_bench_test.go`) measures one request that logs five DEBUG records below the INFO level, then writes its access record. The local machine was busy, so the timings vary by more than 2x between runs; the allocation counts are stable.
- **Evidence:** `go test -run '^$' -bench DebugBuffer -benchmem -count=3 ./engine` (darwin, 14 threads, 2026-11-19):
  | case | ns/op (3 runs) | B/op | allocs/op |
  |------|----------------|------|-----------|
  | off | 3582-4391 | 866 | 15 |
  | on-success | 5676-14659 | 6112 | 20 |
  | on-failure | 5140-5775 | 6992 | 31 |
  Turning buffering on costs about 5.2 KB and 5 allocations per request for five buffered records (the records, the entry slice and the buffer). On failure it costs 11 more allocations to write them.
- **Suggested action:** none here; design D1 revisits default-off with the 8.5 numbers.

### Slice 3 (tasks 3.1-3.7): outcome
- **Status:** closed: recorded
- **Target:** triage-debug-buffer
- **Context:** 3.1: the two `MaxBytes: 2048` rows already passed before GREEN, because slice 2 truncates records when they are buffered; the other rows failed as expected because the caps were fixed. The REFACTOR "Replace Primitive with Object for size accounting" was not applied: two counters under one mutex do not have that smell. 3.4: the rows need the buffer `Level` field and the `BufferLevel` option, so both were added in this slice rather than in 4.1. Errors name the fields `DebugBuffer.Level`, `DebugBuffer.MaxRecords`, `DebugBuffer.MaxBytes` and `DebugBuffer.TotalBytes`. /simplify: buffer validation moved into `Settings.validate` (`validateDebugBuffer`, with `LogSettings.staticLevel` and a shared `parseLevel`), so `newBufferPolicy` only builds the policy. `add` counts the entries to evict before asking the budget, so it makes one budget call per record and evicts nothing for a refused record. `failureStatus` was inlined into `shouldFlush`. The tests share `bufferedCore` and `failedOf`. Skipped: making `sealed` an `atomic.Bool` (minor, one lock per request), building the summary without a slice (flush path only), and reshaping the `TestDebugBuffer_Limits` rows.
- **Evidence:** `GOWORK=off go test -race ./...`, `go vet ./...` and `golangci-lint run ./...` pass. `go tool cover -func`: `reserve`, `release`, `validateDebugBuffer`, `seal`, `shouldFlush` and `flushBuffer` are at 100%; `add` misses only its sealed branch, which task 5.3 tests.
- **Suggested action:** none.

### Spec fix: Suppressed access record rows 4-5 named the wrong record (task 4.3)
- **Status:** closed: fixed in this change's spec delta; confirmed at review 2026-11-19 (owner delegated: consistent with structured-logging, failed records are never suppressed)
- **Target:** triage-debug-buffer
- **Context:** in Scenario "Suppressed access record", rows 1-2 expect a skip path or a disabled access log to suppress a `404`. Only `http.request.completed` records can be suppressed (`structured-logging`: failed records never are), so the handler must write the status itself. Rows 4-5 then named the record `http.request.failed`, but a status the handler writes itself is recorded as `http.request.completed` (as in "Flush on failure", row "writes `500` directly"). The Given now says "writes `<status>` itself", and rows 4-5 name `http.request.completed`. No behaviour changed: the requirement text ("skip paths and a disabled access log only ever suppress statuses below 500") already implied this reading.
- **Evidence:** `TestDebugBuffer_Suppressed` (`debugbuffer_test.go`) passes with the corrected rows. Temporarily removing the `written` check from `flushBuffer` makes rows 1-3 fail (mutation check, reverted). `openspec validate triage-debug-buffer --strict` passes.
- **Suggested action:** none, unless the owner meant a returned error in rows 4-5. In that case rows 1-2 would contradict `structured-logging`.

### Slice 4 (tasks 4.1-4.6): outcome
- **Status:** closed: recorded
- **Target:** triage-debug-buffer
- **Context:** 4.1 failed for the expected reason (fixed threshold) and passed with `FlushStatus` resolved. In 4.2 the lowered rows already passed after 4.1, while the rejected rows failed until the 100-599 range check; the rejected rows now sit in `TestDebugBuffer_InvalidLimits`, with accepted boundary rows 100 and 599. In 4.3 and 4.4 the behaviour already existed: `writeAccess` discards when it suppresses the record (slice 2), and `Handle` evaluates the level at call time. So the RED step was replaced by mutation checks, each reverted, and both tests fail under their mutation: `TestDebugBuffer_Suppressed` when the `written` check is removed from `flushBuffer`, and `TestDebugBuffer_LevelVar` when `Handle` prefers the buffer over the call-time level. 4.3 also fixed two rows of the spec delta (separate entry above). /simplify: the "a later `WithLogConfig` replaces `DebugBuffer`" rule now lives on `WithLogConfig`, and the buffer options point to it; the test tables are simpler (`boolInt` removed, the threshold column dropped). Skipped: a row-wrapper helper for `TestDebugBuffer_Config` (low value).
- **Evidence:** `GOWORK=off go test -race ./...`, `go vet` and `golangci-lint` pass; `newBufferPolicy`, `validateDebugBuffer`, `staticLevel`, `parseLevel` and `shouldFlush` are at 100% in `go tool cover -func`.
- **Suggested action:** none.

### Slice 5 (tasks 5.1-5.6): outcome
- **Status:** closed: recorded
- **Target:** triage-debug-buffer
- **Context:** this slice is tests only. The behaviours it specifies already existed by design from slices 2-3: entries keep the derived `logHandler` and the call-time context, the buffer has a mutex, it is sealed at the outcome point, replay is guarded per record and does not call `Enabled`, and records are truncated when buffered. No RED could fail, so each test was checked against a targeted mutation, each reverted:
  - buffering with `context.Background()` makes the "derived context" row of `TestDebugBuffer_Derived` fail;
  - removing the mutex from `add` makes `TestDebugBuffer_Concurrent` report 14 data races;
  - removing the sealed check from `add` makes `TestDebugBuffer_LateKeepsNothing` fail ("a late record holds 512 bytes of the budget");
  - asking `baseEnabled` on replay makes both rows of `TestDebugBuffer_ConsumerFilter` fail;
  - skipping truncation when buffering makes four rows of `TestDebugBuffer_Redaction` fail.

  The engine test `TestDebugBuffer_LateKeepsNothing` covers a risk the public tests cannot see: a record added after the seal would never be written, but it would hold budget forever. /simplify replaced three consumer-handler wrapper types with one `hookHandler`, and four hand-built servers with `consumerServer`.
- **Evidence:** `GOWORK=off go test -race ./...`, `go vet ./...` and `golangci-lint run ./...` pass; every function in `engine/debugbuffer.go` is at 100% in `go tool cover -func`.
- **Suggested action:** none.

### Slice 6 (tasks 6.1-6.5): outcome
- **Status:** closed: recorded
- **Target:** triage-debug-buffer
- **Context:** the buffer is created and the flush decided in the shared `srvr/engine` pipeline, so net/http, gin and fiber flush identically (`TestDebugBuffer_Parity`); fiber's pooled context cannot carry A's records into B (`TestDebugBuffer_FiberReuse`); hijack, `http.ErrAbortHandler`, `http.TimeoutHandler`, streaming and disconnect endings are classified (`TestDebugBuffer_Endings`, `TestDebugBuffer_AbortOrder`); a disconnect is answered `499` by srvr's mapper `clientClosed` (design D11). The 6.5 review found:
  - a `context.Canceled` returned while the request context had passed its deadline (a server-side timeout, such as a consumer timeout middleware) was answered `499` and its buffer discarded, blaming the client for a server timeout. RED: the new row "deadline exceeded (a server-side timeout), context.Canceled" of `TestClientClosed` (`engine/clientclosed_internal_test.go`) failed with `problem Client Closed Request, want declined` (`GOWORK=off go test -race -run TestClientClosed ./engine`). GREEN: the mapper requires `errors.Is(ctx.Err(), context.Canceled)`; the error-responses requirement, design D11, the `WithProblemOptions` godoc, `LOGGING.md` and `CHANGELOG.md` say so.
  - design D11's forced-drain claim (a handler past the drain deadline returning `context.Canceled` gets `499` on every engine) was untested; `TestClientClosedRequest_ForcedDrain` (`internal/paritytest/problem_test.go`) now pins it and passes on all three engines.
  - the probe ignores `prblm.WithoutBuiltins` (entry "Client-closed probe ignores prblm.WithoutBuiltins"); `TestClientClosed_ProbeFallback` guards the probe's reliance on prblm's `500` fallback.

  /simplify: paritytest servers are built by `newNetServer`, `newGinServer` and `newFiberServer` and started by `start`, which returns a stop function (`run` uses it), so the forced-drain test reuses the harness and its per-server client; one `logBuffer.parse` replaces two copies of the decode loop; `debugEngines` return the base URL and the test sends once; the fiber disconnect row waits on a channel instead of sleeping 50ms; the `http.TimeoutHandler` row waits for its context instead of sleeping 100ms; `logging_test.go` reuses `hijack`; `fibersrvr` gained `logBuffer.where`; the mapper checks the error before the context. A `TotalBytes` option row was added to `TestDebugBuffer_InvalidLimits` (the option was at 0% coverage). Skipped: merging `TestDebugBuffer_AbortOrder` into `TestDebugBuffer_Endings` (each scenario keeps its own test) and reshaping the Endings `send` field (one row uses it).
- **Evidence:** `make check` (vet, golangci-lint, `go test -race`, `go mod tidy -diff`, `go mod verify`, govulncheck with `GOWORK=off` in every module) passes; `gopls check` on every changed Go file reports nothing; `make cover` gives `engine` 99.9% with `clientClosed`, `writeAccess`, `shouldFlush` and `flushBuffer` at 100%; `openspec validate triage-debug-buffer --strict` passes.
- **Suggested action:** none.

### Session handoff 2026-11-19: slice 6 in progress, uncommitted
- **Status:** closed: 6.3 and 6.4 done 2026-11-19 (entry below); 6.5 remains
- **Target:** triage-debug-buffer
- **Context:** slices 2-5 are committed (`293101b`, `0577c5f`, `c92f6bd`, `3358071`). The working tree holds slice 6 work, not committed because one test fails:
  - 6.1 is done and marked: `internal/paritytest/debugbuffer_test.go`, `TestDebugBuffer_Parity`. It passes on all three engines. Removing the flush for requests without a net/http recorder makes the fiber row fail (mutation check, reverted).
  - 6.2 is done and marked: `fibersrvr/debugbuffer_test.go`, `TestDebugBuffer_FiberReuse`, which passes.
  - 6.3 is written but not marked: `TestDebugBuffer_Endings` and `TestDebugBuffer_AbortOrder` in `debugbuffer_test.go`. Every row passes except "client disconnects" (see the next entry).
  - 6.4 and 6.5 are not started.
- **Evidence:** `git status`; `go test -race -run 'TestDebugBuffer_(Endings|AbortOrder)' .`
- **Suggested action:** the next session resolves the entry below, then finishes 6.3-6.5 and commits slice 6.

### Client disconnect resolves to 500, so the buffer flushes (spec and design assumption unconfirmed)
- **Status:** closed: fixed by option (a), owner delegated choice 2026-11-19; srvr's own prblm mapper (owner confirmed 2026-11-19 that srvr keeps it rather than moving it upstream)
- **Target:** triage-debug-buffer
- **Context:** design D3 assumes "prblm maps `context.Canceled` to a status below 500, so the buffer is not flushed", and the Unusual endings row "the client disconnects and the handler returns `context.Canceled`" expects `ctx` not written. With prblm v0.1.0 the returned `context.Canceled` resolves to a 500 problem, so the access record is `http.request.failed` at ERROR with `problem.status` 500, and the buffer flushes. Not yet checked: whether prblm has a mapper for `context.Canceled` that srvr should install, or whether core-server deliberately answers it as 500.
- **Evidence:** `go test -count=1 -run 'TestDebugBuffer_Endings/client_disconnects' .` fails with "ctx written = true, want false: [ctx http.request.failed]". The access record has `http.response.status_code` 500, `problem.status` 500, `level` ERROR and `error.type` `*errors.errorString`.
- **Suggested action:** the owner chooses one of three options. (a) srvr maps `context.Canceled` from a request whose client went away to a status below 500, such as 499: this changes core-server's triage records and needs its own spec rows. (b) The debug buffer treats a cancelled request context as not failed. (c) Change the row to expect a flush, and change D3. Keep the failing row as the RED step of whichever fix is chosen (AGENTS.md rule 2).
- **Decision (2026-11-19):** option (a): an error matching `context.Canceled` answered while the request context is cancelled gets `499` "Client Closed Request"; a live request context keeps `500`. Rationale: prblm v0.1.0 (and v0.2.0) maps only `context.DeadlineExceeded` (`builtins.go:39`) and its `Handler.Respond` doc answers a cancelled request like any other; design D3 already assumed a status below 500; OpenTelemetry semantic conventions mark only `5xx` server spans as errors; a disconnect should not page or flush. Implemented as srvr's own prblm mapper `clientClosed` (`engine/clientclosed.go`), added by `Core.ProblemOptions` after the consumer's options. (It was briefly reverted on a coordinator redirect to move the rule into prblm; the owner then decided srvr keeps its own mapper.) RED: `go test -count=1 -run 'TestDebugBuffer_Endings/client_disconnects|TestClientClosedRequest' .` failed with "ctx written = true, want false: [ctx http.request.failed]" and "want http.request.failed at WARN with status 499" (record 500 ERROR). GREEN: the same command, `go test -race -run 'TestDebugBuffer_Endings|TestDebugBuffer_AbortOrder' .`, `TestClientClosed` in `./engine` and `TestClientClosedRequest_Parity` in `internal/paritytest` pass. Spec rows: `error-responses` delta (Scenario: Client closed request; Client closed request on every engine), `engine-parity` delta (fiber exception row), design D3/D11.

### prblm: map a cancelled request to 499 (optional upstream)
- **Status:** open: optional
- **Target:** unassigned (prblm)
- **Context:** srvr answers `context.Canceled` with a cancelled request context as `499` with its own prblm mapper (design D11). Any other prblm user has the same problem: prblm v0.1.0 and v0.2.0 map only `context.DeadlineExceeded` (`builtins.go:39`), and `Handler.Respond`'s doc answers a cancelled request like any other. If prblm adds a built-in rule later, srvr's mapper becomes redundant but harmless (srvr's runs first and gives the same answer). Fiber quirk for prblm's docs: `fiberprblm` resolves with `c.Context()`, and fasthttp never cancels it on a disconnect. Local prblm repository: `/Users/zakyalvan/Documents/RND/prblm` (main at `c7b4c67`).
- **Evidence:** unverified for prblm itself (not tested there); srvr's behaviour is pinned by `TestClientClosedRequest`, `TestClientClosed` and `TestClientClosedRequest_Parity`.
- **Suggested action:** the owner decides whether to propose the rule to prblm; if adopted, srvr keeps or removes its mapper after bumping prblm, with the tests above unchanged.

### Sampling successful requests into the debug buffer
- **Status:** open
- **Target:** unassigned
- **Context:** the debug buffer writes a request's below-level records only when it fails (status at or above `FlushStatus`, or a panic). Triage of slow or wrong-but-successful requests gets nothing. Other buffering libraries (AWS Powertools, .NET log buffering) leave this to sampling; srvr has no sampling, which proposal and design D1 kept out of scope.
- **Evidence:** unverified: feature gap, not a defect; `TestDebugBuffer_Flush` "200" row pins the discard.
- **Suggested action:** a later change specifies an opt-in rate (per server, deterministic by `trace_id` so a sampled trace is complete across services), its interaction with `TotalBytes` and the summary attributes, and a `srvr.debug_buffer.sampled` key (additive, schema unchanged).

### On-demand flush (API to flush a request's buffer explicitly)
- **Status:** open
- **Target:** unassigned
- **Context:** a handler that detects something worth keeping on a request that will still succeed (a retried call, a fallback taken) cannot ask for its buffer to be written; only the outcome at the access stage decides. A `srvr.FlushDebugBuffer(ctx)` (or a mark that forces the flush at the outcome point) would cover it, but it must define ordering against the access record, late calls after the seal, and concurrent calls.
- **Evidence:** unverified: feature gap, not a defect.
- **Suggested action:** a later change specifies the API (prefer marking the request to flush at the outcome point, which keeps the single-writer order of design D3), with rows for a call after the seal, from another goroutine, with buffering off, and on every engine.

### TestShutdownHooks_Order failed once under a full -race run
- **Status:** open: unconfirmed (AGENTS.md rule 2)
- **Target:** unassigned
- **Context:** one `GOWORK=off go test -race -count=1 ./...` run in the root on 2026-11-19 reported `--- FAIL: TestShutdownHooks_Order (0.11s)` while the paritytest and fibersrvr suites ran at the same time; no production code had changed relative to `HEAD` (only tests and docs). Likely the same CPU-contention timing sensitivity as the `TestShutdownHooks_Faulty` entry above. The failure message was not captured.
- **Evidence:** `GOWORK=off go test -race -count=5 -run TestShutdownHooks_Order .` passes; the full run failure is not reproduced. Task 6.5 (2026-11-19) retried under load: `go test -race -count=3 ./...` in the root while the paritytest (`-count=2`) and fibersrvr (`-count=2`) suites ran at the same time, plus three `make check` runs; it passed every time, so there is still no failure output to capture.
- **Suggested action:** rerun the full suite under load with `-count` and capture the message; if it reproduces, widen the timing bound as suggested for `TestShutdownHooks_Faulty`.

### Client-closed probe ignores prblm.WithoutBuiltins
- **Status:** open: accepted limitation, documented
- **Target:** unassigned (resolves with "prblm: map a cancelled request to 499 (optional upstream)" or a prblm pre-fallback hook)
- **Context:** `clientClosed` (`engine/clientclosed.go`) decides precedence by resolving the error with a package-level prblm handler holding prblm's default built-ins, not the consumer's configured handler. When a consumer passes `prblm.WithoutBuiltins()` through `WithProblemOptions`, a sentinel such as `prblm.ErrUnavailable` wrapping the cancellation still makes srvr's mapper decline, and the real handler, having no built-ins, answers the `500` fallback where the rule would give `499`. It fails toward the visible `500`, so nothing is hidden. srvr cannot read the consumer's prblm options without re-running their mappers, enrichers and hook. The probe also relies on prblm's fallback being `500`; `TestClientClosed_ProbeFallback` (`engine/clientclosed_internal_test.go`) now fails if that changes.
- **Evidence:** a throwaway test `TestClientClosed_WithoutBuiltins` (`prblm.NewHandler(prblm.WithoutBuiltins(), prblm.WithMappers(clientClosedMapper)).Resolve(cancelledCtx, fmt.Errorf("%w: %w", prblm.ErrUnavailable, context.Canceled))`) failed with `status 500, want 499: with built-ins off nothing outranks the client-closed mapper` (`GOWORK=off go test -run TestClientClosed_WithoutBuiltins ./engine`); removed after the run, since the behaviour is kept. Documented in the `clientClosed` comment and the `WithProblemOptions` godoc.
- **Suggested action:** if prblm gains a pre-fallback mapper step or a built-in 499 rule, drop the probe and this limitation with it.

### prblm upgraded to v0.2.0 (owner request 2026-11-19)
- **Status:** closed
- **Target:** triage-debug-buffer
- **Context:** every module that requires prblm moves from v0.1.0 to v0.2.0 (root, `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup` indirect, `internal/paritytest`). The gin and fiber adapters moved to `github.com/kartaladev/prblm/adapters/ginprblm` and `.../adapters/fiberprblm` (v0.2.0); imports, doc comments, `go.mod` files, README and `openspec/config.yaml` were updated. `prblm.Parse` now returns `*prblm.Received`; only test helpers used it (`helpers_test.go`, `ginsrvr/helpers_test.go`, `fibersrvr/helpers_test.go`, `otelsrvr/correlation_test.go`) and now read `.Problem`. `prblm.AddErrorHook` is not adopted here (follow-up step). Note: `go work sync` must not be run: it writes workspace-selected versions (testify v1.12.1 and others) into the modules and breaks their `GOWORK=off` `go.sum`; `go.work`/`go.work.sum` needed no change.
- **Evidence:** `make check` (vet, golangci-lint 0 issues, `go test -race`, `go mod tidy -diff`, `go mod verify`, govulncheck: no vulnerabilities, every module, `GOWORK=off`) passes, including `TestLogSchemaGolden` and the parity suite with unchanged golden output; `go build` in workspace mode passes; `gopls check` on the changed Go files is clean; `GOWORK=off go list -deps ./...` in the root shows only the standard library and prblm.
- **Suggested action:** none.

### Consumer prblm.OnError replaced by srvr's hook (owner request 2026-11-19)
- **Status:** closed: fixed (tasks 12.1-12.4)
- **Target:** triage-debug-buffer
- **Context:** srvr installed its error hook with the single-slot `prblm.OnError`, appended after the consumer's prblm options, so a consumer's `prblm.OnError` passed through `WithProblemOptions` never ran (documented on `WithProblemOptions`; core-server design D4 and proposal; archived core-server notes "Ask prblm for a composable OnError and an exported status-only sentinel", part (1)). prblm v0.2.0 adds `prblm.AddErrorHook`. `engine.Core.ProblemOptions` now adds srvr's hook with it (design D12); net/http, ginsrvr and fibersrvr all build their renderers from that list (gopls references: `engine/core.go:52`, `ginsrvr/server.go:103`, `fibersrvr/server.go:75`). The obsolete upstream reproduction `TestUpstream_PrblmOnErrorComposes` (`errors_test.go`, skipped unless `SRVR_UPSTREAM_TESTS=1`) was removed: prblm kept `OnError` single-slot by design and answered with `AddErrorHook`, which the new tests cover; the `TestErrorExtension` row "consumer prblm.OnError does not replace srvr's hook" moved into `TestConsumerPrblmHooks` ("prblm.OnError and WithErrorHook"). The archived core-server entry is not edited; this entry closes part (1).
- **Evidence:** RED before the switch: `go test -race -count=1 -run TestConsumerPrblmHooks .` failed `consumer hooks called [], want ["fn"]` (rows `prblm.OnError`, `prblm.OnError that panics`) and `["gn"], want ["fn" "gn"]`; `cd internal/paritytest && go test -race -count=1 -run TestParity_ConsumerOnError .` failed on all six rows with `consumer OnError called 0 times`. GREEN after: both pass, and `make check` passes.
- **Suggested action:** none.

### prblm v0.2.0 exports ginprblm.ErrStatusOnly
- **Status:** open
- **Target:** unassigned
- **Context:** part (2) of the archived core-server entry "Ask prblm for a composable OnError and an exported status-only sentinel": prblm v0.2.0 exports `ginprblm.ErrStatusOnly` (`adapters/ginprblm/doc.go`), so a hook can tell a `c.AbortWithStatus(4xx)` report apart with `errors.Is`. ginsrvr still classifies failures from `c.Errors` and recovered panics (gin-and-fiber-engines design D2), which works. Since srvr's hook no longer owns the only slot, a consumer's `prblm.OnError` on gin also receives these status-only reports (prblm's documented behaviour, not srvr's).
- **Evidence:** `git -C ../prblm grep -n ErrStatusOnly v0.2.0` lists the exported sentinel; no srvr test depends on it.
- **Suggested action:** decide whether ginsrvr's `classify` should use `errors.Is(err, ginprblm.ErrStatusOnly)` instead of its `c.Errors` check; keep the gin-engine `c.AbortWithStatus(401)` row as the guard.

