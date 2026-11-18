# Tasks

## 1. Consolidation: refresh plan against merged changes

- [x] 1.1 Sync with the main branch and list every change merged or archived since this change was planned (expected: none since `5d3b678`; `triage-debug-buffer` consolidated in `b0302aa` on this branch); verify they are named in the completion note
- [x] 1.2 Decide every open `notes.md` carry-over from earlier changes addressed to `skills-compliance` or `unassigned` (adopt, defer or close), in particular "Engine modules duplicate the core's test helpers", "otelsrvr's go.mod requires the OpenTelemetry SDK for its tests", "Public engine package stability before v1" and `triage-debug-buffer` task 7.1 (adopted by D4); verify none is left undecided
- [x] 1.3 Refresh this change's proposal, specs, design and tasks against the current main specs and merged code, re-locating every audit finding with gopls (definitions and references, not line numbers); verify `openspec validate skills-compliance --strict` passes
- [x] 1.4 Record the consolidation outcome and every audit finding (with its evidence and which task fixes it, or why it is closed) at the top of `openspec/changes/skills-compliance/notes.md`; verify the file lists all seven audit areas
- [x] 1.5 Commit the consolidation as `docs(openspec): consolidate skills-compliance against main`, or skip when 1.1-1.4 changed nothing; verify with `git log -1 --stat`

## 2. Slice: log safety defaults in the core

- [x] 2.1 Sampled flag from the hex value (trace-context / Scenario: Sampled flag, both tables); verify `go test -race -run TestSampledFlag ./internal/tracectx/ ./...` passes in core
  - RED: `TestSampledFlag` with one case per row (`00`-`ff` accepted, `0B`, `0g`, `1` rejected); fails for `0a`, `0b`, `fe`, `ff` as the audit probe showed
  - GREEN: decode the flags with `hex.Decode` into one byte and test bit 0
  - REFACTOR: Inline the misleading comment into a correct one; /simplify
- [x] 2.2 Fingerprint uses the normalised method (triage-error-records / Scenario: Fingerprint stability, both tables); verify `go test -race -run TestFingerprint` passes in core and `otelsrvr`
  - RED: extend the fingerprint table with the client-chosen-method rows (`FOO1`/`FOO2`, `PURGE`/`BREW` on `Mount`, `QUERY`/`POST`, lowercase `get`); fails because every method hashes differently
  - GREEN: `engine.MethodLabel`; `fingerprint` hashes it
  - REFACTOR: Replace `otelsrvr`'s `knownMethods` lookup with `engine.MethodLabel` (Remove Duplication), keeping its attribute mapping; /simplify
- [x] 2.3 Skip paths copied at construction (structured-logging / Scenario: Caller mutates skip paths after New, all rows); verify `go test -race -run TestAccessLogSkipPathsCopied` passes
  - RED: table over `WithAccessLogConfig`, `WithAccessLogSkipPaths` and the concurrent-mutation row; fails for the `WithAccessLogConfig` rows
  - GREEN: `slices.Clone(c.SkipPaths)` inside `WithAccessLogConfig`
  - REFACTOR: none expected; /simplify
- [x] 2.4 `slog.Any` values capped (structured-logging / Scenario: Capping slog.Any values, within-cap, over-cap and faulty-value tables); verify `go test -race -run TestTruncate_AnyValues` passes
  - RED: one case per row, including the exactly-1024-byte struct, the group and `With` rows, `MaxAttrBytes: -1` and the panicking `String`; fails for every over-cap row
  - GREEN: `truncator.attr` formats other `KindAny` values with `ValueText`, keeps the value when it fits and cuts otherwise; `mayNeedRewrite` reports `KindAny`
  - REFACTOR: Extract Function `capAny` out of the kind switch; /simplify
- [x] 2.5 Default redaction of request error text (triage-error-records / Scenario: Default redaction, masked-by-default rows); verify `go test -race -run TestDefaultRedaction` passes
  - RED: one case per masked-by-default row, including the unmasked email, path, `?=x` and empty rows; fails because the text is written unredacted
  - GREEN: `engine.RedactSecrets` with two precompiled patterns; `Core.redact` uses it when no redactor is set
  - REFACTOR: Extract Variable for each pattern with a comment naming what it matches; /simplify
- [x] 2.6 Redaction overrides (triage-error-records / Scenario: Default redaction, overrides rows; existing Scenario: Redacting error text still green); verify `go test -race -run 'TestDefaultRedaction|TestRedact'` passes
  - RED: the four override rows, including both orders of `WithoutErrorRedaction` and `WithErrorRedactor`; fails because `WithoutErrorRedaction` does not exist (add it first as a no-op so RED is a wrong value, not a compile error)
  - GREEN: `WithoutErrorRedaction()` sets the identity redactor; godoc names it a loosening
  - REFACTOR: /simplify
- [x] 2.7 Default redaction of panics and lifecycle text (triage-error-records / Scenario: Default redaction of panics and lifecycle text, all rows); verify `go test -race -run TestDefaultRedaction_Lifecycle` passes
  - RED: rows for a panicking handler (message and stack), a failing `BeforeDrain` hook and a panicking configurer; fails if any path bypasses `redact`
  - GREEN: route any bypassing path through `exceptionMessage`
  - REFACTOR: /simplify
- [x] 2.8 Document the defaults: godoc on `WithErrorRedactor`, `WithoutErrorRedaction`, `LogConfig.MaxAttrBytes`; `LOGGING.md` sections on default redaction, `slog.Any` capping and the normalised fingerprint method; `CHANGELOG.md` "Unreleased" entries; verify `go test -run Example` and `go doc ./ WithoutErrorRedaction` show the text
- [x] 2.9 Verify the slice end to end (an httptest request whose handler returns a `*url.Error` with `?api_key=` yields a masked `exception.message`), confirm `RedactSecrets`, `capAny`, `MethodLabel` and `parse` branches in `go tool cover -func`, run gopls diagnostics on changed files, `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in core and `otelsrvr`, run /simplify on the slice and commit `fix(core): redact secrets, cap any-valued attributes and bound fingerprints by default`

## 3. Slice: lifecycle robustness

- [x] 3.1 Route-check errors read without an unguarded walk (engine-support / Scenario: Route check outcomes, new rows: self-cyclic, panicking `Unwrap`, panicking `As`, typed nil); verify `go test -race -run TestRouteCheck_FaultyErrors` passes in core, `ginsrvr` and `fibersrvr`
  - RED: the four rows across their engines with a 1s bound; fails by hanging past the bound (self-cyclic) or by a panic escaping `Start`
  - GREEN: `runCheck` uses a direct type assertion on `*panicError`
  - REFACTOR: /simplify
- [x] 3.2 `Shutdown` interrupts a starting `Start` (server-lifecycle / Scenario: Shutdown during the configure phase, both tables); verify `go test -race -count=10 -run TestShutdown_WhileStarting` passes in core, `ginsrvr` and `fibersrvr`
  - RED: rows per engine with a check waiting on its context, the check ignoring its context for `2s`, `Shutdown` before `Start` and two concurrent `Shutdown`s; fails because `Start` blocks until its own context ends
  - GREEN: `context.WithCancelCause` kept under the lifecycle mutex; `Shutdown` in `stateStarting` cancels with `ErrServerClosed`; `Start` maps the cause
  - REFACTOR: Extract Function for the cause mapping; /simplify
- [x] 3.3 `runtime.Goexit` in a check or hook is contained (server-lifecycle / Scenario: Goexit in consumer code, all rows); verify `go test -race -run TestGoexit` passes
  - RED: the four rows with a 1s bound; fails by hanging for the unbounded `AfterDrain` row and the check row
  - GREEN: `runGuarded` sends a `goexitError` when `fn` neither returned nor panicked
  - REFACTOR: /simplify
- [x] 3.4 Serve goroutine named and joined (server-lifecycle / Scenario: Serve goroutine after stop, all rows; adopts `triage-debug-buffer` task 7.1); verify `go test -race -run 'TestServeGoroutine|Goroutines|Leak'` passes in core, `ginsrvr`, `fibersrvr` and `internal/paritytest`
  - RED: change the "never `Shutdown`" leak rows of every engine to match `engine.(*Lifecycle[...]).serveOn` and add the three serve rows, including a listener whose `Close` does not unblock `Accept`; fails against the anonymous `serve.func1` and the missing join
  - GREEN: `go l.serveOn(r, ln)`; the stop owner waits on `r.served` for at most 1s
  - REFACTOR: /simplify; tick `triage-debug-buffer` task 7.1 and close its two notes entries (this change's and the archived goroutine-leak-checks entry) as `closed: fixed by skills-compliance`
- [x] 3.5 Verify the slice end to end, confirm the starting-cancel, Goexit and serve-wait branches in `go tool cover -func`, run gopls diagnostics, `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in core, `ginsrvr`, `fibersrvr` and `internal/paritytest`, run /simplify on the slice and commit `fix(engine): let Shutdown stop a starting server and contain faulty route checks`

## 4. Slice: OpenTelemetry modules and dependencies

- [x] 4.1 Bump `golang.org/x/net` to v0.62.0+ in `otelsetup` and `ginsrvr` and `golang.org/x/crypto` past GO-2026-5932 in `ginsrvr`, `fibersrvr` and `internal/paritytest`; verify `GOWORK=off govulncheck ./...` reports no vulnerability in each module and `go mod tidy -diff` is empty
- [x] 4.2 Nil `otelsrvr` option fails `New` (otel-metrics / Scenario: Nil option, all rows); verify `go test -race -run TestNew_NilOption` passes in `otelsrvr`
  - RED: the four rows; fails with a nil-function panic
  - GREEN: collect a `ConfigError` naming `Option` and its index, as the core does
  - REFACTOR: /simplify
- [x] 4.3 Zero `otelsrvr.Observer` is a no-op (otel-metrics / Scenario: Zero observer, all rows); verify `go test -race -run TestZeroObserver` passes in `otelsrvr`
  - RED: rows on net/http, gin and fiber asserting the status and zero `srvr.observer.fault` records; fails with one `end_panic` fault
  - GREEN: `Begin` returns `ctx` and a nil end function when the observer has no instruments; godoc says to use `New`
  - REFACTOR: /simplify
- [x] 4.4 Nil `otelsetup` option fails `New` (telemetry-setup / Scenario: Nil option, all rows); verify `go test -race -run TestNew_NilOption` passes in `otelsetup`
  - RED: the three rows including that no listener is bound; fails because the nil option is skipped
  - GREEN: reject it with `ErrConfig` naming `Option` and its index
  - REFACTOR: /simplify
- [x] 4.5 OTLP headers copied (telemetry-setup / Scenario: Caller mutates headers after New, both rows); verify `go test -race -run TestOTLPHeadersCopied` passes in `otelsetup`
  - RED: both mutation rows against the fake collector under `-race`; fails with a race or with the mutated header
  - GREEN: `maps.Clone` of the headers when the configuration is resolved
  - REFACTOR: /simplify
- [x] 4.6 Metrics listener closed and joined at the deadline (telemetry-setup / Scenario: In-flight scrape at the deadline, all rows); verify `go test -race -run TestShutdown_InFlightScrape` passes in `otelsetup` under goleak
  - RED: a scrape whose client stalls reading, with a `100ms` deadline, plus the finished and no-scrape rows; fails because the connection goroutine outlives `Shutdown`
  - GREEN: `Close()` after a failed `Shutdown`, and wait for the `Serve` goroutine's done channel
  - REFACTOR: /simplify
- [x] 4.7 `otel.export.failed` text redacted (telemetry-setup / Scenario: Export error with a secret, all rows); verify `go test -race -run TestExportError_Redacted` passes in `otelsetup`
  - RED: the three rows; fails because the token is written
  - GREEN: `engine.RedactSecrets(engine.ValueText(err))`
  - REFACTOR: /simplify
- [x] 4.8 Verify the slice end to end, confirm the nil-option, zero-observer, header-copy and close-on-deadline branches in `go tool cover -func`, run gopls diagnostics, `go vet ./...`, `golangci-lint run ./...`, `go test -race ./...` and `govulncheck ./...` with `GOWORK=off` in `otelsrvr`, `otelsetup`, `ginsrvr`, `fibersrvr` and `internal/paritytest`, run /simplify on the slice and commit `fix(otel): reject nil options, copy headers, close metrics listener and patch x/net`

## 5. Slice: engine-support and engine API

- [x] 5.1 Typed `ConfigError` (engine-support / Scenario: Configuration errors name their field, all rows; Scenario: srvr names are the engine declarations still green); verify `go test -race -run 'TestConfigError|TestSentinels'` passes
  - RED: the five rows with `errors.As` into `*srvr.ConfigError`; fails because no such type exists (add the alias first so RED is `false`, not a compile error)
  - GREEN: `type ConfigError struct{ Field, Value, Reason string }` with `Error()` keeping today's text and `Unwrap()` returning `ErrConfig`; `NewConfigError`; gopls rename of every `ConfigError(` call; `srvr.ConfigError` alias
  - REFACTOR: /simplify
- [x] 5.2 Pure renames with gopls: `engine.RouteError` to `RouteErrorf`, `engine.ForEngine` to `Only` (parameter `engineName`), `ginsrvr.WithEngine` to `WithGinEngine` (engine-support / Scenario: Engine-specific option placement; gin-engine / Scenario: Engine hook, renamed rows); verify `go build ./...` and `go test -race ./...` pass in core, `ginsrvr` and `fibersrvr`, and `grep -rn 'WithEngine(\|ForEngine(\|RouteError(' --include=*.go` finds nothing
- [x] 5.3 Narrow `Core` and `RequestState` accessors: replace `Core.Settings()` with `Core.MaxHeaderBytesSet()`, unexport `RequestState.Route`/`Location` behind `Route()`, `SetRoute`, `Location()`, `SetLocation`; verify with gopls find-references that no caller remains, and `go test -race ./...` passes in core, `ginsrvr` and `fibersrvr`
- [x] 5.4 `srvr.RouteCheck`, `srvr.Routes`, `srvr.Route` aliases; `WithRouteCheck` takes `srvr.RouteCheck`; the route-check `Example` imports only `srvr`; verify `go test -run Example` passes and `go doc ./ RouteCheck` shows the alias
- [x] 5.5 Record every rename and the `Settings()` removal in `CHANGELOG.md` with its replacement, and update `AGENTS.md` and `openspec/config.yaml` where they name `ginsrvr.WithEngine`; verify `grep -rn 'ginsrvr.WithEngine' --exclude-dir=archive .` finds only the CHANGELOG entry
- [x] 5.6 Verify the slice end to end, run gopls diagnostics, `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in core, `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup` and `internal/paritytest`, run /simplify on the slice and commit `refactor(engine)!: type ConfigError and rename engine-support constructors and the gin hook`

## 6. Slice: documentation

- [x] 6.1 Godoc reasons for every default (AGENTS.md rule 7): `Config` fields, `LogConfig.Level` and `MaxAttrBytes`, the matching options and `WithShutdownHookTimeout`; per-name docs for the sentinels, `ProtocolHTTPProtobuf`/`ProtocolGRPC`, `Route`, `AccessInfo`, `Settings`, `LogSettings` and `Hook` fields; rewrite `GuardChain`'s doc around its purpose; fix the "see Config for the reasons" constant comment; verify `go doc -all` on each package shows a reason next to every stated default
- [x] 6.2 `LICENSE` (MIT, copyright the kartaladev authors) in the root, `ginsrvr`, `fibersrvr`, `otelsrvr` and `otelsetup`; verify each released module directory has the file and `go list -m -json` builds
- [x] 6.3 Root `README.md` (install, minimal example, engine matrix, module list, links to `LOGGING.md`, `CHANGELOG.md`, `RELEASING.md`) and a short `README.md` for `ginsrvr`, `fibersrvr` and `otelsrvr`; verify every code block in them compiles by mirroring it in an `Example` test
- [x] 6.4 `RELEASING.md`: tag order (core first, then require its version and drop each module's `replace`), the release guard, and the govulncheck gate; `CHANGELOG.md` in Keep a Changelog format with an "Unreleased" section; verify both files exist and `CHANGELOG.md` lists every entry of slices 2-5
- [x] 6.5 `LOGGING.md`: the user agent is logged by default and why, and the recommendation to set `IgnoreIncoming` on internet-facing servers; Examples handle errors instead of discarding them (`otelsrvr`, `otelsetup` examples); verify `go test -run Example` passes in `otelsrvr` and `otelsetup`
- [x] 6.6 Verify the slice, run `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in every touched module, run /simplify on the slice and commit `docs: give every default its reason and add README, LICENSE, CHANGELOG and RELEASING`

## 7. Slice: tooling and CI

- [ ] 7.1 Enable `thelper`, `tparallel`, `paralleltest`, `usetesting`, `sloglint`, `forcetypeassert`, `modernize`, `intrange` and `depguard` in `.golangci.yml` (test-quality / Scenario: Lint findings fail the run); verify each row with a scratch file that the linter reports it, and remove the scratch files
- [ ] 7.2 Fix every finding the new linters report, module by module (helpers get `t.Helper()`, subtests get `t.Parallel()` or a reasoned `//nolint`, checked type assertions, `t.Context()`, `slog.DiscardHandler`, `slices.Backward`, `maps.Copy`); delete `//nolint` directives for linters that stay disabled; verify `GOWORK=off golangci-lint run ./...` reports 0 issues in every module
- [ ] 7.3 CI: `govulncheck`, `go mod tidy -diff`, `go mod verify`, coverage with `-coverpkg=github.com/kartaladev/srvr/...`, Go `1.27`/`stable` matrix with `fail-fast: false`, lint for `internal/paritytest`, `cache-dependency-path` per module, `actions/checkout@v6` and `actions/setup-go@v6`; a `release` job on tags failing when a released module's `go.mod` has a `replace`; verify with `actionlint` (or `act -n` when available) and by running each step's command locally
- [ ] 7.4 Root Makefile `MODULES` list with `vet`, `lint`, `test`, `cover`, `tidy-check` and `vulncheck` looping over modules with `GOWORK=off`; module Makefiles delegate to it; CI calls the targets; verify `make vet lint test tidy-check vulncheck` passes from the root
- [ ] 7.5 `.github/dependabot.yml` with `gomod` entries for every module and a `github-actions` entry; `git rm --cached go.work.sum` and `.gitignore` it; AGENTS.md's coverage command uses `-coverpkg`; verify `git ls-files go.work.sum` is empty and the Makefile `cover` target prints engine above 95%
- [ ] 7.6 Verify the slice, run gopls diagnostics, run /simplify on the slice and commit `ci: run govulncheck, tidy, coverage and test linters for every module`

## 8. Slice: tests

- [ ] 8.1 Fuzz tests (test-quality / Scenario: Fuzzed properties, all rows): strengthen `FuzzTraceparent` (round trip, sampled bit, seeds for upper-case hex, all-zero ids, version `ff`, over-long header); add `FuzzJoinPattern`, `FuzzCutString`, `FuzzLogLine`; verify each runs its seeds in `go test` and `go test -fuzz <name> -fuzztime 30s` finds nothing (any failure becomes a RED test and a fix, reported with its input)
- [ ] 8.2 Replace sleep-based synchronisation with signals or `testing/synctest` (`logging_test.go`, `otelsetup/setup_test.go`, `otelsetup/otlp_test.go`, `observer_test.go`, `otelsrvr/tracing_test.go`), take `eventually`'s deadline from `t.Deadline()`, and stop binding the fixed `127.0.0.1:9464` in `otelsetup/scrape_test.go`; verify `go test -race -count=5 -shuffle=on ./...` passes in every touched module
- [ ] 8.3 Named subtests for every table loop without `t.Run` (`TestFuncName`, `TestTriage_StackCap`, `TestMatchKey`, `TestGroupPath`, `triage_test.go`); verify `go test -run '<Test>/<case>'` selects a single case for each
- [ ] 8.4 Benchmarks: move `BenchmarkCorrelation` to `engine/log_bench_test.go` with a fixed case order; add `BenchmarkLogHandler` (0, 1, 3 `With` steps; with and without `slog.Any`), `BenchmarkRedactSecrets`, and one request-pipeline benchmark per engine (`httptest` for net/http and gin, `app.Test` for fiber), all with `b.Loop` and `ReportAllocs`; record the numbers in `notes.md`; verify `go test -run x -bench . -benchmem` runs in each module
- [ ] 8.5 Cover the hot-path blocks the audit found uncovered (`runGuarded` same-instant cancel, `BeginObservers` early return, `markSeen` non-comparable error, `FuncPointer`/`FuncLocation` nil and non-function, fiber `configure`/`addMounted`/`walk` error propagation, `fiberVersion` unknown, `fiberHeader.Keys`); verify `go tool cover -func` on the `-coverpkg` profile shows them covered
- [ ] 8.6 Verify the slice, run gopls diagnostics, `go vet ./...`, `golangci-lint run ./...` and `go test -race ./...` with `GOWORK=off` in every touched module, run /simplify on the slice and commit `test: fuzz hostile inputs, drop sleeps and add pipeline benchmarks`

## 9. Slice: style, modernize and measured performance

- [ ] 9.1 Break every non-test line over 120 columns at argument boundaries, split `server.go`'s nested stage expression into named stages, and extract `walkMount` from `fibersrvr`'s `walk`; verify `awk 'length > 120' $(git ls-files '*.go' | grep -v _test)` prints nothing and tests stay green
- [ ] 9.2 Parameter object for `otelsetup`'s `exporterOptions`, named booleans in `engine/observer.go`'s compound condition, `runGuarded` returning `(abandoned bool, err error)` without its `nolint`; verify tests stay green
- [ ] 9.3 Modernize: `maps.Copy` (`server.go`), `slices.Backward` (`engine/observer.go`), `strings.Lines`/`strings.CutLast` in `topFrame`, `slices.Collect(maps.Keys(...))` (`fibersrvr`); constant arrays or functions instead of the mutable package slices and maps (`anyMethods`, `fiberFields`, `renderingNotes`, `knownMethods`); presized maps; `var _` checks for `responseRecorder`'s optional interfaces; verify `modernize -test ./...` reports nothing and tests stay green
- [ ] 9.4 Measured performance: for `logHandler.target` caching (group-free handlers), single ServeMux routing in `answerUnmatched`, and fiber `accessInfo` copies after the suppression check, apply each only when slice 8's benchmark shows a gain of at least 10% in `ns/op` or allocations, keeping a benchmark comparison (`benchstat`) in `notes.md`; record the ones not applied with their numbers; verify the benchmarks and `go test -race ./...` pass
- [ ] 9.5 Re-read `notes.md`: every audit finding is fixed with its task or closed with a reason, and every open item has a target or is `unassigned`; verify by listing them in the completion note
- [ ] 9.6 Verify the change end to end on all three engines, run gopls diagnostics, `go vet ./...`, `golangci-lint run ./...`, `go test -race ./...` and `govulncheck ./...` with `GOWORK=off` in every module, run /simplify on the slice and commit `refactor: tidy style and modernize idioms across modules`

## Workflow follow-up

- Archive `skills-compliance` after review; verify the archived result and the synced main specs.
- Continue `triage-debug-buffer` at slice 2.
