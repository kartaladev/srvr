# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.


### Summary attributes belong in triage-error-records
- **Status:** open
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
- **Status:** adopted by core-server (D1; proven by probe xgroup)
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
