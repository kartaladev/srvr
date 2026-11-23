# Design

## Context

See proposal.md - Why. Current code (main at `a833539`, v0.4.0):

- All three engines delegate their lifecycle to one shared `internal/engine.Lifecycle[C]` (`internal/engine/lifecycle.go`): `srvr.Server.Start` calls `lc.Start(ctx, prepare)` (`server.go:98`); `ginsrvr` and `fibersrvr` do the same with their own `prepare`. A change in `Lifecycle` reaches every engine at once.
- `Lifecycle.Start` (`lifecycle.go:138`) runs begin, prepare, route checks, listen, `serve` (spawns `serveOn`, logs `server.started`, closes `ready`), then blocks in a `select` on `ctx.Done()`, `r.stopping` or `r.done`, then runs `stop` with a drain context of `ShutdownTimeout` detached from `ctx` and returns `errors.Join(served, hookErr)`.
- States: `stateNew`, `stateStarting`, `stateRunning`, `stateStopping`, `stateClosed`; `l.stopped` closes on `stateClosed`; `Shutdown` interrupts a starting server through `stepCtx`.
- Sentinels `ErrServerStarted`, `ErrServerClosed` live in `internal/errchain` and are aliased in `types.go:104-107`.
- Callers of the blocking `Start`: tests in core (about 12 files), `ginsrvr` (8), `fibersrvr` (9), `otelsrvr` (5), `otelsetup` (5), `internal/paritytest`, `example_test.go`, `readme_example_test.go`, and the README snippets of core, `ginsrvr`, `fibersrvr` (`return srv.Start(ctx)`).
- doger v0.3.0 (the motivating consumer, not a dependency; latest release, triaged 2026-11-23, see notes.md "Triage with doger v0.3.0"): `RootScope.Start` waits for every `AsStarter` `Start` to return and has no start timeout of its own (T1, P3); it cancels the context it passed to `Start(ctx)` as soon as `RootScope.Start` returns (T2); it passes the `Shutdown` context's deadline to `Shutdown(ctx) error` (P1) and reports `HealthCheck(ctx) error` results by method shape (P2).
- Fiber's serve loop returns `nil` when the listener's `Accept` reports `net.ErrClosed` although nobody stopped the server; net/http and gin return the error (T3). On v0.4.0 that fiber stop is silent: `Start` returns `nil` and no `server.failed` is written.

## Goals / Non-Goals

**Goals:**
- One shared implementation in `internal/engine` so the three engines cannot drift.
- `Run` reproduces today's `Start` exactly, so existing behaviour specs keep holding under the new name.
- No goroutine srvr starts outlives the server's stop, including the new supervisor.

**Non-Goals:**
- Any import of doger or another container; a container adapter module.
- An HTTP health endpoint, or health states beyond serving/not serving.
- Changing request handling, limits, responses or the log schema.

## Slices

1. **Start returns once serving on every engine, with Run and Wait**: `Lifecycle` splits starting from waiting; a supervisor goroutine owns the stop on a serve failure; `Run` and `Wait` exist on all three engines; `ErrServerNotStarted` exists for `Wait`; every caller and README snippet that needs blocking moves to `Run`. A doger `AsStarter` registration starts and stops cleanly. Scenarios: server-lifecycle "Start returns while serving", "Start bounded by its context", "Run and stop by context", "Run fails to start", "Wait after Start", "Wait misuse", "Listener fails while serving", MODIFIED "Hook phases and order", "Hooks on every stop path", "Start twice", "Lifecycle records"; engine-support MODIFIED both scenarios except the `HealthCheck` rows; test-quality "Lifecycle leaves no goroutine behind" rows for `Start`, `Run`, `Wait` and serve failure. Hot paths: `Lifecycle.Start`, the supervisor, `Run`, `Wait`, `stop`. It must be one slice because the shared `Lifecycle` changes every engine's `Start` at once: a slice that changed it for net/http only would leave gin and fiber red.
2. **Health check on every engine and container conformance**: `HealthCheck(ctx)` on all engines; a conformance test drives every engine through a test-local container-shaped interface and checks the dependency lists. Scenarios: "Health across the lifecycle", "Health with a hostile context", "Container-shaped lifecycle", engine-support `HealthCheck` rows. Hot path: `HealthCheck`.
3. **Documentation and end-to-end verification**: README section on running under a lifecycle container (doger snippets: `Provide`, `InvokeAll` of route configurers, `AsStarter`), migration note and `CHANGELOG.md` **BREAKING** entry, godoc examples for `Start`+`Wait` and `Run`; parity harness runs the lifecycle rows on all engines; `make check`, `make cover`.
4. **Release v0.5.0**: after review and merge, owner confirmation, then tags for every module in `RELEASING.md` order.

## Decisions

### D1. Split `Lifecycle.Start` into a starting phase and a supervisor

`Lifecycle.Start(ctx, prepare)` keeps everything up to `serve` and returns `nil` right after `serve` closes `ready`. `serve` also starts `superviseRun(r)`, a named method (so goleak reports name it) that waits on `r.stopping` or `r.done`:
- `r.stopping` (Shutdown or Run began the stop): return; the stopper runs the sequence.
- `r.done` with a served error: log `server.failed`, record the error in `l.servedErr`, run `stop` with a drain context of `ShutdownTimeout` from `context.WithoutCancel(startCtx)`, return.
- `r.done` without an error and no stop requested (fiber, proven by T3): treated as a failure: the supervisor records `errchain`'s "serving ended without a stop" error wrapping `ErrServerClosed`, logs `server.failed` and stops, so the server never stays `stateRunning` while not serving. This mirrors today's code, which runs the stop whenever `r.done` closes (`lifecycle.go:189-200`), and adds the report today's fiber path lacks.

Alternatives: (a) no supervisor, `Wait` runs the stop on a serve failure: rejected, hooks and draining would depend on someone calling `Wait`, and health would stay wrong; (b) run the old blocking body in a goroutine from `Start`: rejected, two goroutines where one does, and `Start`'s context would keep stopping the server.

**Failure modes:** supervisor and `Shutdown` race on a serve failure: `stop` already guards with the state check (`stateRunning` only), the loser waits on `l.stopped`; Accept panics: `guardedListener` already turns them into an error, the supervisor sees `r.done`; a consumer listener whose `Close` never unblocks `Accept`: `joinServe`'s bounded grace still applies, the supervisor returns after `stop`; the supervisor leaking on a never-stopped server is reported by goleak with its name (test-quality row "Start and never Shutdown" now names serve and supervisor goroutines).

### D2. `Start`'s context bounds startup only

The context passes to route checks, `listen` and records during startup. After `Start` returned it is not watched. Evidence that this is required, not optional: doger cancels that context when `RootScope.Start` returns (T2), so a server tied to it stops right after the container started. Records written later by the supervisor use `context.WithoutCancel(startCtx)` so values (service attributes) survive. Override: none; a consumer who wants the context to stop the server calls `Run`.

**Failure modes:** a consumer relying on the old behaviour exits immediately: the migration note and README changes say so, and the release is a minor bump under v0.x signalling the break (D6); a `nil` context still fails with `ErrConfig`.

### D3. `Run` is `Start` plus the old waiting `select`

`Run(ctx)`: `err := Start(ctx)`; on error return it. Then `select` on `ctx.Done()` or `l.stopped`; on `ctx.Done()` run `stop` with a drain context of `ShutdownTimeout` from `context.WithoutCancel(ctx)`; finally return `Wait()`. Every existing scenario of the removed "Start serves until its context ends" holds for `Run` unchanged.

**Failure modes:** `Run` and `Shutdown` concurrently: one `stop` wins, `Run` waits on `l.stopped`; a serve failure inside `Run`: the supervisor stops, `l.stopped` closes, `Run` returns the served error through `Wait`; `Run` registered as a doger `AsRunnable`: blocks the container by design; the godoc warns.

### D4. `Wait` returns the result of the serving run only

`Wait()` returns `ErrServerNotStarted` in `stateNew`; otherwise it waits on `l.stopped` and returns `errors.Join(l.servedErr, l.hookErr)` when a run was served, `nil` when the server closed without serving (failed `Start`, `Shutdown` before `Start`), because `Start` already returned that failure. `Wait` during `stateStarting` blocks until the start fails (returns `nil`) or the served run stops (spec row "is starting in another goroutine whose route check then fails").

Alternatives: return the `Start` failure again from `Wait`: rejected, the caller would log it twice and `Run` would need to filter it.

**Failure modes:** many concurrent `Wait` callers: all read the same fields after `l.stopped` closes, written before the close (race-clean); `Wait` called from a shutdown hook: the hook runs before `l.stopped` closes, so it would deadlock. srvr cannot tell a hook's goroutine from another waiter without goroutine identity, so detection is rejected; the godoc forbids it, and the misuse stays documented-only (recorded in notes.md).

### D5. `HealthCheck` reads the state, never the context

`HealthCheck(ctx)`: `nil` context returns `ErrConfig`; otherwise, under the mutex: `stateNew`/`stateStarting` → `ErrServerNotStarted`; `stateRunning` → `nil`; `stateStopping`/`stateClosed` → `ErrServerClosed`, joined with `l.servedErr` when set. It never calls `ctx.Err()`, so a hostile context cannot break it, and never blocks beyond the mutex.

Alternatives: probe the listener with a dial: rejected, costs a connection per check and can hang; report `nil` while stopping: rejected, a container should see the server leaving.

**Failure modes:** the error text reaches whoever calls the container's health report (doger returns it verbatim, P2); doger's web UI shows only whether a service implements a health check, not the text (`doger/http/templates.go:126`), so no client exposure was found. The serve-failure text can name the listen address and listener type; it passes through the same redaction and length limit as lifecycle log text (requirement "Lifecycle error text is redacted and limited").

### D6. Sentinel and compatibility

`ErrServerNotStarted = errors.New("srvr: server not started")` in `internal/errchain`, aliased in `types.go` beside `ErrServerStarted`. Core dependencies unchanged; log schema unchanged (`server.failed` gains a trigger). Version v0.5.0 for every module: v0.x minor bump carries the breaking `Start` semantics.

### D7. Conformance without a container import

In `internal/paritytest` (it already imports all engines), a test declares `type containerService interface { Start(context.Context) error; Shutdown(context.Context) error; HealthCheck(context.Context) error }`, asserts every engine's server satisfies it at compile time, and drives each through Start, HealthCheck and Shutdown with `1s` contexts. The dependency check reuses the existing `go list` guards rather than adding a deny-list: `TestCoreDependencies` (`internal/paritytest/layout_test.go:163`, an allowlist) already keeps containers out of the core; it is extended with a container deny check for `ginsrvr` and `fibersrvr`, whose dependency sets are not allowlisted.

**Failure modes:** a container module arriving transitively through gin or fiber: the extended guard names it.

## Defaults

- No new option or config field. Blocking versus non-blocking is the method the consumer calls (`Run` or `Start`+`Wait`); neither loosens a safety default.
- `ShutdownTimeout` and `ShutdownHookTimeout` keep their defaults and apply to every stop path, including the supervisor's stop after a serve failure.

## Hot Paths

- `Lifecycle.Start`: nil context, context already done, prepare failure, route-check failure or interruption, bind failure, listener without address, interrupted after the last check, success.
- `superviseRun`: stopping first, served error, clean serve return.
- `Lifecycle.Run`: start failure, context done, stopped by `Shutdown`, stopped by serve failure.
- `Lifecycle.Wait`: never started, starting then failed, closed without serving, served run with and without errors.
- `Lifecycle.HealthCheck`: nil context and every state.
- `stop`/`stopRun`/`Shutdown`: unchanged, re-covered through the new callers.

## Risks / Trade-offs

- [Silent break for `if err := srv.Start(ctx)` in `main`] → **BREAKING** changelog entry with a snippet, README and examples moved to `Run`, minor version bump, godoc on `Start` stating it returns once serving.
- [scrty-integration plans written against the blocking `Start`] → its consolidation (task group 1) refreshes them; a `notes.md` entry addressed to `scrty-integration` records it.
- [One more goroutine per running server] → named, ends with the stop, pinned by goleak rows.
- [A container whose `Start` deadline passes while srvr's route check ignores its context] → doger waits for the starter regardless (P3); srvr's own route checks get the startup context and `Shutdown` interrupts them, a consumer check that ignores both is the consumer's fault and documented.
- [`Wait` from inside a shutdown hook deadlocks] → documented misuse in godoc.

## Migration Plan

1. Consumers replace blocking `srv.Start(ctx)` with `srv.Run(ctx)`; non-blocking users call `Start` then `Wait`, or register the server in their container.
2. Release v0.5.0 for every module in `RELEASING.md` order; consumers bump all srvr modules together.
3. Rollback: consumers stay on v0.4.0; srvr does not retract v0.5.0.
