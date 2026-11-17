# Design

## Context

See proposal.md - Why. This change builds on the logging pipeline from `core-server` design D7 and the triage record builder from D8, and on the engine-neutral request pipeline from `gin-and-fiber-engines`. The relevant facts these give us:

- A base `slog.Handler` (srvr's JSON/text handler, or the consumer's) is wrapped by srvr in a chain: correlation (`trace_id`/`span_id`, kept top-level under `WithGroup` because the wrapper records `WithAttrs`/`WithGroup` steps and replays them after adding correlation), truncation of every string attribute (default 1024 bytes via `LogConfig.MaxAttrBytes`, marked `srvr.attr.truncated`; done in the wrapper, not in `ReplaceAttr`), and guard (recover, count errors via `LogWriteErrors()`).
- `slog.Logger` calls `Handler.Enabled(ctx, level)` before building a record. That short-circuit is why below-level calls are cheap today.
- Each request has a fresh Go context created by srvr, carrying the trace and the per-request `engine.RequestState`. prblm's `OnError` hook and panic recovery only record the error, panic and status there; the access stage writes the one access record (`http.request.completed` or `http.request.failed`) when the outcome, final status, committed flag and panic flag are known.
- On fiber v3, `fiber.Ctx` is pooled and never cancels. The engine adapter creates its own per-request Go context, so nothing request-scoped may live in `fiber.Ctx` locals.

All of the above are assumptions. The consolidation group checks them against the merged code (see the tasks).

## Goals / Non-Goals

**Goals:**
- Below-level records become available for failed requests only, with hard memory bounds and no duplicates.
- One engine-neutral implementation in the core module; the engines contribute nothing new except the outcome signal they already produce.

**Non-Goals:**
- Sampling of successful requests, on-demand flush from handler code, buffering outside a request context.
- Changing what the access record or the problem response contains, apart from the summary attributes.
- Asynchronous log writing. Flushing is synchronous, like all of srvr's logging.

## Slices

1. **Flush on failure, net/http.**
   - What works: with `WithDebugBuffer()`, a `DEBUG` record from a failing (500 or panic) request is flushed, marked and ordered before the access record; on success it is discarded; with buffering off nothing changes. The default per-request caps (100 records, 64 KiB, oldest evicted) are enforced from this slice, so per-request memory is bounded and the slice is releasable.
   - Scenarios satisfied: Default drops below-level records; Flush on failure; Marking of flushed records; Flush threshold (default rows); Level routing; Per-request limits (default-limit rows).
   - Hot paths: `Enabled` decision, buffer append with the fixed caps, outcome decision, flush loop.
2. **Bounds and summary.**
   - What works: non-default per-request limits, the process-wide budget, summary attributes, limit validation.
   - Scenarios satisfied: Per-request limits (remaining rows); Process-wide budget; Invalid limits; Summary attributes.
   - Hot paths: size estimation, eviction, budget reserve/release.
3. **Configuration.**
   - What works: options and `LogConfig.DebugBuffer` (enabled, level, flush status, limits), lowered thresholds, rejected thresholds, runtime level changes.
   - Scenarios satisfied: Configuration sources; Flush threshold (lowered and rejected rows); Suppressed access record; Runtime level change during a request.
   - Hot paths: configuration resolution at construction.
4. **Derived loggers, concurrency, late records, fault tolerance.**
   - What works: `With`/`WithGroup` replay, concurrent logging within a request, sealing at completion, faulty handlers during flush, redaction and truncation of flushed records, consumer handlers with their own level filter.
   - Scenarios satisfied: Derived loggers; Concurrent logging in one request; Late records; Faulty handler during flush; Redaction and truncation apply to flushed records; Consumer handler with its own level filter.
   - Hot paths: seal check, guarded replay.
5. **Engines and unusual endings.**
   - What works: gin and fiber v3 parity, fiber context reuse, hijack/upgrade, `http.TimeoutHandler`, `http.ErrAbortHandler`, streaming, client disconnect.
   - Scenarios satisfied: Engine parity; Fiber context reuse; Unusual endings.
   - Hot paths: each engine's outcome signal into the shared flush decision.

Each slice ends releasable. After slice 1 the feature works on the default engine with the fixed default per-request caps; the process-wide budget arrives in slice 2, so after slice 1 memory is bounded per request (like the request body limit) but not across requests.

## Decisions

### D1. Opt-in, not default-on
Buffering is off unless enabled.

**Why:**
- When it is on, the wrapper's `Enabled` must return true for every level from the buffer level up. So every `DEBUG` call in every request builds a full `slog.Record` with attributes, even on the 99% of requests that succeed.
- It also adds per-request memory, and it changes what reaches the log stream on failure.

**Alternative considered:** default-on with small limits. That gives more triage value out of the box, but it puts a silent CPU and memory cost on every consumer, and default `DEBUG` content may hold data the consumer never meant to emit. Rejected under the "safe opinionated default" rule. Revisit when per-call cost is measured. The benchmark task records the numbers in `notes.md`.

**Failure modes:**
- A consumer enables it and sees higher latency. Mitigation: benchmark and godoc state the cost, and `Level` can be raised to buffer only `INFO` when the log level is `WARN`.

### D2. The buffer lives in the request context; records are stored with their handler
A `*requestBuffer` is created per request by the engine-neutral pipeline and stored in srvr's per-request Go context.

The wrapper handler works like this:
- `Enabled(ctx, l)`:
  - returns true if the inner chain's `Enabled(ctx, l)` is true (srvr's own handler: `l >= logLevel` through its `Leveler`; a consumer handler: its own decision, since srvr does not know its level);
  - otherwise returns true only if `ctx` carries an unsealed buffer and `l >= bufferLevel`.
- `Handle` re-evaluates at call time, because the level or the seal may change between `Enabled` and `Handle`, and srvr itself calls `Handle` directly for access records:
  - writes immediately when the inner chain's `Enabled(ctx, l)` is true, so a `LevelVar` change mid-request is honoured;
  - appends `entry{handler: the marked derived handler, ctx: the call-time ctx, record: r.Clone()}` when `ctx` carries an unsealed buffer and `l >= bufferLevel`;
  - otherwise drops the record.

Each derived logger from `WithAttrs`/`WithGroup` produces a derived wrapper holding two derived chains: the normal one, and a *marked* one built as `inner.WithAttrs(srvr.buffered=true)` followed by the same `WithAttrs`/`WithGroup` calls. This is the same "attributes before groups" rule core-server's correlation wrapper uses for `trace_id`: the marker is one more recorded `WithAttrs` step ahead of the groups, so this change relies on core-server's replay and adds no second mechanism. Buffered entries replay through the marked chain, so a flushed record keeps its attributes, groups, correlation, truncation and `ReplaceAttr`, and `srvr.buffered` stays a top-level key. Adding the marker to the record itself is wrong: after `WithGroup("db")` it renders as `db.srvr.buffered` (probe P1 in the audit entry of `notes.md`). The marked chain is built lazily, once per derived wrapper, only when buffering is enabled.

Configuration check: an explicit `Level` above the static log level fails construction; `Level` equal to it is accepted and buffers nothing. With `WithLogLevelVar` or a consumer handler the level is not known at construction, so `Level` is not checked.

**Alternative considered:** buffer the pre-rendered bytes. Rejected: it couples the buffer to the JSON format, and with a consumer-supplied handler there is no stable byte form.

**Failure modes:**
- A record logged with a context not derived from the request (for example `context.Background()` in a goroutine) is not buffered and follows the normal level rule. This is documented.
- `r.Clone()` is required because copies of a `slog.Record` share their attribute backing array ("Copies of a Record share state. Do not modify a Record after handing out a copy", `log/slog/record.go`); an upstream handler (fan-out, middleware) may modify or reuse its record after `Handle` returns. Tested by modifying the caller's record after `Handle`.
- *Replay context:* the entry keeps the context the record was logged with, not the request context. A context derived from the request (a child span from `otelsrvr`, a consumer `context.WithValue`) would otherwise lose its values on replay (probe P2). Its cancellation does not matter: slog's `Handle` contract says cancelling the context should not affect record processing.
- *Lazy values:* `LogValuer` attributes are resolved at append (`slog.Value.Resolve`, which recovers a panicking `LogValue`) and the resolved value is stored, so the flushed record shows the state at logging time.
- *Truncation at buffer time:* after resolving, the record's string attributes, `KindAny` values implementing `error` (replaced by their `Error()` text, so the text is captured at logging time) and the record `msg` are truncated at append with core-server's truncation function and limits, so `MaxBytes` and `TotalBytes` count what will actually be written. On replay the core wrapper sees values already within limits and changes nothing. A `KindAny` value holding a pointer to mutable data is still rendered at flush time and shows later mutations (probe P3); documented in godoc and `LOGGING.md`.

### D3. Flush decision at the single outcome point
The engine-neutral pipeline already computes `{status, committed, panicked, err}` once, before writing the access record. Using it:

- **Failure status:** for a returned error, the status resolved for that error (through prblm), even when the response was committed with another status - so `writes 200 then returns errors.New("late")` and a streamed `200` followed by an error count as `500`; without a returned error, the written status.
- **Flush condition:** `panicked || failureStatus >= flushStatus`.
- **Order:** on flush, entries are replayed in append order, then the access record is written with the summary. On no flush, the buffer is released.
- **Seal:** either way, the buffer is sealed under its mutex first, so late `Handle` calls see it sealed and fall back to the level rule.

**Where the flush happens:** in the access stage of the shared pipeline, after it reads the outcome from `engine.RequestState` and immediately before it writes the access record. `OnError` never writes or flushes; it only records the error.

**Suppressed access record:** when skip paths or a disabled access log suppress the access record (only possible below status `500`), the buffer is discarded even if the status meets a lowered threshold, so flushed records never precede a missing access record.

**`http.ErrAbortHandler`:** a panic with it counts as a recovered panic: the buffer is flushed before the access record, which core-server writes as `http.request.failed` at `WARN` with `error.kind` `panic`, `error.type` `panic`, `srvr.response.aborted` `true` and no `problem` group (decision A3), and the panic is then re-raised as core-server specifies.

**Alternative considered:** flush from prblm's `OnError`. Rejected: it misses a directly written 500 and a hijack followed by a panic.

**Failure modes:**
- *Streaming:* the outcome is known only when the handler returns. The buffer is held until then, bounded by the limits.
- *Hijack/upgrade:* the outcome writer reports no status. That is treated as not failed unless the request panicked.
- *`http.TimeoutHandler`:* writes 503 itself, which is seen as the final status.
- *Client disconnect:* prblm maps `context.Canceled` to a status below 500, so the buffer is not flushed.
- *Stream order is not time order:* records at or above the log level are written when logged, buffered ones at the end, so an `INFO` record can precede earlier `DEBUG` records in the stream (Flush on failure, `checkpoint` row). Flushed records keep their original `time`; `LOGGING.md` tells consumers to order a `trace_id` by `time`. .NET log buffering documents the same limitation. Decided 2026-11-17: no per-request sequence key is added; `time` plus write order is enough, and a new key would be a schema commitment.

### D4. Bounds: ring per request, shared byte budget
- **Per-request ring:** a slice of entries capped by `MaxRecords` and `MaxBytes`.
- **Byte size:** estimated as a fixed per-entry overhead (`unsafe.Sizeof` of the entry, which holds a `slog.Record` of 288 bytes on 64-bit Go 1.27 - probe P5) plus the message length plus the sum of attribute key and value string lengths. Without the overhead, 100 tiny records would count as a few hundred bytes while holding about 30 KiB.
- **Eviction:** removes the oldest entries and increments `dropped`.
- **Shared budget:** an `atomic.Int64` budget (`TotalBytes`) is reserved on append and released on flush or discard. When a reservation fails, the record is dropped, `dropped` is incremented, and `truncated` is set.
- A single record larger than `MaxBytes` (after truncation) is dropped and counted in `dropped`, never stored and never written immediately. Decided 2026-11-17: dropping bounds volume; AWS Powertools and .NET write such a record immediately instead.

**Alternative considered:** a pooled byte arena. Rejected as building ahead of need.

**Failure modes:**
- *Budget leak* on a code path that forgets to release. Release is deferred at the outcome point; a test asserts the budget returns to zero after 1000 mixed requests.
- *Estimation too low* for exotic `LogValuer` values. Valuers are resolved before estimating (`slog.Value.Resolve`), with a cap on the resolved string length.

### D5. Fault-tolerant replay
Each replayed entry calls `entry.handler.Handle(entry.ctx, rec)` directly, without `Enabled`, inside the existing guard. Errors and panics are counted in `LogWriteErrors()` and skipped. The entry's context is the call-time context, derived from the request context, so correlation still applies. The `srvr.buffered` `true` attribute comes from the marked chain (D2), so a consumer `ReplaceAttr` can see and filter it.

This deliberately breaks the `slog.Handler` contract "Handle ... will only be called when Enabled returns true" (`log/slog/handler.go`). srvr's own JSON/text handlers do not re-check the level in `Handle` (probe P4); consumer handlers may, and godoc for `WithDebugBuffer` says so.

**Alternative considered:** stop flushing at the first failure. Rejected: one bad record would hide the rest of the story.

**Failure modes:**
- A consumer handler that filters by level inside `Handle` drops flushed records. This is documented, with a scenario.
- A panic inside the summary-attribute construction cannot happen: the summary attributes are plain integers and booleans.

### D6. Engine neutrality and fiber
No engine-specific code goes into the buffer. Engines must:
- create srvr's per-request Go context, which they already do for trace context;
- report the outcome, which they already do for the access record.

The buffer creation and the flush at the outcome point live in the shared request pipeline in the public `srvr/engine` package (gin-and-fiber-engines), which `ginsrvr` and `fibersrvr` already call, and the outcome is read from the single per-request `engine.RequestState`; the buffer type itself stays unexported behind it. On fiber v3 the buffer is reachable only through that Go context. The parity test runs the same table on all three engines, and a fiber test runs two sequential requests on one app (`app.Test` opens a new connection per call; pooled `fiber.Ctx` reuse is not tied to connections).

**Failure modes:**
- An engine that writes the access record from a different code path than the one deciding the outcome. Tests per engine cover this.

## Defaults

| Behaviour | Default | Why | Override |
|-----------|---------|-----|----------|
| Buffering | off | cost and content change are the consumer's choice (D1) | `WithDebugBuffer(...)`, `WithDebugBufferConfig(DebugBufferConfig{Enabled: true})`, `LogConfig.DebugBuffer.Enabled` |
| Buffer level | `DEBUG` | the level production normally drops | `BufferLevel(l)`, `DebugBufferConfig.Level`; must not be above a static log level (equal buffers nothing; not checked with `WithLogLevelVar` or a consumer handler) |
| Flush threshold | status `500`+ or panic | server faults are what triage is for; 4xx are usually client errors | `FlushAtStatus(n)`, `DebugBufferConfig.FlushStatus` (100-599) |
| Records per request | 100 | enough for a request's story; bounded | `MaxRecords(n)`, `DebugBufferConfig.MaxRecords` |
| Bytes per request | 64 KiB | bounded memory with headroom | `MaxBytes(n)`, `DebugBufferConfig.MaxBytes` |
| Process-wide bytes | 16 MiB | bounds total memory under load spikes | `TotalBytes(n)`, `DebugBufferConfig.TotalBytes` (must be at least `MaxBytes`) |

Options and `WithDebugBufferConfig` apply in argument order and the later one wins: `WithDebugBufferConfig` sets every field (zero meaning the default), an option sets one field and `WithDebugBuffer` also sets `Enabled`. `DebugBuffer` stays nested in `LogConfig` so config loaders fill it with the rest of the logging settings; as a consequence a later `WithLogConfig` replaces it too, so `WithDebugBuffer(), WithLogConfig(LogConfig{Format: "json"})` turns buffering off. Godoc on both options states this.

## Hot Paths

- Wrapper `Enabled` and `Handle` for every log call made with a request context. These cover both branches (write immediately, buffer) and the sealed-buffer branch.
- Append: clone, size estimate, budget reserve, ring eviction.
- Outcome point: seal, flush decision, replay loop with guard, budget release, summary attributes.
- Error branches: budget exhausted, oversized record, handler error or panic during replay, invalid configuration.

## Risks / Trade-offs

- [Per-call cost when enabled] → opt-in; a benchmark task records `ns/op` and allocations with buffering on and off.
- [Memory pressure under load] → per-request and process-wide caps; the budget is enforced atomically and tested under `-race`.
- [Flushed `DEBUG` content may be more sensitive] → the same redaction and `ReplaceAttr` path apply; godoc warns; there is no opt-in that loosens redaction.
- [Consumer handlers that filter inside `Handle`] → documented with a scenario; srvr cannot force them.
- [Assumptions about the core-server and engines pipeline] → checked by consolidation task 1.3; mismatches are fixed in this change's artifacts before coding.

## Migration Plan

Additive and opt-in. Release as a minor version of the core module. `srvr.log.schema` keeps its version: the new keys are additive (project policy), and the golden-record test is extended with them. There is nothing to roll back beyond disabling the option.
