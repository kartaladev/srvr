# Tasks

## 1. Consolidation: refresh plan against merged changes

- [ ] 1.1 Sync with the main branch and list every change merged or archived since this change was planned (expected: `core-server`, `gin-and-fiber-engines`, possibly `otel-observability` and `scrty-integration`); verify they are named in the completion note
- [ ] 1.2 Decide every open `notes.md` carry-over from earlier changes addressed to `triage-debug-buffer` or `unassigned` (adopt, defer or close); verify none is left undecided
- [ ] 1.3 Refresh this change's proposal, specs, design and tasks against the current main specs and merged code: confirm with gopls the logging wrapper chain, guard and `LogWriteErrors()` (core-server D7), the single outcome point and access-record writer (core-server D8, engines change), the per-request Go context on each engine (fiber not using `fiber.Ctx` locals), `srvr.log.schema` version, that `trace_id`/`span_id` stay top-level on records from a `WithGroup`-derived logger (core-server D1 replay; the marked chain relies on it), that the core wrapper truncates every string attribute with `LogConfig.MaxAttrBytes` and exposes its truncation function to the buffer, that `OnError` only records into `engine.RequestState` and the access stage writes the access record, and whether `triage-error-records`/`structured-logging` are now main specs (if so, consider moving the summary attributes into a MODIFIED delta); verify `openspec validate triage-debug-buffer --strict` passes
- [ ] 1.4 Record the consolidation outcome at the top of `openspec/changes/triage-debug-buffer/notes.md`; verify the file lists what was refreshed or states none was needed
- [ ] 1.5 Commit the consolidation as `docs(openspec): consolidate triage-debug-buffer against main`, or skip when 1.1-1.4 changed nothing; verify with `git log -1 --stat`

## 2. Slice: below-level records flushed on failure (net/http)

- [ ] 2.1 Buffering off keeps today's behaviour (triage-debug-buffer / Scenario: Default drops below-level records, all rows); verify `go test -race -run TestDebugBuffer_Off` passes
  - RED: table over the three rows; passes already only if no buffer code path exists - write it first and confirm it fails once `WithDebugBuffer` is referenced but not implemented (compile failure is not an acceptable RED: add the option as a no-op stub returning nil first)
  - GREEN: `WithDebugBuffer(opts ...DebugBufferOption)` option and `debugBufferConfig` with `Enabled` false by default; no allocation when off
  - REFACTOR: none expected; /simplify
- [ ] 2.2 Flush on failure in order before the access record (triage-debug-buffer / Scenario: Flush on failure, flushed and discarded rows); verify `go test -race -run TestDebugBuffer_Flush` passes
  - RED: table with error, interleaved `INFO` checkpoint, panic, 503, late error, direct 500, 200, 404 and committed-201-then-409 rows asserting the record sequence; fails because below-level records are dropped
  - GREEN: per-request `requestBuffer` in the request context, wrapper `Enabled`/`Handle` routing, flush in the access stage (reading the outcome from `engine.RequestState`) just before the access record, failure status resolved from the returned error even when committed; `OnError` is not touched
  - REFACTOR: Extract Class `requestBuffer`, Move Function for the outcome decision, then /simplify
- [ ] 2.3 Marking and original time (triage-debug-buffer / Scenario: Marking of flushed records); verify `go test -race -run TestDebugBuffer_Marking` passes
  - RED: fixed clock, assert `time`, `level`, `srvr.buffered`, attributes and `trace_id`; fails because the flushed record lacks the marker or gets the flush time
  - GREEN: `r.Clone()` replayed through the marked derived chain (`srvr.buffered` top level), replay keeps record time and call-time context
  - REFACTOR: /simplify
- [ ] 2.4 Default threshold and level routing without duplicates (triage-debug-buffer / Scenario: Flush threshold, default rows; Scenario: Level routing, all rows); verify `go test -race -run 'TestDebugBuffer_Threshold|TestDebugBuffer_LevelRouting'` passes
  - RED: tables including `499`/`500`/`599` boundaries and `DEBUG-4`; fails on duplicate or misrouted records
  - GREEN: routing by call-time log level and buffer level; flush rule `panicked || status >= 500`
  - REFACTOR: Decompose Conditional into `shouldBuffer`/`shouldFlush`, then /simplify
- [ ] 2.5 Default per-request caps (triage-debug-buffer / Scenario: Per-request limits, default-limit rows `0`, `1`, `100`, `101`); verify `go test -race -run TestDebugBuffer_DefaultLimits` passes
  - RED: table; fails because the buffer grows past 100 records
  - GREEN: fixed 100-record / 64 KiB ring evicting the oldest, size estimate including the per-entry overhead
  - REFACTOR: /simplify
- [ ] 2.6 Godoc for `WithDebugBuffer` stating cost, default off, what is flushed, that flushed records reach the handler's `Handle` without `Enabled`, and that pointer values render at flush time; add an `Example` with a failing handler; verify `go test -run Example` passes
- [ ] 2.7 Verify slice end to end with an httptest request that fails and one that succeeds, confirm `Enabled`, `Handle`, append and flush branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in every touched module, run gopls diagnostics, run /simplify on the slice and commit `feat(core): buffer below-level logs per request and flush on failure`

## 3. Slice: bounds and summary

- [ ] 3.1 Configurable per-request limits (triage-debug-buffer / Scenario: Per-request limits, over-limit rows `MaxRecords: 10`, `MaxBytes: 1024`, `MaxBytes: 50`); verify `go test -race -run TestDebugBuffer_Limits` passes
  - RED: table including an oversized single record; fails because the caps are fixed
  - GREEN: configurable record and byte caps, `LogValuer` resolved and stored at append, string attributes, error values (`Error()` text) and `msg` truncated at append with core-server's truncation function so byte accounting uses truncated sizes (both `MaxBytes: 2048` rows), oversized records dropped and counted
  - REFACTOR: Replace Primitive with Object for size accounting, then /simplify
- [ ] 3.2 Process-wide budget (triage-debug-buffer / Scenario: Process-wide budget, both rows); verify `go test -race -count=20 -run TestDebugBuffer_Budget` passes and a test asserts the budget returns to `0` after 1000 mixed requests
  - RED: concurrent table asserting peak reserved bytes and `truncated`; fails because no shared budget exists
  - GREEN: `atomic.Int64` reserve/release, release deferred at the outcome point
  - REFACTOR: Extract Class `budget`, then /simplify
- [ ] 3.3 Summary attributes (triage-debug-buffer / Scenario: Summary attributes, all rows); verify `go test -race -run TestDebugBuffer_Summary` passes
  - RED: table; fails because the access record lacks `srvr.debug_buffer.*`
  - GREEN: summary attributes on access records at or above the threshold or with a recovered panic, only when enabled
  - REFACTOR: /simplify
- [ ] 3.4 Invalid limits (triage-debug-buffer / Scenario: Invalid limits, all rows); verify `go test -race -run TestDebugBuffer_InvalidLimits` passes
  - RED: table; fails because invalid values are accepted
  - GREEN: construction-time validation naming the field, zero meaning default
  - REFACTOR: /simplify
- [ ] 3.5 Add the new keys to the central log-key file, the golden-record test and `LOGGING.md` without bumping `srvr.log.schema` (additive keys keep the version); verify `go test -run TestLogSchemaGolden` passes and the schema version is unchanged
- [ ] 3.6 Add `BenchmarkDebugBuffer` (off, on-success, on-failure) and record `ns/op` and allocations in `notes.md`; verify the benchmark runs with `go test -run x -bench DebugBuffer -benchmem`
- [ ] 3.7 Verify slice end to end, confirm eviction, budget exhaustion and release branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in every touched module, run gopls diagnostics, run /simplify on the slice and commit `feat(core): bound debug buffers and summarise them on failure`

## 4. Slice: configuration

- [ ] 4.1 Options and config struct (triage-debug-buffer / Scenario: Configuration sources, all rows); verify `go test -race -run TestDebugBuffer_Config` passes
  - RED: table including JSON documents decoded into `LogConfig` and both `WithLogConfig` order rows; fails because `LogConfig.DebugBuffer` does not exist
  - GREEN: `DebugBufferConfig{Enabled, Level, FlushStatus, MaxRecords, MaxBytes, TotalBytes}` with json/yaml/env tags, `WithDebugBufferConfig`, options `BufferLevel`, `FlushAtStatus`, `MaxRecords`, `MaxBytes`, `TotalBytes`, applied in argument order with the later one winning
  - REFACTOR: Combine Functions into Transform for config resolution, then /simplify
- [ ] 4.2 Lowered and rejected thresholds (triage-debug-buffer / Scenario: Flush threshold, lowered and rejected rows); verify `go test -race -run TestDebugBuffer_ThresholdConfig` passes
  - RED: table with `399`/`400`/`404`, `99`, `600`; fails because thresholds are fixed
  - GREEN: configurable threshold with range validation
  - REFACTOR: /simplify
- [ ] 4.3 Discard when the access record is suppressed (triage-debug-buffer / Scenario: Suppressed access record, all rows); verify `go test -race -run TestDebugBuffer_Suppressed` passes
  - RED: table with skip path and disabled access log at threshold `400`, plus the `500` row that skip never suppresses; fails because the buffer flushes without an access record
  - GREEN: the access stage discards the buffer when it suppresses the access record
  - REFACTOR: /simplify
- [ ] 4.4 Runtime level change mid-request (triage-debug-buffer / Scenario: Runtime level change during a request, all rows); verify `go test -race -count=20 -run TestDebugBuffer_LevelVar` passes
  - RED: table synchronising the handler with the level change; fails if the level is captured at request start
  - GREEN: call-time level evaluation through the `Leveler`
  - REFACTOR: /simplify
- [ ] 4.5 Godoc for `DebugBufferConfig` fields with defaults and reasons, and on `WithDebugBuffer`/`WithDebugBufferConfig` stating that a later `WithLogConfig` replaces `LogConfig.DebugBuffer`; an `Example` loading it from JSON; verify `go test -run Example` passes
- [ ] 4.6 Verify slice end to end, confirm resolution and validation branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in every touched module, run gopls diagnostics, run /simplify on the slice and commit `feat(core): configure debug buffering by option or config struct`

## 5. Slice: derived loggers, concurrency, late records and faults

- [ ] 5.1 Derived loggers share the buffer (triage-debug-buffer / Scenario: Derived loggers, all rows); verify `go test -race -run TestDebugBuffer_Derived` passes
  - RED: table with `With`, `WithGroup`, default logger and a derived context value; fails because derived handlers lose the buffer, attributes or context, or nest `srvr.buffered` under the group, or `trace_id` under the group (assert top-level `trace_id`, relying on core-server's replay)
  - GREEN: wrapper `WithAttrs`/`WithGroup` returning derived wrappers holding the normal and marked derived chains; entries store the marked handler and the call-time context
  - REFACTOR: /simplify
- [ ] 5.2 Concurrent logging in one request (triage-debug-buffer / Scenario: Concurrent logging in one request, both rows); verify `go test -race -count=20 -run TestDebugBuffer_Concurrent` passes
  - RED: table; fails with a data race or lost counts
  - GREEN: mutex-guarded append and counters
  - REFACTOR: /simplify
- [ ] 5.3 Late records after the request ends (triage-debug-buffer / Scenario: Late records, all rows); verify `go test -race -run TestDebugBuffer_Late` passes
  - RED: table with goroutines logging after completion; fails because late records are appended or written twice
  - GREEN: seal under the mutex at the outcome point, sealed buffers fall back to the level rule
  - REFACTOR: /simplify
- [ ] 5.4 Fault-tolerant flush (triage-debug-buffer / Scenario: Faulty handler during flush, all rows); verify `go test -race -run TestDebugBuffer_Faults` passes
  - RED: table with erroring and panicking consumer handler; fails because flushing stops or the response changes
  - GREEN: per-entry guarded replay counting `LogWriteErrors()`
  - REFACTOR: /simplify
- [ ] 5.5 Redaction, truncation and consumer level filters on flushed records (triage-debug-buffer / Scenario: Redaction and truncation apply to flushed records; Scenario: Consumer handler with its own level filter); verify `go test -race -run 'TestDebugBuffer_Redaction|TestDebugBuffer_ConsumerFilter'` passes
  - RED: tables including the error-value and `msg` rows; fails if replay bypasses truncation or calls `Enabled`
  - GREEN: replay through the stored derived handler's `Handle` only
  - REFACTOR: /simplify
- [ ] 5.6 Verify slice end to end, confirm seal, replay-guard and derived-handler branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in every touched module, run gopls diagnostics, run /simplify on the slice and commit `feat(core): make debug buffering safe under concurrency and faults`

## 6. Slice: engine parity and unusual endings

- [ ] 6.1 Engine parity (triage-debug-buffer / Scenario: Engine parity, all rows); verify `go test -race -run TestDebugBuffer_Parity` passes in the parity test module for net/http, gin and fiber v3
  - RED: shared table run against each engine (httptest for net/http and gin, `app.Test` for fiber); fails on any engine whose outcome point does not flush
  - GREEN: put buffer creation and the flush decision in the shared request pipeline of `srvr/engine` that each engine's outcome signal already feeds (no engine-specific buffer code)
  - REFACTOR: /simplify
- [ ] 6.2 Fiber context reuse (triage-debug-buffer / Scenario: Fiber context reuse); verify `go test -race -run TestDebugBuffer_FiberReuse` passes in `fibersrvr`
  - RED: two sequential `app.Test` requests on one app; fails if A's records reach B
  - GREEN: buffer reachable only through srvr's per-request Go context
  - REFACTOR: /simplify
- [ ] 6.3 Unusual endings (triage-debug-buffer / Scenario: Unusual endings, all rows; Scenario: Aborted request flush order, all rows); verify `go test -race -run 'TestDebugBuffer_Endings|TestDebugBuffer_AbortOrder'` passes
  - RED: table with hijack, hijack plus panic, `http.ErrAbortHandler` panic, `http.TimeoutHandler`, streaming success and failure, client disconnect; fails on any row whose outcome is misread
  - GREEN: outcome classification for hijacked writers, `http.ErrAbortHandler` (flush, then core's aborted access record, then re-raise) and timeout-written statuses
  - REFACTOR: /simplify
- [ ] 6.4 Update `LOGGING.md` with buffering semantics, cost, ordering and limits, and a sample flushed sequence; write `notes.md` carry-overs for sampling successful requests and on-demand flush (Target: `unassigned`); verify the document matches the golden schema test
- [ ] 6.5 Verify slice end to end on all three engines, confirm every engine's outcome branch is covered in `go tool cover -func` for each touched module, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in every touched module, run gopls diagnostics, run /simplify on the slice and commit `feat(core): flush debug buffers identically on every engine`

## Workflow follow-up

- Re-read `notes.md` and make sure every open item has a target or is `unassigned`.
- Archive the change after review; verify the archived result.
