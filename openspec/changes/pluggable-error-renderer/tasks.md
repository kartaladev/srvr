# Tasks

## 1. Consolidation: refresh plan against merged changes

- [x] 1.1 Sync with the main branch (`git fetch`, rebase or merge `origin/main` into the working branch) and list every change merged or archived since this change was planned on 2026-11-20 (`openspec list --json`, `openspec/changes/archive/`, `git log origin/main`); verify they are named in the completion note (at planning: `main` at `25fa456`, v0.3.0 released, `static-files` complete and not yet archived, `scrty-integration` active and unmerged)
- [x] 1.2 Read every `notes.md` carry-over (archived changes, `static-files`, `scrty-integration`) and decide each open entry addressed to `pluggable-error-renderer` or `unassigned` (adopt, defer or close); verify none is left undecided. To decide here: otel-observability "Unmatched 404 problems differ between net/http and gin/fiber" (this change sends `srvr.ErrNotFound` on every engine; the default renderer keeps today's bodies, so the gap stays unless the owner picks a canonical body now); static-files "gin: a static mount replaces a WithGinEngine hook's NoRoute handlers" (touched by slice 4's error middleware: confirm unaffected); add an entry addressed to `scrty-integration`: its refusals and scrty-to-prblm mapping must render through `ErrorRenderer`
- [x] 1.3 Refresh this change's proposal, specs, design and tasks against the main specs and merged code: recheck with gopls the code facts in design Context (`Core.Problems`, `ProblemOptions`, `onError`, `newProblems`, `recordFailure`, `RecordRejection`, `addFailure`, `GuardChain`, `panicOf`, `clientClosed`, `answerUnmatched`, `wrapHandler`, `StaticResult.Failure`, gin `newEngine`/`clearStatusOnly`/`guardErrors`, fiber `renderError`/`requestStage`, `otelsrvr` panic lookup); re-diff the MODIFIED blocks of `error-responses` and `triage-error-records` against `openspec/specs/`; verify `openspec validate pluggable-error-renderer --strict` passes
- [x] 1.4 Record the outcome of 1.1-1.3 and of 1.6-1.8 at the top of this change's `notes.md` (replace the placeholder entry); verify the file lists what was refreshed and adopted, or states that nothing was needed
- [x] 1.5 Commit the consolidation as `docs(openspec): consolidate pluggable-error-renderer against main`, containing only `openspec/` files; skip it when 1.1-1.4 changed nothing; verify with `git log -1 --stat`
- [x] 1.6 OWNER CONFIRMATION: the change order. AGENTS.md and `openspec/config.yaml` end `... static-files -> scrty-integration`; this change assumes it goes after `static-files` and before `scrty-integration`. Ask the owner; record the answer in `notes.md`; the owner (not this change) updates AGENTS.md and `config.yaml` if confirmed; verify the answer is recorded
- [x] 1.7 OWNER DECISION D8: the new `ErrorHook` signature (options A-D in design D8, recommendation A `func(ctx, err error, sent ErrorReport)`); record the answer in `notes.md` and refresh the error-responses delta and slice 6 tasks if it is not A; verify `openspec validate pluggable-error-renderer --strict` passes. Slices 2-5 do not wait for it (design D8 interim)
- [x] 1.8 OWNER DECISION D9: whether optional `problem.type`/`problem.title` and the new `srvr.renderer.failure` key bump `srvr.log.schema` (recommendation: stays `1`); record the answer in `notes.md`; verify slice 6 tasks match it

## 2. Slice: typed errors and the default prblm renderer, no behaviour change

- [x] 2.1 Load `golang-how-to` (and through it testing, error handling, naming, safety, design patterns, documentation); confirm the root module needs no new dependency; verify `go list -deps ./ | grep -v '^[a-z]*$'` shows no new non-standard, non-prblm import after the slice
- [x] 2.2 Benchmark baseline (design D10): ASK THE OWNER first whether another benchmark session is running on the machine and wait for the go-ahead; create a worktree at the change's base commit, add the `handler_error`, `not_found`, `method_not_allowed` and `panic` rows to `BenchmarkPipeline` in the root module, `ginsrvr` and `fibersrvr` on both sides (benchmark-only test files, no production change), run 10 interleaved runs (`-run '^$' -bench Pipeline -benchmem -count=1`, alternating base and branch); record the commands, machine, Go version and `benchstat` output in `notes.md`; verify `go test -run '^$' -bench Pipeline -benchmem -benchtime=1x ./...` passes in each module
- [x] 2.3 Capture the v0.3.0 golden responses (error-rendering / Scenario: Default rendering is unchanged, every row, every engine) in `internal/paritytest/testdata/` from the base commit; verify `go test -race -run TestParity_DefaultRendering ./internal/paritytest` passes before any production change (the golden test is the safety net for this slice)
- [x] 2.4 srvr's own answers are typed errors (error-rendering / Scenario: Custom renderer on every engine, `match` column, asserted on the error srvr hands to prblm on net/http); verify `go test -race -run 'TestTypedErrors|TestAnswerUnmatched' ./internal/errchain ./internal/engine .` passes
  - RED: `TestTypedErrors` (`errors_test.go`), one case per answer of design D3: `errors.Is(srvr.ErrNotFound, prblm.ErrNotFound)`, `MethodNotAllowedError.Allow`, `ErrRequestTooLarge` wrapping `*http.MaxBytesError`, `ErrClientClosed` wrapping `context.Canceled`, `PanicError` alias identity; plus a recording `prblm.OnError` hook on net/http asserting the router's 404/405 and a static miss deliver the srvr errors. Expected failure: undefined names, then `prblm.ErrNotFound` delivered instead of `srvr.ErrNotFound`
  - GREEN: declare the errors in `internal/errchain` with aliases in `srvr`; `answerUnmatched`, `engine.StaticFailure` and fiber's static `notFound` return them; `clientClosed` wraps with `ErrClientClosed`; prblm mappers in `ProblemOptions` map `MethodNotAllowedError` to today's 405 with `Allow`, `ErrRequestTooLarge` to 413
  - REFACTOR: the 405 construction is duplicated in `answerUnmatched` and `engine.StaticFailure` (Extract Function `methodNotAllowed(allow)`); then `/simplify`
- [x] 2.5 Default renderer value (design D7) produces today's response as an `ErrorResponse`; verify `go test -race -run TestDefaultRenderer ./internal/engine` passes
  - RED: `TestDefaultRenderer`, one case per row of Scenario: Default rendering is unchanged, comparing `ErrorResponse` with the net/http golden response
  - GREEN: `engine.DefaultRenderer` over `Core.Problems()` and an in-memory writer; `srvr.DefaultErrorRenderer()`
  - REFACTOR: none expected beyond `/simplify`
- [x] 2.6 Verify the slice end to end: `TestParity_DefaultRendering` still passes on every engine (no behaviour change), `otelsrvr` tests pass (panic lookup through the alias), `make check` passes (`GOWORK=off`), `go test -coverpkg=./... -coverprofile=c.out ./... && go tool cover -func=c.out` covers each new error and the default renderer; gopls diagnostics clean; goleak passes
- [x] 2.7 Run `/simplify` on the slice's changes, apply what holds up, re-run the tests, then commit `feat(core): type srvr's own error answers`; verify with `git log -1 --stat`

## 3. Slice: WithErrorRenderer on net/http

- [x] 3.1 Renderer configuration is validated (error-rendering / Scenario: Rejected renderer configuration, net/http rows and the accepted rows); gin and fiber reject `WithErrorRenderer` with a `ConfigError` "not supported by this engine yet" until slices 4 and 5; verify `go test -race -run 'TestWithErrorRenderer' . ./ginsrvr/... ./fibersrvr/...` passes
  - RED: one case per row, `errors.As` to `*ConfigError` with `Field` `ErrorRenderer`, the typed-nil row, both option orders, the joined row
  - GREEN: `WithErrorRenderer`, `Settings.ErrorRenderer`, `IsNil`, the `ProblemOptions` conflict in `ResolveConfig` (an empty `WithProblemOptions()` marks the field set)
  - REFACTOR: none expected beyond `/simplify`
- [x] 3.2 `engine.Render` enforces the safety rules (error-rendering / Scenario: Faulty renderer, Hostile errors and committed responses, decision level without HTTP); verify `go test -race -run TestRender ./internal/engine` passes
  - RED: `TestRender` (`internal/engine/render_internal_test.go`), one case per row, with a fake writer, a test hook shortening the 5 s deadline, and a recording hook; expected failure: undefined `Render`
  - GREEN: design D4 steps 1-7 and D5 safe 500; `srvr.renderer.failure` on the failure; the hook gets a synthesised `*prblm.Problem` (design D8 interim)
  - REFACTOR: Decompose Conditional for the validation ladder (one predicate per rule); then `/simplify`
- [x] 3.3 net/http answers every error through the renderer when one is set (error-rendering / Scenario: Custom renderer on every engine, What the renderer receives, Faulty renderer, Hostile errors, Renderer and lifecycle; net/http rows); verify `go test -race -run 'TestErrorRenderer_' .` passes
  - RED: `httptest` rows; the client-closed row disconnects mid-handler; the 413 row sends 11 bytes over a 10-byte limit; the concurrency row uses 50 goroutines
  - GREEN: `stages`, `wrapHandler` and `answerUnmatched` call `engine.Render` when a renderer is set (recovery with srvr's own `recover` producing `*PanicError`); unchanged otherwise
  - REFACTOR: Replace Conditional with Polymorphism if the two paths spread `if renderer != nil` across stages (one `errorWriter` chosen at construction); then `/simplify`
- [x] 3.4 Records for custom renderers (triage-error-records / Scenario: Problem group, the custom-renderer rows); verify `go test -race -run 'TestProblemGroup' .` passes
  - RED: the four new rows, including the hostile `Title` and the panic value not logged
  - GREEN: `failure` holds status, type, title instead of `*prblm.Problem`; `addFailure` omits empty type and title and writes `srvr.renderer.failure`
  - REFACTOR: none expected beyond `/simplify`
- [ ] 3.5 Re-run the D10 benchmarks for net/http (ask the owner first, as in 2.2); record in `notes.md`; STOP for the owner if a threshold is crossed (pending: owner said the machine is busy, 2026-11-21; see notes.md)
- [x] 3.6 Documentation: godoc on `ErrorRenderer`, `ErrorRequest`, `ErrorResponse`, `WithErrorRenderer` (every rule, the fixed limits and why, no config field, the leak the renderer owns), an `ExampleWithErrorRenderer`; `CHANGELOG.md` `[Unreleased]` Added; verify `go test -run Example .` passes
- [x] 3.7 Verify the slice end to end: `make check MODULES=". internal/paritytest"`; coverage of every branch of `engine.Render` with `go tool cover -func`; golden default rows unchanged; gopls diagnostics clean; goleak passes (no rendering goroutine)
- [x] 3.8 Run `/simplify`, apply what holds up, re-run the tests, then commit `feat(core): render errors with a consumer's ErrorRenderer`; verify with `git log -1 --stat`

## 4. Slice: gin renders through the ErrorRenderer

- [x] 4.1 Load `golang-how-to`; remove gin's "not supported yet" rejection (error-rendering / Scenario: Rejected renderer configuration, gin rows); verify `go test -race -run TestWithErrorRenderer ./...` passes in `ginsrvr`
  - RED: the gin rows now expect the renderer to be accepted
  - GREEN: drop the rejection
  - REFACTOR: none expected beyond `/simplify`
- [x] 4.2 gin answers every error through the renderer (error-rendering / Scenarios: Custom renderer, Faulty renderer, Hostile errors, Renderer and lifecycle; gin rows, including `c.AbortWithStatus(401)` left untouched and gin's early-committed headers); verify `go test -race -run 'TestErrorRenderer_' ./...` passes in `ginsrvr`
  - RED: `httptest` rows; expected failure: ginprblm renders instead of `R`
  - GREEN: when a renderer is set, `newEngine` installs srvr's error middleware (last `c.Errors` entry, guarded, through `engine.Render`, status-only aborts skipped as `clearStatusOnly` does today) and srvr's recovery instead of `ginprblm.Middleware`/`Recovery`; the router's 404/405 deliver `srvr.ErrNotFound`/`*MethodNotAllowedError` with gin's sorted `Allow`
  - REFACTOR: share the status-only rule between `clearStatusOnly` and the new middleware (Extract Function); then `/simplify`
- [ ] 4.3 Re-run the D10 benchmarks for gin (ask the owner first); record in `notes.md`; STOP for the owner if a threshold is crossed (skipped 2026-11-21, machine busy: notes.md "Slice 4: benchmarks skipped")
- [x] 4.4 Verify: `make check MODULES="ginsrvr internal/paritytest"`; coverage of the new middleware and recovery; default golden rows unchanged; gopls diagnostics clean
- [x] 4.5 Run `/simplify`, apply what holds up, re-run the tests, then commit `feat(ginsrvr): render errors with the server's ErrorRenderer`; verify with `git log -1 --stat`

## 5. Slice: fiber renders through the ErrorRenderer

- [ ] 5.1 Remove fiber's "not supported yet" rejection (error-rendering / Scenario: Rejected renderer configuration, fiber rows); verify `go test -race -run TestWithErrorRenderer ./...` passes in `fibersrvr`
  - RED: fiber rows expect acceptance
  - GREEN: drop the rejection
  - REFACTOR: none expected beyond `/simplify`
- [ ] 5.2 fiber answers every error, panic and pre-routing rejection through the renderer (error-rendering / Scenarios: Custom renderer, Faulty renderer, Hostile errors, Renderer and lifecycle; fiber rows, including the 431 and 408 rows and a body stream already started); verify `go test -race -run 'TestErrorRenderer_' ./...` passes in `fibersrvr`
  - RED: `app.Test` rows (431 and 408 over a real listener where `app.Test` cannot produce them); expected failure: fiberprblm renders instead of `R`
  - GREEN: `renderError` and srvr's recovery call `engine.Render` when set; `RecordRejection` passes `ErrHeadersTooLarge`/`ErrRequestTimeout`/`ErrRequestTooLarge` and the renderer renders the rejection; router answers map `fiber.ErrNotFound`/`ErrMethodNotAllowed` to srvr's errors
  - REFACTOR: none expected beyond `/simplify`
- [ ] 5.3 Re-run the D10 benchmarks for fiber (ask the owner first); record in `notes.md`; STOP for the owner if a threshold is crossed
- [ ] 5.4 Verify: `make check MODULES="fibersrvr internal/paritytest"`; coverage; default golden rows unchanged; gopls diagnostics clean
- [ ] 5.5 Run `/simplify`, apply what holds up, re-run the tests, then commit `feat(fibersrvr): render errors with the server's ErrorRenderer`; verify with `git log -1 --stat`

## 6. Slice: error hook signature and log schema (after owner decisions 1.7 and 1.8)

- [ ] 6.1 Precondition: 1.7 and 1.8 are answered in `notes.md`; verify the answers are recorded
- [ ] 6.2 `ErrorHook` receives the decided type (error-responses / Scenario: Consumer mapper and hook, every row; Scenario: Consumer prblm error hooks; Scenario: Consumer prblm error hook on every engine); verify `go test -race -run 'TestErrorHook' ./...` passes in the root module, `ginsrvr`, `fibersrvr`, `internal/paritytest`
  - RED: change the rows to the new signature; expected failure: compile error in the test files only, then wrong report contents
  - GREEN: `ErrorReport` (or the decided type), `onError` and `engine.Render` build it; drop the D8 interim synthesised problem; `clearStatusOnly` keeps prblm's hook signature internally
  - REFACTOR: find references with gopls to every `ErrorHook` use; then `/simplify`
- [ ] 6.3 Log schema per 1.8: golden-record test pins `problem.type`/`problem.title` as optional and `srvr.renderer.failure`; `srvr.log.schema` stays `1` or becomes `2` as decided (structured-logging "The log schema is versioned" rows refreshed in this change's specs if bumped); verify `go test -race -run 'TestGolden|TestSchema' ./...` passes
- [ ] 6.4 `CHANGELOG.md` `[Unreleased]`: **BREAKING** `ErrorHook` with a migration snippet; LOGGING.md entries; verify `go vet ./...` passes in every module
- [ ] 6.5 Verify: `make check`; gopls diagnostics clean
- [ ] 6.6 Run `/simplify`, apply what holds up, re-run the tests, then commit `feat(core)!: report sent errors to ErrorHook without prblm`; verify with `git log -1 --stat`

## 7. Slice: parity, documentation and end-to-end verification

- [ ] 7.1 Parity: `internal/paritytest` runs every Examples row of error-rendering on all three engines with the default renderer (golden, byte-for-byte) and with `R` (identical status, body and listed headers); documented differences (fiber never cancels a context, net/http's pre-routing 431) are pinned as engine-parity exceptions; verify `go test -race ./...` passes in `internal/paritytest`
- [ ] 7.2 Documentation: README section "Custom error rendering" (wrapping `DefaultErrorRenderer`, a house-format renderer that never echoes error text), LOGGING.md (`problem` group optionality, `srvr.renderer.failure`, schema note), godoc review of every new name, CHANGELOG complete; verify `go test -run Example ./...` passes
- [ ] 7.3 Final benchmark comparison (design D10): ask the owner first; base commit vs branch on every engine, default and trivial renderer, 10 interleaved runs, `benchstat`; record the table in `notes.md`; STOP for the owner if a threshold is crossed
- [ ] 7.4 End-to-end verification locally (GitHub CI is disabled): `make check` and `make cover` from the root; every hot path of design Hot Paths covered (`go tool cover -func`); `govulncheck`; `openspec validate pluggable-error-renderer --strict`; gopls diagnostics clean
- [ ] 7.5 Run `/simplify`, apply what holds up, re-run the tests, then commit `test(paritytest): pin error rendering parity and document custom renderers`; verify with `git log -1 --stat`

## 8. Slice: release

- [ ] 8.1 Precondition: this change is reviewed and merged to `main`; verify `git log origin/main --oneline -1` contains the merge
- [ ] 8.2 Prepare: move `CHANGELOG.md` `[Unreleased]` under a version heading (placeholder), run `make check` and `make cover` on `main`, dry-run the `RELEASING.md` steps without tagging; verify every step passes
- [ ] 8.3 STOP for the owner: present the proposed version (`v0.4.0`: breaking `ErrorHook`, new API), the changelog section, the final benchmark table and the dry-run results; wait for explicit confirmation of the version and of pushing tags; record the decision in `notes.md`; do not continue without it
- [ ] 8.4 Release in `RELEASING.md` order (core, then `ginsrvr`, `fibersrvr`, `otelsrvr`, `otelsetup`), restore development `replace` lines, publish the GitHub Release page, verify every module through the proxy and with a scratch consumer using a custom renderer on each engine; record the result in `notes.md`

## Workflow follow-up

- Re-read `notes.md` and make sure every open item has a target or is `unassigned`.
- Archive the change after review; verify `openspec validate --specs --strict` passes after the archive sync.
