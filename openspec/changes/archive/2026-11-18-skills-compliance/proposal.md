# Proposal

## Why

A seven-part audit of every module against the Go skills the project mandates (AGENTS.md rule 5, `samber/cc-skills-golang`) on 2026-11-18 found defects that break the project's own rules: credentials in error text reach the logs by default; `slog.Any` values escape the `MaxAttrBytes` cap; the trace's sampled flag is misread for hex digits `a`-`f`; caller-owned slices and maps are aliased after construction; nil options panic or are silently skipped in the OpenTelemetry modules; a client-chosen method makes `error.fingerprint` unbounded; and `otelsetup` reaches five known `golang.org/x/net` vulnerabilities. It also found gaps in documentation (defaults without reasons, no README or LICENSE), tooling (no govulncheck, tidy or coverage in CI, test linters off), tests (no fuzzing of hostile paths and log values, sleep-based synchronisation, one benchmark) and naming. The owner chose to fix all of it before `triage-debug-buffer` continues, because that change builds on the attribute cap and the logging handler.

## What Changes

- **Log safety defaults**
  - A default error-text redactor masks URL query values and the values of secret-looking `key=value` / `key: value` pairs (`token`, `key`, `secret`, `password`, `passwd`, `authorization`, `cookie`, `session`, `credential`) in `exception.message` and `exception.stacktrace` of request and lifecycle records, and in `otelsetup`'s `otel.export.failed`. `WithErrorRedactor(fn)` still replaces it; `WithoutErrorRedaction()` turns it off explicitly. **BREAKING** (default log content changes).
  - A `slog.Any` value (map, slice, struct, `[]byte`, `fmt.Stringer`) whose formatted text exceeds `MaxAttrBytes` is replaced by that text cut like a string, setting `srvr.attr.truncated`; values within the cap render unchanged. **BREAKING** (oversized values become strings in the log).
  - `error.fingerprint` hashes the HTTP method normalised like `otelsrvr` does (known methods, else `_OTHER`), so a client cannot create unbounded error groups.
- **Correctness**
  - `traceparent`'s sampled flag is read from the hex value of the flags byte.
  - Caller-owned slices and maps are copied at construction: `AccessLogConfig.SkipPaths` through `WithAccessLogConfig`, `otelsetup`'s `OTLP.Headers`.
  - `otelsrvr.New(nil)` and `otelsetup.New(ctx, nil)` fail with their module's `ErrConfig` naming the option index; a zero `otelsrvr.Observer` is a documented no-op instead of a panicking observer.
  - Likely defects, each fixed only if a RED test reproduces it: a route check returning a self-cyclic or panicking error hangs or crashes `Start`; `Shutdown` cannot interrupt a `Start` still in its configure phase; a hook calling `runtime.Goexit` hangs `Start`/`Shutdown`; `otelsetup`'s metrics listener is not closed when its `Shutdown` deadline passes and its `Serve` goroutine is not joined; the core's serve goroutine is not joined after the stop sequence.
- **Dependencies:** `golang.org/x/net` to v0.62.0+ in `otelsetup` and `ginsrvr`, `golang.org/x/crypto` to the GO-2026-5932 fix in `ginsrvr`, `fibersrvr` and `internal/paritytest`.
- **Public API** (pre-v1, each a compatibility decision):
  - **BREAKING** `ginsrvr.WithEngine` becomes `ginsrvr.WithGinEngine` (owner decision); `fibersrvr.WithFiberConfig` keeps its name.
  - **BREAKING** `engine.RouteError` becomes `engine.RouteErrorf`; `engine.ConfigError(...)` becomes the exported type `*engine.ConfigError{Field, Value, Reason}` (re-exported as `srvr.ConfigError`, matching `errors.Is(err, srvr.ErrConfig)` and `errors.As`) built with `engine.NewConfigError`; `engine.ForEngine` becomes `engine.Only`.
  - **BREAKING** `engine.Core.Settings()` is replaced by narrow read-only accessors; `engine.RequestState.Route` and `.Location` become read through methods and set through `SetRoute`.
  - Added: `srvr.RouteCheck`, `srvr.Routes`, `srvr.Route` aliases, so route checks need no `engine` import; `srvr.WithoutErrorRedaction`.
- **Documentation:** every default in godoc states its reason (AGENTS.md rule 7); root and per-module `README.md`, `LICENSE` (MIT) in every released module, `CHANGELOG.md`, `RELEASING.md`; per-name docs for sentinels and constants; `LOGGING.md` notes on redaction, the user agent and trusting incoming `traceparent`.
- **Tooling:** CI runs `govulncheck`, `go mod tidy -diff`, `go mod verify`, coverage with `-coverpkg`, a Go `1.27`/`stable` matrix, lint for `internal/paritytest`, per-module cache keys, current action majors, and a release guard failing a tagged build whose released module still has a `replace`; Dependabot; `go.work.sum` untracked; a root Makefile looping over modules; `.golangci.yml` enables the test and logging linters (`thelper`, `tparallel`, `paralleltest`, `usetesting`, `sloglint`, `forcetypeassert`, `modernize`, `intrange`, `depguard` for the core's dependency rule); AGENTS.md's coverage command uses `-coverpkg`.
- **Tests:** fuzz tests for path joining, `cutString` and log-line integrity, a stronger `FuzzTraceparent`; sleeps replaced by signals; named subtests everywhere; `t.Helper` and `t.Context`; no fixed port; benchmarks for the logging handler and each engine's pipeline.
- **Style:** lines broken at 120 columns, nested `walk` split, five-parameter function reduced, named booleans, modernize hits, constant tables instead of mutable package slices, compile-time checks for `responseRecorder`'s optional interfaces, presized maps; performance items (`logHandler.target` replay, double ServeMux routing, fiber's eager `accessInfo` copies) only when a benchmark shows the gain.

## Defaults and Customization

- Zero configuration after this change: error text in logs is redacted by the default redactor, every attribute value is capped at 1024 bytes, nothing new reaches clients (5xx problems still carry only status, title, instance and `trace_id`, never error text, stacks or paths), and no credentials or bodies are logged.
- Opt-ins: `WithErrorRedactor(fn)` replaces the default redactor; `MaxAttrBytes` raises the cap.
- **Loosening a safety default:** `WithoutErrorRedaction()` writes error text unredacted; `LogConfig.MaxAttrBytes` negative disables the cap (unchanged). Both are named explicitly in godoc and `LOGGING.md`.

## Capabilities

### New Capabilities
- none

### Modified Capabilities
- `triage-error-records`: the redaction requirement gains the default redactor and the explicit opt-out; the fingerprint requirement normalises the method.
- `structured-logging`: every attribute value, including `slog.Any`, is capped; configuration slices are copied.
- `trace-context`: the sampled flag is read from the flags' hex value.
- `server-lifecycle`: `Shutdown` interrupts a starting `Start`; a hook or check ending its goroutine with `runtime.Goexit` is contained; construction copies caller-owned slices.
- `engine-support`: engine-specific option names, the typed `ConfigError`, `RouteErrorf`, `engine.Only`, the narrowed `Core` and `RequestState` accessors, route-check errors read without an unguarded chain walk, and the re-exported route-check types.
- `gin-engine`: the hook option is `ginsrvr.WithGinEngine`.
- `otel-metrics`: a nil option fails `New`; a zero `Observer` is a no-op.
- `telemetry-setup`: a nil option fails `New`; `OTLP.Headers` is copied; the metrics listener is closed and joined on a passed deadline; `otel.export.failed` text is redacted by default.
- `test-quality`: hostile inputs are fuzzed; test linters are enforced.

## Scope by Gall's Law

The simplest working version is slice 1: the log-safety defects in the core (redactor, attribute cap, fingerprint, sampled flag, copied `SkipPaths`), each releasable on its own. Every later slice keeps the library working and releasable: lifecycle robustness, the OpenTelemetry modules and dependency bumps, the public API renames, documentation, tooling, tests, and style last (style touches the most lines and is safest once the behaviour is fixed and tested).

Deliberately left out (recorded in `notes.md`): rate limiting or sampling of unmatched-request failed records, an option to omit the user agent, renaming `srvr.LogWriteErrors`, removing `RouteRegistrar` (the project context names it as public shape), splitting test files per source file (decided in commit `0470d34`), and tagging a release (owner keeps release work manual).

## Impact

- Code: every module (core, `engine`, `internal/tracectx`, `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup`, `internal/paritytest`), `.golangci.yml`, `.github/`, Makefiles, `AGENTS.md`, `LOGGING.md`, new `README.md`/`LICENSE`/`CHANGELOG.md`/`RELEASING.md` files.
- Public API: the breaking renames listed above (pre-v1; recorded in `CHANGELOG.md`); additive aliases and `WithoutErrorRedaction`.
- Defaults: error text redacted by default; oversized `slog.Any` values capped and rendered as strings.
- Log schema: no key added, renamed or removed, and `srvr.log.schema` stays `1`. `exception.message` and `exception.stacktrace` keep their meaning, the error text after the redactor, which already existed as an opt-in; only the default redactor changes, recorded in `LOGGING.md` and `CHANGELOG.md`. Oversized `slog.Any` values are consumer attributes, not schema keys.
- Dependency sets: only version bumps of `golang.org/x/net` and `golang.org/x/crypto`; the core module still depends at runtime only on the standard library and prblm.
- Builds on `core-server`, `gin-and-fiber-engines`, `otel-observability` and `goroutine-leak-checks` as archived on `main` at `5d3b678`, and on `triage-debug-buffer`'s consolidation commit `b0302aa`. Assumes the audit's line references still hold; consolidation re-checks them with gopls.
- `triage-debug-buffer` slice 2 starts after this change and relies on the capped `slog.Any` values and the redactor running on buffered records' error text.
