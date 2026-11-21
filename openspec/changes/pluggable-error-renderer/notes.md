# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (task group 1)
- **Status:** closed: done 2026-11-21
- **Target:** pluggable-error-renderer
- **Context:**
  - 1.1: `git fetch`; `main` and `origin/main` at `78c8165`; branch `feat/pluggable-error-renderer` cut from it, nothing to rebase. Since planning (`25fa456`): #23 (this change's and `engine-internal-split`'s proposals, `53ff593`, `88b0437`), #25 `engine-internal-split` applied (`engine` -> `internal/engine`; `internal/errchain`, `internal/static` extracted; `StaticResult.Failure` -> `engine.StaticFailure`; core dependency set pinned), #26 archived it to `archive/2026-11-21-engine-internal-split` and synced `engine-support` and `test-quality` main specs. Active: `static-files` (61/61, not archived), this change, `scrty-integration` (0/45, unapplied). Nothing released since v0.3.0; `engine-internal-split` ships with this change as v0.4.0.
  - 1.2: carry-overs decided:
    - otel-observability "Unmatched 404 problems differ between net/http and gin/fiber": **defer** (owner deferred it 2026-11-19 at static-files; no new owner choice). The default renderer keeps v0.3.0 bodies byte for byte, so the gap stays on the default path; with a custom renderer every engine hands `srvr.ErrNotFound` to the renderer, so its answer is identical. Stays open/unassigned.
    - static-files "gin: a static mount replaces a WithGinEngine hook's NoRoute handlers": **defer**, unaffected. Slice 4 swaps the `e.Use` middleware (`ginprblm.Middleware`/`Recovery` -> srvr's error middleware and recovery) and never calls `NoRoute`/`NoMethod`; the mount's `s.engine.NoRoute(statics.serve)` (`ginsrvr/server.go`) is untouched. Stays open/unassigned.
    - gin-and-fiber-engines "Fiber rejection records build their problem apart from the client's": **defer**, narrowed. With a custom renderer slice 5 records the rejection from the renderer's `ErrorResponse` through `engine.Render`, so record and response match; the default path is unchanged (no behaviour change allowed) and keeps the gap. Stays open/unassigned.
    - core-server "A panicking consumer error hook is contained silently" and "A srvr.problem.note event ...": **defer**; `engine.Render` contains hook panics as today (design D4), adding an event is a schema addition out of scope.
    - Every other open `unassigned` entry in the archived changes, `static-files` and `scrty-integration` is unrelated to error rendering: stays unassigned, unchanged.
    - This change: "Planning 2026-11-20: owner decisions pending" closed (1.6-1.8 answered); "If engine-internal-split goes first ..." and "engine-internal-split applied: final internal packages" adopted (paths re-pointed, below); "scrty-integration must render through ErrorRenderer" already present, stays open for `scrty-integration`; "Later: one render path, configurable limits, net/http 431" stays open/unassigned.
  - 1.3: design Context facts rechecked in the code (gopls/grep): `Core.Problems`, `ProblemOptions`, `onError`, `newProblems`, `ErrorHook` (`internal/engine/core.go`); `failure`, `newFailure`, `prblmNotes`, `recordFailure`, `RecordRejection`, `addFailure` (`internal/engine/access.go`); `clientClosedMapper` (`internal/engine/clientclosed.go`); `traceIDEnricher` (`internal/engine/trace.go`); `GuardChain`, `panicOf` (`internal/engine/triage.go`); `StaticFailure` (`internal/engine/staticfailure.go`); `stages`, `wrapHandler`, `answerUnmatched` (`server.go`); gin `newEngine`, `requestStage`, `guardErrors`, `clearStatusOnly` (`ginsrvr/server.go`); fiber `ErrorHandler`/`Recovery` wiring, `requestStage`, `renderError` (`fibersrvr/server.go`); `otelsrvr` `*prblm.PanicError` lookup (`otelsrvr.go`). All hold except paths. New fact: fiber's static `notFound` (`fibersrvr/static.go`) returns `prblm.ErrNotFound` directly, so it must send `srvr.ErrNotFound` too (design Context, Slices, Hot Paths; task 2.4). The `error-responses` and `triage-error-records` main specs did not change since `25fa456`, so the MODIFIED blocks stand; the `test-quality` ADDED requirement does not collide with main's. `openspec validate pluggable-error-renderer --strict` passes.
  - Refreshed: design (Context paths and baseline, Goals, Slices 2, D1 location and its stale "cannot import internal" reason, typed errors in `internal/errchain`, Hot Paths), proposal (Approach, Public API, Code list, Builds on), error-rendering spec (test hook in `internal/engine`), tasks 2.4, 2.5, 3.2 (packages and paths).
  - 1.6-1.8: answered by the owner on 2026-11-20 (entries "Owner decisions 2026-11-20: change order and release" and "... D8 hook signature, D9 log schema").
- **Evidence:** `openspec validate pluggable-error-renderer --strict`: "Change 'pluggable-error-renderer' is valid"; `git diff 25fa456 origin/main --stat -- openspec/specs/` touches only `engine-support` and `test-quality`.
- **Suggested action:** none.

### Planning 2026-11-20: owner decisions pending
- **Status:** closed: answered 2026-11-20 (tasks 1.6-1.8)
- **Target:** pluggable-error-renderer
- **Context:** Owner chose Option A (pluggable renderer, prblm stays default) on 2026-11-20 and added the benchmark plan (design D10). Pending: change order before `scrty-integration` (task 1.6), `ErrorHook` signature (design D8, task 1.7), `srvr.log.schema` version (design D9, task 1.8).
- **Suggested action:** ask in group 1; benchmarks only after the owner confirms no other benchmark session runs on the machine.

### scrty-integration must render through ErrorRenderer
- **Status:** open
- **Target:** scrty-integration
- **Context:** scrty-integration was planned against prblm (scrty errors to prblm). Once this change lands, security refusals must reach the client through `engine.Render` so a custom renderer formats them too.
- **Suggested action:** its consolidation group refreshes its design against `ErrorRenderer` and the typed errors.

### Later: one render path, configurable limits, net/http 431
- **Status:** open
- **Target:** unassigned
- **Context:** design D2 keeps prblm's adapters for the default path; the 5 s render deadline and 1 MiB body limit are fixed; net/http answers oversized headers with its own plain-text 431 before srvr runs.
- **Suggested action:** revisit with parity and benchmark data after this change.

### engine could be internal/engine: AGENTS.md's reason does not hold
- **Status:** adopted by engine-internal-split (consolidation 2026-11-20)
- **Target:** engine-internal-split
- **Context:** AGENTS.md and `engine`'s package godoc say `engine` is public "because separate modules cannot import core `internal/`". Go's internal rule is by import path, not module: a nested module under `github.com/kartaladev/srvr/` (ginsrvr, fibersrvr, otelsrvr, internal/paritytest) may import `github.com/kartaladev/srvr/internal/...`; only modules outside that path are refused (golang.org/x/tools/gopls does the same with x/tools/internal). Moving `engine` to `internal/engine` would hide it from consumers (no accidental dependency on an unstable API), while `srvr` keeps re-exporting what consumers need as aliases. It does not change the version coupling between engine modules and core (the same as today; mitigated by releasing all modules together). Cost: import changes in every engine module and paritytest, AGENTS.md and godoc corrections, breaking for anyone importing `engine` directly (documented as unsupported before v1).
- **Evidence:** scratch probe (removed from the repo, in the session scratchpad): module `example.com/root` with `internal/x`; nested module `example.com/root/sub` importing `example.com/root/internal/x` with `GOWORK=off`: `go run .` prints `hi`; module `example.com/other`: `main.go:2:16: use of internal package example.com/root/internal/x not allowed`.
- **Suggested action:** owner decides whether and when to move `engine` to `internal/engine`, and AGENTS.md's layout paragraph is corrected either way.

### If engine-internal-split goes first, new types land in internal packages
- **Status:** adopted by consolidation 2026-11-21 (no `internal/problems`: renderer in `internal/engine`)
- **Target:** pluggable-error-renderer
- **Context:** `engine-internal-split` (planned 2026-11-20, proposed to apply before this change, owner confirmation pending) moves `engine` to `internal/engine` and splits it. Under its owner decision "do not oversplit", the prblm handler, error hooks, access and triage records stay together in `internal/engine`; a separate `internal/problems` package exists only if that change's group 1 finds it cohesive. This change's design and tasks name `engine/render.go`, `engine.Render`, `engine.DefaultRenderer` and other `engine/...` paths.
- **Suggested action:** if `engine-internal-split` is applied first, this change's consolidation group re-points every `engine/...` path: the renderer types and `Render` land in `internal/problems` if that package exists, otherwise in `internal/engine` beside the prblm handler; public names stay in `srvr` as aliases.

### Owner decisions 2026-11-20: change order and release
- **Status:** closed: recorded
- **Target:** pluggable-error-renderer
- **Context:** change order confirmed: `static-files` -> `engine-internal-split` -> `pluggable-error-renderer` -> `scrty-integration` (AGENTS.md and openspec/config.yaml updated). `engine-internal-split` and `pluggable-error-renderer` are released together as v0.4.0: the first change's release group does not tag; the release runs once, at the end of `pluggable-error-renderer`, after the owner confirms tags.
- **Suggested action:** group 1 ticks the order and release-coordination tasks against this entry; refresh the release groups accordingly.

### Owner decisions 2026-11-20: D8 hook signature, D9 log schema
- **Status:** closed: recorded (tasks 1.7, 1.8)
- **Target:** pluggable-error-renderer
- **Context:** D8: `ErrorHook` becomes `func(ctx, err error, sent ErrorReport)`, `ErrorReport` a value struct (not a pointer, not an interface), so no allocation and safe to retain; zero report = no answer sent. D9: `srvr.log.schema` stays `1`. Design "D8 and D9 outcome" records the reasons; slice 6 adds a benchmark row with an error hook installed and requires zero added allocs/op.
- **Suggested action:** none.

### engine-internal-split applied: final internal packages
- **Status:** adopted by consolidation 2026-11-21
- **Target:** pluggable-error-renderer
- **Context:** `engine-internal-split` (branch `refactor/engine-internal-split`) moved `engine` to `internal/engine` and extracted only `internal/errchain` (srvr's sentinels, `ConfigError`, `NewConfigError`, `RouteErrorf`, chain walking, guards, value text, redaction) and `internal/static` (static resolution; `engine.StaticFailure` maps its answers to errors). There is no `internal/problems`: the prblm handler, error hooks, access and triage records stay in `internal/engine`. Order pinned by `TestImportDirection` (`internal/paritytest/layout_test.go`): `tracectx` < `errchain` < `static` < `engine`. The two changes share the v0.4.0 release, which runs at the end of this change; `CHANGELOG.md` `[Unreleased]` already holds `engine-internal-split`'s entries.
- **Suggested action:** this change's consolidation re-points every `engine/...` path in its design and tasks to `internal/engine/...` (renderer types and `Render` beside the prblm handler); a new sentinel goes to `internal/errchain`; adds `internal/...` packages only through `internalOrder` and the leak-walk list.

### Slice 2: benchmark baseline (design D10, task 2.2)
- **Status:** closed: recorded 2026-11-21
- **Target:** pluggable-error-renderer
- **Context:** owner go-ahead given for this run. Machine Apple M4 Pro (14 threads), darwin/arm64, `go1.27.4`. Base: a detached worktree at `ca21c12` (the change's base: `main` `78c8165` plus consolidation docs) in the session scratchpad, with the slice's three `server_bench_test.go` files copied in (benchmark-only). Branch: the slice's working tree before `/simplify`. Rows: `handler error`, `unmatched` (= `not_found`), `panic` already existed in all three modules; `method not allowed` added (GET on a POST-only route). Custom-renderer rows wait for slice 3 (no option yet). Command, per module (root, ginsrvr, fibersrvr), alternated base/branch 10 times: `GOWORK=off go test -run '^$' -bench Pipeline -benchmem -count=1 .`; then `benchstat base-<m>.txt branch-<m>.txt`. Worktree removed afterwards.
- **Evidence (sequential rows, `parallel` rows omitted):** sec/op is very noisy (±20-330%): other projects' `go test` processes were running on the machine during the run (not benchmarks, but load). No error row shows a significant ns/op change (all `~`, p ≥ 0.06); `user agent omitted` (a success row) +17.69% p=0.043 with ±210% spread, read as noise. allocs/op: root `success` 20 → 20, `handler error` 77 → 77, `unmatched` 103 → 112 (+9), `method not allowed` 112 → 114 (+2), `panic` 83 → 82, `body over limit` 79 → 79; gin and fiber unchanged on every row except `panic` (gin 83 → 82). B/op: root `unmatched` +2.90%, `method not allowed` +1.02%; `panic` −26% on root and gin. D10 thresholds (error-row ns/op > +5% with p < 0.05, or any extra alloc on `success`): not reached.
- **Suggested action:** the +9 allocs of net/http's router 404 come from `srvr.ErrNotFound` being a wrapper of `prblm.ErrNotFound` (one more chain link for prblm's resolution, the access record's chain walk and redaction; unprofiled). Re-run on a quiet machine after slice 3 together with its renderer rows; profile the 404 row (`-memprofile`) if the owner wants the allocations back (e.g. an `Is` method instead of `Unwrap`).

### Slice 2: access records of srvr's own 404s name srvr.ErrNotFound
- **Status:** decided (owner 2026-11-21); CHANGELOG/LOGGING.md item for slice 7
- **Target:** pluggable-error-renderer (slice 7)
- **Context:** owner decision 2026-11-21: `srvr.ErrNotFound` must not wrap `prblm.ErrNotFound`. It is now a distinct unexported `notFoundError` (`internal/errchain/answers.go`) whose `Error()` is v0.3.0's `prblm: not found` and whose `Is` matches `prblm.ErrNotFound`, so `errors.Is(srvr.ErrNotFound, prblm.ErrNotFound)` holds, prblm answers the same 404, and the `http.request.failed` record of net/http's router 404 and every static miss keeps v0.3.0's `exception.message` and a one-link `error.chain`. What still changes once: `error.fingerprint` (it hashes the chain's type names, `internal/engine/triage.go` ~line 96: `*errors.errorString` becomes `errchain.notFoundError`); `ErrorHook` receives `srvr.ErrNotFound` instead of `prblm.ErrNotFound` (it still matches `errors.Is(err, prblm.ErrNotFound)`), and the router/static 405 hands it `*srvr.MethodNotAllowedError` instead of `*prblm.Problem`. Keys and types are unchanged (schema stays `1`, D9). gin's and fiber's router 404/405 still go through ginprblm/fiberprblm until slices 4 and 5.
- **Evidence:** `TestParity_Exceptions` expects `exception.message` `prblm: not found` again (as at v0.3.0); `TestTypedErrors` "ErrNotFound is distinct, keeps v0.3.0 text and wraps nothing". `go test -run '^$' -bench 'Pipeline/unmatched' -benchmem -count=5 .` (root): 103 allocs/op, ~7516-7523 B/op (was 112 with the wrapper; 103 at the base).
- **Suggested action:** slice 7 states in CHANGELOG and LOGGING.md that the `error.fingerprint` of srvr's own 404 records changes once in v0.4.0 and that `ErrorHook` receives the srvr errors.

### Golden: gin answers a client-closed request with an empty 200, not 499
- **Status:** closed (owner 2026-11-21: fix the spec row, no behaviour change)
- **Target:** pluggable-error-renderer
- **Context:** error-rendering "Default rendering is unchanged" lists the client-closed row as `499` for net/http and gin. The v0.3.0 golden recorded at the base (`internal/paritytest/testdata/default_rendering.json`, row `client closed`) shows gin sends `200` with no body: ginprblm's `isClientGone` writes nothing when the request context is cancelled and the error wraps `context.Canceled`, and gin then completes with its default 200. No client normally reads it (the client half-closed in the test), so it is not a user-visible defect, but the spec row is wrong for gin and the access record says 200.
- **Evidence:** `TestParity_DefaultRendering/gin/client_closed` records status 200, empty body; net/http records 499 `Client Closed Request`.
- **Suggested action:** correct the spec row (gin: as v0.3.0, nothing rendered) in slice 4 or 7; decide in slice 4 whether srvr's gin path renders 499 like net/http.
- **Decision:** owner 2026-11-21: the default path keeps v0.3.0 (net/http `499`, gin an empty `200` with `Content-Length: 0`); the "Default rendering is unchanged" Examples now list the two engines as separate rows. The custom-renderer row (`ErrClientClosed`, `499`) is unchanged.

### Slice 2: notes for slice 3 (engine.Render)
- **Status:** open
- **Target:** pluggable-error-renderer (slice 3)
- **Context:** (1) `engine.DefaultRenderer` answers through the request's `Core.Problems().Respond`, which runs srvr's hook: it records the failure and calls the consumer's `ErrorHook`. `engine.Render` must not record or hook a second time when the custom renderer is (or wraps) `DefaultErrorRenderer`. (2) `errchain.RequestTooLarge` and `errchain.ClientClosed` exist (tested) but nothing calls them yet: the default path must not change, so the 499 mapper (`clientClosed`) still receives the unwrapped error; slice 3 wraps on the custom path. (3) `ErrHeadersTooLarge`/`ErrRequestTimeout` are mapped (431/408) but produced only in slice 5. (4) `/simplify` findings skipped: keep `MethodNotAllowedError.Allow []string` (D3 public shape) despite the split/join round trip (+2 allocs on 405); keep the 413 detail copy in `answerProblem` for a fiber `ErrRequestTooLarge` without `*http.MaxBytesError`; `captureProblem`'s `ctx.Value` lookup in `onError` could become a `RequestState` field when slice 3 touches it.
- **Suggested action:** handle in slice 3.
- **Outcome (slice 3, 2026-11-21):** (1) handled: while a custom renderer runs, `RequestState.rendering` makes `onError` only capture the problem (prblm notes still reach the hook); `Core.Render` records the answer actually sent and calls the hook once (`TestRender` "delegating to DefaultRenderer records and hooks once"). (2) handled: `Core.Render` wraps with `ErrClientClosed` (same `clientClosed` predicate) and `ErrRequestTooLarge` (for an unwrapped `*http.MaxBytesError`) on the custom path only; the default path is unchanged. (3) unchanged, slice 5. (4) not revisited.

### Slice 3: benchmarks pending the owner's go-ahead (task 3.5)
- **Status:** open
- **Target:** pluggable-error-renderer (slice 3, task 3.5)
- **Context:** the owner said on 2026-11-21 that the machine is busy: no benchmark was run in slice 3 (only `go vet`/`make check`, which compile the benchmark files). The D10 re-run for net/http, with the custom-renderer rows, is still to do.
- **Suggested action:** run task 3.5 once the owner confirms the machine is quiet; add the custom-renderer rows to `BenchmarkPipeline` then.

### Slice 3: spec examples that do not match the code
- **Status:** closed: decided (owner 2026-11-21)
- **Target:** pluggable-error-renderer (spec refresh, slice 7 at the latest)
- **Context:** (1) error-rendering "Rejected renderer configuration" uses `prblm.WithNamespace("https://x")`, which prblm v0.3.0 rejects itself ("path must not be empty or /"), so that row reports a `ProblemOptions` error beside the `ErrorRenderer` one; tests use `https://errors.example/p`. (2) "What the renderer receives" expects `Route` `GET /items/{id}`; net/http records the route without the method (`http.route` is `/items/{id}`), and `ErrorRequest.Route` follows it ("the engine's syntax"). (3) The "returns after the 5-second deadline" row is tested at the decision level only (`TestRender`, deadline shortened through `Core.renderDeadline`); the HTTP suite cannot reach the internal hook.
- **Suggested action:** correct the two example values in the spec; owner confirms `Route` follows `http.route`.
- **Outcome (owner 2026-11-21):** (1) and (2) "match the code": the spec rows now use `prblm.WithNamespace("https://errors.example/p")` and `Route` `/items/{id}`; `ErrorRequest.Route`'s godoc says it carries no method, as `http.route`. (3) "add an internal seam": `TestRender_DeadlineEndToEnd` (`internal/engine/render_internal_test.go`) serves the net/http stage chain over httptest with `Core.renderDeadline` at 20ms and asserts the safe 500 bytes and `srvr.renderer.failure=deadline`, plus the disconnected-client row; removing the deadline check in `renderFault` fails it.

### Slice 3: Render is a Core method
- **Status:** closed: recorded
- **Target:** pluggable-error-renderer
- **Context:** the plan names `engine.Render`; it is `(*Core).Render(ctx, ErrorTarget, ErrorRequest, error)`, since it needs the server's renderer, hook and render deadline. Engines supply an `ErrorTarget` (`Committed`, `WriteError`); net/http's is in `internal/engine/renderhttp.go`, chosen once through `Core.HTTPErrors()` (prblm's handler or the renderer, same three methods). gin and fiber implement their own targets in slices 4 and 5.
- **Suggested action:** none.

### Slice 3: one unexplained `make cover` failure
- **Status:** open (unconfirmed)
- **Target:** unassigned
- **Context:** during slice 3 verification one `make cover` run exited with `make: *** [cover] Error 1`; the output was cut by a pipe, so the failing test is unknown. The rerun, three `go test -race -count=1 .` runs and `make check` all passed. Not reproduced, so no defect is claimed (rule 2).
- **Suggested action:** if it recurs, keep the full log and reproduce the failing test.
- **Recurrence (slice 4, 2026-11-21):** one `go test -race -count=1 ./internal/... .` run failed in the root package (`FAIL github.com/kartaladev/srvr 16.692s`); the output was cut by `tail`, so the failing test is unknown again. The immediate rerun and three more `go test -race -count=1 .` runs passed. Still unconfirmed; next time run with `-json` or tee the full log.

### Slice 4: benchmarks skipped (task 4.3)
- **Status:** open
- **Target:** pluggable-error-renderer (task 4.3)
- **Context:** owner 2026-11-21: machine busy, no benchmarks in slice 4. Only `make check` compiled the gin benchmark file. The D10 re-run for gin (default and custom-renderer rows) is still to do.
- **Suggested action:** run task 4.3 together with 3.5 once the machine is quiet.

### Slice 4: gin decisions taken while applying
- **Status:** open (owner to confirm)
- **Target:** pluggable-error-renderer
- **Context:** (1) With a renderer, gin answers a request the client closed `499` through the renderer (spec row "Custom renderer", net/http and gin); the default path keeps v0.3.0's empty `200`. (2) A status-only abort (`c.AbortWithStatus(401)`) calls neither the renderer nor `ErrorHook` on the renderer path; on the ginprblm path the hook still sees `ginprblm.ErrStatusOnly` with a 401 problem, as at v0.3.0 (`TestWithErrorRenderer_Hook`). (3) A consumer root middleware aborting with its own status in gin's no-route chain (only with a static mount) gets its bare status; only an unmatched 404/405 is the router's answer (`TestErrorRenderer_UnmatchedAbort`). (4) The render deadline row cannot be shortened from ginsrvr (`Core.renderDeadline` is unexported and engine's tests cannot import gin); the gin path is the shared `Core.Render`/`callRenderer`, covered end to end by `TestRender_DeadlineEndToEnd`, and `TestErrorRenderer_Request` asserts the gin renderer's context carries a deadline of at most 5 s.
- **Suggested action:** owner confirms (2); if the hook should see status-only aborts on the renderer path too, add it in slice 6 with the `ErrorHook` signature change.
