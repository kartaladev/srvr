# Proposal

## Why

A srvr server cannot be run by a lifecycle container such as `github.com/kartaladev/doger` v0.3.0 (a fork of samber/do; triage proofs in notes.md): its `Start(ctx) error` has the shape of a container starter but serves until stopped, and doger waits for every starter to return, so `RootScope.Start` never returns and nothing after it runs. srvr also has no health check for a container to report. Consumers would each have to write a goroutine-and-`Ready()` wrapper; srvr is pre-1.0, so the clean fix — a non-blocking `Start` — is affordable now, before `scrty-integration` builds shutdown hooks on top of the lifecycle.

## What Changes

- **BREAKING** `Start(ctx) error` on every engine (`srvr.Server`, `ginsrvr.Server`, `fibersrvr.Server`) returns once the server is serving: it configures routes, runs route checks, binds, closes `Ready()` and returns `nil`, or returns the startup error as today. `ctx` bounds startup only; cancelling it after `Start` returned does not stop the server.
- New `Run(ctx) error` on every engine: `Start(ctx)`, then serve until `ctx` is done, `Shutdown` is called or serving fails, then stop gracefully. It is today's `Start` contract under a new name (same results, hooks, logs), the one-call form for a plain `main`.
- New `Wait() error` on every engine: blocks until a started server has stopped and returns how serving ended (a serve failure joined with shutdown-hook errors, or `nil`).
- New `HealthCheck(ctx) error` on every engine: `nil` while the server is serving; an error otherwise (not started, starting, stopping, stopped, or the serve failure).
- New sentinel `srvr.ErrServerNotStarted` for `Wait` and `HealthCheck` on a server that was never started.
- A serve failure after `Start` returned (for example a `WithListener` listener whose `Accept` fails) still stops the server in order, logs `server.failed`, and is reported by `Wait` and `HealthCheck`; it is never dropped. This includes fiber's serve loop ending cleanly without a stop, which v0.4.0 treats as a silent normal stop (proven, notes.md T3).
- A conformance test pins that every engine's server has the method shapes containers detect (`Start(context.Context) error`, `Shutdown(context.Context) error`, `HealthCheck(context.Context) error`) and that `Start` returns, without importing any container.
- README, module READMEs, godoc examples and `CHANGELOG.md` move from `Start` to `Run` for `main`, with a migration note; a README section shows registering a server and its route configurers in a container (doger as the example, in prose and snippets only).
- Release: this change ships as **v0.5.0** of every module (core, `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup`), in its last slice.

## Defaults and Customization

- Zero configuration: unchanged request behaviour, limits and responses. Nothing new is exposed to clients: `HealthCheck` is a Go method, not an HTTP endpoint, so no status, error text or internal path reaches a client; its error text goes only to the caller. No new log records except `server.failed` for a serve failure after `Start` returned, which already exists as an event and carries no credentials or bodies.
- `Run` keeps today's behaviour exactly: the stop on context end drains within `ShutdownTimeout` and runs shutdown hooks as now.
- No new option or config field: whether to block (`Run`) or not (`Start` + `Wait`) is chosen by which method the consumer calls. No safety default is loosened.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `server-lifecycle`: `Start` returns once serving instead of blocking; new `Run`, `Wait`, `HealthCheck` and `ErrServerNotStarted`; the stop paths, single-use rule and lifecycle logs name `Run`'s context where they named `Start`'s; serve failures after `Start` are reported.
- `engine-support`: every engine implements the new lifecycle methods with the core's states and errors, and runs shutdown hooks on the `Run` context path.
- `test-quality`: goroutine-leak rows cover `Start` returning while serving, `Run` with a cancelled context, and a never-stopped started server.

## Scope by Gall's Law

Simplest working version: the net/http engine's `Start` returns once serving, with `Wait` and `Run`, so a doger `AsStarter` registration of `*srvr.Server` starts and stops cleanly end to end. Then `HealthCheck`, then gin and fiber parity, then docs and release.

Left out deliberately:
- Any srvr module importing doger or another container (`dogersrvr`), and a route-configurer "kind" — the consumer wires `InvokeAll` and `Register` themselves.
- A `RegisterAll` helper; a readiness HTTP endpoint (`/healthz`); detailed health (degraded states, metrics).
- Changing `scrty-integration` artifacts: its consolidation (task group 1) adapts them to the new `Start`.

## Impact

- **Compatibility decisions:** breaking public API (`Start` semantics on three engines); new public API (`Run`, `Wait`, `HealthCheck`, `ErrServerNotStarted`); no default changes; no dependency-set change (core stays standard library plus prblm); log schema unchanged (`server.failed` gains a new trigger, no key change, `srvr.log.schema` stays).
- Code: `internal/engine` lifecycle (one shared implementation), `server.go`, `ginsrvr`, `fibersrvr`, `internal/paritytest`; tests across modules that call `Start` and expect it to block move to `Run`; `otelsetup`/`otelsrvr` tests and examples.
- Docs: README, `ginsrvr`/`fibersrvr` READMEs, readme snippet tests, `CHANGELOG.md`, `RELEASING.md` steps reused for v0.5.0.
- Builds on: `core-server` (lifecycle states, `Ready`, shutdown), `gin-and-fiber-engines` (shared lifecycle), `goroutine-leak-checks` (goleak guard), `followups-hardening` (stuck-listener and serve-goroutine bounds), `engine-internal-split` (lifecycle lives in `internal/engine`). Assumes all engines still share `internal/engine.Lifecycle` and that v0.4.0 is the latest release.
- Change order: goes after `pluggable-error-renderer` and before `scrty-integration` (owner decision 2026-11-23); AGENTS.md and `openspec/config.yaml` record it.
