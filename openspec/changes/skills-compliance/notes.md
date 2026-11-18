# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (task group 1, 2026-11-18)
- **Status:** closed: recorded
- **Target:** skills-compliance
- **Context:** 1.1: `git fetch`; `origin/main` and `main` are both at `5d3b678`, nothing merged or archived since planning. The branch `skills-compliance` starts from `b0302aa` (`triage-debug-buffer` consolidation, unmerged), as the proposal assumes. 1.2, decisions on open carry-overs addressed to this change or `unassigned`: *Engine modules duplicate the core's test helpers* (gin-and-fiber-engines) deferred: a public test-support package is new API and no audit finding asks for it. *otelsrvr's go.mod requires the OpenTelemetry SDK for its tests* (otel-observability) deferred: a separate test module changes the layout, beyond the audit; slice 4 only bumps vulnerable versions. *Public engine package stability before v1* (gin-and-fiber-engines) partly adopted by slice 5 (typed `ConfigError`, `RouteErrorf`, `Only`, `Core.Settings()` replaced, `RequestState` route fields hidden); the full surface review stays open. *triage-debug-buffer task 7.1* (named serve goroutine) adopted by D4 / task 3.4, status updated in `triage-debug-buffer/notes.md` and `tasks.md`. *engine-support requirement over 500 characters* (otel-observability) deferred: this delta does not modify that requirement. Every other open `unassigned` entry in the archived changes and `scrty-integration` was read and stays deferred unchanged, since no audit finding covers it. 1.3: every audit target was found again with gopls (workspace symbols, document symbols, references) at the same definitions: `Core.redact` and `Core.exceptionMessage` (`engine/triage.go`), `fingerprint(parts ...string)` with two callers (`errorAttrs`, `panicAttrs`) taking the raw method, `truncator.attr` and `mayNeedRewrite` (`engine/log.go`), `tracectx.parse` reading `flags[1]&1`, `runCheck`'s `errors.As` outside `runGuarded` (`engine/route.go`), `WithAccessLogConfig` assigning `c` unchanged (`options.go`), `knownMethods`/`methodAttr` (`otelsrvr/otelsrvr.go`), `exportErrorHandler.Handle` writing `engine.ValueText(err)` (`otelsetup/setup.go`), `Core.Settings()`, whose only production caller is `fibersrvr/server.go` (`MaxHeaderBytes`), `RequestState.Route`/`.Location` exported fields (`engine/trace.go`), the serve closure in `Lifecycle.serve`. Reference counts: `ConfigError(` 33, `RouteError(` 14, `ForEngine(` 10, `WithEngine(` 7. No artifact needed a change; `openspec validate skills-compliance --strict` passes.
- **Evidence:** `git log --oneline HEAD..origin/main` is empty; gopls queries above; `openspec validate skills-compliance --strict` → `Change 'skills-compliance' is valid`.
- **Suggested action:** none.

### Audit 2026-11-18: findings and where they are fixed
- **Status:** closed: every finding fixed by the tasks listed per area (task 9.5 review, 2026-11-18). Each item listed as unverified was reproduced by its RED test before the fix: faulty route-check errors (3.1, a hang and two escaping panics), Shutdown during start (3.2), Goexit (3.3), the anonymous serve goroutine (3.4), the zero observer (4.3, one end_panic fault per engine), OTLP headers (4.5, reproduced through the trace exporter only: the metric exporter copies the map), the metrics listener at the deadline (4.6, a connection goroutine left). Two defects beyond the audit were found and fixed in slice 3 (see that entry).
- **Target:** skills-compliance
- **Context:** seven read-only audits against `samber/cc-skills-golang` on `b0302aa` (errors and safety; concurrency and context; style, naming and docs; design, types, modernize and data structures; testing, lint and benchmarks; security, observability and performance; dependencies, layout and CI). Every module passed `go vet`, `golangci-lint`, `go test -race` and `-shuffle=on`; gosec reported 0 issues. Not every audit navigated with gopls (errors/safety and concurrency used reads and grep, style used `go doc` and go/ast); consolidation 1.3 re-locates each finding with gopls.
- **Evidence (failing probes, run in the session scratchpad, not kept):**
  - `TestProbeSampledFlagHexLetters` (`internal/tracectx`): `flags ff: Sampled = false, want true`; `fe: true, want false`; `0a: true, want false`; `0b: false, want true` -> task 2.1.
  - `TestAnyNotCapped`: `plain len=1038 hdr json len=5077 list json len=5004` -> task 2.4.
  - `TestSecretInExceptionMessage`: `exception.message="charge: Get \"http://127.0.0.1:1/v1/charge?api_key=SECRET\": ..."` -> tasks 2.5-2.7, 4.7.
  - `TestFingerprintPerMethod`: three methods on `/nope` gave `75fb2fbf…`, `9b6edcc0…`, `3c83889f…` -> task 2.2.
  - `TestAccessLogConfigSkipPathsAliased`: `/healthz was logged: the server's SkipPaths follows the caller's slice mutated after New` -> task 2.3.
  - `TestOtelsrvrNilOption`: `otelsrvr.New(nil) panicked: runtime error: invalid memory address or nil pointer dereference` -> task 4.2.
  - `TestOtelsetupNilOption`: `err = <nil>, want ErrConfig` -> task 4.4.
  - `TestSettingsCopyAliases`: `Core.Settings() copy aliases the core's Hooks` -> task 5.3.
  - `govulncheck` (`GOWORK=off`): `otelsetup` reaches GO-2026-6617, -6612, -6611, -6610, -6603 in `golang.org/x/net@v0.59.0` (fixed in v0.60.0); unreached: GO-2026-6866, -6860 (x/net, fixed v0.62.0) in `otelsetup` and `ginsrvr`, GO-2026-5932 (x/crypto) in `ginsrvr`, `fibersrvr`, `internal/paritytest` -> task 4.1.
- **Unverified (each task's RED test decides):** route check returning a self-cyclic or panicking error (task 3.1); `Shutdown` during `stateStarting` (3.2); `runtime.Goexit` in `runGuarded` (3.3); serve goroutine not joined (3.4); metrics listener not closed at the deadline (4.6); OTLP headers aliased (4.5, mechanism read in otlptracehttp v1.47.0 `internal/otlpconfig/options.go:374`); zero `otelsrvr.Observer` faults (4.3).
- **Non-behavioural findings:** defaults without reasons in godoc, no README/LICENSE/CHANGELOG (slice 6); CI and lint gaps, `go.work.sum` tracked, per-module Makefiles (slice 7); fuzz gaps, sleeps, unnamed subtests, one benchmark, uncovered hot-path blocks (slice 8); long lines, nesting, parameters, modernize hits, mutable package tables, presized maps, missing interface checks, unmeasured performance ideas (slice 9); naming (slice 5).
- **Suggested action:** fix in the listed tasks; a RED test that passes closes its item as `closed: not reproduced`.

### Owner decisions 2026-11-18
- **Status:** closed: applied to proposal, specs, design and tasks
- **Target:** skills-compliance
- **Context:** fix every finding before `triage-debug-buffer` slice 2; MIT license; default redactor masks URL query values and secret-looking pairs; `ginsrvr.WithGinEngine` and `fibersrvr.WithFiberConfig`; keep the `replace` directives with `RELEASING.md` and a CI release guard. Planning decided, within those: `srvr.log.schema` stays `1` (the redactor already existed as an opt-in transform of `exception.message`); `slog.Any` values within the cap render unchanged.
- **Evidence:** unverified: owner answers in the planning session.
- **Suggested action:** none.

### Unmatched-request failed records cannot be sampled
- **Status:** open
- **Target:** unassigned
- **Context:** every unmatched request writes an `http.request.failed` `WARN` record, even with the access log disabled, so a path scanner controls log volume (`engine/access.go`, suppression applies only to `http.request.completed`). The fingerprint fix (task 2.2) bounds grouping, not volume.
- **Evidence:** unverified: follows from the code; the triage design makes failed records unsuppressible on purpose.
- **Suggested action:** a later change decides whether to offer an explicit sampling or rate-limit option for unmatched failed records.

### The user agent is logged by default without an opt-out
- **Status:** open
- **Target:** unassigned
- **Context:** `user_agent.original` is on every access record (`engine/access.go`); some mobile SDK user agents carry device identifiers. Task 6.5 documents it.
- **Evidence:** unverified: privacy judgement.
- **Suggested action:** a later change may add an explicit `AccessLogConfig` field to omit it.

### Kept as is after the audit
- **Status:** closed: owner scope, recorded for traceability
- **Target:** skills-compliance
- **Context:** `srvr.LogWriteErrors` keeps its name (low-confidence naming finding; the name is in the log-schema docs); `RouteRegistrar` stays (named in the project context as public shape); test files stay grouped by concern (commit `0470d34`); log-and-return of `server.failed` and `server.shutdown_hook.failed` stays (the records are schema commitments), with godoc noting that a caller logging the returned error logs it twice (task 6.1); `fibersrvr`'s per-request `context.WithCancel(base)` stays unless slice 8's fiber benchmark shows contention.
- **Evidence:** unverified: design judgement.
- **Suggested action:** none.

### TestStart_Stop deadline row can hang instead of failing under load
- **Status:** closed: fixed by skills-compliance task 8.2
- **Target:** skills-compliance (task 8.2, sleep and timing hygiene)
- **Context:** `TestStart_Stop/the_Start_context_deadline_passes` (`lifecycle_test.go`) gives `Start` a context with a `100ms` deadline created before `Start`, then blocks on `<-started` (line 90) waiting for the in-flight request to reach the handler. Under CPU load the deadline can pass before the request is served, the server stops, `inFlight` reports `-1`, and the test waits on `started` forever, until the package's 10-minute timeout, instead of failing. Found while repeating the core suite during slice 2; unrelated to that slice's code.
- **Evidence:** `go test -race -count=1 .` in a loop beside a parallel build: `panic: test timed out after 10m0s` / `running tests: TestStart_Stop/the_Start_context_deadline_passes (9m56s)`, goroutine `TestStart_Stop.func4` at `lifecycle_test.go:90` in `chan receive, 9 minutes`. Not reproduced on demand: it needs load, so no deterministic RED test exists yet.
- **Suggested action:** in task 8.2, start the deadline only after the request is in flight (or select on `started` together with the request's result and a bound), so a lost race fails fast with a message.

### Slice 2 /simplify: efficiency items deferred to the benchmarks
- **Status:** open (task 9.4 applied only the target cache; these stay deferred, owner decision 2026-11-18: timings too noisy to judge)
- **Target:** unassigned
- **Context:** the slice 2 efficiency review (read-only, no measurement) found two costs on the logging hot path: with a limit set, any `slog.Any` value forces a record rebuild (`mayNeedRewrite`), and `capAny` formats every such value with `fmt` to measure it, after which the base handler formats a value that fits a second time. Suggested cheaper forms: skip formatting for small fixed-size kinds (`time.Time`, `netip.Addr`, scalars), or format into a counting writer that stops after `maxBytes+1`. A third item, a keyword pre-scan before `secretPair` in `RedactSecrets`, only touches failed requests; the cheap `?`/`&` pre-check for `queryValue` was applied. Design D2 already accepts the formatting cost pending a benchmark.
- **Evidence:** unverified: code reading; `BenchmarkLogHandler` (task 8.4, with and without `slog.Any`) decides.
- **Suggested action:** measure on a quiet machine with `benchstat`: (1) skipping `fmt` formatting for small fixed-size `slog.Any` kinds or a counting writer in `capAny`; (2) a keyword pre-scan before `secretPair` in `RedactSecrets`, which the baseline puts at about 8 ms for a 32 KiB panic stack; apply each at a 10% or larger gain.

### Slice 3: defects found beyond the audit and fixed in place
- **Status:** closed: fixed by skills-compliance (tasks 3.1 and 3.4)
- **Target:** skills-compliance
- **Context:** (1) after `runCheck` stopped walking the chain, `Start`'s returned error still unwrapped to the consumer's self-cyclic error, so a caller's `errors.Is(err, X)` for any `X` not in the chain looped forever; `guard(err)` now hides an unwalkable chain (task 3.1). (2) `http.Server.Shutdown` waits for `Serve` on a `WaitGroup` without regard to its context (Go 1.27.4 `net/http/server.go` `Shutdown` and `Close`, `listenerGroup.Wait()`), so a `WithListener` listener whose `Close` does not unblock `Accept` hung srvr's `Shutdown` forever on net/http and gin; `httpRunner.Drain` now returns at the drain deadline (task 3.4). fibersrvr was not checked for the same listener (no spec row); fiber's `ShutdownWithContext` is a separate path.
- **Evidence:** (1) `TestRouteCheck_FaultyErrors/Unwrap_returns_the_error_itself` with `go test -race -timeout 20s`: `panic: test timed out`, goroutine in `errors.is` called from `routing_test.go:785`. (2) `TestServeGoroutine/a_WithListener_listener_whose_Close_does_not_unblock_Accept`: `panic: test timed out after 2m0s`, goroutine `[sync.WaitGroup.Wait]` in `net/http.(*Server).Shutdown` called from `engine.(*httpRunner).Drain`.
- **Suggested action:** none for net/http and gin. A later change may add the stuck-listener row for fiber.

### Configuration errors: log fields are checked after the other fields; otel modules keep plain errors
- **Status:** open
- **Target:** unassigned
- **Context:** found by the slice 5 /simplify altitude review. (1) `LogConfig.Level` and `Format` are validated in `newLogHandler`, after `newSettings` has already returned the other fields' errors, so `LogConfig{Level: "loud"}` with `WithMaxHeaderBytes(-1)` reports only `MaxHeaderBytes`; moving both checks into `Settings.validate` would name every invalid field in one joined error. (2) `otelsrvr` and `otelsetup` return `fmt.Errorf("%w: ...", ErrConfig)` with their own sentinels, not a `*ConfigError`-like type, so `errors.As` cannot read their field. (3) `Core.MaxHeaderBytesSet` reads like a bool; design D5 chose the name.
- **Evidence:** unverified: code reading (`engine/log.go` `newLogHandler`, `engine/config.go` `newSettings`); no spec row covers a log field together with another invalid field.
- **Suggested action:** a later change decides whether to validate log fields in `Settings.validate` and whether the otel modules get a typed error.

### RELEASING.md gates name per-module commands until slice 7
- **Status:** closed: done in slice 7 (`make check` and the CI `release` job)
- **Target:** skills-compliance (tasks 7.3 and 7.4)
- **Context:** slice 6's `RELEASING.md` lists the per-module `GOWORK=off` commands as the release gate, because the root Makefile targets (`tidy-check`, `vulncheck`) and the CI `release` job do not exist yet. Found by the slice 6 /simplify review.
- **Evidence:** unverified: documentation state.
- **Suggested action:** when 7.3 and 7.4 land, change the gate to `make vet lint test tidy-check vulncheck` from the root and describe the CI `release` job's `replace` check.

### Benchmark baseline before the measured changes
- **Status:** closed: used as the task 9.4 baseline (see "Task 9.4: measured performance changes")
- **Target:** skills-compliance (task 9.4)
- **Context:** task 8.4 baseline on an Apple M4 Pro (darwin/arm64, 14 cores), Go 1.27.4, one run each (`-count=1`), so treat differences under ~5% as noise; task 9.4 compares with `benchstat` over repeated runs. Logging goes to `io.Discard` through srvr's JSON handler in every benchmark. `BenchmarkLogHandler` builds the handler with `newLogHandler` (default 1024-byte cap) and logs with a request context; its steps are group-free `With` calls and its `slog.Any` value is a two-key `http.Header`. The pipeline benchmarks run the full per-engine pipeline (`httptest` for net/http and gin, fibersrvr's `Server.Test` over `app.Test` for fiber, whose in-memory connection is part of every measurement).

  | Benchmark | ns/op | B/op | allocs/op |
  |---|---:|---:|---:|
  | engine `BenchmarkCorrelation/ungrouped` | 1352 | 752 | 9 |
  | engine `BenchmarkCorrelation/grouped` | 2508 | 1281 | 15 |
  | engine `BenchmarkLogHandler/0_steps` | 1174 | 400 | 7 |
  | engine `BenchmarkLogHandler/0_steps_with_slog.Any` | 2510 | 770 | 20 |
  | engine `BenchmarkLogHandler/1_step` | 1377 | 712 | 10 |
  | engine `BenchmarkLogHandler/1_step_with_slog.Any` | 3350 | 1084 | 23 |
  | engine `BenchmarkLogHandler/3_steps` | 2142 | 1385 | 16 |
  | engine `BenchmarkLogHandler/3_steps_with_slog.Any` | 4000 | 1759 | 29 |
  | engine `BenchmarkRedactSecrets/no_secret` | 6239 | 112 | 3 |
  | engine `BenchmarkRedactSecrets/URL_query_secret` | 13780 | 610 | 11 |
  | engine `BenchmarkRedactSecrets/key=value_secret` | 8747 | 257 | 5 |
  | engine `BenchmarkRedactSecrets/32_KiB_stack` | 8221091 | 66049 | 3 |
  | srvr (net/http) `BenchmarkPipeline/success` | 3613 | 1333 | 19 |
  | srvr (net/http) `BenchmarkPipeline/handler_error` | 14979 | 7818 | 82 |
  | srvr (net/http) `BenchmarkPipeline/unmatched` | 18275 | 7424 | 104 |
  | ginsrvr `BenchmarkPipeline/success` | 3759 | 1341 | 19 |
  | ginsrvr `BenchmarkPipeline/handler_error` | 15473 | 7883 | 84 |
  | ginsrvr `BenchmarkPipeline/unmatched` | 18239 | 6642 | 86 |
  | fibersrvr `BenchmarkPipeline/success` | 20972 | 7692 | 46 |
  | fibersrvr `BenchmarkPipeline/handler_error` | 36187 | 13653 | 110 |
  | fibersrvr `BenchmarkPipeline/unmatched` | 41006 | 12361 | 114 |

  Observations, unverified until 9.4 measures a change: each `With` step costs about 3 allocations per record (the steps are replayed in `logHandler.target`); a `slog.Any` attribute adds about 13 allocations and 1.1-2 µs; `RedactSecrets` takes about 8 ms on a 32 KiB stack (about 4 MB/s); net/http's unmatched path costs about 20 more allocations than gin's (the `fallbackRecorder` second ServeMux dispatch in `answerUnmatched`).
- **Evidence:** `GOWORK=off go test -run x -bench . -benchmem ./engine` and `GOWORK=off go test -run x -bench . -benchmem .` (root module), `GOWORK=off go test -run x -bench . -benchmem ./...` in `ginsrvr` and in `fibersrvr`. Files: `engine/log_bench_test.go`, `server_bench_test.go`, `ginsrvr/server_bench_test.go`, `fibersrvr/server_bench_test.go`.
- **Suggested action:** baseline for task 9.4's measured changes (`logHandler.target` caching for group-free handlers, single ServeMux routing in `answerUnmatched`, fiber `accessInfo` copies): rerun with `-count=10` before and after each change and compare with `benchstat`.

### Task 9.4: measured performance changes
- **Status:** open (two items deferred by owner decision, 2026-11-18: timings on the development machine were too noisy to judge)
- **Target:** unassigned (the applied target cache is done in skills-compliance)
- **Context:** the rule is "apply only when a benchmark shows a gain of 10% or more in ns/op or allocations".
  - *Applied:* `logHandler` target cache. A handler keeps the last target it built (base handler with the trace attributes and every `With` step) keyed by trace id, span id and the truncated flag, and reuses it while the key matches; `with` and `bind` give each copy its own cache. `BenchmarkLogHandler`, `benchstat` over `-count=6`: allocs/op 7 -> 1 (0 steps), 10 -> 1 (1 step), 16 -> 1 (3 steps), 20 -> 14, 23 -> 14, 29 -> 14 (with `slog.Any`), geomean -76%; B/op geomean -82% (400 -> 64 B with 0 steps). ns/op varied +-137% to +-305% between runs on the loaded machine, so time is not claimed. After the slice-9 /simplify pass (the key is a comparable `targetKey` passed by value; a `cachedTarget` is allocated only on a miss), the hit path drops one more allocation: 0 allocs/op with 0, 1 or 3 steps and 13 with `slog.Any` (`go test -run '^$' -bench BenchmarkLogHandler -benchmem ./engine`). Guarded by `TestLogHandler_TargetCache` (in-place trace replacement, truncation toggling, concurrent traces under `-race`).
  - *Deferred, not measured:* single ServeMux routing in `answerUnmatched` (`server.go`), and fiber `accessInfo` copies after the suppression check. Note for the first: calling the handler `mux.Handler(r)` returns instead of `mux.ServeHTTP` would skip ServeMux setting `r.Pattern` and the path values (`r.PathValue`), so a single pass needs another design, for example letting `withRoute` mark the request matched and intercepting only the fallback's response.
- **Evidence:** `go test -run '^$' -bench BenchmarkLogHandler -benchmem -count=6 ./engine` before and after; `go run golang.org/x/perf/cmd/benchstat@latest old.txt new.txt`.
- **Suggested action:** measure the two deferred items on a quiet machine (or CI benchmark runner) with `benchstat`, and apply each only at a 10% or larger gain.

### Task 9.1: struct tag line over 120 columns
- **Status:** closed: accepted exception
- **Target:** skills-compliance (task 9.1)
- **Context:** after wrapping, the only non-test line over 120 columns is `engine/config.go` field `ShutdownHookTimeout` (122 columns). It is a field name plus one struct tag; a struct tag is a single string literal that `reflect.StructTag` parses with space separators only, so it cannot be broken across lines, and the field name and the `json`/`yaml`/`env` keys are public configuration API (AGENTS.md rule 7), so shortening them would be a breaking change for a style rule.
- **Evidence:** `for f in $(git ls-files '*.go' | grep -v _test); do awk -v f=$f 'length > 120 {print f": "FNR}' $f; done` prints only `engine/config.go: 61`.
- **Suggested action:** none; the line-length check treats struct-tag lines as exempt.

### Target cache: patterns where it cannot hit (from /simplify on slice 9)
- **Status:** open (unconfirmed: allocation cost not yet measured)
- **Target:** unassigned (with the deferred task 9.4 measurements)
- **Context:** `BenchmarkLogHandler` measures one handler logging many records with one trace, where the cache hits. Two common patterns may never hit: (a) `engine.LoggerFromContext(ctx).Info(...)` called once per line, because `LoggerFromContext` calls `bind` on every call (`engine/log.go` `LoggerFromContext`) and each bound copy starts with an empty cache, so each record pays a `targetCache` and a `cachedTarget` allocation on top of the old cost; (b) unbound handlers (the root logger, `slog.Default()` under `SetDefault`, srvr's access and event records) take the trace from the context, so concurrent requests with different trace ids replace each other's single-entry cache, adding a miss allocation and an atomic store to a shared cache line. (The hit path itself no longer allocates: `cachedTarget` takes its key by value and allocates only on a miss.)
- **Evidence:** code reading only (simplify review of slice 9); rule 2 applies: write benchmarks for (a) and (b) and compare allocs/op with and without the cache before calling either a regression.
- **Suggested action:** add `BenchmarkLogHandler` rows for (a) and (b); if either regresses, cache only on handlers with a bound trace or `With` steps, or keep the request logger (or its cache) on `RequestState` so loggers of one request share it.

### fibersrvr `Server.fields` test injection point (from /simplify on slice 9)
- **Status:** closed: kept deliberately
- **Target:** skills-compliance (task 9.3)
- **Context:** the altitude review suggested dropping `Server.fields` (used only by `TestConfigure_FieldCheckFails` to add a renamed field) and calling `checkFields(fiberFields())` directly in `configure`. Task 9.3 introduced the field on purpose to replace the mutable package variable with an injection point, so `configure`'s failure path is tested end to end through `Start`; `TestInternalFieldCheck` alone covers `checkFields` but not its wiring into `configure`.
- **Suggested action:** none.
