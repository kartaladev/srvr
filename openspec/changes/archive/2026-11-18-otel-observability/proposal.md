# Proposal

## Why

After `core-server` and `gin-and-fiber-engines`, srvr services produce triage-ready logs but no metrics and no distributed traces, so latency regressions, error-rate spikes and cross-service failures can only be found by reading logs. The requirement is configurable observability with OpenTelemetry preferred and Prometheus supported, while keeping the core module free of OpenTelemetry (Hybrid B, decided during exploration). This change adds metrics and tracing as opt-in modules, behind one engine-neutral hook that all three engines call.

## What Changes

- Core module (`github.com/kartaladev/srvr`), no new dependency: an engine-neutral `Observer` hook, declared in `srvr/engine` and re-exported by srvr (gin-and-fiber-engines D1 Option A), - `Begin(ctx, RequestInfo) (context.Context, func(ResultInfo))` - registered with `WithObserver`, called once per request by the net/http, gin and fiber engines, between the access log and panic recovery. A trace-context source hook lets an observer supply the request's trace id and span id, so logs and problems use the active span when tracing is on. Per-request state the observer needs (route, status, failure) lives in the shared `engine.RequestState` from `gin-and-fiber-engines`; this change adds the trace-source field to it.
- New module `github.com/kartaladev/srvr/otelsrvr` (OpenTelemetry API only - `go.opentelemetry.io/otel`, `/metric`, `/trace` v1.47.x - plus the srvr core for the observer types; never the SDK or exporters outside tests):
  - `http.server.request.duration` histogram (seconds) with semantic-convention v1.43 attributes, bounded cardinality.
  - Optional `http.server.active_requests`, `http.server.request.body.size`, `http.server.response.body.size`.
  - Optional server spans with propagation through the configured propagator (the global one, falling back to W3C TraceContext when the global one is a no-op); when enabled, srvr's `trace_id`/`span_id` come from the active span.
- New module `github.com/kartaladev/srvr/otelsetup` (OpenTelemetry SDK and exporters, opt-in application wiring): `MeterProvider`/`TracerProvider` construction, a Prometheus exporter on its own registry served on a separate metrics listener, OTLP over HTTP or gRPC, a resource built from srvr's `ServiceConfig`, and provider flushing registered as a core `srvr.AfterDrain` shutdown hook (`setup.ShutdownHook()`), so it runs on every stop path. Empty fields fall back to the SDK's standard `OTEL_*` environment variables; explicit configuration always wins.

## Defaults and Customization

Zero configuration (consumer adds nothing): no observer, no metrics, no spans, no new dependency; behaviour of `core-server` is unchanged.

Consumer adds `WithObserver(otelsrvr.New())`:
- Metrics: only `http.server.request.duration`, recorded through the global `MeterProvider` (a no-op until the application installs one).
- Tracing: off. Incoming `traceparent` keeps working exactly as in `core-server`.
- Attributes: `http.request.method` (unknown methods as `_OTHER`), `http.route` (the matched template; omitted for unmatched requests, as semantic conventions require), `http.response.status_code`, `url.scheme`, `error.type` on failed requests (`semconv.ErrorType` of the returned error, `panic` for a recovered panic, the status code as a string for a failure without an error value: a 5xx written directly or an unmatched 404/405). No raw path, query, client address, user agent or server address. Logs keep their own `error.type` (the Go type, core-server); the two differ by design.

Opt-ins:
- `WithMeterProvider`, `WithTracerProvider`, `WithPropagator` (defaults: the globals; the propagator falls back to W3C TraceContext when the global one declares no fields). Core's ignore-incoming opt-in (`WithIgnoreIncomingTraceContext()` or `WithTraceConfig(TraceConfig{IgnoreIncoming: true})`) also stops otelsrvr's extraction.
- Every setting is available as an option and through `otelsrvr.WithConfig(otelsrvr.Config)` / `otelsetup.WithConfig(otelsetup.Config)`; options apply in argument order.
- Spans get `url.path` and `http.request.method_original` only when core `AccessLogConfig.IncludePath` is on (the same privacy switch as the access log).
- `WithTracing()` / `Config.Tracing` turns on server spans and switches log and problem `trace_id` to the span's.
- `WithActiveRequests()`, `WithBodySizes()`, custom histogram bucket boundaries.
- `otelsetup`: Prometheus on a separate listener (default `127.0.0.1:9464`, path `/metrics`) - **binding it to a public address loosens an exposure default**; OTLP endpoint, protocol and headers; flush timeout (default 5s).

## Capabilities

### New Capabilities
- `request-observer`: the engine-neutral per-request observer hook in the core module, its position in the pipeline, its contract and its fault isolation.
- `otel-metrics`: the HTTP server metrics `otelsrvr` records, their attributes, units, buckets and cardinality bounds.
- `otel-tracing`: optional server spans, context propagation, and sourcing srvr's trace correlation from the active span.
- `telemetry-setup`: opt-in SDK and exporter wiring in `otelsetup` - Prometheus, OTLP, resource, lifecycle and flushing.

### Modified Capabilities
- `engine-support`: "Engines share one per-request state" - the state also holds the trace an observer's trace source supplied (consolidation 2026-11-18). `trace-context` and `structured-logging` need no delta: their rows speak of the request's trace id, which request-observer "Observers may supply the trace context" defines when a source is present.

## Scope by Gall's Law

Simplest working version: the `Observer` hook on the net/http engine and `otelsrvr` recording `http.server.request.duration` through an injected `MeterProvider`, verified with an SDK `ManualReader` in tests. Then: hook on gin and fiber, then fault isolation, then the optional metrics, then tracing, then `otelsetup` with Prometheus, then OTLP.

Deliberately left out: logs over OTLP (logs stay JSON on stdout), client-side (outbound) instrumentation, exemplars, runtime/process metrics (consumers can add `contrib/instrumentation/runtime` themselves), auto-instrumentation of scrty endpoints (`scrty-integration`).

## Impact

- Core module: new public `Observer`, `RequestInfo`, `ResultInfo`, `WithObserver` and a trace-source hook - **compatibility decision** (public API). Core dependency set unchanged.
- Two new modules with new dependency sets: `otelsrvr` (~18 modules, OTel API) and `otelsetup` (SDK ~24, Prometheus exporter ~51, OTLP ~90). The Prometheus exporter is pre-1.0 (v0.69.x).
- Metric names and attributes become a public contract alongside the log schema.

Assumptions about earlier changes (checked by consolidation group 1 on 2026-11-18 against the merged code; the corrections are in design D1-D3 and notes.md "Consolidation 2026-11-18"):
- `core-server`: request pipeline order trace context -> access log -> recovery -> router exists; `srvr.Trace` and `TraceFromContext` exist; `http.route` is derived from `r.Pattern` without the method; `ServiceConfig` exposes name, version and environment; core owns shutdown hooks - `srvr.WithShutdownHook(srvr.BeforeDrain|srvr.AfterDrain, fn)`, `AfterDrain` hooks bounded by `Config.ShutdownHookTimeout` (default 5s) separately from the 15s drain, run once on every stop path (`Start` context cancelled, `Shutdown`, `Shutdown` of a never-started server, every `Start` failure after the server leaves its initial state; never `Start(nil)` or misuse errors); `AccessLogConfig.IncludePath` and `TraceConfig.IgnoreIncoming` exist.
- `gin-and-fiber-engines`: `ginsrvr` and `fibersrvr` reuse core's engine-neutral pipeline pieces, expose the matched route template (gin `c.FullPath()`, fiber route path read after `c.Next()` or `c.Endpoint()`), and accept core server options such as `WithObserver`. They share the pipeline through the public `srvr/engine` package (gin-and-fiber-engines D1), so the observer stage is added there once; route templates stay in each engine's syntax (`/users/:id` on gin and fiber). One per-request holder, `engine.RequestState` (`engine.StateFrom(ctx)`), carries route, matched flag, status, failure and trace context; this change adds a trace-source field to it; on fiber the access stage calls the error handler (gin-and-fiber-engines D7), so the observer only reads the final status; unmatched 404/405 are failed requests on every engine.
