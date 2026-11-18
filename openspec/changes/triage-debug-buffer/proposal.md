# Proposal

## Why

The triage records from `core-server` say what failed and where, but not what the request was doing before it failed. That context lives in `DEBUG` records, which production servers drop because logging them for every request is too noisy and too costly. Per-request debug buffering keeps those records in memory for each request and writes them only when the request fails, so an AI assistant (or a human) triaging a failure gets the request's full story from one `trace_id`, while successful traffic still logs at the normal level.

This change also carries the items `skills-compliance` left out or deferred (owner decision, 2026-11-18): the library has benchmarks but no profiling, no benchmark regression check and no measurement of the optimisations that change deferred; a path scanner controls the volume of unmatched-request failed records; the user agent cannot be left out of access records; `srvr.LogWriteErrors` is named after one of the two failures it counts; an invalid log field is reported apart from the other invalid fields, and the OpenTelemetry modules return untyped configuration errors; fiber has no test for a listener whose `Close` does not unblock `Accept`; and no version has been released.

## What Changes

- New opt-in **debug buffer**: records logged with a request context whose level is below the effective log level but at or above the buffer level (default `DEBUG`) are kept in a bounded, per-request buffer instead of being dropped.
- On a request that fails at or above the flush threshold (default: status `500`+ or a recovered panic; a returned error counts with its resolved status even after the response was committed), the buffered records are written just before the request's access record (`http.request.failed`, or `http.request.completed` for a directly written `5xx`), in their original order, keeping their original time and `trace_id`, each marked `srvr.buffered` `true`.
- On any other outcome the buffer is discarded without writing. It is also discarded when the access record itself is suppressed (a skip path or a disabled access log, which only ever suppress statuses below `500`), so flushed records never precede a missing access record.
- A panic with `http.ErrAbortHandler` (for example from `httputil.ReverseProxy`) counts as a recovered panic and flushes.
- The failed record gains a summary: `srvr.debug_buffer.flushed`, `srvr.debug_buffer.dropped` and `srvr.debug_buffer.truncated`.
- Bounds: records and bytes per request, plus a process-wide byte budget across concurrent requests; the oldest records are evicted first and counted as dropped.
- Configuration through `WithDebugBuffer(opts ...DebugBufferOption)` (options `BufferLevel`, `FlushAtStatus`, `MaxRecords`, `MaxBytes`, `TotalBytes`), `WithDebugBufferConfig(DebugBufferConfig)`, or the `LogConfig.DebugBuffer` field filled by any config loader. Options apply in argument order; the later one wins.
- Works the same on net/http, gin and fiber v3, and with a consumer-supplied `slog.Handler`.

Carried from `skills-compliance`:

- **Performance benchmarking and profiling:** benchmarks for every hot path that has none (`otelsrvr`'s observer, concurrent pipelines with `b.RunParallel`, a request logger used for one line, panic recovery, oversized bodies, deep error chains); `make bench` and `make profile` writing benchmark output and CPU and memory profiles per module; `docs/PERFORMANCE.md`; a CI job failing a pull request whose allocations rise more than 10% against `main`; each deferred optimisation measured and applied only at a gain of 10% or more.
- **Unmatched failed records can be rate limited (opt-in):** `AccessLogConfig.UnmatchedLimit{Burst, Interval}` or `WithUnmatchedRecordLimit(burst, interval)` writes at most `Burst` unmatched-request `http.request.failed` records per `Interval` and counts the rest; the next written one, and `server.stopped`, carry `srvr.unmatched.suppressed`. Handler errors, panics and `5xx` are never limited.
- **The user agent can be omitted (opt-in):** `AccessLogConfig.OmitUserAgent` or `WithoutAccessLogUserAgent()` leaves `user_agent.original` out of access records.
- **BREAKING** `srvr.LogWriteErrors()` becomes `srvr.LogRecordsLost()` (no alias; pre-v1 and unreleased).
- Construction names every invalid field in one joined error, the log fields (`LogConfig.Level`, `Format`) included; `otelsrvr` and `otelsetup` return a typed `*ConfigError` naming the field, still matching their `ErrConfig`.
- Fiber is tested, and fixed if needed, for a passed listener whose `Close` does not unblock `Accept`, as net/http and gin already are.
- **A consumer's `prblm.OnError` runs** (owner request 2026-11-19): srvr adds its own error hook with prblm v0.2.0's `prblm.AddErrorHook` instead of the single-slot `prblm.OnError`, so a consumer's `prblm.OnError` passed through `WithProblemOptions` is no longer silently replaced; `WithErrorHook` stays (design D12).
- **Release:** the change ends with a release prepared by the plan (changelog, `make check`, release guard dry run); tags are created and pushed only after the owner confirms the version.

## Defaults and Customization

- **Zero configuration: buffering is off.** Behaviour is exactly that of `core-server`: records below the log level are dropped. Nothing new is logged, kept in memory or sent to clients.
- Buffering is off by default because enabling it makes every below-level log call build a full record (the `Enabled` short-circuit no longer applies), adds per-request memory, and changes what reaches the log stream on failure (`DEBUG` records may hold more detail than `INFO`). These are costs a consumer should choose knowingly.
- Opt-in: `WithDebugBuffer()`, `WithDebugBufferConfig(DebugBufferConfig{Enabled: true})` or `LogConfig{DebugBuffer: DebugBufferConfig{Enabled: true}}` with safe limits: buffer level `DEBUG`, flush at status `500`+ or panic, 100 records and 64 KiB per request, 16 MiB process-wide.
- Further opt-ins: a lower flush threshold (for example `400`) - **logs more client-error context**; higher limits; a different buffer level.
- Unmatched-record limit: **off by default**, so every failed record is still written, as `structured-logging` requires today; enabling it **loosens a triage default** (records are dropped, only counted), so it is explicit and named.
- User agent: **logged by default** (it identifies bots and client versions in triage); omitting it is an explicit opt-in.
- Nothing changes in responses: buffered records never reach clients. Buffered records go through the same redaction and consumer `ReplaceAttr` as every other record, and through core-server's attribute truncation (applied when a record is buffered, so the byte limits count truncated sizes), so credentials, cookies, bodies and query strings remain excluded.

## Capabilities

### New Capabilities
- `triage-debug-buffer`: per-request buffering of below-level log records, the flush-on-failure rule, marking and summary attributes, memory bounds, and behaviour under concurrency, late records, faulty handlers and engine differences.

### Modified Capabilities
<!-- The debug buffer's summary attributes extend the http.request.failed record defined by triage-error-records without changing any of its requirements; they are specified in triage-debug-buffer so the feature stays self-contained. -->
- `structured-logging`: the write-failure counter is `srvr.LogRecordsLost()`; unmatched failed records may be rate limited by an opt-in; the user agent may be omitted.
- `engine-support`: consumer-fault rows name `srvr.LogRecordsLost()`.
- `error-responses`: an error matching `context.Canceled` returned after the client went away (request context cancelled) is answered `499` instead of `500`, by srvr's own prblm mapper (design D11); a consumer's `prblm.OnError` and `prblm.AddErrorHook` passed through `WithProblemOptions` run alongside srvr's hook, which prblm can no longer replace (design D12).
- `engine-parity`: a new documented difference: on fiber that case stays `500`, because fasthttp does not detect a disconnect.
- `server-lifecycle`: one joined error names every invalid field, log fields included; a listener whose `Close` does not unblock `Accept` never blocks the stop on any engine.
- `otel-metrics`: configuration errors are a typed `*ConfigError`.
- `telemetry-setup`: configuration errors are a typed `*ConfigError`.
- `test-quality`: hot paths are benchmarked; allocation regressions fail CI.

## Scope by Gall's Law

Simplest working version: on net/http, with buffering enabled, a handler logs one `DEBUG` record and returns an error that yields a `500`; that record appears, marked, just before the `http.request.failed` record; on a `200` it never appears. Later slices add bounds, the summary, configuration, the other engines, and the edge cases (late records, faulty handlers, hijacking, streaming).

Each carried-over item is its own slice after the debug-buffer slices (performance, access-log controls, API and robustness, release), so the feature ships without waiting for them and each slice ends releasable.

Deliberately left out:
- Flushing on a sampled fraction of successful requests.
- Flushing on demand from handler code (an explicit "keep this trace" call).
- Buffering records logged without a request context (startup, background jobs).
- Shipping buffered records anywhere other than the normal log output.

## Impact

- Compatibility decisions:
  - New public API: `WithDebugBuffer`, `DebugBufferOption` with `BufferLevel`, `FlushAtStatus`, `MaxRecords`, `MaxBytes` and `TotalBytes`, `WithDebugBufferConfig`, `DebugBufferConfig`, and a new field on `LogConfig`.
  - New log schema keys: `srvr.buffered`, `srvr.debug_buffer.flushed`, `srvr.debug_buffer.dropped`, `srvr.debug_buffer.truncated`. They are additive, so `srvr.log.schema` keeps its version (project policy: only renaming or removing a key, or changing its meaning or type, bumps it); the golden-record test gains the keys.
  - No default changes. No new module and no new dependency: everything lives in the core module and uses only the standard library.
  - Carried items: **BREAKING** `srvr.LogWriteErrors` renamed to `srvr.LogRecordsLost`; new options `WithUnmatchedRecordLimit` and `WithoutAccessLogUserAgent`, fields `AccessLogConfig.UnmatchedLimit` and `OmitUserAgent`; new types `otelsrvr.ConfigError` and `otelsetup.ConfigError`; one new additive log key, `srvr.unmatched.suppressed` (schema stays `1`); new Makefile targets, `docs/PERFORMANCE.md` and a CI benchmark job; release tags (after owner confirmation).
- Archive order: `skills-compliance` must be archived before this change. This change's `engine-support` delta restates "Consumer faults are contained on every engine" as `skills-compliance` modifies it, so archiving in the other order would undo that change's rows.
- Builds on `core-server` and assumes:
  - The logging pipeline of design D7, as merged: one handler, `engine.logHandler`, around the base `slog.Handler`, adding correlation from the request context and keeping `trace_id`/`span_id` top-level under `WithGroup` by replaying recorded `WithAttrs`/`WithGroup` steps after the top-level keys, truncating every string attribute, error text and message (default 1024 bytes, `LogConfig.MaxAttrBytes`, `srvr.attr.truncated`; not `ReplaceAttr`), guarding the base handler, and counting write errors in `LogWriteErrors()`.
  - The single access record of design D8, as merged: prblm's `OnError` hook and panic recovery only collect the outcome into the per-request `engine.RequestState`; `engine.Core.writeAccess` writes the one `http.request.completed` or `http.request.failed` record for every engine, and this change flushes there just before it.
  - `srvr.LoggerFromContext`, `LogConfig`, and `AccessLogConfig` semantics in which skip and disable never suppress status `500`+.
- Builds on `gin-and-fiber-engines` and assumes:
  - One engine-neutral request pipeline. Each engine creates a fresh per-request Go context and reports the final status, the committed state and the panic outcome to it.
  - On Fiber, the context srvr uses is not tied to the pooled `fiber.Ctx`.
- Independent of `otel-observability` and `scrty-integration`. If `otelsrvr` replaces the trace-id source, buffered records still carry whatever `trace_id` the request had when they were logged.
