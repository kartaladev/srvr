# Proposal

## Why

srvr starts goroutines of its own (the serve goroutine of every engine, guarded consumer code such as shutdown hooks and route checks, otelsetup's metrics listener) and drives dependencies that start more (fasthttp, the OpenTelemetry SDK, net/http transports). No test today fails when one of them outlives its owner, so a leak in `Start`/`Shutdown`, a hook path or an exporter would ship unnoticed. A probe run before planning (every test package wrapped in `goleak.VerifyTestMain`) found four leaking test binaries: `engine` (a route-check test leaves a goroutine blocked in `select {}`), `fibersrvr` and `internal/paritytest` (fasthttp's process-wide date refresher and its idle-worker cleaner, which sleeps up to 10s after `Stop`), and `internal/paritytest` again (gin's cyclic `c.Error` handler, stuck on purpose). Goroutine-leak detection is done now, before `scrty-integration` and `triage-debug-buffer` add more goroutine-owning code.

## What Changes

- Every test package of every module runs under `go.uber.org/goleak`: a `TestMain` calling `goleak.VerifyTestMain` in core (`srvr`, `srvr/engine`, `srvr/internal/tracectx`), `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup` and `internal/paritytest`. A test binary that ends with an unexpected goroutine fails.
- Non-parallel lifecycle tests (new ones per engine and per otel module, plus the existing non-parallel tests that start servers, listeners or exporters) check with `goleak.VerifyNone` that every goroutine they caused has ended once the owner stopped, so a leak is attributed to one test.
- Known, deliberate long-lived goroutines get one narrowly scoped ignore each, defined once per module and documented with its reason: fasthttp's `updateServerDate` refresher (fibersrvr, paritytest) and `(*workerPool).Start` cleaner (fibersrvr only: the parity harness shortens fasthttp's `MaxIdleWorkerDuration` so the cleaner ends after `Shutdown`, while fibersrvr's `Example` runs a server with the 10s default), and gin's stuck cyclic-`c.Error` handler, which moves into its own test package (`internal/paritytest/gincycle`) so no other parity test runs under that ignore. A test proves each ignore is still needed and does not hide a server that was never stopped.
- Test fixes instead of ignores where the test is at fault: the `engine` route-check test releases its blocked check at cleanup.
- Any leak found in srvr's own code is a defect, proven with the failing goleak output and fixed with TDD in this change when the fix is small and inside srvr's lifecycle; otherwise it is recorded in `notes.md` and the change pauses.
- Project rules: `AGENTS.md` rule 7, its "Repository layout" core line and `openspec/config.yaml` context state that the core depends only on the standard library and prblm **at runtime**, and that test-only dependencies such as goleak are allowed. `AGENTS.md`'s testing rules say every module's tests run under goleak and that a new goroutine started by srvr needs a test proving it ends. The change order in both files gains `goroutine-leak-checks` before `scrty-integration`.

## Defaults and Customization

Consumers see no behaviour change: no public API, default, response or log record changes, and nothing new is compiled into a consumer's binary. Responses still carry no error text on 5xx, no stack traces and no internal paths; logs still carry no credentials or bodies. There is nothing to configure: the leak checks run only inside srvr's own test binaries. No opt-in loosens a safety default.

Compatibility decision (module dependency set): every module's `go.mod` gains `go.uber.org/goleak v1.3.0` as a test-only requirement. No non-test package imports it (the existing guards `TestCoreHasNoOTel` and `TestNoSDKDependency` keep passing, and neither rejects goleak), but under module graph pruning the requirement, with goleak's own `stretchr/testify` requirement, appears in a consumer's module graph and `go.sum` - the same kind of effect `otelsrvr`'s test-only SDK requirement has. The rule wording changes from "depends only on the standard library and prblm" to "depends at runtime only on the standard library and prblm; test-only dependencies such as goleak are allowed", as the owner decided.

## Capabilities

### New Capabilities
- `test-quality`: guarantees about srvr's own test suites - every module's tests fail on leaked goroutines, goroutines srvr starts are proven to end with their owner, and every deliberate long-lived goroutine is listed with a reason.

### Modified Capabilities
(none: srvr's runtime behaviour does not change; abandoned consumer code is already specified in `server-lifecycle` (hooks) and `engine-support` (route checks), and this change only adds tests proving srvr leaves nothing else behind)

## Scope by Gall's Law

Simplest version that works end to end: the core module (net/http engine, `engine`, `tracectx`) under goleak, with its one test leak fixed and a lifecycle test proving `Start`/`Shutdown` leave no goroutine. Later slices add the gin and fiber engines (with the fasthttp ignores), the two otel modules (exporters, metrics listener), and the parity module (gin cyclic test isolated, a guard that every test package of every module runs the check).

Deliberately left out: goroutine checks inside the released library code (srvr ships no leak detector to consumers), CI changes (the existing `go test -race ./...` jobs run the checks), checks for leaked file descriptors or listeners other than through their goroutines, and changes to how srvr abandons consumer code that ignores its context (owner decisions of `core-server`).

## Impact

- Test code only in every module (`*_test.go`), one new test package `internal/paritytest/gincycle`, `go.mod`/`go.sum` of every module (goleak test-only), `AGENTS.md`, `openspec/config.yaml`.
- Builds on `core-server` (lifecycle, hooks run through a guarded goroutine and abandoned at their budget), `gin-and-fiber-engines` (shared `engine` lifecycle, the documented gin cyclic `c.Error` difference and its parity test) and `otel-observability` (otelsetup's metrics listener and exporters shut down by `Setup.Shutdown`; per-server HTTP clients from task 9.6 that close idle connections at cleanup). Assumptions for consolidation: those test harnesses still close clients and stop servers in `t.Cleanup`, `Setup.Shutdown` still stops the metrics server and both providers, and fiber still runs on fasthttp v1.78.x.
- If a leak in srvr itself is found, the fix touches the package that owns the goroutine (`engine` lifecycle or hooks, `otelsetup`).
