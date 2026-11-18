# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (task group 1, 2026-11-18)
- **Status:** closed: recorded
- **Target:** skills-compliance
- **Context:** 1.1: `git fetch`; `origin/main` and `main` are both at `5d3b678`, nothing merged or archived since planning. The branch `skills-compliance` starts from `b0302aa` (`triage-debug-buffer` consolidation, unmerged), as the proposal assumes. 1.2, decisions on open carry-overs addressed to this change or `unassigned`: *Engine modules duplicate the core's test helpers* (gin-and-fiber-engines) deferred: a public test-support package is new API and no audit finding asks for it. *otelsrvr's go.mod requires the OpenTelemetry SDK for its tests* (otel-observability) deferred: a separate test module changes the layout, beyond the audit; slice 4 only bumps vulnerable versions. *Public engine package stability before v1* (gin-and-fiber-engines) partly adopted by slice 5 (typed `ConfigError`, `RouteErrorf`, `Only`, `Core.Settings()` replaced, `RequestState` route fields hidden); the full surface review stays open. *triage-debug-buffer task 7.1* (named serve goroutine) adopted by D4 / task 3.4, status updated in `triage-debug-buffer/notes.md` and `tasks.md`. *engine-support requirement over 500 characters* (otel-observability) deferred: this delta does not modify that requirement. Every other open `unassigned` entry in the archived changes and `scrty-integration` was read and stays deferred unchanged, since no audit finding covers it. 1.3: every audit target was found again with gopls (workspace symbols, document symbols, references) at the same definitions: `Core.redact` and `Core.exceptionMessage` (`engine/triage.go`), `fingerprint(parts ...string)` with two callers (`errorAttrs`, `panicAttrs`) taking the raw method, `truncator.attr` and `mayNeedRewrite` (`engine/log.go`), `tracectx.parse` reading `flags[1]&1`, `runCheck`'s `errors.As` outside `runGuarded` (`engine/route.go`), `WithAccessLogConfig` assigning `c` unchanged (`options.go`), `knownMethods`/`methodAttr` (`otelsrvr/otelsrvr.go`), `exportErrorHandler.Handle` writing `engine.ValueText(err)` (`otelsetup/setup.go`), `Core.Settings()`, whose only production caller is `fibersrvr/server.go` (`MaxHeaderBytes`), `RequestState.Route`/`.Location` exported fields (`engine/trace.go`), the serve closure in `Lifecycle.serve`. Reference counts: `ConfigError(` 33, `RouteError(` 14, `ForEngine(` 10, `WithEngine(` 7. No artifact needed a change; `openspec validate skills-compliance --strict` passes.
- **Evidence:** `git log --oneline HEAD..origin/main` is empty; gopls queries above; `openspec validate skills-compliance --strict` → `Change 'skills-compliance' is valid`.
- **Suggested action:** none.

### Audit 2026-11-18: findings and where they are fixed
- **Status:** open: being fixed by the tasks listed per finding; locations confirmed with gopls in consolidation 1.3 (see the outcome entry above). The seven audit areas and their tasks: (1) errors and safety: 2.4, 2.5-2.7, 3.1, 3.3, 4.2, 4.4; (2) concurrency and context: 2.3, 3.2, 3.4, 4.5, 4.6; (3) style, naming and docs: 5.1-5.5, 6.1-6.5, 9.1; (4) design, types, modernize and data structures: 5.3, 9.2, 9.3; (5) testing, lint and benchmarks: 7.1, 7.2, 8.1-8.5; (6) security, observability and performance: 2.1, 2.2, 2.5-2.7, 4.3, 4.7, 9.4; (7) dependencies, layout and CI: 4.1, 7.3-7.5
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
- **Status:** open
- **Target:** skills-compliance (task 8.2, sleep and timing hygiene)
- **Context:** `TestStart_Stop/the_Start_context_deadline_passes` (`lifecycle_test.go`) gives `Start` a context with a `100ms` deadline created before `Start`, then blocks on `<-started` (line 90) waiting for the in-flight request to reach the handler. Under CPU load the deadline can pass before the request is served, the server stops, `inFlight` reports `-1`, and the test waits on `started` forever, until the package's 10-minute timeout, instead of failing. Found while repeating the core suite during slice 2; unrelated to that slice's code.
- **Evidence:** `go test -race -count=1 .` in a loop beside a parallel build: `panic: test timed out after 10m0s` / `running tests: TestStart_Stop/the_Start_context_deadline_passes (9m56s)`, goroutine `TestStart_Stop.func4` at `lifecycle_test.go:90` in `chan receive, 9 minutes`. Not reproduced on demand: it needs load, so no deterministic RED test exists yet.
- **Suggested action:** in task 8.2, start the deadline only after the request is in flight (or select on `started` together with the request's result and a bound), so a lost race fails fast with a message.

### Slice 2 /simplify: efficiency items deferred to the benchmarks
- **Status:** open
- **Target:** skills-compliance (tasks 8.4 and 9.4)
- **Context:** the slice 2 efficiency review (read-only, no measurement) found two costs on the logging hot path: with a limit set, any `slog.Any` value forces a record rebuild (`mayNeedRewrite`), and `capAny` formats every such value with `fmt` to measure it, after which the base handler formats a value that fits a second time. Suggested cheaper forms: skip formatting for small fixed-size kinds (`time.Time`, `netip.Addr`, scalars), or format into a counting writer that stops after `maxBytes+1`. A third item, a keyword pre-scan before `secretPair` in `RedactSecrets`, only touches failed requests; the cheap `?`/`&` pre-check for `queryValue` was applied. Design D2 already accepts the formatting cost pending a benchmark.
- **Evidence:** unverified: code reading; `BenchmarkLogHandler` (task 8.4, with and without `slog.Any`) decides.
- **Suggested action:** in task 9.4, apply a cheaper form only when the benchmark shows a gain of 10% or more, as for the other measured items.

### Slice 3: defects found beyond the audit and fixed in place
- **Status:** closed: fixed by skills-compliance (tasks 3.1 and 3.4)
- **Target:** skills-compliance
- **Context:** (1) after `runCheck` stopped walking the chain, `Start`'s returned error still unwrapped to the consumer's self-cyclic error, so a caller's `errors.Is(err, X)` for any `X` not in the chain looped forever; `guard(err)` now hides an unwalkable chain (task 3.1). (2) `http.Server.Shutdown` waits for `Serve` on a `WaitGroup` without regard to its context (Go 1.27.4 `net/http/server.go` `Shutdown` and `Close`, `listenerGroup.Wait()`), so a `WithListener` listener whose `Close` does not unblock `Accept` hung srvr's `Shutdown` forever on net/http and gin; `httpRunner.Drain` now returns at the drain deadline (task 3.4). fibersrvr was not checked for the same listener (no spec row); fiber's `ShutdownWithContext` is a separate path.
- **Evidence:** (1) `TestRouteCheck_FaultyErrors/Unwrap_returns_the_error_itself` with `go test -race -timeout 20s`: `panic: test timed out`, goroutine in `errors.is` called from `routing_test.go:785`. (2) `TestServeGoroutine/a_WithListener_listener_whose_Close_does_not_unblock_Accept`: `panic: test timed out after 2m0s`, goroutine `[sync.WaitGroup.Wait]` in `net/http.(*Server).Shutdown` called from `engine.(*httpRunner).Drain`.
- **Suggested action:** none for net/http and gin. A later change may add the stuck-listener row for fiber.
