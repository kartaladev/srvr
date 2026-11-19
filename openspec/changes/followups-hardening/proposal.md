# Proposal

## Why

`triage-debug-buffer` (released as v0.1.0) left ten open follow-ups in its `notes.md`. The owner adopted nine of them on 2026-11-19 as one hardening change, applied before `scrty-integration`: three suspected defects in construction errors, unmatched-record limiting and otel configuration errors; four main specs that fail `openspec validate --specs --strict`; a test that fails under CPU load; an unused prblm v0.2.0 sentinel that may simplify gin's failure classification; and four performance items that were never measured. Closing them now keeps the next feature change (`scrty-integration`) from starting on a red spec check, a flaky suite and unproven defect claims.

## What Changes

- **Construction joins problem-option errors** (suspected, proven first): an invalid `WithProblemOptions` option together with another invalid field reports both in one error, as every other field already does. If the RED test passes, the claim is recorded as not confirmed and the row stays as a regression row.
- **Only the router's 404 and 405 count as unmatched** (suspected, proven first on every engine): with the opt-in unmatched-record limit, a 404 or 405 error that consumer middleware returns for a path no route matches is never limited, as the godoc already promises ("handler errors are never limited"). Only fiber runs consumer middleware for unmatched paths (`router.Use`); net/http and gin group middleware run only for matched routes, and their rows pin that.
- **Recovered panic errors are kept as `Cause`**: `otelsrvr.ConfigError` and `otelsetup.ConfigError` keep a recovered panic value that is an error in `Cause`, reachable with `errors.Is`/`errors.As`; the text is unchanged.
- **Main specs pass strict validation**: six requirement descriptions over 500 characters in `engine-support`, `error-responses`, `server-lifecycle` and `structured-logging` are shortened, with the detail moved into scenarios. Behaviour does not change; OpenSpec refuses a MODIFIED block that drops a scenario, so no requirement is split.
- **Deterministic shutdown-hook test**: the `TestShutdownHooks_Faulty` "blocks and ignores its context" row proves that `Start` returns while the hook is still blocked, with no wall-clock bound that CPU contention can break.
- **gin status-only aborts**: ginsrvr's failure classification is evaluated against `ginprblm.ErrStatusOnly` (prblm v0.2.0) and switched only if that makes it simpler or more robust. New gin-engine rows pin today's visible behaviour, which must not change.
- **Performance, measured before it is changed**: the per-request `crypto/rand` call for trace ids (on Linux as well as darwin), the three debug-buffer efficiency items, fiber's copies for records the unmatched limit suppresses, and the user-agent read under `OmitUserAgent`. Each is applied only if `benchstat` shows the design D8 gain. Any change of id source keeps the trace-context requirement that ids come from a cryptographically secure random source.
- **RELEASING.md** says that `internal/paritytest` needs `go mod tidy` in the commit that restores the `replace` lines, and that `main` CI is red between the module release commits.
- **Release**: the change ends with a release prepared by the plan. Tags are pushed only after the owner confirms the version.

## Defaults and Customization

- No default changes, and no new option. A consumer with zero configuration gets exactly v0.1.0's behaviour: 5xx responses carry no error text, stack or internal path, and logs carry no credentials or bodies. The only visible differences are on error paths: a construction error that names more fields, a `Cause` that is now set, and, only with the opt-in unmatched-record limit, consumer-reported 404/405 records that are no longer suppressed.
- The unmatched-record limit stays off by default and stays the only loosening of the "failed records are never suppressed" default. This change narrows what it can suppress.
- Trace and span ids stay cryptographically random. A faster source is accepted only if it is still a cryptographically secure generator seeded from `crypto/rand` (design D7).

## Capabilities

### New Capabilities
<!-- none -->

### Modified Capabilities
- `engine-support`: three requirement descriptions are shortened ("srvr re-exports the shared types", whose type list becomes a scenario; "Engines share one per-request state"; "Registered routes are listed with their configurer"), with the detail moved into scenarios. No behaviour change.
- `error-responses`: "Consumers extend error rendering" is shortened; the hook order moves into its scenario. No behaviour change.
- `server-lifecycle`: "Construction validates configuration" is shortened and gains rows that join a rejected problem option with other invalid fields.
- `structured-logging`: "Failed-request records are never suppressed" is shortened, and its late-error levels move into a scenario. "Unmatched failed records can be rate limited" counts only the router's 404/405 and gains rows for consumer middleware on every engine.
- `otel-metrics`: typed configuration errors keep a recovered panic error as `Cause`.
- `telemetry-setup`: typed configuration errors keep a recovered panic error as `Cause`.
- `gin-engine`: the middleware-order scenario gains status-only abort rows that guard the classification refactor.
- `trace-context`: the uniqueness scenario covers span ids and many concurrent clients, as a guard for any change of id source.

## Scope by Gall's Law

Each follow-up is its own working slice. The three defect slices come first, each a RED test and the smallest fix; then the test and spec hygiene; then the gin evaluation; then the performance measurements; then the release. Every slice ends with a releasable library. A suspected defect whose RED test passes ends its slice with a regression row and a notes entry, not a fix.

Deliberately left out: every other open carry-over (the `scrty-integration` entries stay for `scrty-integration`; the remaining `unassigned` entries of `core-server`, `gin-and-fiber-engines` and `otel-observability` stay unassigned unless consolidation adopts one), new options, and prblm or scrty upstream work.

## Impact

- Compatibility decisions:
  - Public API: no new or removed identifiers. Changed behaviour on error paths: `srvr.New` and the engines' `New` return one joined error that also names `ProblemOptions` when other fields are invalid; `otelsrvr.ConfigError.Cause` and `otelsetup.ConfigError.Cause` are set for recovered panic errors, as design D13 of `triage-debug-buffer` documented they were not; with `WithUnmatchedRecordLimit`, consumer-reported 404/405 records are written instead of suppressed.
  - Defaults: none changed. Log schema: no key added, renamed or removed; `srvr.log.schema` stays `1`. Module dependency sets: unchanged. ginsrvr already requires `ginprblm v0.2.0`, and the core module stays standard library plus prblm at runtime.
  - Version: the release slice suggests `v0.2.0` if any of the visible behaviour changes above lands, and `v0.1.1` if none does.
- Code: `engine` (config resolution, unmatched classification, debug buffer, access stage), the root module (`server.go`, `internal/tracectx`, `shutdown_test.go`), `ginsrvr` (`classify`), `fibersrvr` (unmatched origin, access copies), `otelsrvr` and `otelsetup` (recover sites), `internal/paritytest`, benchmarks, `docs/PERFORMANCE.md`, `CHANGELOG.md`, `RELEASING.md`, and main specs at archive.
- Builds on `triage-debug-buffer` (v0.1.0) and assumes, for consolidation to check:
  - `engine.ResolveConfig` validates settings in `newSettings` (joined by `Settings.validate`) and builds the prblm handler afterwards in `newProblems`.
  - `RequestState.unmatched` derives "unmatched" from no matched route, not aborted, a non-panic failure and status 404/405. fiber's router returns the exported `fiber.ErrNotFound`/`fiber.ErrMethodNotAllowed` values (v3.7.0).
  - `otelsrvr` and `otelsetup` flatten recovered panic values with `engine.ValueText`. `ConfigError.Unwrap` returns `[ErrConfig, Cause]`.
  - ginsrvr's `classify` clears a failure that has no `c.Errors`, a matched route and no panic. ginprblm v0.2.0 reports status-only aborts wrapped around `ErrStatusOnly`.
  - `tracectx.New` reads 24 bytes from `crypto/rand` per generated trace. Design D8's gain rule and the `bench`/`profile` targets exist, and the CI `bench` job runs on `ubuntu-latest`.
- Builds on `core-server`, `gin-and-fiber-engines`, `otel-observability` and `goroutine-leak-checks` only through those merged pieces. It is independent of `scrty-integration`, which is applied after it.
