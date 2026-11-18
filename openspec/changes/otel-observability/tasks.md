# Tasks

## 1. Consolidation: refresh plan against merged changes

- [x] 1.1 Sync with the main branch and list every change merged or archived since this change was planned (expected: `core-server`, `gin-and-fiber-engines`); verify they are named in the completion note
- [x] 1.2 Decide every open `notes.md` carry-over from earlier changes addressed to `otel-observability` or `unassigned` (adopt, defer or close); verify none is left undecided
- [x] 1.3 Refresh this change's proposal, specs, design and tasks against the current main specs and merged code - in particular the pipeline order, `srvr.Trace`/`TraceFromContext`, route derivation per engine, `ServiceConfig`, core's `WithShutdownHook` phases and `ShutdownHookTimeout`, `AccessLogConfig.IncludePath`, `TraceConfig.IgnoreIncoming` (`WithIgnoreIncomingTraceContext()`), the fields of `engine.RequestState` (route, matched, status, failure, trace context; this change adds the trace source), whether the fiber access stage owns error rendering, whether `trace-context`/`structured-logging` need MODIFIED deltas, and current otel/semconv/Prometheus exporter versions; verify `openspec validate otel-observability --strict` passes
- [x] 1.4 Record the consolidation outcome at the top of `openspec/changes/otel-observability/notes.md`; verify the file lists what was refreshed or states none was needed
- [x] 1.5 Commit the consolidation as `docs(openspec): consolidate otel-observability against main`, or skip when 1.1-1.4 changed nothing; verify with `git log -1 --stat`

## 2. Slice: observer hook and duration metric on net/http

- [x] 2.1 Create module `github.com/kartaladev/srvr/otelsrvr` (go 1.27) requiring only `go.opentelemetry.io/otel`, `/metric`, `/trace` and the srvr core through `replace ../` until it is tagged (SDK only in `_test.go` via a test-only require), add it to `go.work` and a CI job (no `release-modules` target exists; tagging is the "Workflow follow-up" step); verify `GOWORK=off go list -deps ./otelsrvr | grep -c sdk` prints `0` for non-test packages
- [x] 2.2 Add a dependency-guard test in the core module asserting it imports no `go.opentelemetry.io` package; verify `go test -run TestCoreHasNoOTel` passes
- [x] 2.3 Observer called once per request on net/http (request-observer / Scenario: Begin and end per engine, net/http rows); verify `go test -race -run TestObserver_NetHTTP` passes
  - RED: table over the net/http rows (including the unmatched row) with a recording observer; fails because `WithObserver` does not exist
  - GREEN: `Observer`, `RequestInfo`, `ResultInfo` in `engine` with srvr aliases, `WithObserver`, the `Core.Observe` http stage between `LogAccess` and `LimitBody`, reading route, status and failure from `engine.RequestState`
  - REFACTOR: Extract Function for building `ResultInfo` from the request state, then /simplify
- [x] 2.4 Observer placement (request-observer / Scenario: Observer view of the request, net/http rows); verify `go test -race -run TestObserver_Placement` passes
  - RED: table asserting trace id at Begin and status/kind at end, including the unmatched `GET /nope` row with kind `error`; fails if placed outside trace context or inside recovery
  - GREEN: order the middleware accordingly
  - REFACTOR: /simplify
- [x] 2.5 Result fields (request-observer / Scenario: Result fields, all rows); verify `go test -race -run TestObserver_Result` passes
  - RED: table including unmatched, 405 and late-error rows; fails on sizes or committed flag
  - GREEN: populate sizes, route, committed from the request state
  - REFACTOR: /simplify
- [x] 2.6 Duration recorded through an injected provider (otel-metrics / Scenario: Duration per engine, net/http row; Scenario: Provider selection, all rows); verify `cd otelsrvr && go test -race -run TestDuration` passes
  - RED: table with ManualReader, global, no-op and nil provider; fails because `otelsrvr.New` does not exist
  - GREEN: `otelsrvr.New`, options, histogram in seconds, scope name
  - REFACTOR: Introduce Parameter Object for options, then /simplify
- [x] 2.7 Godoc and an `Example` wiring `otelsrvr` with an SDK ManualReader in a test; verify `go test -run Example` passes
- [x] 2.8 Verify slice end to end (net/http request -> collected data point) and confirm observer begin/end and record are covered in `go tool cover -func`; in the core module and `otelsrvr`, run `GOWORK=off go vet ./...`, `GOWORK=off golangci-lint run ./...` and `GOWORK=off go test -race ./...`; run gopls diagnostics on changed files, run /simplify on the slice and commit `feat(otelsrvr): record request duration through an engine-neutral observer`

## 3. Slice: observer on gin and fiber

- [x] 3.1 Observer on gin (as implemented in slice 1, gin is served through `LogAccess`, so the RED may pass at once: record it and keep the test as the guard) (request-observer / Scenario: Begin and end per engine, gin rows; Scenario: Observer view of the request, gin row; otel-metrics / Scenario: Duration per engine, gin row); verify `cd ginsrvr && go test -race -run TestObserver` passes with a recording observer, and the otel-metrics gin row passes in the test-only `internal/paritytest` module (ginsrvr's go.mod gains no OTel requirement)
  - RED: table; fails because ginsrvr does not call the observer
  - GREEN: the shared `Core.Observe` http stage around the gin engine, between `LogAccess` and `LimitBody` as on net/http (design D1 as merged), outside the whole gin chain; end from `engine.RequestState` (route set by `requestStage` from `c.FullPath()`, failure classified after `c.Next()`)
  - REFACTOR: /simplify
- [x] 3.2 Observer on fiber (same scenarios, fiber rows); verify `cd fibersrvr && go test -race -run TestObserver` passes using `app.Test`, and the otel-metrics fiber row passes in `internal/paritytest`
  - RED: table including the `errors.New("boom")` and unmatched rows; fails because fibersrvr does not call the observer, reports the middleware's `/` route, or reports status `200` before the error is rendered
  - GREEN: `Core.BeginObserve` in `requestStage` after `BeginRequest` and before `c.Next()`; the end functions run from `Core.WriteAccess` with the status the stage passes after calling the error handler (gin-and-fiber-engines D7), and route (empty when unmatched) and failure from `engine.RequestState`, so the state's status needs no live source (closes the carry-over "RequestState.Status is not live on fiber"); the observer stage never calls the error handler; strings copied before end
  - REFACTOR: /simplify
- [x] 3.3 Add a cross-engine parity test in `internal/paritytest` asserting identical `ResultInfo` and data points for the same request on all three engines except `http.route`, which is compared per engine syntax, and that the error handler runs exactly once per failed fiber request; verify `cd internal/paritytest && go test -race -run TestObserverParity` passes
- [x] 3.4 Verify slice end to end on all engines and confirm engine adapter branches are covered in `go tool cover -func`; in `srvr/engine`'s module, `ginsrvr`, `fibersrvr` and `internal/paritytest`, run `GOWORK=off go vet ./...`, `GOWORK=off golangci-lint run ./...` and `GOWORK=off go test -race ./...` (`internal/paritytest` is not released and may use `go.work`); run gopls diagnostics on changed files, run /simplify on the slice and commit `feat(engines): call request observers on gin and fiber`

## 4. Slice: composition and fault isolation

- [x] 4.1 Composition order (request-observer / Scenario: Composition, all rows); verify `go test -race -run TestObserver_Compose` passes
  - RED: table; fails because only one observer is kept
  - GREEN: ordered slice, reverse-order end
  - REFACTOR: /simplify
- [x] 4.2 Faulty observers (request-observer / Scenario: Observer faults, all rows); verify `go test -race -run TestObserver_Faults` passes
  - RED: table with nil, typed-nil, Begin panic, nil ctx, nil end, end panic, end panic with `http.ErrAbortHandler`, repeated panic, blocking `Begin` and blocking end-function rows (the blocking end row asserts status `200` after at least `50ms` and the access record after the end function, owner decision 2026-11-18), a `Begin` returning a context whose `Value` panics, and an end panic whose value's `Error()` panics; fails because panics escape or an observer's `http.ErrAbortHandler` is re-raised; no lock is held while observer code runs
  - GREEN: guarded calls, fallbacks, once-per-kind fault logging
  - REFACTOR: Extract Class `guardedObserver`, then /simplify
  - DOC: the godoc of `engine.Observer` (aliased as `srvr.Observer`) and of package `otelsrvr` states that observers run on the request path and must be fast, and that slow work belongs in the SDK's batch processors
- [x] 4.3 Add the `srvr.observer.fault` record to core's golden-record test (additive key, `srvr.log.schema` unchanged); verify `go test -run TestLogSchemaGolden` passes
- [x] 4.4 Verify slice end to end and confirm guard branches are covered in `go tool cover -func`; in the core module, run `GOWORK=off go vet ./...`, `GOWORK=off golangci-lint run ./...` and `GOWORK=off go test -race ./...`; run gopls diagnostics on changed files, run /simplify on the slice and commit `feat(core): compose observers and isolate their faults`

## 5. Slice: bounded attributes, buckets and optional instruments

- [x] 5.1 Attribute values (otel-metrics / Scenario: Attribute values, normal and hostile rows); verify `cd otelsrvr && go test -race -run TestAttributes` passes
  - RED: table; fails on `_OTHER`, the omitted `http.route` for unmatched requests, and `error.type` for wrapped, `ErrorType()`, joined, panic, `http.ErrAbortHandler` and status-only rows
  - GREEN: method allow-list, route omitted when empty, `semconv.ErrorType` for returned errors, `panic`, status code string for failures without an error value
  - REFACTOR: Replace Conditional with lookup table, then /simplify
- [x] 5.2 Cardinality under scanning (otel-metrics / Scenario: Cardinality under path scanning); verify `go test -race -run TestCardinality` passes
  - RED: 10000 random paths; fails if any raw path leaks or an unmatched request carries `http.route`
  - GREEN: no change expected beyond 5.1; keep test as guard
  - REFACTOR: /simplify
- [x] 5.3 Buckets (otel-metrics / Scenario: Bucket boundaries, all rows); verify `go test -race -run TestBuckets` passes
  - RED: table including equal, NaN, +Inf and `WithConfig` rows; fails because buckets are not validated
  - GREEN: advisory boundaries default, `WithDurationBuckets` and `Config.DurationBuckets` validation
  - REFACTOR: /simplify
- [x] 5.4 Configuration sources (otel-metrics / Scenario: Configuration sources, all rows); verify `go test -race -run TestConfigSources` passes
  - RED: table of option orders; fails because `WithConfig` does not exist or does not reset unset fields to defaults
  - GREEN: `otelsrvr.Config` with `json`/`yaml`/`env` tags, `WithConfig` setting every field, options applied in argument order, validation after all options
  - REFACTOR: /simplify
- [x] 5.5 Optional instruments (otel-metrics / Scenario: Optional instruments; Scenario: Active requests under concurrency); verify `go test -race -count=20 -run 'TestOptional|TestActiveRequests'` passes
  - RED: tables including panicking handlers; fails because instruments do not exist or counter leaks
  - GREEN: up-down counter incremented at Begin, decremented in end; body size histograms
  - REFACTOR: /simplify
- [x] 5.6 Provider faults (otel-metrics / Scenario: Provider faults, all rows); verify `go test -race -run TestProviderFaults` passes
  - RED: table with failing meter, a meter whose instrument constructor panics, a histogram whose `Record` panics, a typed-nil provider and shut-down SDK provider; fails on error wrapping or an escaping panic
  - GREEN: wrap creation errors in `ErrConfig`
  - REFACTOR: /simplify
- [x] 5.7 Golden test of metric names, units and attribute keys against semconv v1.43; verify `go test -run TestMetricSchemaGolden` passes
- [x] 5.8 Verify slice end to end and confirm attribute, config and instrument branches are covered in `go tool cover -func`; in `otelsrvr` and `internal/paritytest`, run `GOWORK=off go vet ./...`, `GOWORK=off golangci-lint run ./...` and `GOWORK=off go test -race ./...` (`go.work` allowed for `internal/paritytest`); run gopls diagnostics on changed files, run /simplify on the slice and commit `feat(otelsrvr): bound metric attributes and add optional instruments`

## 6. Slice: tracing and trace source

- [x] 6.1 Trace source in core (request-observer / Scenario: Trace source, all rows); verify `go test -race -run TestTraceSource` passes
  - RED: table, plus a source whose `TraceFromContext` panics (ignored, core's trace kept, one `srvr.observer.fault` `trace_source_panic`); fails because core ignores observer-supplied ids
  - GREEN: `TraceSource` interface consulted by logging, problem enrichment and `TraceFromContext`; ADD a trace-source field to `engine.RequestState` (gin-and-fiber-engines defines the struct with route, matched, status, failure and trace context only), filled by the observer stage so the outer access record sees the source's ids
  - REFACTOR: /simplify
- [x] 6.2 Tracing switch (otel-tracing / Scenario: Tracing switch, all rows); verify `cd otelsrvr && go test -race -run TestTracingSwitch` passes
  - RED: table with in-memory exporter (test-only SDK); fails because spans are never started
  - GREEN: `WithTracing`, `Config.Tracing`, `WithTracerProvider` validation
  - REFACTOR: /simplify
- [x] 6.3 Span shape per engine (otel-tracing / Scenario: Span shape per engine, all rows); verify `go test -race -run TestSpanShape` passes in otelsrvr (net/http rows) and `internal/paritytest` (gin and fiber rows)
  - RED: table including the `http.ErrAbortHandler` row (status `Error`, exception event, no status code attribute); fails on naming, status and exception event
  - GREEN: span name from route, status rules, exception event for panics
  - REFACTOR: /simplify
- [x] 6.4 Span path attributes (otel-tracing / Scenario: Span path attributes, all rows); verify `go test -race -run TestSpanPathAttributes` passes
  - RED: table over `IncludePath` on and off, `_OTHER` method and an oversized path; fails because `url.path` is never set or always set
  - GREEN: read core's `IncludePath` through the request info; add `url.path` and `http.request.method_original` only when on, truncated by core's attribute limit
  - REFACTOR: /simplify
- [x] 6.5 Parent extraction (otel-tracing / Scenario: Parent extraction, all rows); verify `go test -race -run TestPropagation` passes (global propagator restored after each case)
  - RED: table including the no-op global, Baggage-only global and both ignored-incoming-context rows (option and `WithTraceConfig`); fails because no extraction happens, the no-op global drops `traceparent`, or ignored context is still extracted
  - GREEN: per-request propagator choice (`WithPropagator`, else global, else `propagation.TraceContext{}` when the global declares no fields); skip extraction when core ignores incoming trace context
  - REFACTOR: /simplify
- [x] 6.6 Correlation follows the span (otel-tracing / Scenario: Correlation equality; Scenario: Unsampled spans); verify `go test -race -run TestSpanCorrelation` passes
  - RED: tables; fails because logs keep srvr's generated ids
  - GREEN: otelsrvr implements `TraceSource` when tracing is on
  - REFACTOR: /simplify
- [x] 6.7 Spans always end (otel-tracing / Scenario: Span ending, all rows); verify `go test -race -run TestSpanEnding` passes
  - RED: table including abort, client disconnect and forced shutdown; fails if a span is left open or ended twice
  - GREEN: end in the end function with once-guard; core guarantees end function on abort paths
  - REFACTOR: /simplify
- [x] 6.8 Godoc and an `Example` enabling tracing with a provider, documenting the propagator fallback and that log and metric `error.type` differ by design; verify `go test -run Example` passes
- [x] 6.9 Verify slice end to end (one failing request -> span, log record and problem share ids) and confirm span, propagator and trace-source branches are covered in `go tool cover -func`; in the core module, `otelsrvr` and `internal/paritytest`, run `GOWORK=off go vet ./...`, `GOWORK=off golangci-lint run ./...` and `GOWORK=off go test -race ./...` (`go.work` allowed for `internal/paritytest`); run gopls diagnostics on changed files, run /simplify on the slice and commit `feat(otelsrvr): add optional server spans and span-based correlation`

## 7. Slice: otelsetup with Prometheus

- [x] 7.1 Create module `github.com/kartaladev/srvr/otelsetup` with SDK, sdk/metric, Prometheus exporter and client_golang; add to `go.work`, CI and release list; verify `GOWORK=off go build ./...` passes in `otelsetup`
- [x] 7.2 Provider construction, Prometheus rows (telemetry-setup / Scenario: Provider construction, Prometheus rows and the rejected rows that do not involve OTLP); verify `cd otelsetup && go test -race -run TestNew` passes
  - RED: table, including a failure after the meter provider was built (it is shut down, no global touched, no listener left bound); fails because `otelsetup.New` does not exist
  - GREEN: `Config`, `New(ctx, opts...)`, `WithConfig`, validation, resource from service fields, meter provider with Prometheus reader on a dedicated registry
  - REFACTOR: Split Phase (validate, build, start), then /simplify
- [x] 7.3 Configuration precedence, non-OTLP rows (telemetry-setup / Scenario: Configuration precedence, `service.name` and flush-timeout rows); verify `go test -race -run TestConfigPrecedence` passes (environment set with `t.Setenv`, so not parallel)
  - RED: table; fails because options do not apply in order or `OTEL_SERVICE_NAME` beats `Config.Service.Name`
  - GREEN: individual options (`WithPrometheus`, `WithService`, `WithFlushTimeout`, `WithSetGlobal`, ...), srvr's `service.*` attributes merged over `resource.Default`
  - REFACTOR: /simplify
- [x] 7.4 Scraping and listener (telemetry-setup / Scenario: Scraping, all rows; Scenario: Registry isolation, all rows); verify `go test -race -run 'TestScrape|TestRegistryIsolation'` passes
  - RED: tables; fails because no listener exists or the default registry is used
  - GREEN: private listener, `MetricsAddr`, `MetricsHandler`, `Listen: false`, rollback on bind failure
  - REFACTOR: /simplify
- [x] 7.5 Globals (telemetry-setup / Scenario: Globals, all rows); verify `go test -race -run TestSetGlobal` passes (globals restored after each case)
  - RED: table; fails because globals are always/never set
  - GREEN: `SetGlobal` installing providers and composite propagator as the last step of a successful `New`; a failing `New` leaves every global unchanged
  - REFACTOR: /simplify
- [x] 7.6 Shutdown, misuse and servers that never served (telemetry-setup / Scenario: Flush outcomes, Prometheus row; Scenario: Lifecycle misuse; Scenario: Server that never served, all rows); verify `go test -race -run 'TestShutdown|TestShutdownHook_NeverServed'` passes
  - RED: tables; fails because shutdown is not idempotent, the hook does not run when the server never started or `Start` failed after leaving its initial state, or it runs for `Start(nil)`, `ErrServerStarted` or `ErrServerClosed`
  - GREEN: idempotent bounded `Shutdown` (no mutex of otelsetup's own held across provider calls; a blocking provider `Shutdown` is bounded, errors joined; a panicking one is not guarded - superseded by design D11 as corrected in task group 9); `ShutdownHook()` returning `srvr.WithShutdownHook(srvr.AfterDrain, setup.Shutdown)` (core's mechanism; no hook mechanism is added here)
  - REFACTOR: /simplify
- [x] 7.7 Godoc and an `Example` serving Prometheus metrics for a srvr server with `setup.ShutdownHook()`, documenting which `OTEL_*` variables the SDK reads when fields are empty; verify `go test -run Example` passes
- [x] 7.8 Verify slice end to end (srvr server + otelsrvr + otelsetup -> scrape shows the route) and confirm construction rollback and shutdown branches are covered in `go tool cover -func`; in `otelsetup`, run `GOWORK=off go vet ./...`, `GOWORK=off golangci-lint run ./...` and `GOWORK=off go test -race ./...`; run gopls diagnostics on changed files, run /simplify on the slice and commit `feat(otelsetup): wire SDK and Prometheus scraping`

## 8. Slice: OTLP export and failure isolation

- [x] 8.1 OTLP providers (telemetry-setup / Scenario: Provider construction, OTLP rows); verify `cd otelsetup && go test -race -run TestNew_OTLP` passes against an in-process fake collector
  - RED: table for http/protobuf and grpc, metrics and traces, the empty-endpoint row and the OTLP rejected rows (`Protocol`, no signal enabled); fails because OTLP is not wired
  - GREEN: OTLP exporters, batch span processor, periodic reader; exporter endpoint option set only when `Endpoint` is non-empty
  - REFACTOR: /simplify
- [x] 8.2 Endpoint precedence (telemetry-setup / Scenario: Configuration precedence, OTLP endpoint rows); verify `go test -race -run TestConfigPrecedence_OTLP` passes (environment set with `t.Setenv`, pointing at fake collectors)
  - RED: table; fails if `OTEL_EXPORTER_OTLP_ENDPOINT` beats an explicit `Endpoint` or is ignored when `Endpoint` is empty
  - GREEN: no change expected beyond 8.1; keep the test as a guard for SDK upgrades
  - REFACTOR: /simplify
- [x] 8.3 Flush outcomes with OTLP (telemetry-setup / Scenario: Flush outcomes, OTLP rows); verify `go test -race -run TestShutdown_OTLP` passes
  - RED: table with accepting, unreachable and hanging fake collectors; fails if shutdown exceeds bounds
  - GREEN: `min(ctx, FlushTimeout)` deadline, joined errors
  - REFACTOR: /simplify
- [x] 8.4 Ordering with the server (telemetry-setup / Scenario: Ordering with the server, all rows); verify `go test -race -run TestShutdownOrdering` passes against a fake collector
  - RED: table over `Start` context cancelled, `Shutdown`, and `Shutdown` followed by the consumer's `setup.Shutdown`; fails if the in-flight duration is missing from the final export or the flush runs twice
  - GREEN: no change expected beyond 7.6 and core's hook mechanism; fix only what the test proves
  - REFACTOR: /simplify
- [x] 8.5 Export failures never fail requests (telemetry-setup / Scenario: Collector down during traffic, all rows); verify `go test -race -run TestExportFailure` passes
  - RED: table; fails because errors are not logged or flood the log
  - GREEN: global error handler under `SetGlobal`, per-kind rate limiting; an export error whose `Error()` panics is logged with a placeholder
  - REFACTOR: /simplify
- [x] 8.6 Add the `otel.export.failed` record to a golden-record test in `otelsetup` (additive key, `srvr.log.schema` unchanged); verify `go test -run TestLogSchemaGolden` passes in `otelsetup`
- [x] 8.7 Document `otelsetup` configuration (Prometheus in Kubernetes with `Addr: ":9464"`, OTLP endpoints, which `OTEL_*` variables apply to empty fields and that explicit fields win) in the module README; write `notes.md` entries for a stdout exporter and runtime metrics (Target: `unassigned`); verify README examples compile via `Example` tests
- [x] 8.8 Verify slice end to end with a fake collector and confirm exporter and error-handler branches are covered in `go tool cover -func`; in `otelsetup`, run `GOWORK=off go vet ./...`, `GOWORK=off golangci-lint run ./...` and `GOWORK=off go test -race ./...`; run gopls diagnostics on changed files, run /simplify on the slice and commit `feat(otelsetup): add OTLP export with bounded flushing`

## 9. Slice: verification fixes

- [ ] 9.1 OTLP endpoint validation (telemetry-setup / Scenario: Provider construction, the `OTLP.Endpoint` rejected rows; the `http://collector:4318` rows); verify `cd otelsetup && go test -race -run 'TestNew_OTLP|TestNew$'` passes
  - RED: the scratch reproduction `TestScratchInvalidEndpoint` as the spec's rejected `OTLP.Endpoint` rows; fails because `New` returns `nil` for them (the SDK's `WithEndpointURL` ignores unparsable URLs); existing rows use `http://collector:4318`
  - GREEN: validation per design D9, through one endpoint-parsing function shared with `exporterOptions`
  - REFACTOR: /simplify
- [ ] 9.2 Prometheus path validation (telemetry-setup / Scenario: Provider construction, the `Prometheus.Path` rejected rows); verify `go test -race -run TestNew` passes
  - RED: the scratch reproductions `TestScratchInvalidPath` and `TestScratchPanicLeaksListener` as rows: `/metrics/{` and `/metrics x` must fail `New` with `ErrConfig` naming `Prometheus.Path`, without panicking, and the address given in `Addr` must be free afterwards; fails with a panic from `mux.Handle` after the listener was bound, and the port stays bound
  - GREEN: validation registers the path on a throwaway `http.ServeMux` under `recover`, before anything is built
  - REFACTOR: /simplify
- [ ] 9.3 Faulty logger (telemetry-setup / Scenario: Faulty logger, all rows); verify `go test -race -run 'TestExportErrorHandler|TestFaultyLogger'` passes
  - RED: the scratch reproduction `TestScratchPanickingLoggerContained` as the panic row (fails: `panic escaped otel.Handle: log handler boom`), plus the re-entrant row (a handler that calls `otel.Handle` again; must not deadlock)
  - GREEN: `recover` around the `LogAttrs` call; the rate-limit lock stays released while the logger runs
  - REFACTOR: /simplify
- [ ] 9.4 Replace `PrometheusConfig.Listen *bool` with `DisableListener bool` (`json:"disable_listener" yaml:"disable_listener" env:"DISABLE_LISTENER"`; zero value listens) (telemetry-setup / Scenario: Scraping, the `DisableListener` row); verify `go test -race -run TestScrape` passes
  - RED: the Scraping row with `DisableListener: true`; fails to compile until the field exists (a public API shape change before v0.1.0, no compatibility shim), then must serve no listener and a working `MetricsHandler()`
  - GREEN: the field, godoc, README and examples
  - REFACTOR: /simplify
- [ ] 9.5 Documentation: the metrics listener limits (design Defaults) in the `PrometheusConfig` and `MetricsHandler` godoc and the README; in the `otelsrvr` godoc, that its `go.mod` requires the SDK only for its tests, its non-test packages import only the API, and what `TestNoSDKDependency` and core `TestCoreHasNoOTel` enforce; verify `go vet` and `go test -run Example` pass in both modules
- [ ] 9.6 Test harness: every started test server gets its own `http.Client` and `http.Transport`, and its cleanup closes only that client's idle connections, in core `helpers_test.go` and the equivalent helpers of `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup` and `internal/paritytest`; verify the reproduction passes and the full core suite passes 10 times in a row (`GOWORK=off go test -race -count=1 ./...`, foreground)
  - REFACTOR first (green): route the helpers' client and cleanup through two seams that still use `http.DefaultClient`
  - RED: the scratch reproduction `flake_repro_test.go.txt` adapted to the seams (requests to a started server through its client while another server's cleanup runs repeatedly); fails with `http: CloseIdleConnections called`, as `TestPanicRecovery/string` did
  - GREEN: a client per started server, looked up by host by the request helpers; direct `http.DefaultClient.Do` calls against started servers move to that client too; other modules follow the same pattern
- [ ] 9.7 Observer and span parity (request-observer / Scenario: Aborts and late failures per engine, all rows; otel-tracing / Scenario: Span shape per engine, the panic rows); verify `cd internal/paritytest && go test -race -run 'TestObserverParity|TestObserverAborts|TestSpanShape'` passes
  - RED: rows for `http.ErrAbortHandler` (net/http and gin `Aborted: true`, `Status: 0`; fiber the documented exception `Status: 500`, `Aborted: false`), a streamed fiber body with a late error (`Committed: true`, the access record's size) and a `"boom"` panic span (`exception.type` `string` on every engine); expected to pass at first run as guards, since no engine code changes; a failure is a defect to record in notes.md
  - GREEN: none expected
- [ ] 9.8 The `https` scheme of `RequestInfo` (engine `httpRequestInfo`); verify `go test -race -run TestObserver_RequestInfo` passes in the core
  - RED: a row over a TLS listener (`srvr.WithListener(tls.NewListener(...))`) expecting `Scheme` `https`; the branch is uncovered today, so the row is expected to pass at first run as a guard (check the coverage of the branch before and after)
- [ ] 9.9 Update notes.md (outcome of this group, the cause of the unreproduced core failures, the `otelsrvr` SDK carry-over); in every touched module run `GOWORK=off go vet ./...`, `GOWORK=off golangci-lint run ./...` and `GOWORK=off go test -race ./...`, check coverage of the changed branches, run /simplify, and commit as `fix(otelsetup)!: ...` and `test(core): ...`

## Workflow follow-up

- Re-read `notes.md` and make sure every open item has a target or is `unassigned`.
- Tag `otelsrvr/v0.1.0`, `otelsetup/v0.1.0` and the core minor release after review; archive the change; verify the archived result.
