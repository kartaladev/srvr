# Design

## Context

See proposal.md - Why. This change builds on the logging pipeline from `core-server` design D7 and the triage record builder from D8, and on the engine-neutral request pipeline from `gin-and-fiber-engines`. The relevant facts these give us:

- A base `slog.Handler` (srvr's JSON/text handler, or the consumer's) is wrapped by one srvr handler, `engine.logHandler` (`engine/log.go`). It adds `trace_id`/`span_id`, kept top-level under `WithGroup` because it records `WithAttrs`/`WithGroup` steps and replays them on the base handler after the top-level keys (`target`); it truncates every string attribute, error text and the message (`truncator`, default 1024 bytes via `LogConfig.MaxAttrBytes`, marked `srvr.attr.truncated`); and it guards the base handler (recover, count errors via `LogRecordsLost()`). srvr's own records go through `handle(ctx, r, own=true)`.
- `slog.Logger` calls `Handler.Enabled(ctx, level)` before building a record. That short-circuit is why below-level calls are cheap today.
- Each request has a fresh Go context created by srvr (`StartTrace`/`BeginRequest`), carrying the trace and the per-request `engine.RequestState`. prblm's `OnError` hook and panic recovery only record the failure there (`recordFailure`); `Core.writeAccess` writes the one access record (`http.request.completed` or `http.request.failed`) for net/http and gin (`LogAccess`, including its `http.ErrAbortHandler` recover path) and for fiber (`WriteAccess`), once the outcome, final status, committed flag and panic flag are known. It gates the record on `c.log.Enabled(ctx, level)` and on `AccessLogConfig` suppression.
- On fiber v3, `fiber.Ctx` is pooled and never cancels. `requestStage` builds its own per-request Go context and sets it with `c.SetContext`; nothing request-scoped lives in `fiber.Ctx` locals. Handlers may log with `c` itself as the context; `requestFrom` reaches the state through `c.Context()`.

Consolidation (2026-11-18) checked these against the merged code with gopls; they hold.

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

6. **Carried-over leak-check hardening** (tasks group 7, from `goroutine-leak-checks`).
7. **Performance benchmarking and profiling** (carried from `skills-compliance`).
   - What works: benchmarks for every hot path still without one, `make bench` and `make profile`, `docs/PERFORMANCE.md`, a CI job comparing allocations with `main`; each deferred optimisation measured and applied only at a 10% gain (D8).
   - Scenarios satisfied: Benchmarks and profiles; Benchmark comparison in CI.
   - Hot paths: none added; the slice measures the existing ones and the debug buffer.
8. **Access-log controls** (carried from `skills-compliance`).
   - What works: the opt-in unmatched-record limit with its suppressed count (D7) and the opt-in user-agent omission.
   - Scenarios satisfied: Limiting unmatched failed records; Omitting the user agent; Failures while the access log is suppressed (new rows).
   - Hot paths: the limiter's admit decision on every unmatched request; the access-record builder.
9. **API and robustness** (carried from `skills-compliance`).
   - What works: `srvr.LogRecordsLost` (D9), one joined construction error naming every invalid field, typed `*ConfigError` in `otelsrvr` and `otelsetup` keeping the underlying cause (D13), the stuck-listener row on fiber.
   - Scenarios satisfied: Own handler, Broken output and Consumer faults on every engine (renamed counter); Valid and invalid configuration (joined rows); Typed configuration errors (both modules); Stuck listener on every engine.
   - Hot paths: construction validation; fiber's stop path.
10. **Release** (carried from `skills-compliance`): a release prepared by the plan and tagged only on owner confirmation (D10). No scenario: it is a process step.

Each slice ends releasable. Slices 7-10 each depend only on a working system, not on one another, except that slice 7's debug-buffer benchmarks need slices 1-5 and the release comes last. After slice 1 the feature works on the default engine with the fixed default per-request caps; the process-wide budget arrives in slice 2, so after slice 1 memory is bounded per request (like the request body limit) but not across requests.

## Decisions

### D1. Opt-in, not default-on
Buffering is off unless enabled.

**Why:**
- When it is on, the wrapper's `Enabled` must return true for every level from the buffer level up. So every `DEBUG` call in every request builds a full `slog.Record` with attributes, even on the 99% of requests that succeed.
- It also adds per-request memory, and it changes what reaches the log stream on failure.

**Alternative considered:** default-on with small limits. That gives more triage value out of the box, but it puts a silent CPU and memory cost on every consumer, and default `DEBUG` content may hold data the consumer never meant to emit. Rejected under the "safe opinionated default" rule. Revisit when per-call cost is measured: task 3.6 records the first numbers in `notes.md`, and slice 7 measures them with `benchstat` on the CI runner (D8).

**Failure modes:**
- A consumer enables it and sees higher latency. Mitigation: benchmark and godoc state the cost, and `Level` can be raised to buffer only `INFO` when the log level is `WARN`.

### D2. The buffer lives in the request state; records are stored with their handler
A `*requestBuffer` is created per request with its `engine.RequestState` (in `withState`) when buffering is enabled, and is reachable only through srvr's per-request Go context; with buffering off the field stays nil and nothing is allocated.

`engine.logHandler` gains the routing:
- `Enabled(ctx, l)`:
  - returns true if the base handler's `Enabled(ctx, l)` is true (srvr's own handler: `l >= logLevel` through its `Leveler`; a consumer handler: its own decision, since srvr does not know its level);
  - otherwise returns true only if `ctx` carries an unsealed buffer and `l >= bufferLevel`.
- `Handle` re-evaluates at call time, because the level or the seal may change between `Enabled` and `Handle`:
  - writes immediately when the base handler's `Enabled(ctx, l)` is true, so a `LevelVar` change mid-request is honoured;
  - appends `entry{handler: h, ctx: the call-time ctx, record: the truncated clone, cut}` when `ctx` carries an unsealed buffer and `l >= bufferLevel`;
  - otherwise drops the record.
- srvr's own records (`handle(ctx, r, own=true)`: access, lifecycle and warning records) are never buffered, and the access stage checks only the base handler's `Enabled` for the access record, never the buffer-aware one.

The entry keeps the derived `*logHandler` the record was logged through, with its recorded `WithAttrs`/`WithGroup` steps. Replay calls its `handle` with `srvr.buffered` `true` added to the top-level attributes ahead of the replayed steps, the same mechanism that keeps `trace_id` top-level. So a flushed record keeps its attributes, groups, correlation and `ReplaceAttr`, and `srvr.buffered` stays a top-level key. Adding the marker to the record itself is wrong: after `WithGroup("db")` it renders as `db.srvr.buffered` (probe P1 in the audit entry of `notes.md`). No second derived chain is needed.

Configuration check: an explicit `Level` above the static log level fails construction; `Level` equal to it is accepted and buffers nothing. With `WithLogLevelVar` or a consumer handler the level is not known at construction, so `Level` is not checked.

**Alternative considered:** buffer the pre-rendered bytes. Rejected: it couples the buffer to the JSON format, and with a consumer-supplied handler there is no stable byte form.

**Failure modes:**
- A record logged with a context not derived from the request (for example `context.Background()` in a goroutine) is not buffered and follows the normal level rule. This is documented.
- The stored record is a clone (`r.Clone()`, or the new record `truncator.record` builds) because copies of a `slog.Record` share their attribute backing array ("Copies of a Record share state. Do not modify a Record after handing out a copy", `log/slog/record.go`); an upstream handler (fan-out, middleware) may modify or reuse its record after `Handle` returns. Tested by modifying the caller's record after `Handle`.
- *Replay context:* the entry keeps the context the record was logged with, not the request context. A context derived from the request (a child span from `otelsrvr`, a consumer `context.WithValue`) would otherwise lose its values on replay (probe P2). Its cancellation does not matter: slog's `Handle` contract says cancelling the context should not affect record processing. On fiber the call-time context may be the pooled `fiber.Ctx`; replay runs inside `requestStage` before it returns, so the context still belongs to the request.
- *Lazy values and truncation at buffer time:* at append the record goes through the same `truncator` the handler uses on write: `LogValuer` attributes are resolved (`slog.Value.Resolve`, which recovers a panicking `LogValue`), string attributes, `error` values (replaced by their guarded `Error()` text, captured at logging time) and the `msg` are cut with `LogConfig.MaxAttrBytes`, and whether anything was cut is stored with the entry. So `MaxBytes` and `TotalBytes` count what will actually be written; on replay the values are already within limits and `srvr.attr.truncated` comes from the stored flag. A `KindAny` value holding a pointer to mutable data is still rendered at flush time and shows later mutations (probe P3); documented in godoc and `LOGGING.md`.

### D3. Flush decision at the single outcome point
`Core.writeAccess` is the single outcome point for every engine: it reads `{status, aborted, failure}` from `engine.RequestState` before writing the access record. Using it:

- **Failure status:** for a returned error, the status resolved for that error (`failure.problem.Status`, through prblm), even when the response was committed with another status - so `writes 200 then returns errors.New("late")` and a streamed `200` followed by an error count as `500`; without a returned error, the written status.
- **Flush condition:** `panicked || failureStatus >= flushStatus`, where `panicked` is a recovered panic (`failure.panic`) or an aborted request (`RequestState.aborted`).
- **Order:** on flush, entries are replayed in append order, then the access record is written with the summary. On no flush, the buffer is released.
- **Seal:** either way, the buffer is sealed under its mutex first, so late `Handle` calls see it sealed and fall back to the level rule.

**Where the flush happens:** in `Core.writeAccess`, after it reads the outcome from `engine.RequestState` and immediately before it writes the access record. The seal comes first, so the access record itself and anything logged concurrently afterwards follow the level rule. `OnError` never writes or flushes; it only records the error.

**Suppressed access record:** when skip paths or a disabled access log suppress the access record (only possible below status `500`), or the access record's own level is not enabled on the base handler (for example a `4xx` failed record at `WARN` with log level `ERROR`), the buffer is discarded even if the status meets a lowered threshold, so flushed records never precede a missing access record.

**`http.ErrAbortHandler`:** a panic with it counts as a recovered panic: the buffer is flushed before the access record, which core-server writes as `http.request.failed` at `WARN` with `error.kind` `panic`, `error.type` `panic`, `srvr.response.aborted` `true` and no `problem` group (decision A3), and the panic is then re-raised as core-server specifies.

**Alternative considered:** flush from prblm's `OnError`. Rejected: it misses a directly written 500 and a hijack followed by a panic.

**Failure modes:**
- *Streaming:* the outcome is known only when the handler returns. The buffer is held until then, bounded by the limits.
- *Hijack/upgrade:* the outcome writer reports no status. That is treated as not failed unless the request panicked.
- *`http.TimeoutHandler`:* writes 503 itself, which is seen as the final status.
- *Client disconnect:* prblm v0.1.0 does not map `context.Canceled` (it falls to its 500 fallback), so srvr maps it with its own prblm mapper (D11): an error matching `context.Canceled` returned while the request context is cancelled is answered `499`, below the threshold, so the buffer is not flushed. On fiber the disconnect cancels nothing, so it stays a `500` and flushes.
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
Each replayed entry calls the stored handler's `handle(entry.ctx, rec)` directly, without `Enabled`, inside the existing guard. Errors and panics are counted in `LogRecordsLost()` and skipped. The entry's context is the call-time context, derived from the request context, so correlation still applies. The `srvr.buffered` `true` attribute is added as a top-level attribute on replay (D2), so a consumer `ReplaceAttr` can see and filter it.

This deliberately breaks the `slog.Handler` contract "Handle ... will only be called when Enabled returns true" (`log/slog/handler.go`). srvr's own JSON/text handlers do not re-check the level in `Handle` (probe P4); consumer handlers may, and godoc for `WithDebugBuffer` says so.

**Alternative considered:** stop flushing at the first failure. Rejected: one bad record would hide the rest of the story.

**Failure modes:**
- A consumer handler that filters by level inside `Handle` drops flushed records. This is documented, with a scenario.
- A panic inside the summary-attribute construction cannot happen: the summary attributes are plain integers and booleans.

### D6. Engine neutrality and fiber
No engine-specific code goes into the buffer. Engines must:
- create srvr's per-request Go context, which they already do for trace context;
- report the outcome, which they already do for the access record.

The buffer creation (`withState`) and the flush at the outcome point (`writeAccess`) live in the `srvr/engine` package, which `ginsrvr` (through `StartTrace`/`LogAccess`) and `fibersrvr` (through `BeginRequest`/`WriteAccess`) already call, and the outcome is read from the single per-request `engine.RequestState`; the buffer type itself stays unexported, and no public `engine` API is added. On fiber v3 the buffer is reachable only through that Go context. The parity test runs the same table on all three engines, and a fiber test runs two sequential requests on one app (`app.Test` opens a new connection per call; pooled `fiber.Ctx` reuse is not tied to connections).

**Failure modes:**
- An engine that writes the access record from a different code path than the one deciding the outcome. Tests per engine cover this.

### D7. Unmatched failed records: opt-in burst-per-interval limit
Every unmatched request writes an `http.request.failed` record, so a path scanner controls log volume. The limit follows the common log rate-limiting pattern: journald writes at most `RateLimitBurst` messages per `RateLimitIntervalSec` per service and then a message with the number suppressed; zap's sampler and `samber/slog-sampling`'s threshold strategy keep the first N records per tick and count the rest.

- **Scope:** only records of requests no route matched (`404` and `405` answered by the router, on every engine). Handler errors, panics, `5xx` and `http.request.completed` records are never limited.
- **Window:** a fixed window per server, its start and count in atomics: the first `Burst` unmatched records per `Interval` are written, later ones are counted. A new window starts at the first unmatched request after the current one ends.
- **Report:** the count is added as `srvr.unmatched.suppressed` to the next unmatched record written and reset; `server.stopped` carries what is left. No timer goroutine, as the kernel's `printk_ratelimit` reports "N callbacks suppressed" on the next allowed call.
- **Other consumers of the request:** the observer (`otelsrvr` metrics and spans) still sees every request; a suppressed record discards the request's debug buffer, as a suppressed access record does (D3).
- **Configuration:** `AccessLogConfig.UnmatchedLimit UnmatchedLimitConfig{Burst int, Interval time.Duration}` with `json`/`yaml`/`env` tags, and `WithUnmatchedRecordLimit(burst, interval)`. `Burst` `0` means no limit (the default); a negative `Burst`, or a positive one with `Interval` at or below zero, fails construction naming the field.

**Why off by default:** failed records are unsuppressible by design (`structured-logging`), so dropping any is a loosening of a triage default the consumer chooses knowingly.

**Alternatives considered:** 1-in-N sampling, whose volume still grows with the attacker's rate; zap's "every Mth thereafter", which keeps more visibility but grows with the rate as well; a per-path key, which a scanner defeats by varying the path. Rejected for now: a single bounded burst is predictable and simplest.

**Failure modes:**
- *Concurrent requests at the window boundary:* atomics only; exactly `Burst` records per window under `-race` (scenario row with 50 clients).
- *Clock steps:* the window uses monotonic time (`time.Now` with its monotonic reading).
- *Count lost on a crash:* accepted; the count is best effort, metrics still count every request.

### D8. Benchmarks, profiles and the CI allocation check
- **Benchmarks:** added where a hot path has none (Benchmarks and profiles rows), with `b.Loop` and `ReportAllocs`; concurrency with `b.RunParallel`.
- **Commands:** `make bench` runs `go test -run '^$' -bench . -benchmem -count=6` per module with `GOWORK=off` and writes `bench/<module>.txt`; `make profile` adds `-cpuprofile` and `-memprofile` per module under `bench/`; `bench/` is ignored by git. `docs/PERFORMANCE.md` explains running them, reading profiles with `go tool pprof` and comparing with `benchstat`.
- **CI:** one job checks out `main` and the branch, runs the benchmarks of both in the same job, compares them with `benchstat`, and fails when `allocs/op` or `B/op` of a benchmark rises more than 10%. Timing differences are reported, never gating: `skills-compliance` task 9.4 saw timings vary by more than 100% between runs on one machine.
- **Measured items:** `answerUnmatched` single routing, fiber `accessInfo` copies, `capAny` formatting, a keyword pre-scan in `RedactSecrets`, the target-cache patterns (one-line request logger, concurrent root logger) and the debug buffer's cost. Each is applied only when `benchstat` shows a 10% or larger gain in allocations, or in `ns/op` with `p < 0.05` on the CI runner, and the comparison is recorded in `notes.md`.

**Failure modes:** a flaky gate (allocation counts of pooled objects vary): the 10% margin and `-count=6` absorb it; a benchmark that panics fails the job like a test.

### D9. `LogWriteErrors` becomes `LogRecordsLost`, without an alias
The counter counts records lost to base-handler errors and panics alike, not only write errors. The library is unreleased, so the rename has no deprecated alias, as `ginsrvr.WithEngine` had none. The rename goes through gopls (core function and the engine counter it reads), then `LOGGING.md`, the specs and `CHANGELOG.md`.

### D10. The release is prepared by the plan and tagged on confirmation
`RELEASING.md` already fixes the order: tag the core, then in each released module require that version and drop the `replace`, then tag the module. The slice does everything up to tagging (merged to `main`, changelog section, `make check`, release-guard dry run), then stops for the owner to confirm the version and the push. Tags are outward-facing and cannot be taken back once the module proxy caches them.

### D11. A request the client closed is answered with 499, by srvr's own prblm mapper
Decided 2026-11-19 (owner delegated the choice: option (a) of the notes entry "Client disconnect resolves to 500"; the owner then confirmed that srvr keeps its own prblm mapper rather than moving the rule upstream). `engine.Core.ProblemOptions` adds one prblm mapper, `clientClosed` (`engine/clientclosed.go`), after the consumer's prblm options, so every engine shares it. It answers `499` "Client Closed Request" (the nginx, grpc-gateway and Google AIP-193 convention), without `detail`, when the error matches `errors.Is(err, context.Canceled)` and the context prblm resolves with (the request context on every engine) is cancelled.

- *Why:* prblm v0.1.0 maps only `context.DeadlineExceeded` (`504`); `Handler.Respond` answers a cancelled request like any other, so a disconnect became a `500` at `ERROR` that flushed the buffer. OpenTelemetry semantic conventions mark only `5xx` server spans as errors, and a disconnect should neither page nor flush. With `499` the access record is `http.request.failed` at `WARN` (the `4xx` level rule) with `problem.status` `499`: a value change, no key change, so `srvr.log.schema` stays 1.
- *Server-side cancellation stays visible:* a `context.Canceled` returned while the request context is live is a server bug and stays `500`; so does one returned while the request context ended by its deadline (`ctx.Err()` is `context.DeadlineExceeded`, a server-side timeout such as a consumer timeout middleware), which the client did not cause. Only `ctx.Err()` matching `context.Canceled` counts as the client going away.
- *Precedence:* the consumer's mappers, scoped mappers and describers run before it (prblm resolution order). It declines when prblm's built-ins or a `prblm.WithStatus` hint would answer with another status than the `500` fallback (for example `prblm.ErrUnavailable` wrapping the cancellation, or `context.DeadlineExceeded` joined to it), checked by resolving the error with a built-ins-only prblm handler, on this rare path only. A hint of exactly `500` cannot be told from the fallback and is answered `499`.
- *Forced drain:* at the drain deadline net/http closes connections and fiber cancels its request contexts; a handler returning `context.Canceled` then also gets `499`. The client gets no response either way.
- *fiber:* fasthttp does not detect a disconnect and srvr's fiber request context (derived from the server's base context) is cancelled only by a forced drain, so on fiber a disconnect stays `500` (engine-parity exception row).
- *Alternatives considered:* a built-in rule upstream in prblm (not chosen: owner wants srvr to own the mapper; prblm may still adopt it later). (b) the buffer treats a cancelled request context as not failed: it would leave a `500` `ERROR` record for a disconnect and split the flush rule from the status. (c) expect a flush: a disconnect is not a server failure.

### D12. srvr adds its error hook with `prblm.AddErrorHook`; `WithErrorHook` stays
Decided 2026-11-19 (owner request). prblm v0.2.0 keeps `prblm.OnError` as the one replaceable, application-owned hook and adds `prblm.AddErrorHook` for libraries: added hooks are never replaced, each report calls the `OnError` hook first and then the added hooks in order, and a panicking hook no longer stops the later ones. `engine.Core.ProblemOptions` now ends with `prblm.AddErrorHook(c.onError)` instead of `prblm.OnError(c.onError)`; every engine builds its renderers from that list (net/http's `Core.Problems`, ginsrvr's `ginprblm.Middleware`/`Recovery`, fibersrvr's `fiberprblm.ErrorHandler`), so the switch covers all three.

- *Why:* core-server design D4 had srvr own the single `OnError` slot and override a consumer's `prblm.OnError`, documented on `WithProblemOptions`. With `AddErrorHook` the consumer's hook runs and srvr's recording still always runs: `prblm.OnError(nil)` or a later `prblm.OnError` replace only the consumer's slot.
- *Order and faults:* the consumer's `OnError` runs before srvr's hook, then any consumer `AddErrorHook` passed through `WithProblemOptions` (added before srvr's, since srvr's options come last), then srvr's hook, which records the failure and calls the `WithErrorHook` hook. A panicking consumer hook is contained by prblm and srvr still records; a consumer hook that blocks delays srvr's hook and the response, the same as a blocking `WithErrorHook` (not guarded; consumer code runs on the request goroutine).
- *`WithErrorHook` stays:* it is public API, typed as `srvr.ErrorHook`, validated (nil fails construction), contained by srvr's own `recover`, and guaranteed to run after srvr recorded the failure, so a hook reading srvr state sees it. It is now one of two equivalent ways to observe answered errors rather than the only one; removing it pre-v1 would break consumers for no gain. Godoc on both options states the relation.
- *Alternatives considered:* keep `prblm.OnError` and document the limitation (rejected: owner request; prblm now offers the composable hook). Drop `WithErrorHook` (rejected: breaking with no gain).

### D13. `otelsrvr` and `otelsetup` `ConfigError` keep their cause
Decided 2026-11-19 (owner decision). Slice 9's typed `ConfigError{Field, Value, Reason}` unwrapped only to the module's `ErrConfig`, so an `OTLP.Endpoint` parse error (`*url.Error`, `*net.AddrError`), a `Prometheus.Addr` error or the meter's instrument-creation error was reachable only as text. Each module's `ConfigError` gains a `Cause error` field, and `Unwrap() []error` returns `ErrConfig` followed by `Cause` when it is non-nil, so `errors.Is(err, ErrConfig)` and `errors.Is`/`errors.As` on the cause both work.

- *Text unchanged:* the cause's text is already part of `Reason` (or the unexported message), so `Error()` does not change; the messages of every invalid case are diffed before and after.
- *Nil cause:* rules with no underlying error (nil values, ranges, a recovered panic value) leave `Cause` nil and `Unwrap` returns `ErrConfig` alone, never a nil element.
- *Core stays as is:* `engine.ConfigError` has no wrapped cause today (its rules produce no underlying error), so the field is not added there ahead of need (Gall's Law); the name `Cause` is the one to use if it ever does.
- *Alternatives considered:* `fmt.Errorf("%w")` wrapping inside `Reason` (rejected: `Reason` is a string; it would change the type). Unwrapping to the cause only (rejected: `errors.Is(err, ErrConfig)` must keep working).

## Defaults

| Behaviour | Default | Why | Override |
|-----------|---------|-----|----------|
| Buffering | off | cost and content change are the consumer's choice (D1) | `WithDebugBuffer(...)`, `WithDebugBufferConfig(DebugBufferConfig{Enabled: true})`, `LogConfig.DebugBuffer.Enabled` |
| Buffer level | `DEBUG` | the level production normally drops | `BufferLevel(l)`, `DebugBufferConfig.Level`; must not be above a static log level (equal buffers nothing; not checked with `WithLogLevelVar` or a consumer handler) |
| Flush threshold | status `500`+ or panic | server faults are what triage is for; 4xx are usually client errors | `FlushAtStatus(n)`, `DebugBufferConfig.FlushStatus` (100-599) |
| Records per request | 100 | enough for a request's story; bounded | `MaxRecords(n)`, `DebugBufferConfig.MaxRecords` |
| Bytes per request | 64 KiB | bounded memory with headroom | `MaxBytes(n)`, `DebugBufferConfig.MaxBytes` |
| Process-wide bytes | 16 MiB | bounds total memory under load spikes | `TotalBytes(n)`, `DebugBufferConfig.TotalBytes` (must be at least `MaxBytes`) |
| Unmatched-record limit | off (`Burst` `0`) | failed records are unsuppressible by design; dropping any is the consumer's choice (D7) | `WithUnmatchedRecordLimit(burst, interval)`, `AccessLogConfig.UnmatchedLimit` |
| User agent in access records | logged | identifies bots and client versions in triage | `WithoutAccessLogUserAgent()`, `AccessLogConfig.OmitUserAgent` |

Options and `WithDebugBufferConfig` apply in argument order and the later one wins: `WithDebugBufferConfig` sets every field (zero meaning the default), an option sets one field and `WithDebugBuffer` also sets `Enabled`. `DebugBuffer` stays nested in `LogConfig` so config loaders fill it with the rest of the logging settings; as a consequence a later `WithLogConfig` replaces it too, so `WithDebugBuffer(), WithLogConfig(LogConfig{Format: "json"})` turns buffering off. Godoc on both options states this.

## Hot Paths

- Wrapper `Enabled` and `Handle` for every log call made with a request context. These cover both branches (write immediately, buffer) and the sealed-buffer branch.
- Append: clone, size estimate, budget reserve, ring eviction.
- Outcome point: seal, flush decision, replay loop with guard, budget release, summary attributes.
- Error branches: budget exhausted, oversized record, handler error or panic during replay, invalid configuration.
- Unmatched-record limiter: admit decision, window rollover, count report on the next record and at `server.stopped`.
- Construction validation joining every field error; fiber's stop path with a stuck listener.

## Risks / Trade-offs

- [Per-call cost when enabled] → opt-in; a benchmark task records `ns/op` and allocations with buffering on and off.
- [Memory pressure under load] → per-request and process-wide caps; the budget is enforced atomically and tested under `-race`.
- [Flushed `DEBUG` content may be more sensitive] → the same redaction and `ReplaceAttr` path apply; godoc warns; there is no opt-in that loosens redaction.
- [Consumer handlers that filter inside `Handle`] → documented with a scenario; srvr cannot force them.
- [Assumptions about the core-server and engines pipeline] → checked by consolidation task 1.3; mismatches are fixed in this change's artifacts before coding.

- [Rate limit hides an attack's shape] → off by default; the count is reported and metrics still count every request.
- [CI benchmark job flakes or slows CI] → it gates only allocations with a 10% margin; timings are reported only.
- [Breaking rename] → unreleased; listed in `CHANGELOG.md` with its replacement.
- [A release tag is permanent] → tags only after owner confirmation (D10).

## Migration Plan

The debug buffer and the access-log controls are additive and opt-in; `srvr.LogWriteErrors` is renamed to `srvr.LogRecordsLost` (breaking, pre-v1, in `CHANGELOG.md`). This change ends with the first release (D10). `srvr.log.schema` keeps its version: the new keys are additive (project policy), and the golden-record test is extended with them. Rolling back a feature is disabling its option; a published tag is never moved or deleted, a fix ships as the next version.
