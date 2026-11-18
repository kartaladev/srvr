# Design

## Context

See proposal.md - Why. State observed before planning (scratch copy of `52fdbfc` with `goleak.VerifyTestMain` added to every test package, `GOWORK=off go test -race ./...` per module, 4 runs of the passing modules):

- Pass with no leak: `srvr`, `srvr/internal/tracectx`, `ginsrvr`, `otelsrvr`, `otelsetup`. The harnesses already close each server's own HTTP client and stop servers, hooks and providers in `t.Cleanup` (`otel-observability` task 9.6); shutdown-hook tests release blocked hooks at cleanup.
- `srvr/engine`: `TestCheckRoutes/context ends` leaves its check blocked in `select {}` (`engine/route_test.go:62`); srvr abandons it as specified. A test fault.
- `fibersrvr` (38 goroutines) and `internal/paritytest` (31): fasthttp v1.78.0 `(*workerPool).Start.func2`, the idle-worker cleaner, sleeps `MaxIdleWorkerDuration` (10s by default) between checks of `stopCh`, so it outlives `Stop` by up to 10s; `updateServerDate.func1` is started once per process and never ends.
- `internal/paritytest`: the gin cyclic `c.Error` subtest leaves a net/http connection goroutine spinning in gin's `errors.As` (`exceptions_test.go:346`), as `gin-and-fiber-engines` documented.

No leak in srvr's own code showed at the binary level. srvr starts three goroutines of its own: the serve goroutine (`engine/lifecycle.go:312`), `runGuarded` for hooks and route checks (`engine/hooks.go:99`), and otelsetup's metrics server (`otelsetup/setup.go:159`). Almost every lifecycle test is parallel (`t.Parallel`), so per-test checks need new non-parallel tests.

goleak's `VerifyTestMain` and `Find` retry for about 430ms (20 retries, backoff capped at 100ms) and ignore the testing package's own goroutines, including tests paused in `t.Parallel`.

## Goals / Non-Goals

**Goals:**
- Every test binary of every module fails on an unexpected goroutine.
- srvr's own goroutines, and those of dependencies srvr drives, are proven to end per owner (server, setup) in non-parallel tests that attribute a leak to one lifecycle.
- Each exception is one named function with a written reason, proven needed and narrow.

**Non-Goals:**
- Changing srvr's runtime behaviour, unless a leak in srvr itself is proven (then the smallest fix inside srvr's lifecycle, with TDD).
- Making existing parallel tests sequential to run `VerifyNone` in them.
- Leak checks of file descriptors, timers or memory.

## Slices

1. **Core module under goleak**: `srvr`, `srvr/engine` and `srvr/internal/tracectx` each have a `TestMain` calling `goleak.VerifyTestMain`; the `engine` route-check test releases its check; a non-parallel lifecycle test proves net/http `Start`/`Shutdown` (and every stop path) leaves nothing, and that abandoned hooks and route checks are the only goroutines left until released. Rule 7, the layout line and `openspec/config.yaml` allow test-only dependencies. Satisfies test-quality: "A goroutine left running fails the module's tests" (core rows and boundary rows), "Lifecycle leaves no goroutine behind" (net/http rows), "Abandoned consumer code is the only goroutine left" (all rows). Touches the `Start`/`Shutdown` hot path only through tests.
2. **Gin and fiber under goleak**: `ginsrvr` and `fibersrvr` `TestMain`s; fibersrvr defines the two fasthttp ignores; lifecycle rows for both engines; the ignores are proven needed and not to hide a never-stopped fiber server. Satisfies the ginsrvr and fibersrvr rows of "A goroutine left running fails the module's tests" and "Lifecycle leaves no goroutine behind", and the fibersrvr rows of "Each ignore is needed and narrow".
3. **Otel modules under goleak**: `otelsrvr` and `otelsetup` `TestMain`s; lifecycle rows for an `otelsrvr` traced server and for `otelsetup` (Prometheus listener, OTLP over HTTP and gRPC, a failed `New`, a never-stopped setup); the existing non-parallel tests that start listeners or exporters check per test. Satisfies the otel rows of the first two scenarios.
4. **Parity module, guard and rules**: `internal/paritytest` `TestMain` with the fasthttp ignores; the gin cyclic test moves to `internal/paritytest/gincycle` with its own `TestMain` ignoring only its named handler; a guard parses every test package directory of the repository and fails when one lacks a `TestMain` calling `goleak.VerifyTestMain`; `AGENTS.md` testing rules and the change order. Satisfies the paritytest rows, "Every test package runs the leak check" (all rows), and the remaining rows of "Each ignore is needed and narrow".

5. **Parity cleaner ends instead of being ignored** (owner decision after slice 4): the parity harness's `fiberServer` registers, before the test's configurer, one that sets the fiber app's fasthttp `MaxIdleWorkerDuration` to `20ms`; the paritytest cleaner ignore and its "needed" row go; a parity lifecycle row proves the cleaner ends after `Shutdown`, and a "not hidden" row proves the default duration still leaves it, so the helper stays needed. fibersrvr is unchanged. Satisfies the "parity harness" rows of "Lifecycle leaves no goroutine behind" and the paritytest cleaner row of "Each ignore is needed and narrow" (not hidden).

Each slice ends with every module it touched green under goleak; no slice needs a later one.

## Decisions

### D1 Leak check at the binary level in every test package

A `leak_test.go` in each test package (the external test package where one exists, since internal and external tests share one binary) holds `TestMain(m) { goleak.VerifyTestMain(m, leakOptions...) }` and the module's `leakOptions`. `goleak.VerifyTestMain` runs after all tests, so it covers parallel tests too. Alternative: `VerifyNone` in every test - rejected, it is wrong in parallel tests (it sees the goroutines of tests running alongside).

goleak v1.3.0 (latest) goes in every module's `go.mod` as a test-only requirement. The core module's non-test packages still import only the standard library and prblm; `TestCoreHasNoOTel` (any `go.opentelemetry.io/` import, tests included) and `TestNoSDKDependency` (`otelsrvr` non-test packages, SDK and exporters only) do not reject goleak and stay unchanged.

**Failure modes:** a module already defining `TestMain` in a package - none does today (checked: no `func TestMain` in the repository); a binary fails only because goleak ran out of retries for a goroutine that ends slowly (OTel batch processors, gRPC) - mitigated by owners stopping synchronously in `t.Cleanup` and by the 5x foreground runs per module; a binary that fails tests skips the leak report (goleak only reports on a passing run) - acceptable, the failing test is fixed first; a new package added later without `TestMain` - caught by the guard (D5).

### D2 Per-test checks in non-parallel tests

A helper `checkGoroutines(t)` per module captures `goleak.IgnoreCurrent()` when called and registers, as the test's first `t.Cleanup`, `goleak.VerifyNone(t, leakOptions..., current)`. Cleanups run last-in first-out, so the check runs after the test's own cleanups that stop servers and close clients; a `defer` would run before them and report false leaks. Rows that expect a leak call `goleak.Find` instead and assert the reported stack names the expected function, then stop the owner. Only non-parallel tests use it: during a non-parallel top-level test, parallel tests are paused in `t.Parallel`, which goleak ignores.

**Failure modes:** a later edit makes such a test parallel - the check would see other tests' goroutines and fail noisily, not silently; the helper's godoc states it. A goroutine of an earlier non-parallel test still ending is excluded by `IgnoreCurrent`, so it does not fail the wrong test (the binary-level check still catches it).

### D3 Ignores name one function each, defined once per module

`goleak.IgnoreAnyFunction` with the full function name, because the top frame of the fasthttp goroutines is `time.Sleep`, which `IgnoreTopFunction` could only match by ignoring every sleeping goroutine. Each ignore sits in `leak_test.go` with a comment giving the reason and the dependency version it was observed with. A test per ignoring module (test-quality "Each ignore is needed and narrow") starts the goroutine, asserts `goleak.Find` without the option reports that function and with the options does not, and that a fiber server never shut down is still reported (its serve and worker goroutines are not ignored).

Where a test can end a dependency goroutine, it does so instead of ignoring it (owner decision, slice 5). fasthttp's idle-worker cleaner sleeps `MaxIdleWorkerDuration` (`10s` by default; `0` means the default) between checks of its stop channel; `Serve` reads the value at start and stops the pool before returning, and srvr's serve goroutine ends after that, so with a short duration the cleaner ends within one sleep after `Shutdown`. `internal/paritytest` therefore shortens it to `20ms` in its one fiber helper (`fiberServer`, through a first configurer `r.(*fiber.App).Server().MaxIdleWorkerDuration = 20ms`; it panics with a clear message if the router is not a `*fiber.App`, which srvr turns into a `Start` error) and does not ignore the cleaner. `fibersrvr` keeps the cleaner ignore: its `Example()` (`example_test.go`) starts a server with the default duration and must not show test-only plumbing. fasthttp's `updateServerDate` refresher is started once per process and never ends, so it stays ignored in both modules.

**Failure modes:** a fasthttp upgrade renames the closure - the "needed" row fails and names the function, so the ignore is updated rather than silently dead; the cleaner of a never-stopped server is hidden - the server's other goroutines are still reported (row "not hidden"); a parity fiber server that bypasses the helper keeps the 10s cleaner and fails the binary only when no later test runs long enough - every fiber server of the module is built by `fiberServerIdle` (the one `fibersrvr.New` call site): `fiberServer` wraps it with the short duration, and the "not hidden" cleaner row calls it directly with the default duration; a write to `MaxIdleWorkerDuration` racing with `Serve` - the configurer runs during `Register`/`Start`, before `Serve` reads it (-race clean).

### D4 The gin cyclic test runs in its own binary

The subtest "gin never completes c.Error with a cyclic chain" moves from `internal/paritytest/exceptions_test.go` to package `internal/paritytest/gincycle` (same module, test-only), with a named handler function and a `TestMain` ignoring only that function. The parity binary then needs no gin ignore, and the spinning goroutine no longer shares CPU with parity tests. Alternative: ignore the handler inside the parity binary - rejected, it would hide any other stuck parity handler with the same frame, and the CPU spin would stay. The engine-parity documented-difference row stays covered by the moved test.

**Failure modes:** the moved test needs the parity harness helpers - it gets its own small helpers (a ginsrvr server on `127.0.0.1:0`, a request with a 300ms deadline); the stuck handler keeps spinning until the binary exits - bounded by the binary, as before.

### D5 Guard: every test package runs the leak check

A test in `internal/paritytest` (the repository's test-only module) walks the repository from its root, skipping `openspec`, hidden directories and `testdata`, parses the `_test.go` files of each directory with `go/parser`, and fails naming every directory with test files whose `TestMain` does not call `goleak.VerifyTestMain`. The decision function takes parsed files, so the rejected rows run on synthetic sources. Alternative: `go list -json` per module - rejected, it needs every module's dependencies resolved and does not tell whether `TestMain` actually calls `VerifyTestMain`.

**Failure modes:** an unparsable test file - reported with the parse error; a new module directory - found by the walk; the guard run from another working directory - it locates the root from its own source directory (`runtime.Caller` or the module's known relative path `../..`).

### D6 Fix the test, not the check

Where a test leaves a goroutine, the test is fixed (release the blocked function at cleanup, close clients, stop providers); an ignore is added only for a dependency or deliberately stuck goroutine that a test cannot stop. A leak in srvr's own code is a defect: proven by the failing goleak output (rule 2), fixed with TDD when small and inside srvr's lifecycle, otherwise written to `notes.md` and the change pauses.

## Defaults

No consumer-facing default changes: no option, config field or behaviour is added. The project-level default is that every module's test binaries run the leak check; there is no switch to turn it off.

## Hot Paths

No production hot path changes unless a srvr leak is proven. The tests exercise the `Start`/`Shutdown` hot path of each engine (stop sequence, hooks, drain, failed start) and `otelsetup.New`/`Setup.Shutdown`; coverage is checked to be unchanged or higher per module with `go tool cover -func`.

## Risks / Trade-offs

- [goleak's ~430ms retry window is too short under load for a goroutine that ends asynchronously] → owners are stopped synchronously in cleanups; 5 foreground runs per module; a flaky failure is recorded in `notes.md` with its output rather than widened into a broad ignore.
- [The fasthttp cleaner ignore hides a cleaner whose server was never stopped] → the server's own goroutines are still reported (D3); only `fibersrvr` keeps that ignore, the parity binary ends the cleaner instead.
- [goleak appears in consumers' module graphs] → accepted by the owner; no package is compiled into consumers (proposal, Defaults and Customization).
- [Test-only `go.mod` requirement in the core contradicts the old wording of rule 7] → rule 7, the layout line and `openspec/config.yaml` are amended in slice 1, before the requirement is added.

## Migration Plan

None for consumers. Contributors: a new test package needs a `TestMain` calling `goleak.VerifyTestMain` (the guard enforces it), and a new goroutine started by srvr needs a test proving it ends.
