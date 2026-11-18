# Tasks

## 1. Consolidation: refresh plan against merged changes

- [x] 1.1 Sync with the main branch and list every change merged or archived since this change was planned (expected: `core-server`, `gin-and-fiber-engines`, possibly `otel-observability` and `scrty-integration`); verify they are named in the completion note
- [x] 1.2 Decide every open `notes.md` carry-over from earlier changes addressed to `triage-debug-buffer` or `unassigned` (adopt, defer or close); verify none is left undecided
- [x] 1.3 Refresh this change's proposal, specs, design and tasks against the current main specs and merged code: confirm with gopls the logging wrapper chain, guard and `LogWriteErrors()` (core-server D7), the single outcome point and access-record writer (core-server D8, engines change), the per-request Go context on each engine (fiber not using `fiber.Ctx` locals), `srvr.log.schema` version, that `trace_id`/`span_id` stay top-level on records from a `WithGroup`-derived logger (core-server D1 replay; the marked chain relies on it), that the core wrapper truncates every string attribute with `LogConfig.MaxAttrBytes` and exposes its truncation function to the buffer, that `OnError` only records into `engine.RequestState` and the access stage writes the access record, and whether `triage-error-records`/`structured-logging` are now main specs (if so, consider moving the summary attributes into a MODIFIED delta); verify `openspec validate triage-debug-buffer --strict` passes
- [x] 1.4 Record the consolidation outcome at the top of `openspec/changes/triage-debug-buffer/notes.md`; verify the file lists what was refreshed or states none was needed
- [x] 1.5 Commit the consolidation as `docs(openspec): consolidate triage-debug-buffer against main`, or skip when 1.1-1.4 changed nothing; verify with `git log -1 --stat`

## 2. Slice: below-level records flushed on failure (net/http)

- [x] 2.1 Buffering off keeps today's behaviour (triage-debug-buffer / Scenario: Default drops below-level records, all rows); verify `go test -race -run TestDebugBuffer_Off` passes
  - RED: table over the three rows; passes already only if no buffer code path exists - write it first and confirm it fails once `WithDebugBuffer` is referenced but not implemented (compile failure is not an acceptable RED: add the option as a no-op stub returning nil first)
  - GREEN: `WithDebugBuffer(opts ...DebugBufferOption)` option and `debugBufferConfig` with `Enabled` false by default; no allocation when off
  - REFACTOR: none expected; /simplify
- [x] 2.2 Flush on failure in order before the access record (triage-debug-buffer / Scenario: Flush on failure, flushed and discarded rows); verify `go test -race -run TestDebugBuffer_Flush` passes
  - RED: table with error, interleaved `INFO` checkpoint, panic, 503, late error, direct 500, 200, 404 and committed-201-then-409 rows asserting the record sequence; fails because below-level records are dropped
  - GREEN: per-request `requestBuffer` in the request context, wrapper `Enabled`/`Handle` routing, flush in the access stage (reading the outcome from `engine.RequestState`) just before the access record, failure status resolved from the returned error even when committed; `OnError` is not touched
  - REFACTOR: Extract Class `requestBuffer`, Move Function for the outcome decision, then /simplify
- [x] 2.3 Marking and original time (triage-debug-buffer / Scenario: Marking of flushed records); verify `go test -race -run TestDebugBuffer_Marking` passes
  - RED: fixed clock, assert `time`, `level`, `srvr.buffered`, attributes and `trace_id`; fails because the flushed record lacks the marker or gets the flush time
  - GREEN: the stored clone replayed through the entry's `logHandler` with `srvr.buffered` added to the top-level attributes ahead of the replayed steps; replay keeps record time and call-time context
  - REFACTOR: /simplify
- [x] 2.4 Default threshold and level routing without duplicates (triage-debug-buffer / Scenario: Flush threshold, default rows; Scenario: Level routing, all rows); verify `go test -race -run 'TestDebugBuffer_Threshold|TestDebugBuffer_LevelRouting'` passes
  - RED: tables including `499`/`500`/`599` boundaries and `DEBUG-4`; fails on duplicate or misrouted records
  - GREEN: routing by call-time log level and buffer level; flush rule `panicked || status >= 500`
  - REFACTOR: Decompose Conditional into `shouldBuffer`/`shouldFlush`, then /simplify
- [x] 2.5 Default per-request caps (triage-debug-buffer / Scenario: Per-request limits, default-limit rows `0`, `1`, `100`, `101`); verify `go test -race -run TestDebugBuffer_DefaultLimits` passes
  - RED: table; fails because the buffer grows past 100 records
  - GREEN: fixed 100-record / 64 KiB ring evicting the oldest, size estimate including the per-entry overhead
  - REFACTOR: /simplify
- [x] 2.6 Godoc for `WithDebugBuffer` stating cost, default off, what is flushed, that flushed records reach the handler's `Handle` without `Enabled`, and that pointer values render at flush time; add an `Example` with a failing handler; verify `go test -run Example` passes
- [x] 2.7 Verify slice end to end with an httptest request that fails and one that succeeds, confirm `Enabled`, `Handle`, append and flush branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in every touched module, run gopls diagnostics, run /simplify on the slice and commit `feat(core): buffer below-level logs per request and flush on failure`

## 3. Slice: bounds and summary

- [x] 3.1 Configurable per-request limits (triage-debug-buffer / Scenario: Per-request limits, over-limit rows `MaxRecords: 10`, `MaxBytes: 1024`, `MaxBytes: 50`); verify `go test -race -run TestDebugBuffer_Limits` passes
  - RED: table including an oversized single record; fails because the caps are fixed
  - GREEN: configurable record and byte caps, `LogValuer` resolved and stored at append, string attributes, error values (`Error()` text) and `msg` truncated at append with core-server's truncation function so byte accounting uses truncated sizes (both `MaxBytes: 2048` rows), oversized records dropped and counted
  - REFACTOR: Replace Primitive with Object for size accounting, then /simplify
- [x] 3.2 Process-wide budget (triage-debug-buffer / Scenario: Process-wide budget, both rows); verify `go test -race -count=20 -run TestDebugBuffer_Budget` passes and a test asserts the budget returns to `0` after 1000 mixed requests
  - RED: concurrent table asserting peak reserved bytes and `truncated`; fails because no shared budget exists
  - GREEN: `atomic.Int64` reserve/release, release deferred at the outcome point
  - REFACTOR: Extract Class `budget`, then /simplify
- [x] 3.3 Summary attributes (triage-debug-buffer / Scenario: Summary attributes, all rows); verify `go test -race -run TestDebugBuffer_Summary` passes
  - RED: table; fails because the access record lacks `srvr.debug_buffer.*`
  - GREEN: summary attributes on access records at or above the threshold or with a recovered panic, only when enabled
  - REFACTOR: /simplify
- [x] 3.4 Invalid limits (triage-debug-buffer / Scenario: Invalid limits, all rows); verify `go test -race -run TestDebugBuffer_InvalidLimits` passes
  - RED: table; fails because invalid values are accepted
  - GREEN: construction-time validation naming the field, zero meaning default
  - REFACTOR: /simplify
- [x] 3.5 Add the new keys to the central log-key file, the golden-record test and `LOGGING.md` without bumping `srvr.log.schema` (additive keys keep the version); verify `go test -run TestLogSchemaGolden` passes and the schema version is unchanged
- [x] 3.6 Add `BenchmarkDebugBuffer` (off, on-success, on-failure) and record `ns/op` and allocations in `notes.md`; verify the benchmark runs with `go test -run x -bench DebugBuffer -benchmem`
- [x] 3.7 Verify slice end to end, confirm eviction, budget exhaustion and release branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in every touched module, run gopls diagnostics, run /simplify on the slice and commit `feat(core): bound debug buffers and summarise them on failure`

## 4. Slice: configuration

- [x] 4.1 Options and config struct (triage-debug-buffer / Scenario: Configuration sources, all rows); verify `go test -race -run TestDebugBuffer_Config` passes
  - RED: table including JSON documents decoded into `LogConfig` and both `WithLogConfig` order rows; fails because `LogConfig.DebugBuffer` does not exist
  - GREEN: `DebugBufferConfig{Enabled, Level, FlushStatus, MaxRecords, MaxBytes, TotalBytes}` with json/yaml/env tags, `WithDebugBufferConfig`, options `BufferLevel`, `FlushAtStatus`, `MaxRecords`, `MaxBytes`, `TotalBytes`, applied in argument order with the later one winning
  - REFACTOR: Combine Functions into Transform for config resolution, then /simplify
- [x] 4.2 Lowered and rejected thresholds (triage-debug-buffer / Scenario: Flush threshold, lowered and rejected rows); verify `go test -race -run TestDebugBuffer_ThresholdConfig` passes
  - RED: table with `399`/`400`/`404`, `99`, `600`; fails because thresholds are fixed
  - GREEN: configurable threshold with range validation
  - REFACTOR: /simplify
- [x] 4.3 Discard when the access record is suppressed (triage-debug-buffer / Scenario: Suppressed access record, all rows); verify `go test -race -run TestDebugBuffer_Suppressed` passes
  - RED: table with skip path, disabled access log and a log level that disables the access record's level at threshold `400`, plus the `500` row that skip never suppresses; fails because the buffer flushes without an access record
  - GREEN: `writeAccess` discards the buffer when it suppresses the access record or the base handler does not enable its level
  - REFACTOR: /simplify
- [x] 4.4 Runtime level change mid-request (triage-debug-buffer / Scenario: Runtime level change during a request, all rows); verify `go test -race -count=20 -run TestDebugBuffer_LevelVar` passes
  - RED: table synchronising the handler with the level change; fails if the level is captured at request start
  - GREEN: call-time level evaluation through the `Leveler`
  - REFACTOR: /simplify
- [x] 4.5 Godoc for `DebugBufferConfig` fields with defaults and reasons, and on `WithDebugBuffer`/`WithDebugBufferConfig` stating that a later `WithLogConfig` replaces `LogConfig.DebugBuffer`; an `Example` loading it from JSON; verify `go test -run Example` passes
- [x] 4.6 Verify slice end to end, confirm resolution and validation branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in every touched module, run gopls diagnostics, run /simplify on the slice and commit `feat(core): configure debug buffering by option or config struct`

## 5. Slice: derived loggers, concurrency, late records and faults

- [x] 5.1 Derived loggers share the buffer (triage-debug-buffer / Scenario: Derived loggers, all rows); verify `go test -race -run TestDebugBuffer_Derived` passes
  - RED: table with `With`, `WithGroup`, default logger and a derived context value; fails because derived handlers lose the buffer, attributes or context, or nest `srvr.buffered` under the group, or `trace_id` under the group (assert top-level `trace_id`, relying on core-server's replay)
  - GREEN: entries store the derived `logHandler` (with its recorded steps) and the call-time context; replay adds the marker ahead of the steps
  - REFACTOR: /simplify
- [x] 5.2 Concurrent logging in one request (triage-debug-buffer / Scenario: Concurrent logging in one request, both rows); verify `go test -race -count=20 -run TestDebugBuffer_Concurrent` passes
  - RED: table; fails with a data race or lost counts
  - GREEN: mutex-guarded append and counters
  - REFACTOR: /simplify
- [x] 5.3 Late records after the request ends (triage-debug-buffer / Scenario: Late records, all rows); verify `go test -race -run TestDebugBuffer_Late` passes
  - RED: table with goroutines logging after completion; fails because late records are appended or written twice
  - GREEN: seal under the mutex at the outcome point, sealed buffers fall back to the level rule
  - REFACTOR: /simplify
- [x] 5.4 Fault-tolerant flush (triage-debug-buffer / Scenario: Faulty handler during flush, all rows); verify `go test -race -run TestDebugBuffer_Faults` passes
  - RED: table with erroring and panicking consumer handler; fails because flushing stops or the response changes
  - GREEN: per-entry guarded replay counting `LogWriteErrors()`
  - REFACTOR: /simplify
- [x] 5.5 Redaction, truncation and consumer level filters on flushed records (triage-debug-buffer / Scenario: Redaction and truncation apply to flushed records; Scenario: Consumer handler with its own level filter); verify `go test -race -run 'TestDebugBuffer_Redaction|TestDebugBuffer_ConsumerFilter'` passes
  - RED: tables including the error-value and `msg` rows; fails if replay bypasses truncation or calls `Enabled`
  - GREEN: replay through the stored derived handler's `Handle` only
  - REFACTOR: /simplify
- [x] 5.6 Verify slice end to end, confirm seal, replay-guard and derived-handler branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in every touched module, run gopls diagnostics, run /simplify on the slice and commit `feat(core): make debug buffering safe under concurrency and faults`

## 6. Slice: engine parity and unusual endings

- [x] 6.1 Engine parity (triage-debug-buffer / Scenario: Engine parity, all rows); verify `go test -race -run TestDebugBuffer_Parity` passes in the parity test module for net/http, gin and fiber v3
  - RED: shared table run against each engine (httptest for net/http and gin, `app.Test` for fiber); fails on any engine whose outcome point does not flush
  - GREEN: put buffer creation and the flush decision in the shared request pipeline of `srvr/engine` that each engine's outcome signal already feeds (no engine-specific buffer code)
  - REFACTOR: /simplify
- [x] 6.2 Fiber context reuse (triage-debug-buffer / Scenario: Fiber context reuse); verify `go test -race -run TestDebugBuffer_FiberReuse` passes in `fibersrvr`
  - RED: two sequential `app.Test` requests on one app; fails if A's records reach B
  - GREEN: buffer reachable only through srvr's per-request Go context
  - REFACTOR: /simplify
- [x] 6.3 Unusual endings (triage-debug-buffer / Scenario: Unusual endings, all rows; Scenario: Aborted request flush order, all rows); verify `go test -race -run 'TestDebugBuffer_Endings|TestDebugBuffer_AbortOrder'` passes
  - RED: table with hijack, hijack plus panic, `http.ErrAbortHandler` panic, `http.TimeoutHandler`, streaming success and failure, client disconnect; fails on any row whose outcome is misread
  - GREEN: outcome classification for hijacked writers, `http.ErrAbortHandler` (flush, then core's aborted access record, then re-raise) and timeout-written statuses
  - GREEN (design D11): srvr's own prblm mapper `clientClosed` answers `context.Canceled` with a cancelled request context as `499`, so a disconnect does not flush (`TestClientClosedRequest`, `TestClientClosedRequest_Parity`)
  - REFACTOR: /simplify
- [x] 6.4 Update `LOGGING.md` with buffering semantics, cost, ordering and limits, and a sample flushed sequence; write `notes.md` carry-overs for sampling successful requests and on-demand flush (Target: `unassigned`); verify the document matches the golden schema test
- [x] 6.5 Verify slice end to end on all three engines, confirm every engine's outcome branch is covered in `go tool cover -func` for each touched module, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in every touched module, run gopls diagnostics, run /simplify on the slice and commit `feat(core): flush debug buffers identically on every engine`

## 7. Slice: carried-over leak-check hardening (from goroutine-leak-checks)

- [x] 7.1 Name srvr's serve goroutine: (done by skills-compliance task 3.4) RED - change the "never `Shutdown`" leak rows of every engine (core, `ginsrvr`, `fibersrvr`, `internal/paritytest`) to match `engine.(*Lifecycle[...]).serveOn` and confirm they fail against the anonymous `serve.func1`; GREEN - move the goroutine body into the unexported method `(*Lifecycle[C]).serveOn`, no behaviour or API change; verify `go test -race -run 'Goroutines|Leak'` passes in every touched module
- [x] 7.2 Guard ignores `TestMain` files excluded by build constraints: RED - restore `TestGuardRejectsTestMainBehindBuildTag` in `internal/paritytest` and confirm it fails; GREEN - evaluate each file's `//go:build` line with `go/build/constraint` against the default build context and skip excluded files; verify `go test -race -run 'TestEveryTestPackageChecksLeaks|TestGuardRejectsTestMainBehindBuildTag'` passes
- [x] 7.3 Verify the slice, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in every touched module, run gopls diagnostics, update both entries in `openspec/changes/archive/2026-11-18-goroutine-leak-checks/notes.md` and this change's `notes.md` to `closed: fixed`, run /simplify on the slice and commit `test(engine): name the serve goroutine and honour build tags in the leak guard`

## 8. Slice: performance benchmarking and profiling (carried from skills-compliance)

- [ ] 8.1 Benchmarks for the hot paths still without one (test-quality / Scenario: Benchmarks and profiles, all rows): `BenchmarkPipeline` gains `b.RunParallel` variants on net/http, gin and fiber; `otelsrvr` `BenchmarkObserver`; `BenchmarkLogHandler` gains a one-record `LoggerFromContext` row and a concurrent root-logger row; benchmarks for panic recovery, a body over `MaxRequestBodyBytes` and a 32-link error chain; `BenchmarkDebugBuffer` from 3.6 is kept; verify `go test -run '^$' -bench . -benchmem` runs in every module
- [ ] 8.2 `make bench` and `make profile` (design D8): per module with `GOWORK=off`, output under an ignored `bench/` directory; verify both run from the root and `go tool pprof -top bench/<module>.cpu` reads each CPU profile
- [ ] 8.3 `docs/PERFORMANCE.md`: running the benchmarks and profiles, reading them with `go tool pprof`, comparing with `benchstat`, what the CI job gates and why timings do not; link it from `README.md`; verify every command in it runs
- [ ] 8.4 CI benchmark job (test-quality / Scenario: Benchmark comparison in CI, all rows): `main` and the branch benchmarked in one job, compared with `benchstat`, failing above 10% in `allocs/op` or `B/op`; RED - a script test over recorded `benchstat` inputs, one case per row, failing before the comparison script exists; GREEN - the script and the job; verify the script test passes and `actionlint` reports nothing
- [ ] 8.5 Measure each deferred item with `benchstat` (`-count=10`, on the CI runner for timings): `answerUnmatched` single routing (keeping `r.Pattern` and path values, see `skills-compliance` notes), fiber `accessInfo` copies after the suppression check, `capAny` formatting (skip fixed-size kinds or a counting writer), a keyword pre-scan in `RedactSecrets`, the target cache on a one-line request logger and on a concurrent root logger, and the debug buffer's cost (design D1); apply each only at the D8 gain, test-first where behaviour could change; record every comparison, applied or not, in `notes.md`; verify `go test -race ./...` passes in every touched module
- [ ] 8.6 Verify the slice, confirm `make bench`, `make profile` and the CI job work end to end, run `make check`, gopls diagnostics, run /simplify on the slice and commit `perf: benchmark and profile every hot path and gate allocation regressions in CI`

## 9. Slice: access-log controls (carried from skills-compliance)

- [ ] 9.1 Unmatched-record limit (structured-logging / Scenario: Limiting unmatched failed records, all rows; Scenario: Failures while the access log is suppressed, new rows); verify `go test -race -count=10 -run 'TestUnmatchedLimit|TestFailedRecords'` passes on every engine
  - RED: tables over both Examples blocks on net/http, gin and fiber (fake clock for the window rows, 50 concurrent clients, `Shutdown` rows, the `otelsrvr` metrics row); add `WithUnmatchedRecordLimit` as a no-op first so RED is a wrong count, not a compile error
  - GREEN: `UnmatchedLimitConfig`, option and validation; a per-server fixed-window limiter in atomics (design D7) consulted by `writeAccess` for unmatched records only; `srvr.unmatched.suppressed` on the next written record and on `server.stopped`; a suppressed record discards the debug buffer
  - REFACTOR: Extract Class for the limiter; /simplify
- [ ] 9.2 User-agent omission (structured-logging / Scenario: Omitting the user agent, all rows); verify `go test -race -run TestAccessLog_OmitUserAgent` passes on every engine
  - RED: table; fails because `user_agent.original` is always written (no-op option first)
  - GREEN: `AccessLogConfig.OmitUserAgent` and `WithoutAccessLogUserAgent()`
  - REFACTOR: /simplify
- [ ] 9.3 Add `srvr.unmatched.suppressed` to the central log-key file, the golden-record test and `LOGGING.md` without bumping `srvr.log.schema`; document both options with their defaults and reasons in godoc and `LOGGING.md`, the limit named as a loosening; `CHANGELOG.md` entries; verify `go test -run 'TestLogSchemaGolden|Example'` passes
- [ ] 9.4 Verify the slice on all three engines, confirm the limiter's admit, rollover and report branches in `go tool cover -func`, run `make check`, gopls diagnostics, run /simplify on the slice and commit `feat(core): rate limit unmatched failed records and omit the user agent on request`

## 10. Slice: API and robustness (carried from skills-compliance)

- [ ] 10.1 Rename `srvr.LogWriteErrors` to `srvr.LogRecordsLost` with gopls, its engine counterpart included (structured-logging / Scenario: Own handler and Scenario: Broken output; engine-support / Scenario: Consumer faults on every engine); update `LOGGING.md`, godoc and `CHANGELOG.md` (BREAKING, no alias); verify `grep -rn 'LogWriteErrors' --include='*.go' --include='*.md' . --exclude-dir=archive` finds only the CHANGELOG entry and `go test -race ./...` passes in every module
- [ ] 10.2 One joined construction error (server-lifecycle / Scenario: Valid and invalid configuration, new rejected rows); verify `go test -race -run TestNew_Config` passes
  - RED: the three joined rows; fails because the log fields are checked after the other fields return
  - GREEN: validate `LogConfig.Level` and `Format` with the other fields and join every field error
  - REFACTOR: Move Function for the log-field checks into the settings validation; /simplify
- [ ] 10.3 Typed configuration errors in `otelsrvr` and `otelsetup` (otel-metrics and telemetry-setup / Scenario: Typed configuration errors, all rows); verify `go test -race -run TestConfigError` passes in both modules
  - RED: tables with `errors.As` into each module's `*ConfigError` (add the type first so RED is `false`)
  - GREEN: `ConfigError{Field, Value, Reason}` per module, `Unwrap` returning the module's `ErrConfig`, every `fmt.Errorf("%w: ...", ErrConfig)` replaced; error text unchanged
  - REFACTOR: /simplify
- [ ] 10.4 Stuck listener on fiber (server-lifecycle / Scenario: Stuck listener on every engine, all rows); verify `go test -race -run TestShutdown_StuckListener` passes in core, `ginsrvr` and `fibersrvr`
  - RED: the table on all three engines; if the fiber row passes, record in `notes.md` that the suspected defect is not confirmed (AGENTS.md rule 2) and keep the row as a regression test
  - GREEN (only if it fails): bound fiber's drain at its deadline as `httpRunner.Drain` does
  - REFACTOR: /simplify
- [ ] 10.5 Verify the slice, run `make check` and `make cover`, gopls diagnostics, run /simplify on the slice and commit `refactor!: rename LogWriteErrors to LogRecordsLost, join and type configuration errors`

## 12. Slice: keep the consumer's prblm OnError hook (owner request 2026-11-19)

Note: owner request 2026-11-19, placed before the release slice so it ships in the first release (design D12).

- [x] 12.1 Spec rows and tests (error-responses / Scenario: Consumer prblm error hooks, all rows; Scenario: Consumer prblm error hook on every engine, all rows): RED - `TestConsumerPrblmHooks` in the core module and `TestParity_ConsumerOnError` in `internal/paritytest`, confirming the consumer `OnError` rows fail because srvr's `prblm.OnError` replaces the consumer's; verify `go test -race -run 'TestConsumerPrblmHooks|TestParity_ConsumerOnError'` fails for that reason
- [x] 12.2 Implementation: GREEN - `engine.Core.ProblemOptions` adds srvr's hook with `prblm.AddErrorHook` (find references with gopls first: net/http, ginsrvr and fibersrvr all build from it); REFACTOR; drop the obsolete upstream reproduction `TestUpstream_PrblmOnErrorComposes`; verify `go test -race ./...` passes in the core, `ginsrvr`, `fibersrvr` and `internal/paritytest` modules
- [x] 12.3 Docs: godoc of `WithProblemOptions`, `WithErrorHook`, `ErrorHook` and `Core.ProblemOptions`, `ExampleWithErrorHook`, `openspec/config.yaml` context, `CHANGELOG.md` Unreleased (Changed), `notes.md` entry closing the composable-hook carry-over; verify `go test -run Example` passes and `grep -rn 'OnError' --include='*.go' --include='*.md' . --exclude-dir=archive` shows no stale claim
- [x] 12.4 Verify the slice: `make check`, gopls diagnostics on the changed files, coverage of the hook path (`go test -coverpkg=./... -coverprofile=c.out ./...`), run /simplify on the slice and re-run `make check`
- [x] 12.5 Commit `feat(core): keep the consumer's prblm OnError hook with AddErrorHook`

## 11. Slice: release (carried from skills-compliance)

- [ ] 11.1 Precondition: this change is reviewed and merged to `main`, and `skills-compliance` is archived; verify `git log main --oneline -1` contains the merge and `openspec list` no longer lists `skills-compliance`
- [ ] 11.2 Prepare: move the `CHANGELOG.md` "Unreleased" entries under a version heading (version left as a placeholder), run `make check` and `make cover` on `main`, dry-run the release guard and the `RELEASING.md` steps without tagging; verify every step passes
- [ ] 11.3 STOP for the owner: present the proposed version (suggested `v0.1.0`), the changelog section and the dry-run results, and wait for explicit confirmation of the version and of pushing tags; record the decision in `notes.md`; do not continue without it
- [ ] 11.4 Release in `RELEASING.md` order: tag the core module and push the tag; in each released module (`ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup`) require that core version, drop the `replace`, run its checks with `GOWORK=off`, commit, tag `<module>/<version>` and push; verify the CI `release` job passes for every tag
- [ ] 11.5 Verify through the module proxy: `GOPROXY=https://proxy.golang.org GOFLAGS=-mod=mod go list -m github.com/kartaladev/srvr@<version>` and the same for every module, and build a scratch consumer outside the repository that imports each module at the tagged version; record the result in `notes.md`

## Workflow follow-up

- Re-read `notes.md` and make sure every open item has a target or is `unassigned`.
- Archive the change after review; verify the archived result.
