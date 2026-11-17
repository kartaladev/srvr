# Tasks

## 1. Consolidation: refresh plan against merged changes

- [x] 1.1 Sync with the main branch and list every change merged or archived since this change was planned; verify they are named in the completion note (first change: expect none, and confirm the repository is initialised with a main branch)
- [x] 1.2 Decide every open `notes.md` carry-over from earlier changes addressed to `core-server` or `unassigned` (adopt, defer or close); verify none is left undecided (first change: expect none)
- [x] 1.3 Refresh this change's proposal, specs, design and tasks against the current main specs, the released prblm version and Go toolchain (re-check prblm's `OnError`, `Wrap`, `Recover`, `RequestFrom` signatures with gopls); verify `openspec validate core-server --strict` passes
- [x] 1.4 Record the consolidation outcome at the top of `openspec/changes/core-server/notes.md`; verify the file lists what was refreshed or states none was needed
- [x] 1.5 Commit the consolidation as `docs(openspec): consolidate core-server against main`, or skip when 1.1-1.4 changed nothing; verify with `git log -1 --stat`

## 2. Slice: one route served with problem errors

- [x] 2.1 Create module `github.com/kartaladev/srvr` (go 1.27) with `go.work`, `.golangci.yml`, `Makefile` targets (`test`, `lint`, `cover`) and a CI workflow running `go vet`, `golangci-lint`, `go test -race` with `GOWORK=off`; verify `make test lint` passes on an empty package with a `doc.go`
- [x] 2.2 Add prblm as the only third-party dependency; verify `go list -m all` shows only the standard library toolchain modules, srvr and prblm
- [x] 2.3 Register a configurer and serve its route (route-registration / Scenario: Registration outcome, accepted rows; Scenario: Configure order); verify `go test -race -run TestRegister` passes
  - RED: `TestRegister` table with the accepted rows and the configure-order row; fails because `New`, `Register`, `RouteConfigurer` do not exist
  - GREEN: `New`, generic `RouteConfigurer[G]`/`RouteRegistrar[G]`, `Register` storing configurers, `*Group` with `GET`/`POST`/`PUT`/`PATCH`/`DELETE`/`Handle`
  - REFACTOR: Extract Function for method helpers delegating to `Handle`, then /simplify
- [x] 2.4 Start blocks and stops by context or Shutdown (server-lifecycle / Scenario: Start and stop by context); verify `go test -race -run TestStart_Stop` passes
  - RED: `TestStart_Stop` with the three trigger rows using an in-flight request; fails because `Start` does not block/drain
  - GREEN: minimal state, `http.Server` with listener, `Start` waiting on `ctx.Done()` then `Shutdown`
  - REFACTOR: Extract Method for the drain step, then /simplify
- [x] 2.5 Readiness and bound address (server-lifecycle / Scenario: Ephemeral port; Scenario: Address before readiness); verify `go test -race -run TestReady` passes
  - RED: `TestReady` and `TestAddr_BeforeReady` with all rows; fails because `Ready`/`Addr` do not exist
  - GREEN: close `Ready` after bind, store listener address
  - REFACTOR: Encapsulate Variable for the state fields, then /simplify
- [x] 2.6 Handler results and errors become problems (route-registration / Scenario: Handler result; error-responses / Scenario: Error to problem, mapped and unmapped rows); verify `go test -race -run 'TestHandlerResult|TestErrorToProblem'` passes
  - RED: both tables with every row including XML and `text/html` negotiation; fails because handlers are not wrapped
  - GREEN: one `prblm.Handler` per server, routes installed through `Wrap`
  - REFACTOR: Replace Temp with Query for the handler construction, then /simplify
- [x] 2.7 Godoc for `Server`, `RouteConfigurer`, `RouteRegistrar`, `Group` and a runnable `Example` (net/http hello world with a problem error); verify `go test -run Example` passes and `go doc` shows the defaults
- [x] 2.8 Verify slice end to end with an httptest round trip (register, start, request, problem, shutdown), confirm dispatch, `Wrap` and Start/Shutdown are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from the module directory, run gopls diagnostics on changed files, run /simplify on the slice and commit `feat(core): serve routes with RFC 9457 errors on net/http`

## 3. Slice: lifecycle hardening and limits

- [x] 3.1 Construction validates configuration (server-lifecycle / Scenario: Valid and invalid configuration, all rows except the `WithShutdownHook` rows, which 3.8 adds); verify `go test -race -run TestNew_Config` passes
  - RED: table with accepted and rejected rows; fails because no validation exists
  - GREEN: `Config` struct with tags, `WithConfig` and individual options, `ErrConfig`, validation after all options naming field and value
  - REFACTOR: Replace Conditional with Polymorphism is overkill - use Extract Function per field validator, then /simplify
- [x] 3.2 Effective defaults and overrides (server-lifecycle / Scenario: Effective limits, defaults and overrides, except the `ShutdownHookTimeout` rows, which 3.8 adds); verify `go test -race -run TestEffectiveLimits` passes
  - RED: table over every row asserting the built `http.Server` fields and body limit; fails on missing defaults
  - GREEN: defaults resolution (zero = default, negative = disabled)
  - REFACTOR: Introduce Parameter Object for resolved settings, then /simplify
- [x] 3.3 Options and config struct compose in argument order (server-lifecycle / Scenario: Option and config precedence, the listen-address rows; the log, access-log, service and trace rows are added in 7.1, 7.5, 7.9 and 6.3); verify `go test -race -run TestOptionPrecedence` passes
  - RED: table over the `WithConfig`/`WithAddr` rows in both orders; fails because `WithConfig` does not exist or always wins
  - GREEN: options mutate one settings value in order; `WithConfig` overwrites every `Config` field
  - REFACTOR: Combine Functions into Transform for settings resolution, then /simplify
- [x] 3.4 Body limit renders 413 (error-responses / Scenario: Body limit, all rows including boundary `4194304`/`4194305` and `-1`); verify `go test -race -run TestBodyLimit` passes
  - RED: table posting bodies of exact sizes; fails because no `MaxBytesReader` is installed
  - GREEN: wrap request bodies with `http.MaxBytesReader` unless disabled
  - REFACTOR: Extract Function for body limiting middleware, then /simplify
- [x] 3.5 Start failures (server-lifecycle / Scenario: Start fails, rows for port in use, pre-cancelled context and nil context; the configurer-panic and conflict rows are added in 4.3 and 4.7); verify `go test -race -run TestStart_Fails` passes
  - RED: table including the `Start(nil)` row; fails because `Start` binds before checking the context and `Ready` is closed too early
  - GREEN: check context, bind, then close `Ready`
  - REFACTOR: Split Phase (configure, bind, serve), then /simplify
- [x] 3.6 Single-use Start (server-lifecycle / Scenario: Start twice, all rows including the concurrent row); verify `go test -race -count=50 -run TestStart_Twice` passes
  - RED: table with concurrent starts; fails with data race or double serve
  - GREEN: mutex-guarded state machine `new/starting/running/stopping/closed`, `ErrServerStarted`, `ErrServerClosed`
  - REFACTOR: Replace Type Code with State enum and transition table, then /simplify
- [x] 3.7 Bounded, idempotent shutdown (server-lifecycle / Scenario: Drain within and beyond the deadline; Scenario: Shutdown misuse; Scenario: Shutdown with a nil context); verify `go test -race -run TestShutdown` passes
  - RED: tables with slow in-flight handler, never-started, repeated and concurrent shutdown, nil context; fails because no forced close and no idempotency
  - GREEN: `Shutdown` with `Close` on deadline, closed state reachable from `new`, nil-context guard
  - REFACTOR: Consolidate Duplicate Conditional Fragments in shutdown paths, then /simplify
- [x] 3.8 Shutdown hooks run once on every stop path (server-lifecycle / Scenario: Hook phases and order; Scenario: Hooks on every stop path, all rows including concurrent stop, failed bind, pre-cancelled context, and the `Start(nil)`/`ErrServerStarted`/`ErrServerClosed` rows that run no hooks; the configure-panic and route-conflict rows are added in 4.7 and 4.3); verify `go test -race -count=50 -run TestShutdownHooks` passes
  - RED: tables recording hook calls and the order against a slow in-flight request; fails because `WithShutdownHook` does not exist and the `Start`-cancel path skips hooks
  - GREEN: `ShutdownPhase`, `BeforeDrain`, `AfterDrain`, `WithShutdownHook`, `Config.ShutdownHookTimeout` (default 5s, with its Effective limits and validation rows), one `sync.Once`-guarded stop sequence used by `Start` cancellation, `Shutdown`, failed bind and never-started `Shutdown`
  - REFACTOR: Extract Function for the stop sequence shared by every path (Consolidate Duplicate Conditional Fragments), then /simplify
- [x] 3.9 Faulty shutdown hooks never stop the shutdown (server-lifecycle / Scenario: Faulty hooks; Scenario: Faulty hook during Shutdown; Scenario: Blocking and re-entrant hooks, all rows, asserting the `server.shutdown_hook.failed` record from 7.10 on); verify `go test -race -run TestShutdownHooks_Faulty` passes, plus a test that a hook calling `Shutdown` returns after the hook budget
  - RED: tables with erroring, panicking and context-ignoring hooks; fails because one failure stops the sequence or `Start` returns `nil`
  - GREEN: per-hook goroutine with `recover` and budget, `ErrShutdownHook` joined with hook and drain errors (the `server.shutdown_hook.failed` record is added in 7.10)
  - REFACTOR: Introduce Parameter Object for the hook result, then /simplify
- [x] 3.10 Register misuse is rejected (route-registration / Scenario: Registration outcome, rejected rows); verify `go test -race -run TestRegister` passes
  - RED: add the nil, typed-nil, running and stopped rows to `TestRegister`; fails because `Register` accepts them
  - GREEN: nil and typed-nil check (reflect on pointer kinds), state check returning `ErrServerStarted`/`ErrServerClosed`
  - REFACTOR: Introduce Assertion helper shared with nil option checks, then /simplify
- [x] 3.11 Verify slice end to end with `go test -race -count=20` over the lifecycle package, confirm every state transition, the forced-close branch and the hook runner are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from the module directory, run gopls diagnostics, run /simplify on the slice and commit `feat(core): validate config and harden server lifecycle`

## 4. Slice: complete routing on the net/http group

- [x] 4.1 Prefix and path joining (route-registration / Scenario: Joining prefix and path, all rows); verify `go test -race -run TestJoin` passes
  - RED: table of prefix/path/request rows through a real mux; fails on duplicate slashes and empty path
  - GREEN: explicit join rules preserving trailing slash and wildcards
  - REFACTOR: Extract Function `joinPattern`, then /simplify
- [x] 4.2 Invalid paths fail start (route-registration / Scenario: Invalid paths, all rows); verify `go test -race -run TestInvalidPaths` passes
  - RED: table; fails because ServeMux panics escape or relative paths are accepted
  - GREEN: validation before registration, ServeMux registration under `recover` -> `ErrRouteConfig`
  - REFACTOR: Replace Error Code with Exception style typed `RouteError`, then /simplify
- [x] 4.3 Conflicting routes name both sides (route-registration / Scenario: Conflicting routes, conflict and no-conflict rows; server-lifecycle / Scenario: Hooks on every stop path, the route-conflict row); verify `go test -race -run TestConflicts` passes
  - RED: table, plus the conflict row of `TestStart_Fails`; fails because the error does not name configurers
  - GREEN: registry of `(pattern, configurer type)`, conflict error naming both
  - REFACTOR: Move Function registry into its own type, then /simplify
- [x] 4.4 Middleware order and scope (route-registration / Scenario: Middleware order and scope, all rows); verify `go test -race -run TestMiddlewareOrder` passes
  - RED: table recording middleware calls; fails on order and sub-group inheritance
  - GREEN: snapshot middleware per route, copy-on-create sub-groups
  - REFACTOR: Replace Loop with Pipeline for chain composition, then /simplify
- [x] 4.5 Faulty middleware (route-registration / Scenario: Faulty middleware, all rows); verify `go test -race -run TestFaultyMiddleware` passes
  - RED: table with nil middleware, nil-returning middleware, panicking and short-circuiting middleware; fails on nil handling
  - GREEN: configure-time nil checks; middleware runs inside recovery
  - REFACTOR: Introduce Assertion helpers for nil checks, then /simplify
- [x] 4.6 Mount plain handlers (route-registration / Scenario: Mounted handler, all rows); verify `go test -race -run TestMount` passes
  - RED: table with `http.FileServerFS`, panicking handler and nil; fails because `Mount` does not exist
  - GREEN: `Mount` registering a method-less pattern with group middleware and the group prefix stripped (`http.FileServerFS` returns 404 for `/static/a.txt` without it)
  - REFACTOR: Remove duplication with `Handle`, then /simplify
- [x] 4.7 Configurer panics name the configurer (route-registration / Scenario: Configure panics, all rows; server-lifecycle / Scenario: Hooks on every stop path, the configure-panic row); verify `go test -race -run TestConfigurePanics` passes
  - RED: table, plus the configurer-panic row of `TestStart_Fails`; fails because panics escape `Start`
  - GREEN: per-configurer recover with `%T` and panic value, no bind
  - REFACTOR: Extract Function for safe invocation, then /simplify
- [x] 4.8 Godoc for joining rules and middleware scope with an `Example` showing groups and middleware; verify `go test -run Example` passes
- [x] 4.9 Verify slice end to end with a multi-configurer app, confirm join, conflict and recovery branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from the module directory, run gopls diagnostics, run /simplify on the slice and commit `feat(core): add route groups, middleware and registration checks`

## 5. Slice: unmatched routes, panics and error extension

- [x] 5.1 404 and 405 problems with `Allow` (error-responses / Scenario: No route or wrong method, all rows); verify `go test -race -run TestUnmatched` passes
  - RED: table; fails because ServeMux writes plain text
  - GREEN: `mux.Handler(r)` empty-pattern interception with a recording writer; convert only 404/405
  - REFACTOR: Extract Class for the recording writer, then /simplify
- [x] 5.2 Recovered panics are safe problems (error-responses / Scenario: Recovered panic, all rows including `panic(nil)` and `http.ErrAbortHandler`); verify `go test -race -run TestPanicRecovery` passes
  - RED: table; fails because panics are not recovered or leak the value
  - GREEN: `prblm.Recover` outermost around the router
  - REFACTOR: Inline unnecessary wrappers, then /simplify
- [x] 5.3 Late errors are not rendered (error-responses / Scenario: Late error, all rows); verify `go test -race -run TestLateError` passes
  - RED: table asserting the client response; fails because a second write is attempted or commit is not tracked (the `srvr.response.committed` access-record assertion is added in 6.10, when access records exist)
  - GREEN: commit tracking on the response writer, expose committed state for logging
  - REFACTOR: Combine writer wrappers into one (Combine Functions into Class), then /simplify
- [x] 5.4 Consumer prblm options and error hook (error-responses / Scenario: Consumer mapper and hook, all rows); verify `go test -race -run TestErrorExtension` passes
  - RED: table including panicking hook and nil hook, asserting the response (the "access record is still written" assertion is added in 6.10); fails because options are not passed through
  - GREEN: `WithProblemOptions`, `WithErrorHook`, srvr `OnError` appended last, consumer hook guarded by recover, invalid prblm options recovered into `ErrConfig`
  - REFACTOR: Separate Query from Modifier in the hook composition, then /simplify
- [x] 5.5 Godoc on why `prblm.OnError` must not be passed directly and an `Example` with `WithErrorHook`; add the godoc link to the existing `unassigned` prblm carry-over in `notes.md` (composable `OnError`, exported status-only sentinel) and attach a failing test as its evidence; verify `go test -run Example` passes and the note cites the test
- [x] 5.6 Verify slice end to end, confirm interception, recovery and commit branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from the module directory, run gopls diagnostics, run /simplify on the slice and commit `feat(core): render unmatched routes and panics as problems`

## 6. Slice: trace context and access log

- [x] 6.1 Continue a valid traceparent (trace-context / Scenario: Valid traceparent, all rows); verify `go test -race -run TestTraceparent_Valid` passes
  - RED: table; fails because no parser exists
  - GREEN: internal W3C level 1 parser and span id generation
  - REFACTOR: Replace Magic Literal for field lengths, then /simplify
- [x] 6.2 Start a new trace for absent or invalid input (trace-context / Scenario: New trace, both Examples tables); verify `go test -race -run TestTraceparent_New` passes, plus a `FuzzTraceparent` fuzz target running 30s without crash
  - RED: table with every malformed and invalid row and duplicate headers; fails on acceptance of uppercase and zero ids
  - GREEN: strict validation, single-header check, length cap
  - REFACTOR: Decompose Conditional into named validators, then /simplify
- [x] 6.3 Ignore incoming context opt-in (trace-context / Scenario: Ignoring incoming context, all rows); verify `go test -race -run TestTrace_Ignore` passes
  - RED: table with option and config rows; fails because the option does not exist
  - GREEN: `WithIgnoreIncomingTraceContext`, `TraceConfig.IgnoreIncoming`, `WithTraceConfig`; add the `WithTraceConfig` row of `TestOptionPrecedence`
  - REFACTOR: Parameterize Function, then /simplify
- [x] 6.4 Trace accessor (trace-context / Scenario: Reading the trace context, all rows including nil context); verify `go test -race -run TestTraceFromContext` passes
  - RED: table; fails because accessor does not exist
  - GREEN: unexported context key, `TraceFromContext`
  - REFACTOR: none expected; /simplify
- [x] 6.5 Unique ids under concurrency (trace-context / Scenario: Concurrent generation); verify `go test -race -run TestTrace_Unique` passes
  - RED: 1000 concurrent requests collecting ids; fails if a shared buffer is reused
  - GREEN: `crypto/rand` per id
  - REFACTOR: /simplify
- [x] 6.6 Problems carry `trace_id` (error-responses / Scenario: Trace id in the problem, all rows); verify `go test -race -run TestProblemTraceID` passes
  - RED: table; fails because no enricher exists
  - GREEN: srvr enricher setting `trace_id`
  - REFACTOR: /simplify
- [x] 6.7 Default JSON stdout logger (structured-logging / Scenario: Default output); verify `go test -race -run TestDefaultOutput` passes
  - RED: capture stdout via an injectable writer defaulting to `os.Stdout`; fails because nothing is logged
  - GREEN: base JSON handler at INFO, correlation wrapper
  - REFACTOR: Extract Function for handler resolution, then /simplify
- [x] 6.8 Correlation keys and request logger (structured-logging / Scenario: Correlation keys, all rows; Scenario: Grouped loggers, all rows); verify `go test -race -run 'TestCorrelation|TestCorrelation_Groups'` passes
  - RED: tables, including `WithGroup("db")`, nested groups, `With` before and after `WithGroup`, `WithGroup("")` and `slog.Default()` after `WithSetDefaultLogger()`; the group rows fail with a naive `Record.AddAttrs` correlation (`{"db":{"trace_id":..}}`, as probe `probes/xgroup` showed)
  - GREEN: srvr handler recording `WithGroup`/`WithAttrs` steps and replaying them after `base.WithAttrs(trace_id, span_id)`, fast path without steps, `LoggerFromContext`, `WithSetDefaultLogger` opt-in
  - REFACTOR: Replace Primitive with Object for the step list, then /simplify
- [x] 6.9 Measure the group-replay cost; verify `go test -run '^$' -bench BenchmarkCorrelation -benchmem` reports grouped and ungrouped records and the numbers are recorded in `notes.md` (an open carry-over targeting `core-server` with per-trace caching as the suggested action if grouped records cost more than twice ungrouped ones)
- [x] 6.10 One access record per request (structured-logging / Scenario: Access record content; Scenario: Unmatched requests, with `msg` `http.request.failed` at `WARN` and the `problem` group for 404 and 405); verify `go test -race -run TestAccessRecord` passes
  - RED: tables for every behaviour and unmatched row, plus the access-record assertions deferred from 5.3 (`srvr.response.committed`) and 5.4 (panicking hook); fails because no access middleware exists
  - GREEN: access middleware with status/bytes/duration recording, `http.route` from the pattern the mux matched (read through a per-request holder, since `r.Pattern` is set only on the request value the mux receives), without method and omitted when unmatched, failure taken from the per-request state that srvr's `OnError` and the unmatched interception fill, event and level selection
  - REFACTOR: Extract Function for level selection, then /simplify
- [x] 6.11 Centralise log keys in one internal file with a golden-record test pinning the keys of every event srvr writes and `srvr.log.schema` `1` on each (structured-logging / Scenario: Schema version attribute, rows available in this slice; the lifecycle rows are added in 7.10); verify `go test -run TestLogSchemaGolden` passes and fails when a key is renamed without bumping the version
- [x] 6.12 Verify slice end to end (request with traceparent -> problem `trace_id` equals access record `trace_id`), confirm parser, generator, correlation replay and access branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from the module directory, run gopls diagnostics, run /simplify on the slice and commit `feat(core): add W3C trace context and JSON access log`

## 7. Slice: logging configuration and safety

- [x] 7.1 Level and format configuration (structured-logging / Scenario: Level and format, accepted and rejected rows; Scenario: Config struct decoded from a file); verify `go test -race -run 'TestLogConfig|TestLogConfigDecode'` passes
  - RED: tables; fails because `LogConfig` does not exist
  - GREEN: `LogConfig`, `WithLogConfig`, `WithLogLevel`, `WithLogFormat`, `WithLogOutput`, construction-time parsing naming fields; add the log row of `TestOptionPrecedence`
  - REFACTOR: Replace Primitive with Object for format, then /simplify
- [x] 7.2 Runtime level changes (structured-logging / Scenario: Runtime level change, all rows including concurrent); verify `go test -race -count=20 -run TestLogLevelVar` passes
  - RED: table; fails because the level is fixed at construction
  - GREEN: `WithLogLevelVar` passed as the handler's `Leveler`
  - REFACTOR: /simplify
- [x] 7.3 Source attribute points to the caller (structured-logging / Scenario: Source attribute); verify `go test -race -run TestAddSource` passes
  - RED: table asserting file and line; fails because the wrapper changes PC or access records carry source
  - GREEN: wrapper preserves record PC, access records use PC 0
  - REFACTOR: /simplify
- [x] 7.4 Own handler or logger (structured-logging / Scenario: Own handler, all rows); verify `go test -race -run TestOwnHandler` passes
  - RED: table including conflicting settings, nil, erroring and panicking handler; fails because conflicts are not detected
  - GREEN: `WithLogHandler`, `WithLogger`, conflict validation, guard handler with recover and error counting
  - REFACTOR: Extract Class for the guard, then /simplify
- [x] 7.5 Access log configuration (structured-logging / Scenario: Access log configuration, all rows); verify `go test -race -run TestAccessLogConfig` passes
  - RED: table including 500 on skipped path and disabled log; fails because suppression is unconditional
  - GREEN: `AccessLogConfig{Disabled, SkipPaths}`, `WithAccessLogConfig`, `WithAccessLogSkipPaths`, `WithAccessLogDisabled` suppressing only below 500, exact path match; add the access-log row of `TestOptionPrecedence`
  - REFACTOR: /simplify
- [x] 7.6 Redaction at every level (structured-logging / Scenario: Redaction at every level, defaults and opt-ins); verify `go test -race -run TestRedaction` passes
  - RED: table scanning every output line for forbidden values; fails if any value appears
  - GREEN: allow-list of attributes, opt-ins `IncludePath`, `IncludeQuery`, `IncludeClientAddress` and their options `WithAccessLogIncludePath`, `WithAccessLogIncludeQuery`, `WithAccessLogIncludeClientAddress`
  - REFACTOR: Replace Conditional with table of attribute builders, then /simplify
- [x] 7.7 Hostile values and truncation of every string attribute (structured-logging / Scenario: Hostile user agent; Scenario: Truncating any string attribute, including the `error` value, `LogValuer`, `msg` and consumer `exception.message` rows; Scenario: Error attributes keep their own limits, all rows); verify `go test -race -run 'TestHostileValues|TestTruncation'` passes
  - RED: tables including consumer attributes, `With` attributes, attributes in groups, non-string kinds, `error` values via `slog.Any`, an unresolved `LogValuer`, a long `msg`, a consumer attribute named `exception.message`, `MaxAttrBytes` 0/-1/16 by option and config, and a consumer handler; fails because only request attributes srvr builds are cut
  - GREEN: truncation in srvr's wrapper handler (record `msg`, record attributes and recorded `With` steps, recursing into groups, after `Value.Resolve()`; strings and `error` values) at UTF-8 boundaries, `LogConfig.MaxAttrBytes`, `WithLogMaxAttrBytes`, top-level `srvr.attr.truncated`; larger `exception.*` limits only for attributes srvr marks on its own records
  - REFACTOR: Extract Function for the attribute walk shared with correlation replay, then /simplify
- [x] 7.8 Log write failures never fail requests (structured-logging / Scenario: Broken output, all rows); verify `go test -race -run TestBrokenOutput` passes
  - RED: table with EPIPE and short-write writers; fails because errors are ignored silently or counted wrong
  - GREEN: counting writer guard, `LogWriteErrors()`
  - REFACTOR: /simplify
- [x] 7.9 Service and build information (structured-logging / Scenario: Service attributes, all rows); verify `go test -race -run TestServiceAttributes` passes
  - RED: table with an injectable build-info reader; fails because attributes are missing
  - GREEN: `ServiceConfig`, `WithServiceConfig`, `WithServiceName`, `WithServiceVersion`, `WithServiceEnvironment`, defaults from `debug.ReadBuildInfo`, `unknown_service:<exe>`; add the service row of `TestOptionPrecedence`
  - REFACTOR: /simplify
- [x] 7.10 Lifecycle records (server-lifecycle / Scenario: Lifecycle records, all rows including the shutdown-hook rows); verify `go test -race -run TestLifecycleRecords` passes
  - RED: table; fails because lifecycle events are not logged
  - GREEN: `server.started/stopping/stopped/failed` and `server.shutdown_hook.failed` (phase, index, hook `code.function.name`, `exception.message`) with service attributes and `srvr.log.schema`; add the lifecycle rows of `TestLogSchemaGolden`
  - REFACTOR: /simplify
- [x] 7.11 Godoc for every logging option with defaults and reasons, and an `Example` loading `LogConfig` from JSON; verify `go test -run Example` passes
- [x] 7.12 Verify slice end to end, confirm guard, truncation and redaction branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from the module directory, run gopls diagnostics, run /simplify on the slice and commit `feat(core): make logging configurable and safe`

## 8. Slice: triage error records

- [x] 8.1 Error description (triage-error-records / Scenario: Error description, all rows); verify `go test -race -run TestTriage_Description` passes
  - RED: table; fails because records lack error attributes
  - GREEN: srvr's `OnError` only stores the error in the per-request state; the access stage builds the single `http.request.failed` record from it with `error.type`, `exception.message`, `error.chain`
  - REFACTOR: Extract Class `triageRecord`, then /simplify
- [x] 8.2 Problem group and committed flag (triage-error-records / Scenario: Problem group, all rows); verify `go test -race -run TestTriage_Problem` passes
  - RED: table; fails because problem fields are missing
  - GREEN: problem group from the hook's `*prblm.Problem`, committed flag from the writer
  - REFACTOR: /simplify
- [x] 8.3 Handler code location (triage-error-records / Scenario: Handler location, all rows); verify `go test -race -run TestTriage_CodeLocation` passes
  - RED: table with method value, closure and mounted handler; fails on `-fm` names
  - GREEN: resolve at registration with `runtime.FuncForPC`, normalise names
  - REFACTOR: /simplify
- [x] 8.4 Panic records with stack (triage-error-records / Scenario: Panic record; Scenario: Oversized stack; structured-logging / Scenario: Aborted request, both rows, with the access record written before the `http.ErrAbortHandler` panic is re-raised, and `srvr.response.aborted` added to the golden-record test with `srvr.log.schema` unchanged); verify `go test -race -run TestTriage_Panic` passes
  - RED: tables including panicking `String()`, deep recursion and both Aborted request rows (connection closed, access record with `srvr.response.aborted`); fails because stack is missing or uncapped and no record is written for an aborted request
  - GREEN: `error.type` `panic` and `exception.type` from the panic value's Go type, stack from `*prblm.PanicError`, 32 KiB cap tested at 32768/32769 bytes, message from `PanicError.Value` with guarded formatting
  - REFACTOR: /simplify
- [x] 8.5 Stable fingerprint (triage-error-records / Scenario: Fingerprint stability, all rows); verify `go test -race -run TestTriage_Fingerprint` passes
  - RED: table of pairs; fails because no fingerprint exists
  - GREEN: SHA-256 over method, route and type chain (or panic type and top application frame)
  - REFACTOR: /simplify
- [x] 8.6 Bounded, safe chain walking (triage-error-records / Scenario: Hostile error values, all rows); verify `go test -race -run TestTriage_Hostile` passes, plus a `FuzzErrorChain` target running 30s without crash
  - RED: table with deep, cyclic, wide, panicking and typed-nil errors and 1 MiB message; fails or hangs without limits
  - GREEN: breadth-first walk with caps and per-call recover
  - REFACTOR: /simplify
- [x] 8.7 Build attributes on errors (triage-error-records / Scenario: Build attributes on errors); verify `go test -race -run TestTriage_Build` passes
  - RED: table; fails because error records lack service attributes
  - GREEN: attach service/build attributes to failed records
  - REFACTOR: /simplify
- [x] 8.8 Consumer redactor (triage-error-records / Scenario: Redacting error text, all rows); verify `go test -race -run TestTriage_Redactor` passes
  - RED: table including panicking and nil redactor; fails because no redactor exists
  - GREEN: `WithErrorRedactor`, guarded application
  - REFACTOR: /simplify
- [x] 8.9 Document the log schema (event names, keys, levels, limits, the `srvr.log.schema` change policy, `error.type` meaning per failure kind) in `LOGGING.md` and godoc, with a sample triage record; write `notes.md` carry-overs for stack capture at error creation, TLS, h2c and health endpoints (Target: `unassigned`); verify the golden schema test matches the document
- [x] 8.10 Verify slice end to end (one failing request yields one self-contained record that links to the problem by `trace_id`), confirm builder, walk, fingerprint and redactor branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from the module directory, run gopls diagnostics, run /simplify on the slice and commit `feat(core): write self-contained triage error records`

## 9. Slice: verification fixes

Findings of the `core-server` verification (C1, C2, W1-W4, S1-S6). Reproductions kept from the verification run are the RED tests where a defect was found; rows that pin behaviour already correct (characterization) are confirmed by a mutation check instead (break the code, see the row fail, restore).

- [x] 9.1 Faulty consumer handler methods never reach the response (structured-logging / Scenario: Own handler, the `Enabled`, `WithAttrs` and `WithGroup` rows); verify `go test -race -run TestOwnHandler` passes
  - RED: add the three rows to `TestOwnHandler` (handler panicking in `Enabled` for request records, in `WithAttrs`, in `WithGroup`), asserting status `200`, no record and an increased `srvr.LogWriteErrors()`; fails with `EOF` because only `Handle` is guarded (`logguard.go`), `Enabled` is called unguarded from `writeAccess` and the replay of `WithAttrs`/`WithGroup` runs before the guard
  - GREEN: guard `Enabled` (a panic counts and reports disabled) and build the replayed target handler inside the guard, so a panic there counts and skips the record
  - REFACTOR: Extract Function for one guarded call shared by `Enabled`, replay and `Handle`; /simplify then inlined it back into one deferred recover in `Enabled` and one in `handle`, which avoids a closure call on every `Enabled` (including disabled levels)
- [x] 9.2 Logging a hostile error value never fails the request (structured-logging / Scenario: Hostile logged errors, all rows); verify `go test -race -run TestHostileLoggedErrors` passes
  - RED: table with a typed-nil error and a panicking `Error()` in the record, and a typed-nil error through `l.With`; fails with status `500` because `truncator.attr` calls `Error()` unguarded on both paths
  - GREEN: read the text through the guarded `errorText` (`triage.go`)
  - REFACTOR: Move Function `errorText` next to the other guarded formatting if /simplify finds it clearer, then /simplify
- [x] 9.3 Failed records are never suppressed and late failures keep their level (structured-logging / Scenario: Failures while the access log is suppressed, all rows; error-responses / Scenario: Late error, all rows with `level`, `srvr.response.committed` and no `problem`; triage-error-records / Scenario: Problem group, the late-panic row); verify `go test -race -run 'TestAccessLogSuppressed|TestLateError|TestTriage_Problem'` passes
  - RED: new `TestAccessLogSuppressed` table; extend `TestLateError` to read the access record for every row (fixing its comment that pointed to `TestAccessRecord`) and drop the now-duplicate late-error subtest of `TestAccessRecord_Deferred`; add the late-panic row to `TestTriage_Problem`; fails because `writeAccess` suppresses failed records below 500 and takes the level from the status sent (a late panic is `INFO`)
  - GREEN: suppression applies only to `http.request.completed`; a failure's level is `ERROR` for a panic, otherwise the higher of the sent status's level and its problem's status's level
  - REFACTOR: Extract Function for the event and level choice of a finished request, then /simplify
- [x] 9.4 A passed listener is owned by the server (server-lifecycle / Scenario: A passed listener is owned by the server, all rows); verify `go test -race -run TestListenerOwnership` passes
  - RED: table over every row dialling the listener after the call returns; the failure rows fail with "still accepts connections after Start failed" (verification reproduction `TestScratch_ListenerOwnership`)
  - GREEN: close the configured listener whenever the server closes without serving (`closeUnserved`); `Start(nil)` and misuse errors never reach it
  - REFACTOR: Consolidate Duplicate Conditional Fragments for the listener close on the unserved paths, then /simplify
- [x] 9.5 Nil option row (server-lifecycle / Scenario: Valid and invalid configuration, the nil `Option` row); verify `go test -race -run TestNew_Config` passes
  - Characterization: add the row to `TestNew_Config`; it passes against the existing nil check in `newSettings`; confirm it fails when that check is removed (mutation), then restore
- [x] 9.6 `WithLogAddSource` row (structured-logging / Scenario: Source attribute, the `WithLogAddSource()` row); verify `go test -race -run TestAddSource` passes
  - Characterization: add the row to `TestAddSource`; confirm it fails when `WithLogAddSource` sets nothing (mutation), then restore
- [x] 9.7 Build attributes on failed records through a build-info seam (triage-error-records / Scenario: Build attributes on errors, both rows); verify `go test -race -run TestTriage_Build` passes
  - RED: make `TestTriage_Build` table-driven over both rows, injecting build information through an `export_test.go` option; fails to compile because no seam exists, then fails on the `(none)` row if the reader is ignored
  - GREEN: settings hold the build-info reader (default `debug.ReadBuildInfo`) used by `New`; `export_test.go` exposes an option setting it for tests only
  - REFACTOR: /simplify
- [x] 9.8 Option precedence covers every config struct (server-lifecycle / Scenario: Option and config precedence, the access-log and service rows that tasks 7.5 and 7.9 named); verify `go test -race -run TestOptionPrecedence` passes
  - Characterization: add both rows to `TestOptionPrecedence`; confirm each fails when its `With<Struct>` option stops overwriting the field (mutation), then restore
- [x] 9.9 Deterministic stop races and recorder paths (server-lifecycle / Scenario: Shutdown misuse, the "being shut down by another goroutine" row; structured-logging / Scenario: Access record content); verify `go test -race -count=20 -run 'TestShutdown|TestResponseRecorder'` passes
  - Shutdown race: let a `Shutdown` that finds the server running claim the stop sequence under the lock that read the state (design D1), removing the unreachable "lost the race" branch; a test stops the server through its context with a blocking `BeforeDrain` hook and calls `Shutdown` meanwhile, asserting `nil` only after the stop finishes
  - Recorder: internal test calling `ReadFrom` before `WriteHeader`, asserting status `200`, the started flag and the byte count
  - Bounded test: `TestShutdown_Drain` waits on every channel with a deadline and fails with a message instead of hanging until the 60s test timeout
- [x] 9.10 Godoc and log schema document: `AccessLogConfig` and its options (failed records are never suppressed), `LogWriteErrors` (process-wide), `WithListener` (ownership), `recordFailure` (W3 rule), `LOGGING.md` levels and suppression; verify `go doc` shows them and `TestLogSchemaGolden` still passes with `srvr.log.schema` unchanged (no key renamed or removed; a failure's level is not a key)
- [x] 9.11 Verify the slice: confirm the log guard, truncation, access level and suppression, listener close and stop-sequence branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from the module directory, run gopls diagnostics, check no `.test` process is left, run /simplify on the slice and commit `fix(core): ...`

## 10. Slice: second verification fixes

Findings of the second `core-server` verification (C1, W1, S1, S2) and the two earlier choices it asked to state explicitly (a late 4xx is logged at the higher of the two levels; `Shutdown` before `Start` closes the `WithListener` listener). Rows that reproduce a defect are the RED tests; rows that pin behaviour already correct are characterization rows confirmed by a mutation check (break the code, see the row fail, restore).

- [x] 10.1 Hostile hook error values never break the stop sequence (server-lifecycle / Scenario: Faulty hooks, the typed-nil and `Error()`-panics rows and the later-`Shutdown` assertion; Scenario: Hostile hook errors when Shutdown is called before Start, all rows); verify `go test -race -run 'TestShutdownHooks_Faulty$|TestHostileHookErrors'` passes
  - RED: register an `AfterDrain` hook returning a typed-nil error or an error whose `Error()` panics, call `Shutdown` before `Start` under a `recover`, then call `Shutdown` again with a deadline; fails because `runHooks` (`hooks.go`) calls `err.Error()` unguarded in the caller's goroutine, so the first call panics, `finish` never runs and the second call never returns
  - GREEN: read the hook error's text through the guarded `errorText`
  - REFACTOR: /simplify
- [x] 10.2 Audit the core module for other unguarded `Error()` calls on consumer-supplied errors and guard each with a test; the known one is `fail` (`server.go`), which logs `err.Error()` of a `Start` context's `Err()` (server-lifecycle / Scenario: Start fails, the two hostile `Err()` rows); verify `go test -race -run TestStart_Fails` passes
  - RED: a `Start` context that is done and whose `Err()` returns a typed-nil error or an error whose `Error()` panics; fails because `Start` panics
  - GREEN: log the text through `errorText`
  - Audit result: no other unguarded `Error()` on a consumer-supplied error in the core module (`config.go` `validate` formats a `net.SplitHostPort` error; `triage.go` `valueText` calls `Error()` under `recover`); a probe of a handler returning a typed-nil or `Error()`-panicking error answered `500` with a failed record, so the handler path is not affected (suspicion not confirmed, probe not kept)
  - Audit correction (third verification): this audit looked only for direct `Error()` calls and missed `fmt` verbs (`%v`, `%w`) and `fmt.Sprint` applied to consumer values, which call `Error()`/`String()` and are guarded by `fmt` only one level deep (a value whose `Error()` panics with itself makes `fmt` panic again): `hooks.go` `runHooks` (`%w` of the hook error) and `runHook` (`%v` of the panic value, `%w` of the hook context's `Err()`), `group.go` `safeConfigure`, `problems.go` `newProblems` and `config.go` `validateListener`. Task 12.8 repeats the audit for the whole class
  - REFACTOR: /simplify
- [x] 10.3 The access record reports the final status (structured-logging / Scenario: Access record content, the `103` row); verify `go test -race -run 'TestAccessRecord$|TestResponseRecorder'` passes
  - RED: a handler that calls `w.WriteHeader(103)` and writes nothing else; fails because `responseRecorder.statusCode` (`access.go`) returns the informational status
  - GREEN: report `200` when no final (`>= 200`) header was written (`WriteHeader` no longer records a `1xx`)
  - REFACTOR: Substitute Algorithm, `statusCode` is `cmp.Or(w.status, http.StatusOK)`; /simplify
- [x] 10.4 Late 4xx level row (error-responses / Scenario: Late error, the `500` then `prblm.ErrConflict` row); verify `go test -race -run TestLateError` passes
  - Characterization: add the row; confirm it fails when the level stops taking the higher of the two (mutation), then restore
- [x] 10.5 Listener ownership starts when `New` returns (server-lifecycle / Scenario: A passed listener is owned by the server, the failed-`New` row); verify `go test -race -run TestListenerOwnership` passes
  - Characterization: add the row; confirm it fails if `New` closes the listener on failure (mutation), then restore
- [x] 10.6 Godoc: `WithListener` (ownership starts once `New` returns a server; a failed `New` leaves the listener with the caller; `ErrServerClosed` does not close it because it was closed when the server closed; `Shutdown` before `Start` closes it), `WithShutdownHook` (typed-nil and panicking hook errors count as failures); `LOGGING.md` if it describes the status or late-failure levels; verify `go doc` shows them and `TestLogSchemaGolden` still passes with `srvr.log.schema` unchanged
- [x] 10.7 Verify the slice: confirm the hook, `fail` and recorder branches are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from the module directory, run gopls diagnostics, check no `.test` process is left, run /simplify on the slice and commit `fix(core): ...`

## 11. Slice: redact and limit lifecycle error text

Owner decision (notes.md "Lifecycle records do not pass exception.message through the error redactor"): srvr's lifecycle records build `exception.message` like request failure records do - redacted, panicking redactor contained, 8 KiB limit. No key is renamed or retyped, so `srvr.log.schema` stays unchanged.

- [x] 11.1 Lifecycle error text is redacted (server-lifecycle / Scenario: Lifecycle error text is redacted and limited, the redactor rows); verify `go test -race -run TestLifecycleErrorText` passes
  - RED: recreate the deleted probe as a table row - `WithErrorRedactor` masking `secret`, an `AfterDrain` hook returning `errors.New("close db: secret")`, `Shutdown` before `Start`; fails with `unredacted: close db: secret` because `runHooks` (`hooks.go`) and `fail` (`server.go`) log `errorText(err)` as a plain string attribute
  - GREEN: one helper in `triage.go` building the `exception.message` attribute (guarded text -> `(*Server).redact` -> `cutString(..., maxMessageBytes)` -> `ownText`), used by `errorAttrs`, `runHooks` and `fail`
  - REFACTOR: Extract Function so `errorAttrs` and the lifecycle sites share it; /simplify
- [x] 11.2 Lifecycle error text has the 8 KiB limit (same scenario, the 5000- and 9000-byte rows and the expanding-redactor row); verify `go test -race -run TestLifecycleErrorText` passes
  - RED: the 5000-byte hook error row; fails before 11.1's GREEN because `MaxAttrBytes` (1024) cuts the plain string attribute (if 11.1's GREEN already made it pass, record the row as characterization with a mutation check: replace the helper call with the plain attribute, see the row fail, restore)
- [x] 11.3 Godoc and log schema document: `WithErrorRedactor` names the lifecycle records; `LOGGING.md` lifecycle section (written with the plan); verify `go doc` shows it and `TestLogSchemaGolden` and `TestLogSchemaGolden_Lifecycle` still pass with `srvr.log.schema` unchanged
- [x] 11.4 Verify the slice: confirm the helper and its lifecycle call sites are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from the module directory, run the full suite without `-race` about 10 times to look for the unreproduced failure (notes.md "One unreproduced failure of the core test suite"), run gopls diagnostics, check no `.test` process is left, run /simplify on the slice and commit `fix(core): redact and cap lifecycle error records`

## 12. Slice: third verification fixes

Findings of the third `core-server` verification (C1, W1, S1 and one unconfirmed documentation point). Owner decisions given with the findings: a listener whose `Addr()` returns nil or panics fails `Start` through `fail` and is closed under the ownership rules, never a silent fallback (AGENTS rule 7); no srvr lock is held across a call into consumer code. Each RED step recreates a reproduction from the verification or from this plan's own probes (probe sources kept in the session scratchpad, not in the repository).

- [ ] 12.1 A typed-nil listener and an unreadable listener address fail construction (server-lifecycle / Scenario: Valid and invalid configuration, the typed-nil `*net.TCPListener` rows and the panicking-`Addr()` row); verify `go test -race -run TestNew_Config` passes
  - RED: add the three rows; the typed-nil row fails because `WithListener` (`config.go`) checks only `ln == nil` and `New` returns a server; the two rows combined with `Addr` fail because `validateListener` formats `s.listener.Addr()` and `New` panics
  - GREEN: `WithListener` rejects `isNil(ln)` naming `Listener` and does not keep the listener; `validateListener` names the listener through the guarded address helper of 12.2 (its type when the address cannot be read)
  - REFACTOR: /simplify
- [ ] 12.2 A listener without a usable address fails `Start` without hanging the server (server-lifecycle / Scenario: A listener without a usable address, all rows); verify `go test -race -run TestListener_UnusableAddress` passes
  - RED: table over `Addr()` returning nil, a typed-nil `*net.TCPAddr`, panicking, and an address whose `String()` panics with a value whose `Error()` panics; each row bounds every call with a deadline; fails because `serve` (`server.go`) calls `ln.Addr().String()` while holding `s.mu` with no deferred unlock, so `Start` panics and `Shutdown`, `Addr` and `Register` block forever (probe: `Shutdown HANG`)
  - GREEN: a guarded `listenerAddr(ln) (string, error)` (`recover`, `isNil`, panic value through `valueText`) called by `Start` before `serve`; a failure goes through `s.fail` with an error wrapping `ErrConfig` naming `Listener`, which logs `server.failed`, runs the `AfterDrain` hooks and closes the listener; `serve` receives the address and calls no consumer code under the lock
  - Lock audit: list every `s.mu` critical section and the one `sync.Once` (`onceCloseListener`) and record which consumer calls, if any, run inside them; add a test for each violation found
  - REFACTOR: /simplify
- [ ] 12.3 Hook panic values and hook errors whose `Error()` panics with itself (server-lifecycle / Scenario: Faulty hooks and Scenario: Hostile hook errors when Shutdown is called before Start, the new rows); verify `go test -race -run 'TestShutdownHooks_Faulty$|TestHostileHookErrors'` passes
  - RED: the panicking-value row crashes the test binary with `fatal error: panic while printing panic value` from `runHook`'s deferred `fmt.Errorf("panic: %v", v)` (`hooks.go`), on a goroutine srvr owns; the returned-error row makes `Shutdown` panic in `runHooks`' `fmt.Errorf("%w: %s hook %d: %w", ...)`; the test helper `recoverPanic` reads the recovered value through a guarded formatter so the test itself cannot crash the same way
  - GREEN: `runHook` formats the panic value with `valueText`; `runHooks` returns a `hookError{phase, index, err}` whose `Error()` reads `err` through `errorText` and whose `Unwrap() []error` returns `ErrShutdownHook` and `err`
  - REFACTOR: /simplify
- [ ] 12.4 A `Shutdown` context with a hostile `Err()` (server-lifecycle / Scenario: A Shutdown context with a hostile Err, all rows); verify `go test -race -run TestShutdown_HostileContext` passes
  - RED: a running server with a blocking `BeforeDrain` hook and `Shutdown` with a context done after `50ms` whose `Err()` returns an error whose `Error()` panics with itself; fails because the abandoned hook's `fmt.Errorf("abandoned: %w", ctx.Err())` (`hooks.go`) panics out of `Shutdown` and the later `Shutdown` never returns (probe: `Shutdown2 HANG`)
  - GREEN: the abandoned error is a small type whose `Error()` reads the context error through `errorText` and whose `Unwrap()` returns it
  - REFACTOR: /simplify
- [ ] 12.5 A configurer panic value whose `Error()` panics (route-registration / Scenario: Configure panics, the `*main.HostileAPI` row); verify `go test -race -run TestConfigurePanics` passes
  - RED: the row; fails because `safeConfigure` (`group.go`) builds `routeError("configurer %T panicked: %v", c, v)`, so `Start` panics and the server stays starting
  - GREEN: format the value with `valueText`
  - REFACTOR: /simplify
- [ ] 12.6 A prblm option panic value whose `Error()` panics (error-responses / Scenario: Consumer mapper and hook, the two `WithProblemOptions` rows); verify `go test -race -run TestErrorExtension_Config` passes
  - RED: the hostile row; fails because `newProblems` (`problems.go`) formats the recovered value with `fmt.Sprint`, so `New` panics. The `WithNamespace("not a uri")` row is already in `TestErrorExtension_Config` (characterization, now named in the spec)
  - GREEN: format the value with `valueText`
  - REFACTOR: /simplify
- [ ] 12.7 Text reaches a consumer handler as strings when `MaxAttrBytes` is negative (structured-logging / Scenario: Text reaches a consumer handler as strings when the limit is disabled, all rows); verify `go test -race -run TestDisabledLimitKinds` passes
  - RED: a kind-capturing handler; fails because `truncator.record` (`truncate.go`) returns the record unchanged when `maxBytes < 0`, so `exception.message` and `exception.stacktrace` arrive as `slog.KindAny` wrapping the unexported `ownText` (regression from 5bd8e34) and logged `error` values arrive unconverted, for the consumer handler to format
  - GREEN: the record is rewritten whenever it carries srvr's own text or an `error` value, at any limit; only cutting is disabled by a negative limit
  - REFACTOR: /simplify
- [ ] 12.8 Audit the whole core module (every non-test `.go` file, `internal/` included) for each `fmt`, `slog`, `errors` call or string conversion that formats a consumer-supplied value (errors, panic values, configurers, options, listener addresses, context errors, handler results) without `errorText`/`valueText`; fix each site with a test; record the result here with every site checked and found safe and why
- [ ] 12.9 Godoc: `WithListener` (typed nil rejected; an unusable address fails `Start`), `WithShutdownHook` (any panic value and hostile errors are contained), `WithErrorRedactor`, `WithLogHandler` and `WithLogger` (called synchronously: blocking in them stalls the request or the lifecycle call), `WithLogMaxAttrBytes`/`LogConfig.MaxAttrBytes` if they describe what a negative value hands the handler; `LOGGING.md` where it describes the same; verify `go doc` shows them and `TestLogSchemaGolden` and `TestLogSchemaGolden_Lifecycle` still pass with `srvr.log.schema` unchanged
- [ ] 12.10 Verify the slice: confirm the address helper, hook error types, truncator rewrite and every audited site are covered in `go tool cover -func`, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` from the module directory, run gopls diagnostics, check no `.test` process is left, run /simplify on the slice and commit `fix(core): ...`

## Workflow follow-up

- Re-read `notes.md` and make sure every open item has a target or is `unassigned`.
- Tag `v0.1.0` after review; archive the change; verify the archived result.
