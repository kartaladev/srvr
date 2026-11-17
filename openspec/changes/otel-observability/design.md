# Design

## Context

See proposal.md - Why. Constraints:
- The core module stays standard library plus prblm (config context); OpenTelemetry may only appear in `otelsrvr` (API only) and `otelsetup` (SDK and exporters). The OpenTelemetry specification forbids instrumentation libraries from referencing the SDK.
- Versions verified during exploration: otel, metric, trace, sdk, sdk/metric, OTLP exporters v1.47.0; Prometheus exporter v0.69.0 (pre-1.0, registers on `prometheus.DefaultRegisterer` unless given a registerer); semconv v1.43.0 used by otelhttp, otelgin and the fiber contrib module.
- Measured footprint for a consumer: OTel API ~18 modules, SDK ~24, Prometheus exporter + promhttp ~51, OTLP ~90.
- `core-server` gives the pipeline order trace context -> access log -> recovery -> router, an internal trace-context source, `r.Pattern` route derivation, `ServiceConfig`, `AccessLogConfig.IncludePath` (set by `WithAccessLogConfig` or `WithAccessLogIncludePath()`), `TraceConfig.IgnoreIncoming` (set by `WithTraceConfig` or `WithIgnoreIncomingTraceContext()`) - separate structs, not fields of `srvr.Config` - and the shutdown-hook mechanism (`srvr.WithShutdownHook` with `BeforeDrain`/`AfterDrain` phases, `ShutdownHookTimeout` default 5s, run on every stop path). `gin-and-fiber-engines` gives two more engines. Neither is merged when this is planned; consolidation group 1 re-checks every name used below.

## Goals / Non-Goals

**Goals:**
- One engine-neutral hook so metrics and tracing behave identically on net/http, gin and fiber.
- `otelsrvr` that a library-minded consumer can add without the SDK; `otelsetup` that an application can add to get Prometheus or OTLP in a few lines.

**Non-Goals:**
- Reusing `otelhttp`, `otelgin` or `gofiber/contrib/v3/otel`. Three third-party instrumentations with their own semconv upgrade schedules would make cross-engine parity untestable and pull gin/fiber-specific modules into `otelsrvr`.
- OTLP logs, outbound client instrumentation, runtime metrics, exemplars.

## Slices

1. **Observer hook and duration metric on net/http**: `Observer`, `RequestInfo`, `ResultInfo`, `WithObserver` in core, called on the net/http engine; `otelsrvr.New` recording `http.server.request.duration` through an injected `MeterProvider`, verified with an SDK `ManualReader` in tests. Satisfies request-observer (Observers are called once per request - net/http rows; Observers sit between the access log and recovery; Result information is complete and bounded), otel-metrics (Request duration is recorded - net/http row; Provider selection). Hot paths: observer begin/end in the pipeline, record.
2. **Observer on gin and fiber**: call the same hook from both engines through the shared observer stage in `srvr/engine`, placed outside ginprblm's middleware and fiberprblm's error handling, route template, status and failure read from `engine.RequestState` after routing, in each engine's own syntax (gin `c.FullPath()`, fiber matched route; on fiber the access stage has already called the error handler). Satisfies request-observer and otel-metrics engine rows. Hot paths: engine adapters' begin/end.
3. **Observer composition and fault isolation**: multiple observers, nil, typed-nil and faulty observers, fault logging once per observer and fault kind. Satisfies request-observer (Several observers compose in order; Faulty observers never break requests). Hot paths: guarded begin/end.
4. **Bounded attributes, buckets and optional instruments**: method normalisation, `http.route` omitted when unmatched, `error.type` through `semconv.ErrorType`, bucket validation, `otelsrvr.WithConfig` and option precedence, active requests, body sizes, provider faults. Satisfies otel-metrics (remaining). Hot paths: attribute building, optional instruments.
5. **Tracing and trace-source**: trace-source extension in core, `WithTracing`, server spans, span path attributes tied to `IncludePath`, propagator extraction with W3C fallback and core's ignore-incoming opt-in, correlation following the span, guaranteed span end. Satisfies request-observer (Observers may supply the trace context), otel-tracing (all). Hot paths: extract, start/end span, trace-source lookup in logging and problem enrichment.
6. **otelsetup with Prometheus**: `otelsetup.New(ctx, opts...)`, `WithConfig` and options, resource (Config beats `OTEL_SERVICE_NAME`), Prometheus reader on a dedicated registry and private listener, `SetGlobal`, idempotent bounded `Shutdown`, `ShutdownHook()` registering it as a core `AfterDrain` hook. Satisfies telemetry-setup (Setup builds providers - Prometheus rows; Setup is configurable - non-OTLP rows; Prometheus metrics are served on a separate private listener; Setup can install global providers on request; Shutdown flushes - Prometheus and misuse rows; Server that never served). Hot paths: scrape handler, shutdown sequence.
7. **OTLP export and export-failure isolation**: OTLP http/protobuf and gRPC for metrics and traces, endpoint precedence over `OTEL_EXPORTER_OTLP_ENDPOINT`, error-handler routing to srvr's logger with rate limiting. Satisfies telemetry-setup (remaining, including Ordering with the server against a fake collector). Hot paths: exporter construction, error handler.

## Decisions

### D1. srvr instruments itself through one hook instead of contrib instrumentations
`Observer.Begin(ctx, RequestInfo) (context.Context, func(ResultInfo))`. The observer stage lives in the public `srvr/engine` package (introduced by `gin-and-fiber-engines` D1) next to the trace and access stages, so all three engines call one implementation. Engines build `RequestInfo` (method, scheme, host, flavor) before routing and `ResultInfo` (route, status, sizes, duration, error kind, error, committed) after. Alternative: otelhttp/otelgin/fiber contrib - rejected (Non-Goals). Alternative: put OTel types in the hook - rejected by the core dependency rule.
**Failure modes:** an engine computes the route at the wrong moment (fiber middleware sees its own route) - each engine's route source is pinned by the Examples rows per engine; fasthttp context reuse in fiber - `ResultInfo` copies strings before the end function runs; fiber renders a returned error only after the outermost middleware returns, so a middleware reading `c.Response().StatusCode()` after `c.Next()` sees `200` for a handler error and for an unmatched route, and `c.Route().Path` is the middleware's `/` when nothing matched (probe against fiber v3.7.0) - error rendering has one owner: the access stage calls the error handler (gin-and-fiber-engines D7) and records status, matched route (empty when `!c.Matched()`) and failure in `engine.RequestState`; the observer stage only reads that state and never renders, pinned by the fiber error and unmatched rows; route templates stay in each engine's syntax (`/users/{id}` on net/http, `/users/:id` on gin and fiber), as in the access log (engine-parity).

### D2. Observer placement: inside access log, outside recovery and error rendering
So the end function sees the final status of recovered panics and rendered errors, and the access log already has the trace id. On gin the order is srvr trace -> access -> observer -> `ginprblm.Middleware` -> `ginprblm.Recovery`; placing the observer after `ginprblm.Middleware` would see the pre-render status. Alternative: outermost - rejected, the observer could not supply the trace context to the access record.
**Failure modes:** observer panics - guarded per call, reported once per (observer, fault kind) via `sync.Once`-style flags; nil context or nil end - replaced by the incoming context or a no-op; blocking observer - not mitigated (synchronous by design, documented, same as logging).

### D3. Trace source as an optional interface on the observer
`type TraceSource interface { TraceFromContext(ctx) (srvr.Trace, bool) }`; core consults sources (last registered wins) before its own context value when building logs and the problem `trace_id`. When `otelsrvr` tracing is on, its source returns the active span's ids. An all-zero or invalid id from a source is ignored. Because the access log stage sits outside the observer, it never sees the context the observer returned (a `r.WithContext` made by an inner middleware is invisible to the outer one; probe confirms), so the trace source lives in the shared per-request holder `engine.RequestState` (gin-and-fiber-engines `srvr/engine`, created by the outermost stage - trace context - before calling inward, per gin-and-fiber-engines design D7). That change defines the struct with route, matched, status, failure and trace context only; this change ADDS a trace-source field to it, in the slice that first writes it (slice 5, task 6.1); the observer stage stores the derived context's trace there and the access record reads it. No separate holder is introduced. Alternative: let the observer overwrite core's context value - rejected, it would hide which component owns the id.
**Failure modes:** tracing on but the global propagator is a no-op - otelsrvr falls back to W3C TraceContext (D8), so enabling tracing never loses an incoming `traceparent` that core honoured; unsampled span - non-recording span still has valid ids, logs use them.

### D4. Cardinality policy and `error.type`
Attributes limited to method (known set + `_OTHER`), route template (omitted when unmatched, as semconv's "if and only if available" requires), status code, scheme and `error.type`. `error.type` is set whenever the request failed (core's `http.request.failed`, or any 5xx) and follows OpenTelemetry Go's `semconv.ErrorType`: an `ErrorType() string` method anywhere in the chain wins (first match by `errors.As`); otherwise the concrete type after unwrapping single-`%w` `fmt.Errorf` wrappers - `pkgpath.Name` for named types, `t.String()` for pointers (`*net.OpError`, `*errors.errorString`). Joined errors (`errors.Join`, multi-`%w` `fmt.Errorf`) are not unwrapped: `*errors.joinError` / `*fmt.wrapErrors`, unless a member has `ErrorType()` (probe `TestSemconvJoined`, otel v1.47.0). Recovered panics are `panic`; failures without an error value (5xx written directly, unmatched 404/405) are the status code string. Logs keep core-server's Go-type `error.type`, and panics there are `panic` too with the value's type in `exception.type`; metric and log values for returned errors may differ by design (owner decision D2, 2026-11-17). `server.address`, user agent and client address are excluded; `url.path` and `http.request.method_original` appear on spans only (never on metrics) when core `AccessLogConfig.IncludePath` is on, truncated by core's attribute limit. Alternative: follow semconv strictly and set `error.type` only for 5xx - rejected because 4xx returned errors are the main signal for client-contract regressions; accepted by the owner during planning. Spans still leave status `Unset` for 4xx (semconv MUST).
**Failure modes:** a consumer returns dynamically created error types (e.g. per-request generic instantiations) - bounded by code, documented; `%T` of anonymous structs - acceptable.

### D5. Prometheus on a separate private listener by default
`otelsetup` runs its own `http.Server` on `127.0.0.1:9464` serving a `promhttp.HandlerFor(registry)` from a dedicated `prometheus.Registry` passed via `prometheus.WithRegisterer`. `Listen: false` plus `MetricsHandler()` lets the consumer mount it instead. Alternative: mount `/metrics` on the application listener - rejected as a default, it exposes internals publicly and mixes scrape traffic into application metrics and logs; Kubernetes scrapes pods by port, so a separate port is the common practice (9464 is the OpenTelemetry Prometheus exporter's conventional port). Loopback default is safe but needs a pod-level override (`Addr: ":9464"`) to be scraped from outside the pod - documented prominently.
**Failure modes:** port in use - `New` fails and shuts down anything already built; two setups in one process - separate registries; exporter pre-1.0 API changes - pinned minor version, consolidation re-checks.

### D6. Shutdown ordering through core's AfterDrain hook
This change defines no shutdown mechanism; it uses core-server's (owner decision D5): `srvr.WithShutdownHook(srvr.AfterDrain, fn)` runs after the drain, once, on every stop path - `Start` returning because its context ended, `Shutdown`, `Shutdown` of a server that never started, and every `Start` failure after the server leaves its initial state (configure, route check, bind, already-cancelled context); never for `Start(nil)`, `srvr.ErrServerStarted` or `srvr.ErrServerClosed` (owner decision A4). `setup.ShutdownHook()` returns `srvr.WithShutdownHook(srvr.AfterDrain, setup.Shutdown)`; `setup.Shutdown` flushes providers then stops the metrics listener, bounded by `min(ctx, FlushTimeout)` where `ctx` carries core's `ShutdownHookTimeout` budget (default 5s), which is separate from the 15s drain, so a slow drain never starves the flush. `otelsrvr` registers no hook: it uses the API only and owns nothing to flush; a consumer with its own SDK providers registers `srvr.WithShutdownHook(srvr.AfterDrain, tp.Shutdown)`. Alternative: consumer orders shutdown manually - kept possible (`setup.Shutdown` is idempotent).
**Failure modes:** hook error or panic - handled by core (logged, joined into the stop path's error, later hooks still run); collector hangs - bounded by `FlushTimeout` and the hook budget; consumer calls `setup.Shutdown` again - `nil`.

### D7. Export errors routed to srvr's logger
`otelsetup` installs an `otel.ErrorHandler` only when `SetGlobal` is true (the handler is global); otherwise errors go through exporter-level options where available. Records are `otel.export.failed` at WARN, rate-limited per error kind per minute.
**Failure modes:** error storms - rate limited; logger itself failing - counted by core's `LogWriteErrors`.
`otel.export.failed` and `srvr.observer.fault` are new log event names; adding them keeps `srvr.log.schema` unchanged (additive keys do not bump it) and both are added to the golden-record tests.

### D8. Propagator default and ignored incoming context
otelsrvr uses `WithPropagator` if given, else the global propagator; when that declares no fields (`len(p.Fields()) == 0`, the no-op default) it uses `propagation.TraceContext{}`. A global propagator with any fields (for example Baggage only) is respected as configured. When core `TraceConfig.IgnoreIncoming` (`WithIgnoreIncomingTraceContext()`) is on, otelsrvr skips extraction so every span is a new root, matching core. Alternative: require `WithPropagator` with tracing - rejected, a safe default should not need it.
**Failure modes:** the global propagator is replaced after `otelsrvr.New` - read per request, so the new one applies; a hostile `traceparent` - the propagator rejects it and the span is a new root (Parent extraction rows).

### D9. Configuration surface
`otelsrvr.New(opts...)` and `otelsetup.New(ctx, opts...)` take functional options; `otelsrvr.WithConfig(otelsrvr.Config)` and `otelsetup.WithConfig(otelsetup.Config)` set every field from a struct a config loader filled (zero = default). Options apply in argument order, later wins (owner decision D13). srvr reads no environment variables; in `otelsetup` the SDK and OTLP exporters still read their standard `OTEL_*` variables for fields srvr leaves empty (owner decision D12), and an explicit value always wins - the SDK's own precedence, pinned by the Configuration precedence rows. An empty OTLP `Endpoint` is therefore accepted and resolved by the SDK.
**Failure modes:** an operator's `OTEL_EXPORTER_OTLP_ENDPOINT` silently redirecting export - only when the consumer left `Endpoint` empty, documented in godoc and the README; `OTEL_RESOURCE_ATTRIBUTES` adds attributes - srvr's `service.*` attributes are merged last and win.

## Defaults

| Behaviour | Default | Why | Override |
|-----------|---------|-----|----------|
| Observer | none | zero-config keeps core unchanged and dependency-free | `WithObserver` |
| Meter/Tracer provider | globals | OTel library guidance | `WithMeterProvider`, `WithTracerProvider` |
| Propagator | global, W3C TraceContext when the global declares no fields; no extraction with core `TraceConfig.IgnoreIncoming` (`WithIgnoreIncomingTraceContext()`) | OTel library guidance without silently dropping `traceparent` | `WithPropagator` |
| Span `url.path`, `http.request.method_original` | off | privacy, same switch as the access log | core `AccessLogConfig.IncludePath` |
| Config sources | options and `WithConfig`, in argument order | project rule 7 | `otelsrvr.WithConfig`, `otelsetup.WithConfig` |
| Unset otelsetup fields | the SDK's `OTEL_*` environment, else SDK defaults | standard operator interface | set the field explicitly |
| Tracing | off | spans cost more and need a backend | `WithTracing`, `Config.Tracing` |
| Optional instruments | off | fewer series; these semconv metrics are Development (Opt-In), only `http.server.request.duration` is Stable | `WithActiveRequests`, `WithBodySizes`, `Config.ActiveRequests`, `Config.BodySizes` |
| Buckets | semconv advisory | comparable dashboards | `WithDurationBuckets`, `Config.DurationBuckets` |
| Prometheus listener | `127.0.0.1:9464/metrics`, own registry | not public; no global state | `Prometheus.Addr`, `Path`, `Listen` |
| Globals | untouched | library hygiene | `SetGlobal` |
| Flush timeout | 5s | runs in core's `AfterDrain` budget (`ShutdownHookTimeout` 5s) after the 15s drain; 20s total fits the 30s k8s grace | `FlushTimeout`, `WithFlushTimeout`, core `ShutdownHookTimeout` |

## Hot Paths

- Per request: observer begin/end (all engines), guarded calls, attribute building, histogram record, optional counters, span start/end, propagator extract, trace-source lookup used by logging and problem enrichment.
- Error branches: observer faults, provider shut down, invalid incoming context, panic and abort span ending.
- Setup: provider construction rollback on failure, scrape handler, shutdown sequence with deadline.

## Risks / Trade-offs

- [srvr owns semconv compliance] -> attribute and metric names centralised with a golden test against semconv v1.43; consolidation re-checks semconv on upgrade.
- [`error.type` on 4xx deviates from strict semconv] -> accepted by the owner (D4); easy to switch before v1.
- [Log and metric `error.type` differ for the same returned error] -> deliberate (owner decision D2): logs need the precise Go type, metrics need semconv's bounded value; panics are `panic` in both. Documented in both godocs.
- [Spans omit semconv-Required `url.path` by default] -> deliberate privacy deviation; on only with core `IncludePath`.
- [SDK environment variables can change otelsetup behaviour] -> accepted (owner decision D12); explicit fields win and godoc lists the variables involved.
- [Optional metrics are semconv Development] -> names may change upstream; opt-in only and re-checked by consolidation on each semconv upgrade.
- [Prometheus exporter is pre-1.0] -> isolated in `otelsetup`; consumers of `otelsrvr` never see it.
- [Loopback default surprises Kubernetes users] -> documented with the pod `Addr` override; `otelsetup` logs the listening address at INFO.
- [Global error handler is process-wide] -> only installed with `SetGlobal`.

## Migration Plan

Additive. Existing `core-server` consumers are unaffected until they add `WithObserver`. Release core minor, `otelsrvr/v0.1.0`, `otelsetup/v0.1.0`.

## Open Questions

- Whether `otelsetup` should also offer a stdout exporter for local development (does not change specs of the other requirements; can be added later).
