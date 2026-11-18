# Design

## Context

See proposal.md - Why. The audit's findings and their evidence (failing probe tests, govulncheck output) are recorded in `notes.md`. The code facts this design relies on, checked with gopls on `b0302aa`:

- Error text for records srvr writes itself passes through `Core.redact` (`engine/triage.go`), which returns the text unchanged when `Settings.Redactor` is nil, then is cut by `exceptionMessage`. `otelsetup.exportErrorHandler.Handle` writes `exception.message` with `engine.ValueText(err)` and no redactor.
- `engine.logHandler` cuts attributes with `truncator.attr` (`engine/log.go`), which handles strings, groups, srvr's own text and errors; any other `KindAny` value passes through unchanged.
- `fingerprint(method, route, ...)` (`engine/triage.go`) hashes the raw request method. `otelsrvr` already maps methods to the semantic-convention set or `_OTHER` (`otelsrvr/otelsrvr.go`, `knownMethods`).
- `tracectx.parse` reads `Sampled` as `flags[1]&1` on the ASCII byte (`internal/tracectx/tracectx.go:93`); a probe test failed for `0a`, `0b`, `fe` and `ff`.
- `runCheck` (`engine/route.go`) calls `errors.As` on the check's error outside `runGuarded`. The lifecycle (`engine/lifecycle.go`) runs configurers, checks and `listen` under `Start`'s context; `Shutdown` in `stateStarting` only changes the state. `runGuarded` sends nothing when `fn` calls `runtime.Goexit`. The serve goroutine is an anonymous closure in `serve` and is not received from after the stop.
- `Core.Settings()` has one production caller, `fibersrvr/server.go:75`, reading `MaxHeaderBytes`. `RequestState.Route` is written by `routing.go`, `ginsrvr` and `fibersrvr` and read in `engine`.
- `engine.ConfigError(field, value, reason) error` (33 references), `engine.RouteError(format, args...)` (14) and `engine.ForEngine(engine, name, value)` (10) are the engine-support constructors renamed here.

## Goals / Non-Goals

**Goals:**
- Every audit finding is either fixed with a RED test first, or closed in `notes.md` with the reason it was not (its RED test passed, or the owner kept the current behaviour).
- Each slice leaves every module green under `go vet`, `golangci-lint` and `go test -race` with `GOWORK=off`.

**Non-Goals:**
- New features beyond the audit (rate limiting unmatched records, a user-agent opt-out, sampling): recorded in `notes.md`, `unassigned`.
- Tagging or publishing a release; removing the `replace` directives.
- Performance changes without a benchmark showing the gain.

## Slices

1. **Log safety in the core.** Default redactor and `WithoutErrorRedaction`; capped `slog.Any` values; method-normalised fingerprint; sampled flag; copied `SkipPaths`.
   - Scenarios: triage-error-records "Fingerprint stability", "Default redaction", "Default redaction of panics and lifecycle text"; structured-logging "Capping slog.Any values", "Caller mutates skip paths after New"; trace-context "Sampled flag".
   - Hot paths: redaction and error-text building of every failed and lifecycle record; `truncator.attr` on every record; fingerprint; `traceparent` parsing.
2. **Lifecycle robustness.** Route-check errors read without an unguarded chain walk; `Shutdown` interrupts a starting `Start`; `runtime.Goexit` contained; the serve goroutine named `serveOn` and joined with a bounded wait.
   - Scenarios: engine-support "Route check outcomes" (new rows); server-lifecycle "Shutdown during the configure phase", "Goexit in consumer code", "Serve goroutine after stop".
   - Hot paths: `Start`, `Shutdown`, `runGuarded`, `runCheck`.
3. **OpenTelemetry modules and dependencies.** Nil options in `otelsrvr` and `otelsetup`; zero `Observer`; copied OTLP headers; metrics listener closed and joined at the deadline; `otel.export.failed` redacted; `golang.org/x/net` and `golang.org/x/crypto` bumps.
   - Scenarios: otel-metrics "Nil option", "Zero observer"; telemetry-setup "Nil option", "Caller mutates headers after New", "In-flight scrape at the deadline", "Export error with a secret".
   - Hot paths: `otelsrvr.Observer.Begin` and its end function; `setup.Shutdown`.
4. **Engine-support and engine API.** `*ConfigError` type and `NewConfigError`, `RouteErrorf`, `engine.Only`, `ginsrvr.WithGinEngine`, narrowed `Core` accessor, `RequestState` route and location methods, `srvr` aliases for `ConfigError`, `RouteCheck`, `Routes`, `Route`.
   - Scenarios: engine-support "Engine-specific option placement" (renamed rows), "srvr names are the engine declarations", "Configuration errors name their field"; gin-engine "Engine hook".
   - Hot paths: construction (option application and validation), the access stage reading the route.
5. **Documentation.** Default reasons in godoc (`Config`, `LogConfig`, options), per-name docs, `README.md` (root and each released module), `LICENSE` (MIT) in the root and each released module, `CHANGELOG.md`, `RELEASING.md`, `LOGGING.md` updates; Examples handle their errors.
   - Scenarios: none (no behaviour change); verified by `go doc` review and `go test -run Example`.
6. **Tooling and CI.** `.golangci.yml` linters, CI jobs (govulncheck, tidy, verify, coverage with `-coverpkg`, Go matrix, paritytest lint, cache keys, action majors, release guard), Dependabot, `go.work.sum` untracked, root Makefile over modules, AGENTS.md coverage command. Fixes every finding the new linters report.
   - Scenarios: test-quality "Lint findings fail the run".
7. **Tests.** Fuzz tests, sleeps replaced by signals, named subtests, `t.Helper`, `t.Context`, no fixed port, `eventually` deadline from `t.Deadline()`, benchmarks for the logging handler and each engine pipeline (`b.Loop`, `ReportAllocs`, fixed case order).
   - Scenarios: test-quality "Fuzzed properties".
   - Hot paths: none new; the benchmarks measure the existing ones.
8. **Style, modernize and measured performance.** Long lines, `walk` split, parameter object for `exporterOptions`, named booleans, modernize hits, constant tables, compile-time interface checks, presized maps; then, only where slice 7's benchmarks show a gain of 10% or more in `ns/op` or allocations: cached `logHandler.target` for group-free handlers, single ServeMux routing in `answerUnmatched`, fiber `accessInfo` copies after the suppression check.
   - Scenarios: none new; every existing scenario keeps passing.

Slice 1 works alone: it changes only the core and keeps every API. Each later slice adds to a working system; slice 4's renames come after the behaviour fixes so their RED tests are written against stable names once, and slice 8 comes last because it touches the most lines.

## Decisions

### D1. Default redactor in `engine`, applied when no redactor is set
`engine.RedactSecrets(text string) string` masks URL query values and secret-looking pairs with two precompiled `regexp` patterns from the standard library. Configuration resolution (`newSettings`, after every option applied) sets `Settings.Redactor` to it when no option set one, so `Core.redact` always calls the configured redactor. A colon separator needs a space or a quote after it, so stack frames (`cookie.go:42`) and `host:port` stay readable (pinned by `TestRedactSecrets`). `srvr.WithoutErrorRedaction()` sets `Settings.Redactor` to the identity function, so options keep applying in argument order with the later one winning. `otelsetup` calls `engine.RedactSecrets` on its export error text.

- **Default:** on. **Override:** `WithErrorRedactor(fn)` replaces it; `WithoutErrorRedaction()` disables it, an explicitly named loosening.
- **Alternative considered:** a list of URL parsers per token. Rejected: error text is free-form, so a pattern over the text is the only general approach; consumers with stricter needs install their own redactor.
- **Failure modes:**
  - *False positives* such as `key: value` in harmless text are masked. This is accepted: masking too much only loses triage detail, never leaks. Rows in "Default redaction" pin what stays unmasked (`alice@example.com`, a path, `?=x`).
  - *Catastrophic regex cost:* Go's `regexp` is linear-time (RE2), and the text is already bounded at 8 KiB.
  - *Redactor panics:* not possible for the default; a consumer's still gives `[redaction failed]`.

### D2. Capping `slog.Any` by formatted size
`truncator.attr` formats every other `KindAny` value (not srvr's own text, not an error) with the guarded `ValueText`; numbers, bools, times and durations are other `slog` kinds and are never formatted. If the text fits `MaxAttrBytes`, the original value is kept so handlers render it as before; otherwise the cut text replaces it and `srvr.attr.truncated` is set. `mayNeedRewrite` reports `KindAny` values as needing the check.

- **Default:** on, at 1024 bytes. **Override:** `MaxAttrBytes`; negative disables it.
- **Alternative considered:** walking maps and slices to cut strings in place. Rejected: unbounded types (structs, `[]byte`) still escape, and the walk costs as much as formatting.
- **Failure modes:**
  - *Formatting cost* on every record with an `Any` value: measured by slice 7's handler benchmark. A record with no `Any` value never formats.
  - *`fmt` text differs from JSON rendering in size:* the cap is approximate for values near the limit. The spec's within-cap rows use values far from it, and the exact-limit row uses formatted text.
  - *Panicking `String`:* `ValueText` gives `<panic formatting value>`.

### D3. One method table for fingerprints and metrics
`engine.MethodLabel(method string) string` returns the method if it is in the semantic-convention set (case-sensitive), else `_OTHER`. `fingerprint` hashes `MethodLabel(method)`, and `otelsrvr.methodAttr` uses the same table, so the two never drift. `http.request.method` in the access record keeps the raw method (bounded by net/http's token check and the attribute cap).

- **Failure modes:** a consumer whose clients use custom methods on purpose (WebDAV) sees one fingerprint per route for all of them. Accepted: the route still separates them, and `error.type` still differs per error.

### D4. Lifecycle: cancel the starting context, contain Goexit, join `serveOn`
- `begin` derives `context.WithCancelCause(ctx)` and keeps the cancel function under the lifecycle mutex. `Shutdown` in `stateStarting` calls it with `ErrServerClosed`, and `Start` maps that cause to `ErrServerClosed`. A check that ignores its context keeps the existing abandonment rule at the `Shutdown` deadline.
- `runGuarded` records whether `fn` returned normally; when its deferred function finds no panic and no result, it sends a `goexitError`, reported like a panic.
- `serve` starts `go l.serveOn(r, ln)`. After the stop sequence, the owner waits on `r.served` for at most 1s. This adopts `triage-debug-buffer` task 7.1 (the named goroutine and the leak rows matching `serveOn`); that change's task is ticked when this slice lands.
- `runCheck` replaces `errors.As` with a direct type assertion on the `*panicError` that `runGuarded` itself produces, so a consumer error's chain is never walked unguarded.
- **Failure modes:**
  - *A cancelled configurer leaves partial routes:* `Start` fails before binding, so they are never served.
  - *`Shutdown` racing `Start` reaching `stateRunning`:* both transitions happen under the mutex, so it is either the starting branch (cancel) or the running branch (drain).
  - *A consumer listener whose `Close` never unblocks `Accept`:* the bounded wait abandons `serveOn` after 1s.

### D5. Engine-support renames are pre-v1 breaks
`engine` is documented as unstable before v1, and the owner chose to fix the names now:
- `ConfigError` becomes a type: `type ConfigError struct{ Field, Value, Reason string }` with `Error()` producing today's text exactly and `Unwrap() error` returning `ErrConfig`. `NewConfigError(field string, value any, reason string) error` reads `value` through `ValueText`.
- `RouteErrorf` and `Only` are pure renames done with gopls rename.
- `Core.Settings()` is replaced by `Core.MaxHeaderBytesSet() int`, the only value an engine reads.
- `RequestState.Route` and `.Location` become unexported fields with `Route()`, `SetRoute(string)`, `Location()` and `SetLocation(Location)`.
- `ginsrvr.WithEngine` becomes `WithGinEngine`, with no deprecated alias: the library is unreleased.

- **Failure modes:** a missed reference fails to compile, which gopls rename prevents; `CHANGELOG.md` lists every rename for consumers.

### D6. Copies at construction
Configuration resolution (`newSettings`, after every option applied) clones `Access.SkipPaths` (`slices.Clone`), so no option has to remember to, and `otelsetup` clones `OTLP.Headers` (`maps.Clone`) when it resolves its configuration, matching `WithAccessLogSkipPaths` and `otelsrvr.WithConfig`.

### D7. Linters and CI
`.golangci.yml` adds `thelper`, `tparallel`, `paralleltest`, `usetesting`, `sloglint`, `forcetypeassert`, `modernize`, `intrange` and `depguard`, with `depguard` allowing only the standard library and `github.com/kartaladev/prblm` in core non-test files. CI adds `govulncheck` per module, `go mod tidy -diff`, `go mod verify`, a coverage step using `-coverpkg=github.com/kartaladev/srvr/...` in workspace mode, a `1.27`/`stable` matrix with `fail-fast: false`, lint for `internal/paritytest`, `cache-dependency-path` per module, and `checkout@v6`/`setup-go@v6`. A `release` job runs on tags and fails if a released module's `go.mod` has a `replace`. The root Makefile's `MODULES` list drives `vet`, `lint`, `test`, `cover`, `tidy-check` and `vulncheck`; CI calls those targets.

- **Failure modes:** a new linter reports many findings at once; slice 6 fixes them module by module, and justified exceptions get `//nolint:<linter> // reason`.

## Defaults

| Behaviour | Default | Why | Override |
|-----------|---------|-----|----------|
| Error-text redaction | on (D1) | credentials in error text must not reach logs by default (AGENTS.md rule 3) | `WithErrorRedactor(fn)` replaces it; `WithoutErrorRedaction()` disables it (loosens a safety default) |
| `slog.Any` cap | 1024 bytes (D2) | one careless value must not flood the log pipeline | `LogConfig.MaxAttrBytes`; negative disables it |
| Fingerprint method | normalised (D3) | bounded error groups | none: the raw method stays in `http.request.method` |
| Serve goroutine wait | 1s (D4) | a hostile listener cannot hang a stop | none: internal bound |

## Hot Paths

- Redaction in `exceptionMessage` for every failed and lifecycle record: match, no match, consumer redactor, disabled, panicking consumer redactor.
- `truncator.attr` for every attribute: string, error, group, own text, `Any` within the cap, over it, panicking.
- `fingerprint` for every failed record: known method, unknown method.
- `tracectx.parse` for every request with `traceparent`.
- `Start`/`Shutdown`: starting interrupted, running, Goexit in a check or hook, serve wait elapsed.
- `otelsrvr` `Begin` on a zero observer; `setup.Shutdown` with and without an in-flight scrape.

## Risks / Trade-offs

- [The default redactor masks text a consumer wanted to see] → `WithoutErrorRedaction()` and `WithErrorRedactor`; `LOGGING.md` lists the masked patterns.
- [Breaking renames annoy early adopters] → pre-v1, unreleased (no tags); `CHANGELOG.md` lists each rename with its replacement.
- [New linters slow the change down] → fixes are mechanical; slice 6 owns them so earlier slices stay small.
- [The audit's line references drift while earlier slices change code] → every task re-locates its target with gopls before editing.

## Migration Plan

Additive for consumers except the renames and the two default changes, all listed in `CHANGELOG.md` under "Unreleased". Rollback is reverting the slice's commit; each slice is independent of later ones.
