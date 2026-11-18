# Proposal

## Why

The triage records from `core-server` say what failed and where, but not what the request was doing before it failed. That context lives in `DEBUG` records, which production servers drop because logging them for every request is too noisy and too costly. Per-request debug buffering keeps those records in memory for each request and writes them only when the request fails, so an AI assistant (or a human) triaging a failure gets the request's full story from one `trace_id`, while successful traffic still logs at the normal level.

## What Changes

- New opt-in **debug buffer**: records logged with a request context whose level is below the effective log level but at or above the buffer level (default `DEBUG`) are kept in a bounded, per-request buffer instead of being dropped.
- On a request that fails at or above the flush threshold (default: status `500`+ or a recovered panic; a returned error counts with its resolved status even after the response was committed), the buffered records are written just before the request's access record (`http.request.failed`, or `http.request.completed` for a directly written `5xx`), in their original order, keeping their original time and `trace_id`, each marked `srvr.buffered` `true`.
- On any other outcome the buffer is discarded without writing. It is also discarded when the access record itself is suppressed (a skip path or a disabled access log, which only ever suppress statuses below `500`), so flushed records never precede a missing access record.
- A panic with `http.ErrAbortHandler` (for example from `httputil.ReverseProxy`) counts as a recovered panic and flushes.
- The failed record gains a summary: `srvr.debug_buffer.flushed`, `srvr.debug_buffer.dropped` and `srvr.debug_buffer.truncated`.
- Bounds: records and bytes per request, plus a process-wide byte budget across concurrent requests; the oldest records are evicted first and counted as dropped.
- Configuration through `WithDebugBuffer(opts ...DebugBufferOption)` (options `BufferLevel`, `FlushAtStatus`, `MaxRecords`, `MaxBytes`, `TotalBytes`), `WithDebugBufferConfig(DebugBufferConfig)`, or the `LogConfig.DebugBuffer` field filled by any config loader. Options apply in argument order; the later one wins.
- Works the same on net/http, gin and fiber v3, and with a consumer-supplied `slog.Handler`.

## Defaults and Customization

- **Zero configuration: buffering is off.** Behaviour is exactly that of `core-server`: records below the log level are dropped. Nothing new is logged, kept in memory or sent to clients.
- Buffering is off by default because enabling it makes every below-level log call build a full record (the `Enabled` short-circuit no longer applies), adds per-request memory, and changes what reaches the log stream on failure (`DEBUG` records may hold more detail than `INFO`). These are costs a consumer should choose knowingly.
- Opt-in: `WithDebugBuffer()`, `WithDebugBufferConfig(DebugBufferConfig{Enabled: true})` or `LogConfig{DebugBuffer: DebugBufferConfig{Enabled: true}}` with safe limits: buffer level `DEBUG`, flush at status `500`+ or panic, 100 records and 64 KiB per request, 16 MiB process-wide.
- Further opt-ins: a lower flush threshold (for example `400`) - **logs more client-error context**; higher limits; a different buffer level.
- Nothing changes in responses: buffered records never reach clients. Buffered records go through the same redaction and consumer `ReplaceAttr` as every other record, and through core-server's attribute truncation (applied when a record is buffered, so the byte limits count truncated sizes), so credentials, cookies, bodies and query strings remain excluded.

## Capabilities

### New Capabilities
- `triage-debug-buffer`: per-request buffering of below-level log records, the flush-on-failure rule, marking and summary attributes, memory bounds, and behaviour under concurrency, late records, faulty handlers and engine differences.

### Modified Capabilities
<!-- none. The summary attributes extend the http.request.failed record defined by core-server's triage-error-records without changing any of its requirements; they are specified here so this change stays self-contained. If triage-error-records has been archived to main specs by the time this change is consolidated, consolidation may move them into a MODIFIED delta. -->

## Scope by Gall's Law

Simplest working version: on net/http, with buffering enabled, a handler logs one `DEBUG` record and returns an error that yields a `500`; that record appears, marked, just before the `http.request.failed` record; on a `200` it never appears. Later slices add bounds, the summary, configuration, the other engines, and the edge cases (late records, faulty handlers, hijacking, streaming).

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
- Builds on `core-server` and assumes:
  - The logging pipeline of design D7, as merged: one handler, `engine.logHandler`, around the base `slog.Handler`, adding correlation from the request context and keeping `trace_id`/`span_id` top-level under `WithGroup` by replaying recorded `WithAttrs`/`WithGroup` steps after the top-level keys, truncating every string attribute, error text and message (default 1024 bytes, `LogConfig.MaxAttrBytes`, `srvr.attr.truncated`; not `ReplaceAttr`), guarding the base handler, and counting write errors in `LogWriteErrors()`.
  - The single access record of design D8, as merged: prblm's `OnError` hook and panic recovery only collect the outcome into the per-request `engine.RequestState`; `engine.Core.writeAccess` writes the one `http.request.completed` or `http.request.failed` record for every engine, and this change flushes there just before it.
  - `srvr.LoggerFromContext`, `LogConfig`, and `AccessLogConfig` semantics in which skip and disable never suppress status `500`+.
- Builds on `gin-and-fiber-engines` and assumes:
  - One engine-neutral request pipeline. Each engine creates a fresh per-request Go context and reports the final status, the committed state and the panic outcome to it.
  - On Fiber, the context srvr uses is not tied to the pooled `fiber.Ctx`.
- Independent of `otel-observability` and `scrty-integration`. If `otelsrvr` replaces the trace-id source, buffered records still carry whatever `trace_id` the request had when they were logged.
