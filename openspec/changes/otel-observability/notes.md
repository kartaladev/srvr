# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation 2026-11-18 (task group 1)
- **Status:** closed: consolidation done; one open decision recorded below ("A blocking end function cannot let the client have its response first")
- **Target:** otel-observability
- **Context:** Synced with `main` at `649b030` (in step with `origin/main`). Merged and archived since planning: `core-server` (`openspec/changes/archive/2026-11-17-core-server/`) and `gin-and-fiber-engines` (`openspec/changes/archive/2026-11-18-gin-and-fiber-engines/`), both synced into `openspec/specs/`. Checked against the merged code: pipeline order (net/http `StartTrace` -> `LogAccess` -> `LimitBody` -> `Recover` -> unmatched answer -> mux; gin the same outer stages around the gin engine, whose chain is `requestStage` -> `ginprblm.Middleware` -> `guardErrors` -> `ginprblm.Recovery`; fiber `requestStage` (trace and access in one stage, the only caller of the error handler) -> `fiberprblm.Recovery`); `srvr.Trace`/`srvr.TraceFromContext` (aliases of `engine.Trace`/`engine.TraceFromContext`, Option A of gin-and-fiber-engines D1); route derivation (net/http `r.Pattern` without the method, gin `c.FullPath()` before `c.Next()`, fiber `c.Route().Path` when `c.Matched()` after rendering); `ServiceConfig` (name, version, environment); `WithShutdownHook(BeforeDrain|AfterDrain, fn)` and `Config.ShutdownHookTimeout` (default 5s); `AccessLogConfig.IncludePath`; `TraceConfig.IgnoreIncoming`; `engine.RequestState` (exported `Route`, `Location`; `Matched()`, `Status()`, `Failed()`, `Panicked()`; unexported trace, response recorder, status, aborted, failure: "matched" is derived from the route, not a field); the failed-construction rule (engine-support "A failed construction changes no process-wide state"); route listing and `WithRouteCheck`; the engine modules' `replace ../`. Versions re-checked on the module proxy 2026-11-18: otel, metric, trace, sdk, sdk/metric, OTLP exporters v1.47.0, Prometheus exporter v0.69.0, client_golang v1.25.0 - unchanged.
  Refreshed: proposal (Modified Capabilities: engine-support; assumptions marked as checked); design Context, D1 (observer stage per engine as merged: an `engine` http stage between `LogAccess` and `LimitBody` for net/http and gin, the fiber `requestStage` for fiber), D2 (gin placement), D3 (trace source kept in `engine.RequestState`), new D10 (sizes and abort in `ResultInfo`), new D11 (consumer-fault plan for this change); a MODIFIED delta of engine-support "Engines share one per-request state" (the trace source); tasks 2.1 (no `release-modules` target exists: the CI job and the release step of "Workflow follow-up" instead), 2.3, 3.1, 3.2, 4.2, 5.6, 6.1, 7.2, 7.5, 7.6 and 8.5 (consumer-fault rows listed below). `trace-context` and `structured-logging` need no MODIFIED delta: their rows speak of "the request's trace id", which the trace source keeps true, and request-observer "Observers may supply the trace context" states the override. `openspec validate otel-observability --strict` passes.
- **Evidence:** `git fetch` then `git log -1 origin/main` = `649b030`; `go list -m -versions` for each module above.
- **Suggested action:** none.

### Unmatched 404 problems differ between net/http and gin/fiber
- **Status:** open
- **Target:** unassigned (engine parity; found by otel-observability task 3.3)
- **Context:** engine-parity "Identical problems across engines" requires the same problem members for the same failure. A request matching no route gets, on net/http, `prblm.ErrNotFound`'s problem with `detail` "The requested resource was not found." (`unmatched.go` writes `prblm.ErrNotFound`), while gin and fiber send a status-only 404 problem without `detail` (ginprblm and fiberprblm render the bare status). The Examples rows only cover handler failures, so the existing parity suite did not catch it. Observers see a different `ResponseBodySize` for the same unmatched request (148 vs 99 bytes in the probe).
- **Evidence:** `internal/paritytest/unmatched_problem_test.go` `TestUnmatchedProblemParity` (skipped until the fix; it is the fix's RED step), with the skip removed: `go test -count=1 -run TestUnmatchedProblemParity .` in `internal/paritytest`: `gin problem = {"title":"Not Found","status":404,"instance":"/nope","trace_id":"…"}, net/http {"title":"Not Found","status":404,"detail":"The requested resource was not found.","instance":"/nope","trace_id":"…"}` and the same for fiber.
- **Suggested action:** owner decides which body is canonical (status-only, like 405 on every engine, or `ErrNotFound` with `detail`), adds unmatched rows to engine-parity "Problem parity", and unskips the test. `TestObserverParity` leaves the response size of its unmatched row out until then.

### Slice 2 notes (2026-11-18)
- **Status:** closed: applied
- **Target:** otel-observability
- **Context:** Task 3.1's RED passed at once, as slice 1's seam predicted: gin is served through `LogAccess`, so `ginsrvr/observer_test.go` `TestObserver` is a guard and ginsrvr has no code change. Task 3.2's RED failed as expected (`Begin calls = 0, end calls = 0, want 1 and 1` on every fiber row); the fiber stage now calls `Core.BeginObservers` and its `WriteAccess` runs the end functions with the status it passes after rendering, which closes "RequestState.Status is not live on fiber". The parity harness's fiber handler used `c.SendStatus(200)`, which writes the body `OK`; /simplify changed it to `c.Status(200)` like gin, so the success row compares response sizes too. A fiber server without observers skips building `RequestInfo` (`Core.Observed`), so the default path allocates nothing new. The error hook counts one rendered problem per failed request on every engine, including fiber.
- **Evidence:** as named.
- **Suggested action:** none.

### Slice 1 decisions during apply (2026-11-18)
- **Status:** closed: applied
- **Target:** otel-observability
- **Context:** (1) /simplify altitude review moved the observer seam into the access stage: `Core.BeginObservers` begins observers (called by `LogAccess`, and by fibersrvr's `requestStage` in slice 2), `writeAccess` runs the end functions from `engine.RequestState` before the record, with `ResultInfo` built from `AccessInfo` (which gains `RequestBodySize`). Design D1 records it. This also makes the end functions run for `http.ErrAbortHandler` (`observer_test.go` `TestObserver_Abort`, RED: `the end function did not run for an aborted request`). (2) `RequestInfo` has method, scheme and host; the planned flavor (protocol version) is postponed until an attribute needs it (Gall). (3) Task 2.1's `grep -c sdk` check prints 2, not 0: `go.opentelemetry.io/auto/sdk` is imported by the OTel API's own `internal/global` package, it is not the SDK. The guard is `otelsrvr/deps_test.go` `TestNoSDKDependency`, which rejects `go.opentelemetry.io/otel/sdk...` and `.../exporters...`. (4) The `otelsrvr` module requires `go.opentelemetry.io/otel/sdk/metric` for its tests; Go has no test-only require, and the non-test package graph does not import it (pinned by the same guard). (5) Tasks 2.4 and 2.5 passed at their first run because 2.3's GREEN already placed the stage and filled the fields; a mutation (stage moved inside recovery) made `TestObserver_Placement/panic` fail (`end status, kind = 0, "panic", want 500, "panic"`), so the test is a working guard.
- **Evidence:** as named above.
- **Suggested action:** none.

### Carry-over decisions 2026-11-18
- **Status:** closed: decided at consolidation
- **Target:** otel-observability
- **Context:** Every open entry of the archived notes addressed to `otel-observability` or `unassigned`:
  - gin-and-fiber-engines "RequestState.Status is not live on fiber" (target otel-observability): adopted, resolved by design rather than by a live status source. The observer's end runs in the fiber `requestStage` after the error handler rendered, where `c.Response().StatusCode()` is final, and is passed in the same way `WriteAccess` already receives it (design D1, task 3.2). No stage reads the status mid-request; the entry is closed by task 3.2's fiber rows.
  - gin-and-fiber-engines "Metrics and tracing hooks needed in both engines" (target otel-observability): adopted, design D1: one observer stage in `srvr/engine` used by every engine (net/http and gin as an http stage between `LogAccess` and `LimitBody`, fiber inside `requestStage`), parity rows in task 3.3.
  - gin-and-fiber-engines "Public engine package stability before v1" (unassigned): deferred; this change adds `Observer`, `RequestInfo`, `ResultInfo`, `TraceSource` and the observer stage to `engine`, aliased in srvr, which adds to the same pre-v1 review.
  - gin-and-fiber-engines "Engine modules duplicate the core's test helpers" (unassigned): deferred; `otelsrvr` tests use their own small helpers.
  - gin-and-fiber-engines "Fiber rejection records build their problem apart from the client's", "A fiber sub-app shared between servers races in fiber", "A fiber sub-app's own merged duplicate is not reported", "Domain-mounted fiber sub-apps are not covered by the route listing tests", "One construction helper could own the order of New's final steps" (unassigned): deferred, unrelated to observability.
  - core-server "AfterDrain hooks share one budget, so a blocking hook leaves later hooks a spent context" (unassigned): deferred but relevant: `setup.ShutdownHook()` runs in that budget, so a consumer hook registered before it and blocking past the budget leaves the flush a spent context; documented in otelsetup's godoc (task 7.7) rather than changed here.
  - core-server "prblm type discovery is not served by srvr", "Ask prblm for a composable OnError and an exported status-only sentinel", "Shutdown waits up to 5s for a connection that never sent a request", "A panicking consumer error hook is contained silently", "Stack capture at error creation", "TLS termination", "HTTP/2 cleartext (h2c)", "Health endpoints", "A srvr.problem.note event for notes and contained hook panics", "Start's \"Serve returned because the stop closed the listener\" branch is covered only by timing", "A Start or Shutdown context whose methods panic leaves the server unable to stop", "A connection from a WithListener listener whose methods panic crashes the process", "Errors and panics from a passed listener's Close are not logged" (unassigned): deferred, unrelated to observability.
  - gin-and-fiber-engines "Engine modules build against the in-repository core until it is tagged" (target gin-and-fiber-engines release step): applies to `otelsrvr` too: it implements `srvr.Observer`, whose types live in the core's `engine` package, so its go.mod requires the core with `replace github.com/kartaladev/srvr => ../` until the core is tagged (task 2.1); the release step of "Workflow follow-up" covers it. `internal/paritytest` keeps its `replace` lines.
- **Evidence:** `grep -n "Status:\*\* open" openspec/changes/archive/*/notes.md`.
- **Suggested action:** none.

### A blocking end function cannot let the client have its response first
- **Status:** open (owner decision needed before task 4.2)
- **Target:** otel-observability
- **Context:** request-observer "Observer faults" row `blocks in the end function for 50ms` expects `status 200 received by the client before the end function returns, the access record written after it returns`. Design D2 says a blocking observer is "not mitigated (synchronous by design, documented, same as logging)". On net/http (and gin, served through net/http) a handler's buffered response reaches the client only when the outermost `ServeHTTP` returns, so a synchronous end function inside the access stage delays the response; fasthttp (fiber) also writes the response only after the handler returns. Meeting the row needs the observer stage to flush the response before calling the end functions (on net/http and gin: `http.ResponseController.Flush`, which turns a small response without `Content-Length` into a chunked one on every request with an observer) and has no equivalent on fiber; or the end functions run asynchronously, which breaks "the access record written after it returns" unless the access record moves off the request goroutine too.
- **Evidence:** probe `scratchpad/probes/blockend/p_test.go` `TestBlockingEndDelaysResponse` (session `0cf21349`), `GOWORK=off go test -count=1 .`: `client got its response only after the end function returned: 202.107792ms` for a net/http handler that writes `200` then blocks 200ms.
- **Suggested action:** owner decides: (a, recommended) keep D2: a blocking end function delays the response, as a blocking `Begin` does; change the row to `status 200 after at least 50ms, the access record written after the end function returns, and no srvr.observer.fault record`; (b) flush before the end functions on net/http and gin and accept chunked encoding and a fiber parity exception; (c) run end functions asynchronously and write the access record after them off the request goroutine.


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
