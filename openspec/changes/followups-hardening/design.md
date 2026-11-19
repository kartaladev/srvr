# Design

## Context

See proposal.md - Why. Each item comes from an open entry in `openspec/changes/archive/2026-11-19-triage-debug-buffer/notes.md`, and the entry's Context, Evidence and Suggested action are the starting point. The code facts these decisions rely on (to be rechecked with gopls in consolidation):

- `engine.ResolveConfig` (`engine/core.go`) returns `newSettings`'s joined field errors before it builds the prblm handler in `newProblems(c.ProblemOptions())`, so a rejected problem option is reported only when every other field is valid.
- `RequestState.unmatched` (`engine/unmatched.go`) derives "answered by the router" from no matched route, not aborted, a failure that is not a panic, and status 404/405. net/http answers unmatched requests in `Server.answerUnmatched` (`server.go`) and runs group middleware only for matched routes. gin answers them with the `allNoRoute`/`allNoMethod` chains, which srvr builds with `e.Use` at construction. Consumer group middleware is added with `RouterGroup.Use` on `&engine.RouterGroup`, which does not rebuild those chains (gin's `routergroup.go:65`, `gin.go:340`), so it runs only for matched routes; this was read in the code, not tested, and slice 3 pins it. fiber runs root `router.Use` middleware for every path, and its router returns the exported values `fiber.ErrNotFound` and `fiber.ErrMethodNotAllowed` (fiber v3.7.0 `router.go:834-836`).
- `otelsrvr` (`otelsrvr.go`: propagator check, `tracer`, `newObserver`) and `otelsetup` (`config.go` `validatePath`) recover panics and flatten the value with `engine.ValueText`, so `ConfigError.Cause` stays nil. `ConfigError.Unwrap` returns `[ErrConfig, Cause]`.
- ginsrvr's `classify` (`ginsrvr/server.go`) clears a failure that has no `c.Errors`, a matched route and no panic, because ginprblm reports a status-only abort to srvr's hook. prblm v0.2.0's ginprblm wraps that report around the exported `ErrStatusOnly` (`finish`, `middleware.go`), and gin's own unrouted 404/405 match it too.
- `tracectx.New` (`internal/tracectx/tracectx.go`) reads 24 bytes from `crypto/rand` per generated trace. On darwin that is an `arc4random` call, about 35% of `BenchmarkPipeline`'s CPU samples in `make profile`. On Linux, Go's `crypto/rand` uses `getrandom` (vDSO on recent kernels), and the cost there is unmeasured.
- Measurement tooling from `triage-debug-buffer` design D8: `make bench`, `make profile`, `benchstat`, the CI `bench` job (`ubuntu-latest`, pull requests only), and the gain rule. An optimisation is applied only at a 10% or larger gain in `allocs/op` or `B/op`, or in `ns/op` with `p < 0.05`.

## Goals / Non-Goals

**Goals:**
- Prove or drop each suspected defect (items 4, 5) with a RED test first (AGENTS.md rule 2), and fix only what the test confirms.
- Make `openspec validate --specs --strict` pass after archive, with no change in behaviour.
- Make the suite deterministic where a wall-clock bound stood in for an ordering fact.
- Decide every performance item by measurement, and record the numbers in `notes.md`.
- Release the result after explicit owner confirmation.

**Non-Goals:**
- New options, new log keys or new public identifiers.
- Changing the unmatched limit's mechanism (design D7 of `triage-debug-buffer`), only what it counts.
- Weakening trace-id randomness to gain speed.
- Upstream prblm, fiber or gin changes.

## Slices

1. **Consolidation** (task group 1, not a code slice).
2. **Construction joins problem-option errors** (item 4): `server-lifecycle` / "Valid and invalid configuration" (the new `WithProblemOptions` rows) and "Problem-option errors are joined on every engine". Hot path: `Start` is untouched; construction (`ResolveConfig`) changes. It works on its own: construction reports every invalid field.
3. **Only the router's 404/405 are limited** (item 5): `structured-logging` / "Failures consumer middleware reports are not limited". Hot path: the access stage (`writeAccess` → `unmatched`) and fiber's request stage. It works on its own: the existing limiter counts only router answers.
4. **Recovered panic errors are kept as `Cause`** (item 6): `otel-metrics` and `telemetry-setup` / "Typed configuration errors" (the new panic rows). Construction only, no request path.
5. **Spec and test hygiene** (items 1, 2, RELEASING.md): the shortened requirements' new detail ("Re-exported names", "Level of a failure after the response started", the moved route-listing and hook-order steps) mapped to tests; `TestShutdownHooks_Faulty` made deterministic; RELEASING.md completed. It works on its own and changes no production code.
6. **gin status-only classification** (item 7): `gin-engine` / "Consumer middleware behaviour" (the new rows). Hot path: gin's request stage (`classify`). The new rows land first and pass on today's code; `classify` changes only if D6's criteria hold.
7. **Performance** (items 8, 9, 10): benchmark rows, measurement, and changes only at the gain (D7, D8, D9); `trace-context` / "Concurrent generation" guards any change of id source. Hot paths: trace context generation, the debug buffer, fiber's access stage, and the user-agent read in every engine.
8. **Release**: precondition, preparation, owner stop, tags in RELEASING.md order, restoring `replace` with `internal/paritytest` tidied, the GitHub Release page, and verification through the proxy.

Slices 2-4 are independent of each other; the order (smallest first) only keeps each RED step simple. No slice depends on a later one: slice 5's spec deltas already exist in this change, and its tests pin behaviour that is already merged.

## Decisions

### D1. Requirement length is fixed by MODIFIED deltas that move detail into scenarios
Each over-long requirement is restated whole under MODIFIED with a description of 500 characters or fewer. The removed detail becomes a step of an existing scenario (route-listing rules, hook order), a new scenario whose rows map to an existing test (`TestLateError`'s four rows become "Level of a failure after the response started"), a new scenario with a new small test (the type list becomes "Re-exported names", one row per alias), or a reference to the main spec that already requires it ("Engines share one per-request state" points to request-observer's trace-source requirement). The schema's rule "never trim existing text just to meet the length" is aimed at changes that touch a requirement for other reasons; here shortening is the owner's stated purpose. Splitting a requirement was tried: OpenSpec refuses a MODIFIED block that drops a scenario the main spec still has, so nothing is split. `server-lifecycle` and `structured-logging` carry both the shortened description and this change's new rows in one MODIFIED block each.

**Alternatives:** editing `openspec/specs/` directly (bypasses the archive sync and the change's review); splitting requirements (refused by the validator, as above).

**Failure modes:** a moved sentence silently loses a behaviour. Task 5.1 lists every removed clause and names the scenario step or row that now carries it. Another change archived first could rewrite the same requirement. Consolidation (1.3) re-diffs each MODIFIED block against main.

### D2. Problem options are validated together with every other field
When `newSettings` reports errors, `ResolveConfig` still builds the problem handler from the consumer's options and returns `errors.Join(settingsErr, problemsErr)`, so the prblm handler is still built once per construction. If the settings are too broken to build srvr's own prblm options from them, the consumer's options are validated alone (a throwaway `prblm` handler under the existing recover), and the throwaway is discarded. A rejected option keeps its existing `*ConfigError` with `Field` `ProblemOptions`. No override: reporting every invalid field is the existing requirement.

**Alternatives:** moving prblm handler construction into `Settings.validate` (settings would then own an engine object); leaving it and narrowing the requirement (contradicts "one error naming every offending field").

**Failure modes:** a consumer option that panics. It is already contained by `newProblems`, and the new row with `MaxHeaderBytes` pins it inside a joined error. An option with side effects could run twice if the fallback path builds twice; the fallback runs only when settings are invalid, so construction fails anyway. If the RED rows pass on current code, the claim is not confirmed: the rows stay, `notes.md` records it, and the slice commits as `test(core)`.

### D3. "Unmatched" is recorded where the router answers, not derived
`RequestState` gets a mark set by the stage that knows the router answered:
- net/http: `answerUnmatched`, before it writes the 404/405 problem.
- gin: srvr's request stage, when no route matched (`c.FullPath()` is empty). Only srvr's stages run in gin's no-route chains.
- fiber: srvr's request stage, when no route matched and `c.Next()` returned exactly `fiber.ErrNotFound` or `fiber.ErrMethodNotAllowed`, compared by identity, never with `errors.Is`, so a consumer's wrapped error never counts.

`unmatched` then requires the mark (plus status 404/405 and no panic) in place of the derivation. `RequestState.result`'s similar classification is reviewed in the same slice; it changes only if a RED row shows a visible difference, otherwise a notes entry records why it stays.

**Alternatives:** keep the derivation and narrow the godoc (cheap, but the limit would then suppress consumer errors, which the "failed records are never suppressed" default forbids); classify fiber errors with `errors.Is(err, fiber.ErrNotFound)` (would count a wrapped consumer error, the second row of "consumer-reported errors").

**Failure modes:** a fiber upgrade that stops returning the shared values. Then fiber router answers are never marked and the limit stops applying. The "answered by the router" fiber row (`written` `1`) fails, which guards it. A consumer returning `fiber.ErrNotFound` itself is indistinguishable and is counted; the last row documents this, and so does the `WithUnmatchedRecordLimit` godoc. Concurrency: the mark is written by the request's own goroutine before the access stage reads it, and the 50-client row runs under `-race`. Default unchanged: the limit stays off; consumers opt in with `WithUnmatchedRecordLimit` or `AccessLogConfig.UnmatchedLimit`.

### D4. A recovered panic value is kept as `Cause` only when it is a usable error
At each recover site, `Cause` is the recovered value when it is an `error`, is not a typed nil, and has an error chain that can be walked (engine's chain guard decides). The text stays `engine.ValueText(v)`, byte-identical. Each module gets one small unexported helper rather than a new `engine` export: `engine` is public, and the 10.5/10.6 reviews of `triage-debug-buffer` rejected widening it for the otel modules.

**Alternatives:** keep any `error` value (a cyclic chain would make the caller's `errors.Is` loop forever); expose `engine.PanicCause` (widens `engine` API).

**Failure modes:**
- A typed nil carries no cause, so it is dropped (row `*MyError` typed nil).
- A cyclic or panicking `Unwrap` is dropped by the chain guard (row "Unwrap returns the error itself", bounded by `1s`).
- An `Error` method that panics is kept, because `errors.Is` compares without calling `Error`, and the text already goes through `ValueText` (row).
- An error type that is not comparable: the helper never compares values with `==`, since that would panic (row `sliceError`).

### D5. The blocking-hook row asserts order, not elapsed time
The hook signals that it has entered, then blocks on a channel that the test closes only after `Start` has returned. `Start` returning at all proves that it did not wait for the hook. A generous hang guard (10s, 200 times the 50ms hook budget) turns a regression into a failure instead of a hung test. The test also asserts that the hook was still blocked when `Start` returned, and that `errors.Is(err, context.DeadlineExceeded)` holds. Closing the channel lets the abandoned goroutine end before goleak runs.

**Failure modes:** a regression that makes `Start` wait for the hook deadlocks until the guard fires, and is reported by the guard's message. Under `-race` and CPU contention, nothing depends on elapsed time.

### D6. ginprblm.ErrStatusOnly replaces the `c.Errors` inference only if it is simpler and keeps the rows
Candidate: `classify` clears a failure when the recorded error matches `errors.Is(err, ginprblm.ErrStatusOnly)`, the route matched and there was no panic, instead of inferring it from `len(c.Errors) == 0`. ginsrvr already requires ginprblm v0.2.0; the core module cannot import ginprblm, so the check stays in ginsrvr. Adopt it only if all three hold:
1. Every new and existing gin-engine and paritytest row passes unchanged.
2. The result is no larger than today's `classify`.
3. It removes a dependence on gin internals (`c.Errors` after the chain), for example a consumer that resets `c.Errors`.

Otherwise keep `classify` and close the notes entry as evaluated and kept. Either way the new rows stay as guards. A status-only 5xx stays `http.request.completed` at `ERROR`, like a directly written 5xx on net/http and fiber; making it `http.request.failed` would be a visible change that this change does not make.

**Failure modes:** the recorded error is not reachable from `RequestState` without new API (then the candidate fails criterion 2); gin's unrouted 404/405 also match `ErrStatusOnly` (the candidate keeps them failed because the route did not match: the existing "Gin no route" rows guard it).

### D7. Trace ids: measure on Linux and darwin; any faster source stays a CSPRNG seeded from `crypto/rand`
Measure `BenchmarkPipeline` and a new `BenchmarkNewTrace` (serial and `RunParallel`) on darwin and on Linux, then compare three candidates with `benchstat -count=10`:
- (a) Today: one `crypto/rand.Read` of 24 bytes per trace.
- (b) Batched `crypto/rand`: a `sync.Pool` of buffers, each refilled by one `crypto/rand.Read` (for example 32 traces' worth) and consumed by one goroutine at a time; bytes are never handed out twice. The random source does not change.
- (c) `math/rand/v2` ChaCha8 per pool entry, seeded with 32 bytes from `crypto/rand`. Go documents ChaCha8 as a cryptographically strong generator, and it erases its key as it goes, so earlier outputs cannot be recovered.

Linux numbers come from the CI `bench` job of the slice's pull request, or from a local Linux container. If neither is available, the Linux cost is recorded as unverified with that reason.

**Security trade-off** (stated for the owner before anything is applied):
- (a) keeps no random state in srvr's memory.
- (b) keeps up to one buffer of future ids in process memory: a heap disclosure (core dump, memory-read bug) reveals the next ids drawn from that buffer.
- (c) keeps the generator key in memory: its disclosure reveals every later id from that entry until the entry is dropped and reseeded.
- Both (b) and (c) repeat ids after a VM or process snapshot is restored twice (snapshot-and-clone platforms), because the copied state is not reseeded; (a) gets the kernel's reseed.

Trace ids are not secrets. srvr sends `trace_id` in every problem response and writes it to logs. W3C Trace Context asks for random ids so that they are globally unique and are not predictable enough to be planted. So the risk is predictability and collisions, not confidentiality.

**Decision rule:**
- Apply nothing unless `BenchmarkPipeline` (not only the micro-benchmark) shows the D8 gain.
- Prefer (b) at a similar gain, because the source stays `crypto/rand` and the snapshot exposure is bounded by the buffer size.
- Any candidate is applied only after the owner confirms the trade-off (task 7.4).
- The modified `trace-context` requirement allows (b) and (c) and forbids a non-cryptographic or self-seeded generator.
- The all-zero rejection loop stays. The W3C sampled flag and the other flags are unchanged.

**Failure modes:**
- A pool entry used by two goroutines at once would duplicate ids. `sync.Pool` gives exclusive ownership, and the "Concurrent generation" rows (10,000 requests from 64 clients, `-race`) guard it.
- A `crypto/rand` failure: since Go 1.24 `crypto/rand.Read` never returns an error and crashes the program instead, so there is no new error path.
- The GC empties the pool: entries are reseeded on creation.

### D8. Debug-buffer efficiency items are measured on a row that can show them
Add a `BenchmarkDebugBuffer` row whose buffered records carry six or more attributes and a `slog.Any` value, because `slog.Record.Clone` allocates only past five. Then prototype the three items one at a time: `attrSize` formatting a `KindAny` value twice, a `Clone` discarded when `truncator.record` rewrites the record, and a ring instead of reslicing. Each item is applied only at the D8 gain, with `benchstat -count=10` before and after recorded in `notes.md`. Buffering stays off by default, and none of the items changes what is written. The existing `TestDebugBuffer_*` rows guard behaviour.

**Failure modes:** a ring that changes flush order. The order rows in `TestDebugBuffer_*` (eviction of the oldest record, original order on flush) guard it.

### D9. fiber copies and the user-agent read are skipped only when measurement pays
Add `BenchmarkPipeline` rows on fiber for an unmatched request that the limit suppresses, and rows on every engine with `OmitUserAgent`. If `benchstat` shows the gain:
- The limit is decided once per request: `AccessSuppressed` calls `admit` and caches the result on `RequestState`, and `writeAccess` reads the cached result instead of calling `admit` again.
- The omission is exposed to the engines so they skip reading the header.

**Failure modes:** `admit` counts a record that is then not written for another reason (level disabled, skip path). The cached decision is taken only where `writeAccess` would consult it (`baseEnabled`, not suppressed, `unmatched`). `TestUnmatchedLimit`'s exact counts and the "next window reports the count" row guard it. If `admit` were called twice, a record would be counted twice; the 1000-request concurrent row (`990` suppressed) guards it.

## Defaults

No default changes:
- The unmatched-record limit stays off.
- Buffering stays off.
- The user agent is still logged by default.
- Trace ids stay cryptographically random from `crypto/rand`, unless D7 is applied with owner confirmation; even then they remain cryptographically strong and seeded from `crypto/rand`.

The overrides are the same as in v0.1.0: `WithUnmatchedRecordLimit` or `AccessLogConfig.UnmatchedLimit`, `WithDebugBuffer` or `LogConfig.DebugBuffer`, and `WithoutAccessLogUserAgent` or `AccessLogConfig.OmitUserAgent`.

## Hot Paths

- Construction: `engine.ResolveConfig` (slice 2), including the fallback validation branch.
- Access stage: `RequestState.unmatched`, `writeAccess`, net/http `answerUnmatched`, gin and fiber request stages (slice 3), with error branches: the panic row, the wrapped-error row and the router-value row.
- otel construction: every recover site and the cause helper's branches (error, non-error, typed nil, cyclic chain, panicking `Error`, uncomparable type) (slice 4).
- gin `classify` (slice 6).
- `tracectx.New`, the debug buffer's `attrSize`/`record`/eviction, fiber `AccessSuppressed`, and user-agent extraction in each engine (slice 7, only for what is applied).

Coverage is checked with `go test -coverpkg=./... -coverprofile=c.out ./...` and `go tool cover -func`, plus `make cover` for `engine`.

## Risks / Trade-offs

- [The `fiber.ErrNotFound` identity check depends on fiber's implementation] → The router row fails on a fiber upgrade that changes it, and the godoc names the limitation.
- [A shortened requirement drops a behaviour] → Task 5.1 traces every removed clause to a scenario step or row.
- [Performance changes make security weaker] → D7 keeps a CSPRNG seeded from `crypto/rand`, requires the gain on the end-to-end benchmark, and stops for the owner.
- [Visible behaviour changes in a patch release] → The release slice suggests `v0.2.0` if slices 2-4 or 6 land a visible change, `v0.1.1` otherwise, and stops for the owner.
- [Timings on the local machine are noisy] → Allocation gains are exact; timing gains need `p < 0.05`; Linux numbers come from the CI runner when available.

## Migration Plan

Nothing for consumers to do. The CHANGELOG lists the error-path changes: the extra `ProblemOptions` field in joined errors, `Cause` now set for panics, and consumer-reported 404/405 records no longer suppressed under the opt-in limit. Rollback is reverting the slice commit; no data or configuration format changes.

## Open Questions

- Which version the owner confirms (task 8.3).
- Whether the owner accepts D7's trade-off if a candidate meets the gain (task 7.4). Without acceptance the candidate is recorded and not applied, which changes neither specs nor task structure.
