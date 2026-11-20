# Proposal

## Why

The shared pipeline package `github.com/kartaladev/srvr/engine` is public only because AGENTS.md and its godoc say separate modules cannot import core `internal/`. That reason is wrong: Go's internal rule is by import path, so the nested modules (ginsrvr, fibersrvr, otelsrvr, otelsetup, internal/paritytest) may import `github.com/kartaladev/srvr/internal/...` (evidence in `pluggable-error-renderer/notes.md`, "engine could be internal/engine"). Being public, `engine` invites consumers to depend on an unstable API. It has also grown to about 5,800 lines of production code in one package (`lifecycle.go` 899, `static.go` 806, `log.go` 797, `triage.go` 557), where every concern can reach every other's unexported state. The owner approved on 2026-11-20 moving it to `internal/engine` and then splitting it by responsibility, before `pluggable-error-renderer` adds its types there.

## What Changes

- **BREAKING (pre-v1)**: `github.com/kartaladev/srvr/engine` moves to `github.com/kartaladev/srvr/internal/engine`. A module outside `github.com/kartaladev/srvr/...` can no longer import it. Every type a consumer uses stays available through `srvr`'s aliases, with the same names and identity (`srvr.Config`, `srvr.ErrConfig`, ...). The `engine` package was documented as unstable and for srvr's engines only, so the break affects only unsupported use; CHANGELOG records it.
- The engine modules (ginsrvr, fibersrvr), otelsrvr, otelsetup and `internal/paritytest` import `internal/engine` (and later its sibling packages) instead of `engine`.
- `internal/engine` is split by responsibility into a few cohesive internal packages, leaf first, each step behaviour-preserving. Owner decision 2026-11-20: do not oversplit; aim for 5 to 7 packages, split only where a package has one clear responsibility and a small API, and merge thin ones into a neighbour. Target: `internal/errchain` (error-chain walking, guards against faulty values, redaction), `internal/config` (the configuration model, options, validation, sentinels and the declarative types it holds), `internal/static` (static file resolution), `internal/logging` (the slog handler, schema, truncation, debug buffer), `internal/lifecycle` (the state machine and runner), with `internal/engine` keeping `Core`, `ResolveConfig`, the per-request state and the request pipeline (access and triage records, prblm handler and hooks, observers, route checks). The consolidation group verifies the real dependency graph with gopls and applies the "merge if thin" criterion before finalising.
- A guard test pins the direction of dependencies between the internal packages (no package imports `internal/engine`; leaves import no srvr package), and no package outside the srvr module path can import them.
- AGENTS.md's layout paragraph, `openspec/config.yaml`'s public-shape note, the package godoc, `docs/PERFORMANCE.md`, the Makefile's coverage summary, `.golangci.yml`'s depguard comment and `internal/benchgate` follow the new paths.
- No behaviour changes: responses, log records, the log schema (`srvr.log.schema` stays `1`), defaults, options and every module's dependency set stay as in v0.3.0.

## Defaults and Customization

- Zero configuration: identical to v0.3.0 on every engine, byte for byte: no error text on 5xx, no stack traces, no internal paths in responses; no credentials, cookies, bodies or query strings in logs. The existing parity, golden-record and log-safety suites prove it unchanged after every slice.
- Customization: unchanged. Every option and config struct stays where consumers reach it (`srvr`, `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup`).
- Loosened safety: none. The change tightens encapsulation: the unstable pipeline API is no longer reachable from outside srvr.

## Capabilities

### New Capabilities
(none)

### Modified Capabilities
- `engine-support`: "The engine-support API is marked as such" becomes: the shared pieces live under srvr's `internal/` path, importable by srvr's own modules only, with the core dependency set unchanged. "srvr re-exports the shared types" keeps the same aliases, now declared in srvr's internal packages.
- `test-quality`: the leak-check package list follows the new package paths; a new requirement pins the internal packages' dependency direction; a new requirement compares `BenchmarkPipeline` before and after the refactor.

`structured-logging` (schema version, golden records) and the public option and error API are unchanged and need no delta; their existing tests are the refactor's safety net.

## Scope by Gall's Law

The simplest version that works end to end is slice 2: move the package to `internal/engine` with no other change, every module building and the whole suite green. That alone fixes the wrong AGENTS.md reason and hides the unstable API, and is releasable on its own. Each later slice extracts one package (at most five), leaf first, and ends green, so the work can stop after any slice and still ship. If an extraction would make the code worse (a cycle, a wide exported surface that was unexported coupling), the slice stops at the last working state and the concern stays in `internal/engine`, recorded in `notes.md`.

Left for later: `pluggable-error-renderer` types (they land in `internal/engine` beside the prblm handler and hooks when it is applied after this change, or in a problems package only if group 1 of that change finds one cohesive); any behavioural cleanup found while moving code (recorded in `notes.md`, never fixed silently); a public `engine`-like API for third-party engines (no demand).

## Impact

- Compatibility decisions:
  - Public API: **breaking** only for direct importers of `github.com/kartaladev/srvr/engine` (documented unstable). `srvr`, `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup` public APIs unchanged.
  - Defaults: unchanged. Log schema: unchanged (`srvr.log.schema` `1`). Module dependency sets: unchanged.
  - Version: suggested `v0.4.0` for all modules together, confirmed by the owner at release; `pluggable-error-renderer` also proposes `v0.4.0`, so the owner decides whether they share one release or ship as `v0.4.0` and `v0.5.0`.
- Code: `engine/*.go` (moved and split), every importer: `server.go`, `routing.go`, `options.go`, `types.go`, `static.go`, root tests, `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup`, `internal/paritytest`, `internal/benchgate`; `Makefile`, `.golangci.yml`, `docs/PERFORMANCE.md`, `AGENTS.md`, `openspec/config.yaml`, `CHANGELOG.md`, godoc.
- Change order: proposed `engine-internal-split` -> `pluggable-error-renderer` -> `scrty-integration`, for the owner to confirm (AGENTS.md's order is changed only by the owner).
- Builds on, for consolidation to check: everything released as v0.3.0, in particular `core-server` and `gin-and-fiber-engines` (the `engine` package and its aliases in `srvr`), `goroutine-leak-checks` (the leak-check guard in `internal/paritytest` lists package paths), `triage-debug-buffer` and `followups-hardening` (`triage.go`, `debugbuffer.go`, `unmatched.go`, `clientclosed.go`), `static-files` (`static.go`), and the `BenchmarkPipeline` benchmarks of each engine. It assumes `pluggable-error-renderer` is not applied yet.
