# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.


### Observer must plug into srvr/engine
- **Status:** adopted by otel-observability (planning audit 2026-11-17: design D1/D2, slices 2-3 and tasks 2.x (net/http observer stage) and 3.1-3.3 (gin and fiber) put the observer stage in srvr/engine; consolidation 1.3 re-checks against merged code)
- **Target:** otel-observability
- **Context:** gin-and-fiber-engines introduces a public srvr/engine package holding the shared pipeline; this change assumed observers attach through core options only.
- **Evidence:** unverified: planning-time inconsistency found when the five changes were drafted in parallel
- **Suggested action:** Re-express Observer wiring in terms of srvr/engine as merged.

### WithShutdownHook shared with security shutdown hooks
- **Status:** adopted by core-server (owner decision D5, planning audit 2026-11-17): one core `srvr.WithShutdownHook(srvr.BeforeDrain|srvr.AfterDrain, fn)` with its own `ShutdownHookTimeout` budget (default 5s), run once on every stop path; this change no longer defines `WithShutdownHook` and `setup.ShutdownHook()` registers an `AfterDrain` hook (design D6, telemetry-setup Ordering rows). The duplicate scrty-integration entry "Shutdown hooks shared with otel-observability" is merged into this one.
- **Target:** core-server
- **Context:** This change adds core WithShutdownHook (runs after drain); scrty-integration needs FlushRefusalLogs/CloseSessionSignals inside the drain deadline. Two shutdown mechanisms would diverge.
- **Evidence:** unverified: planning-time inconsistency found when the five changes were drafted in parallel
- **Suggested action:** Define one ordered shutdown-hook mechanism in core (pre-drain and post-drain phases) used by both changes.

### http.route "(unmatched)" deviates from semantic conventions
- **Status:** adopted by otel-observability (owner decision D9, 2026-11-17): `http.route` omitted when unmatched; otel-metrics Attribute values and Cardinality rows updated, design D4.
- **Target:** otel-observability
- **Context:** otel-metrics "Attributes have bounded cardinality" sets `http.route` `(unmatched)` for unmatched requests, while otel-tracing names such spans `{method}` as if no route existed. Semconv v1.43 makes `http.route` Conditionally Required "If and only if it's available", so a made-up value is a deviation, and design Risks did not list it.
- **Evidence:** unverified: specification text (opentelemetry.io/docs/specs/semconv/http/http-metrics and http-spans, fetched 2026-11-17); no runtime defect
- **Suggested action:** Owner decides: omit `http.route` when unmatched (semconv) or keep `(unmatched)` and list it under design Risks as a deliberate deviation.

### Server spans omit semconv Required url.path and Conditionally Required http.request.method_original
- **Status:** adopted by otel-observability (owner decision D10, 2026-11-17): `url.path` and `http.request.method_original` on spans only when core `AccessLogConfig.IncludePath` is on; otel-tracing Span path attributes rows, design D4 and Risks, task 6.4.
- **Target:** otel-observability
- **Context:** otel-tracing gives spans "the same bounded attributes as the metrics". Semconv v1.43 HTTP server spans make `url.path` Required and `http.request.method_original` Conditionally Required (when the method became `_OTHER`); `network.protocol.version`, `server.address`, `client.address`, `user_agent.original` are Recommended. Omitting `url.path` matches srvr's privacy default (path not logged unless `IncludePath`) but is not stated as a deviation.
- **Evidence:** unverified: specification text (opentelemetry.io/docs/specs/semconv/http/http-spans, fetched 2026-11-17)
- **Suggested action:** Owner decides per attribute; suggested: `url.path` on spans only with an explicit opt-in tied to core `IncludePath`, `http.request.method_original` truncated and only with that opt-in; record the choice in design D4 and Risks.

### error.type "innermost Go type" differs from OTel Go's semconv.ErrorType
- **Status:** adopted by otel-observability (owner decision D2, 2026-11-17): metrics and spans use `semconv.ErrorType` (joined errors keep `*errors.joinError`/`*fmt.wrapErrors` unless a member has `ErrorType()`, probe `TestSemconvJoined`), `panic` for panics, status code for failures without an error value; logs keep the Go type and are changed in core-server. The claim that `Unwrap() []error` is undefined was stale: core-server triage-error-records defines it.
- **Target:** otel-observability (metrics, spans); core-server (logs)
- **Context:** Proposal and design D4 define `error.type` as the innermost Go type. `go.opentelemetry.io/otel/semconv/v1.43.0` (part of the API module otelsrvr already requires) provides `ErrorType(err)`, which prefers an `ErrorType() string` method anywhere in the chain and otherwise unwraps only `fmt.Errorf` wrappers, using the package path for named types. "Innermost" also leaves `Unwrap() []error` undefined, and every prblm sentinel (`errors.New`) collapses to `*errors.errorString`, so `prblm.ErrConflict` and `errors.New("x")` share one value.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/otel/main_test.go` `TestSemconvErrorType`, `go test -run TestSemconvErrorType -v`: `errors.New("x")` -> `*errors.errorString`; `fmt.Errorf("wrap: %w", myErr{})` -> `probe.myErr`; prblm v0.1.0 `sentinels.go:86` `ErrConflict = errors.New("prblm: conflict")`
- **Suggested action:** Owner decides; suggested: use `semconv.ErrorType` semantics (consistent with otelhttp-era Go conventions) and define joined-error handling; update D4 and the Attribute values rows.

### Default propagator makes WithTracing drop incoming traceparent
- **Status:** adopted by otel-observability (owner decision D11, 2026-11-17): W3C TraceContext fallback when the global propagator declares no fields; extraction skipped with core `IgnoreIncomingTraceContext`; design D8, Parent extraction rows, task 6.5.
- **Target:** otel-observability
- **Context:** otelsrvr defaults to the global propagator, which is a no-op until something calls `otel.SetTextMapPropagator`. With `WithTracing()` alone, spans start new roots and, through the trace source, replace core's trace id that honoured `traceparent` - so enabling tracing silently breaks cross-service correlation that worked with tracing off. Also core's `IgnoreIncomingTraceContext` opt-in is not honoured by otelsrvr's extraction.
- **Evidence:** probe `TestSemconvErrorType` prints `default propagator fields: []` for `otel.GetTextMapPropagator()` (otel v1.47.0); otel-tracing Parent extraction row 4 pins the new-root behaviour
- **Suggested action:** Owner decides: default to W3C TraceContext when the global propagator has no fields (or require `WithPropagator` with tracing), and make otelsrvr skip extraction when core ignores incoming trace context; add rows to Parent extraction.

### OTel SDK and OTLP exporters read environment variables
- **Status:** adopted by otel-observability (owner decision D12, 2026-11-17): SDK env fallback accepted for empty fields, explicit config wins; telemetry-setup "Setup is configurable" requirement with Configuration precedence rows, design D9, tasks 7.3, 8.2, 8.7.
- **Target:** otel-observability
- **Context:** Project rule 7 says srvr never reads environment variables. In otelsetup the OTLP exporters read `OTEL_EXPORTER_OTLP_*` (endpoint, headers, TLS, temporality) and `resource.Default` reads `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES`; explicit options win, but unset fields fall back to the pod environment silently. No scenario pins the precedence.
- **Evidence:** module source otlpmetrichttp@v1.47.0 `internal/oconf` (`Namespace: "OTEL_EXPORTER_OTLP"`), sdk@v1.47.0 `resource/env.go:20,23` and `resource/resource.go:258` (`fromEnv{}` in `DefaultWithContext`)
- **Suggested action:** Owner decides whether otelsetup accepts the SDK's env fallback (documented, with a precedence scenario: Config beats env) or builds the resource without `fromEnv` and passes every exporter field explicitly.

### otelsetup has a config struct but no functional options
- **Status:** adopted by otel-observability (owner decision D13, 2026-11-17): `otelsrvr.WithConfig` / `otelsetup.New(ctx, opts...)` with `otelsetup.WithConfig`, individual options, argument order wins; otel-metrics Configuration sources and telemetry-setup Configuration precedence rows, design D9, tasks 5.4, 7.2, 7.3.
- **Target:** otel-observability
- **Context:** telemetry-setup specifies only `otelsetup.New(ctx, Config)`; project rule 7 and config.yaml require every behaviour to be configurable both programmatically (functional options) and by config struct, and spec rules require both override scenarios. otelsrvr also uses `Config{...}` in rows without saying how a `Config` reaches `otelsrvr.New` (e.g. `WithConfig`).
- **Evidence:** unverified: planning-artifact inconsistency (telemetry-setup spec, AGENTS.md section 7)
- **Suggested action:** Owner decides: add `otelsetup` options (or document `Config` as the single programmatic surface) and name the otelsrvr config entry point; add the missing override rows.

### Fiber unmatched route reads as "/" from middleware
- **Status:** adopted by gin-and-fiber-engines (its design reads the route only when `c.Matched()`, and its D7 makes the fiber access stage the only caller of the error handler); this change's observer reads status, route and failure from `engine.RequestState` and never renders (design D1, task 3.2).
- **Target:** gin-and-fiber-engines
- **Context:** gin-and-fiber-engines design D4 says the access middleware reads `c.Route().Path` after `c.Next()` with a fallback to empty "when the route is Fiber's internal not-found handler". On fiber v3.7.0 an `app.Use` middleware sees `c.Route().Path == "/"` for `GET /nope` and status `200` for both a handler error and not-found (fiber renders errors after the chain returns), so a naive read logs route `/` and status `200`.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/fiberstatus/p_test.go` `TestFiberStatusAfterNext`, `go test -v .`: `observer view after Next: status=200 err=boom route="/users/:id"`, then `status=200 err=Not Found route="/"` while the client got `500`/`404`
- **Suggested action:** Pin these in fiber-engine's "Fiber route attribute" rows and detect unmatched by matched-route identity, not by path; the otel observer stage reuses the same logic.

### Planning audit 2026-11-17 decisions applied
- **Status:** closed: applied during planning
- **Target:** otel-observability
- **Context:** The cross-change audit's owner decisions were applied to this change: D2 (`semconv.ErrorType` in metrics/spans), D5 (core AfterDrain hook, no own mechanism; `otelsrvr` registers none because it owns nothing to flush), D6 (`srvr.observer.fault` and `otel.export.failed` are additive, no `srvr.log.schema` bump, golden tasks 4.3 and 8.6), D8 (unmatched 404/405 are failures; metric `error.type` is the status code), D9-D13, single owner of fiber error rendering (access stage) and one per-request holder (`engine.RequestState`). Workflow: task 1.5 added, scaffolding folded into slice 2, groups renumbered, every slice end runs vet, lint and race tests with `GOWORK=off`. The OTLP empty-`Endpoint` rejected row became an accepted row (SDK resolves it), replaced by a "no signal enabled" rejected row.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/otel/joined_test.go` `TestSemconvJoined`, `go test -run TestSemconvJoined -v`: `join -> *errors.joinError`, `multi%w -> *fmt.wrapErrors`, `opErr -> *net.OpError`, `joinTyped -> db_timeout`, `wrapTyped -> db_timeout`
- **Suggested action:** none; consolidation 1.3 re-checks the names against merged core-server and gin-and-fiber-engines.

### Planning audit 2 2026-11-17
- **Status:** closed: applied during planning
- **Target:** otel-observability
- **Context:** Second cross-change audit and owner decisions A3/A4. Applied: `engine.RequestState` is created by the outermost (trace context) stage per gin-and-fiber-engines D7; core config structs referenced as separate structs (`TraceConfig.IgnoreIncoming` via `WithTraceConfig`/`WithIgnoreIncomingTraceContext()`, `AccessLogConfig.IncludePath`), with a `WithTraceConfig` Parent extraction row; blocking end function and `http.ErrAbortHandler` end-panic observer rows; `http.ErrAbortHandler` metric (`error.type` `panic`, no status code) and span rows (A3, consistent with core's `srvr.response.aborted`); otelsetup's `AfterDrain` hook runs once on every `Start` failure after leaving the initial state and never for `Start(nil)`/misuse (A4), scenario renamed "Server that never served"; this file's task reference corrected. Tasks 3.x-7.6 updated.
- **Evidence:** unverified: planning-artifact inconsistencies found by the audit; the metric `*net.OpError` row was confirmed correct by audit probe `semconv/TestErrorTypeRows`.
- **Suggested action:** none.
