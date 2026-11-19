# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation 2026-11-17 (task group 1)
- **Status:** closed: consolidation done
- **Target:** core-server
- **Context:** 1.1: `main` is the only branch (tracking `origin/main`, one commit `6eebc25`); no change has been merged or archived (`openspec/changes/archive` is empty, `openspec/specs` is empty), as expected for the first change. 1.2: open carry-overs addressed to `core-server` or `unassigned` - "Ask prblm for a composable OnError and an exported status-only sentinel" (unassigned) is deferred, kept open, with task 5.5 attaching its failing test; the entries other changes address to `core-server` (otel-observability "WithShutdownHook shared with security shutdown hooks", "error.type innermost Go type" (logs part), "Correlation keys may nest under WithGroup"; triage-debug-buffer entries) are already `adopted` in this change's artifacts, nothing undecided. 1.3: re-checked against the released prblm `v0.1.0` (the latest version, `go list -m -versions`) and Go `1.27.4`: `NewHandler(opts ...Option) *Handler` (panics on invalid options), `OnError(func(ctx, err, *Problem)) Option` (single slot), `(*Handler).Wrap(func(w, r) error) http.Handler`, `(*Handler).Recover(http.Handler) http.Handler` (re-panics `http.ErrAbortHandler`, reports with a nil problem when the response started), `RequestFrom(ctx) (Request, bool)`, `PanicError{Value, Stack}`, the built-in `*http.MaxBytesError` -> 413 mapper and `context.DeadlineExceeded` -> 504 all match design D4/D5/D8; `golangci-lint` 2.15.0 and `gopls` are installed. `openspec validate core-server --strict` passes. No artifact needed a change.
- **Evidence:** `git branch -a` (`* main`, `remotes/origin/main`); prblm source `~/go/pkg/mod/github.com/kartaladev/prblm@v0.1.0` (`handler.go:63,411,473`, `options.go:62`, `scope.go:64`, `builtins.go:27`); `openspec validate core-server --strict` -> `Change 'core-server' is valid`.
- **Suggested action:** none; artifacts unchanged. One new open follow-up is recorded below ("prblm type discovery is not served by srvr").

### prblm type discovery is not served by srvr
- **Status:** open
- **Target:** unassigned
- **Context:** prblm v0.1.0 serves documentation for types registered with `WithTypes` + `WithNamespace` through `Handler.Discovery()` mounted at `Handler.DiscoveryPath()`; on net/http nothing mounts it automatically. srvr accepts `WithProblemOptions(prblm.WithTypes(...))` (error-responses "Consumers extend error rendering") but no spec says whether srvr mounts discovery, so type URLs in problems would lead to srvr's own 404 problem. srvr's 404/405 interception also never lets the consumer mount discovery under a route without registering it as a route.
- **Evidence:** unverified: specification gap, no runtime defect yet; prblm `handler.go:640-690` (`DiscoveryPath`, `Discovery` godoc shows the manual `mux.Handle` mount).
- **Suggested action:** Owner decides: srvr mounts `Discovery()` at `DiscoveryPath()` when non-empty (engine-neutral, every engine), or documents that consumers `Mount` it themselves; add a spec scenario either way.


### Planning audit 2026-11-17 decisions
- **Status:** closed: applied during planning
- **Target:** core-server
- **Context:** The cross-change audit raised 26 owner decisions; the owner accepted every recommended default. Applied here: D1 (correlation handler replays `WithGroup`/`WithAttrs` after adding `trace_id`/`span_id`; Grouped loggers scenario, design D7, task 6.8, benchmark task 6.9), D2 (panic records: `error.type` `panic`, value type in `exception.type`; design D8, task 8.4), D5 (core `WithShutdownHook` with `BeforeDrain`/`AfterDrain`, `ShutdownHookTimeout` 5s, run once on every stop path, `ErrShutdownHook`; design D9, tasks 3.8-3.9, 7.10), D6 (`srvr.log.schema` on every srvr-written record; additive keys keep the version, renames/removals/meaning or type changes bump it; golden-record test, task 6.11), D7 (every string attribute truncated in the wrapper, `MaxAttrBytes` 1024, `srvr.attr.truncated`; design D7, task 7.7), D8 (unmatched 404/405 log `http.request.failed` at `WARN` with `problem`; task 6.10), D13 (`WithConfig`, `WithLogConfig`, `WithAccessLogConfig`, `WithServiceConfig`, `WithTraceConfig` plus individual options, argument order wins; design D10, task 3.3), D25 (task 1.5 consolidation commit; scaffolding folded into slice 2 as tasks 2.1-2.2), D26 (entry below). Also: srvr's `OnError` only collects into the per-request state and the access stage writes the one failed record (design D4, task 8.1); slice-end tasks run vet, lint and tests with `GOWORK=off`.
- **Evidence:** `trace_id` nesting proven by planning probe `probes/xgroup/main_test.go` (`go test -count=1 -v .`, Go 1.27.4): naive handler wrote `{"db":{"table":"users","trace_id":"4bf9…"}}`, replay wrote `{"trace_id":"4bf9…","db":{"table":"users"}}`. The other decisions are design choices, not defects.
- **Suggested action:** none; consolidation 1.3 re-checks these against merged code.

### Ask prblm for a composable OnError and an exported status-only sentinel
- **Status:** closed: part 1 answered by prblm v0.2.0 (`triage-debug-buffer`); part 2 done in followups-hardening slice 6 (gin's classification uses `ginprblm.ErrStatusOnly`)
- **Target:** followups-hardening
- **Context:** One upstream issue on kartaladev/prblm covering two asks. (1) `prblm.OnError` is a single slot (the last option wins), so srvr must own it and offer `WithErrorHook`; a consumer passing `prblm.OnError` through `WithProblemOptions` is silently overridden. (2) `ginprblm` reports `c.AbortWithStatus(4xx)` to `OnError` with the unexported `errStatusOnly`, so callers cannot tell it apart with `errors.Is`; the gin engine therefore classifies failures from `c.Errors` (gin-and-fiber-engines notes "ginprblm reports status-only aborts to OnError with an unexported error" is merged into this entry). Asks: allow several `OnError` hooks run in order, and export `ginprblm.ErrStatusOnly`.
- **Evidence:** (1) `upstream_test.go` `TestUpstream_PrblmOnErrorComposes` (`SRVR_UPSTREAM_TESTS=1 go test -count=1 -run TestUpstream ./...`, prblm v0.1.0): `OnError hooks called: first=false second=true, want both`; it is skipped without the variable so CI stays green, and srvr documents the workaround on `WithProblemOptions` and `WithErrorHook` (godoc, `ExampleWithErrorHook`). Earlier: unverified for (1) as a running test until task 5.5 adds one; (2) probe `probes/gin/hook_test.go` `TestHookOnStatusOnly` (`go test -count=1 -v -run TestHookOnStatusOnly .`): `OnError called: err="ginprblm: response aborted with an error status and no error" type=*prblm.statusError`.
- **Suggested action:** Open one issue on kartaladev/prblm with both asks; when released, drop srvr's single-slot workaround note and switch the gin classification to `errors.Is(err, ginprblm.ErrStatusOnly)`.

### Planning audit 2 2026-11-17: decisions A1-A4 and audit fixes applied
- **Status:** closed: applied during planning
- **Target:** core-server
- **Context:** A second fresh-context audit and owner decisions A1-A4 (recommended defaults accepted). Applied here: A1 - kept D2 and fixed the triage-error-records `*net.OpError` row to `Err: syscall.ECONNREFUSED` (innermost `syscall.Errno`, three-entry chain); A2 - truncation also cuts `error` values, resolved `LogValuer` values and the record `msg`, and only srvr's own `exception.*` attributes get larger limits (structured-logging rows, design D7, task 7.7); A3 - `Scenario: Aborted request` for `http.ErrAbortHandler` with `srvr.response.aborted`, `WARN`, no `problem` group (additive key, no schema bump; task 8.4); A4 - which `Start` failures run hooks, rows in `Hooks on every stop path` (tasks 3.8, 4.3, 4.7) and design D9; new `Scenario: Blocking and re-entrant hooks` (task 3.9); config structs are separate top-level structs and a `With<Struct>` resets nested structs (design D10; trace-context and service rows now use `WithTraceConfig`/`WithServiceConfig`); error-responses consumer-hook wording follows D4 (srvr's hook only records the error).
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/audit2/core2`, `GOFLAGS=-mod=mod go test -v ./...` (Go 1.27): `TestOpErr` -> `msg="insert user: dial: connection refused" chain=[*fmt.wrapError *net.OpError syscall.Errno]`; `TestKinds` -> `attr err kind=Any`, `attr lv kind=LogValuer` (slog does not resolve `LogValuer` before `Handle`, so the wrapper calls `Value.Resolve()`).
- **Suggested action:** none beyond the applied changes; keep both probe cases as RED rows in tasks 8.1 and 7.7.

### AfterDrain hooks share one budget, so a blocking hook leaves later hooks a spent context
- **Status:** open
- **Target:** unassigned
- **Context:** Design D9 gives the `AfterDrain` phase one fresh context bounded by `ShutdownHookTimeout`, shared by all its hooks, and srvr stops waiting at the budget. After a hook that blocks past the budget, every later `AfterDrain` hook is still called, but with an already-done context, and srvr does not wait for it; a flush that checks its context then fails immediately. The Faulty hooks row "the second hook ran" holds (the hook is called), but the second hook can finish after `Start` returned. Per-hook budgets would avoid this but make the total stop time `n x ShutdownHookTimeout`, which breaks the "15s + 5s fits a 30s grace period" default.
- **Evidence:** `hooks_faulty_test.go` `TestShutdownHooks_Faulty/blocks_and_ignores_its_context` failed 5 of 60 runs (`go test -race -count=20 ./...`, three runs) with `the second hook did not run` when it asserted completion right after `Start` returned; it now waits for the call (`eventually`), matching D9.
- **Suggested action:** Owner decides whether to keep one shared phase budget (documented on `WithShutdownHook`) or give each hook a slice of it; no change needed for core-server as designed.

### Shutdown waits up to 5s for a connection that never sent a request
- **Status:** open
- **Target:** unassigned
- **Context:** `http.Server.Shutdown` treats a connection in `StateNew` (accepted, no request read yet) as active for 5 seconds before closing it as idle. A client that opens a connection and sends nothing (a pre-connecting proxy, a speculative dial of `http.Transport` after a response body was closed unread) therefore delays every graceful drain by up to 5s, inside `ShutdownTimeout`. srvr inherits this from net/http; it is not specified anywhere in core-server.
- **Evidence:** unverified as a srvr defect (stdlib behaviour, `net/http/server.go` `closeIdleConns`); observed while writing tests: `TestUnmatched_Redirect` took 6.42s and `TestPanicRecovery/nil` 5.89s while their clients closed response bodies unread, and 0.00s once the bodies were read (`go test -count=1 -v ./...`).
- **Suggested action:** Owner decides whether to document it on `Config.ShutdownTimeout` (and the Kubernetes guidance) or to close `StateNew` connections at the start of the drain via `http.Server.ConnState` tracking; add a scenario either way.

### A panicking consumer error hook is contained silently
- **Status:** open (deferred at the end of core-server: no spec scenario asks for a record, and a new `srvr.problem.note` event would be an unspecified schema addition; the access record is still written, see `TestAccessRecord_Deferred`)
- **Target:** unassigned
- **Context:** `WithErrorHook`'s hook runs under `recover` in srvr's prblm `OnError` (design D4), and the panic is discarded, so a broken consumer hook never shows up in triage. No spec row asks for a record of it.
- **Evidence:** unverified as a defect: behaviour per D4 (`problems.go` `onError`); raised by the slice 5 /simplify altitude review.
- **Suggested action:** When per-request state and logging land (slices 6-7), write a `WARN` `srvr.problem.note` record for a panicking consumer hook, or record the decision not to.

### Correlation group replay cost (task 6.9)
- **Status:** closed: measured, within budget
- **Target:** core-server
- **Context:** Design D7 replays a logger's `WithAttrs`/`WithGroup` steps per record after adding `trace_id`/`span_id`; the task asked for per-trace caching if grouped records cost more than twice ungrouped ones.
- **Evidence:** `correlation_bench_test.go` `BenchmarkCorrelation` (`go test -run '^$' -bench BenchmarkCorrelation -benchmem .`, Go 1.27.4, Apple M-series, 14 cores): ungrouped 649.6 ns/op, 400 B/op, 7 allocs/op; grouped (`With("table","users").WithGroup("db")`) 848.4 ns/op, 880 B/op, 13 allocs/op - 1.31x the time, 2.2x the bytes.
- **Suggested action:** none; time stays under 2x, so per-trace caching is not needed. Re-measured after slice 7 added truncation and failure containment to the same handler: ungrouped 784.8 ns/op, 752 B/op, 9 allocs/op; grouped 986.4 ns/op, 1281 B/op, 15 allocs/op (1.26x); the 2 extra allocations come from the attribute scan closure that decides whether a record needs cutting.

### Task 7.7 completes with 8.1 (plan ordering)
- **Status:** closed: applied during apply
- **Target:** core-server
- **Context:** Task 7.7 includes structured-logging "Scenario: Error attributes keep their own limits", whose rows assert `exception.message` on the failed record; that attribute only exists from task 8.1. Slice 7 shipped the mechanism (srvr-owned text bypasses `MaxAttrBytes`) and every other 7.7 row; the two own-limit rows are tested with 8.1, and 7.7 is checked then.
- **Evidence:** planning order in `tasks.md` (7.7 before 8.1); no defect.
- **Suggested action:** none.

### A cyclic error chain hangs prblm's resolution (errors.Is never returns)
- **Status:** closed: fixed in core-server (task 8.6)
- **Target:** core-server
- **Context:** An error whose `Unwrap` returns itself (triage-error-records Hostile error values, "an error whose `Unwrap()` returns itself") made the request hang: prblm resolves errors with `errors.Is`/`errors.As`, which loop forever on a cycle, so no response and no record were ever written. srvr now checks a returned error's chain for a cycle or a panicking `Unwrap` (`hasCycle`, bounded at 4096 errors) and hands prblm an opaque stand-in that renders the safe 500, while the access record still describes the original error.
- **Evidence:** `triage_test.go` `TestTriage_Hostile/self_unwrap` (`go test -count=1 -timeout 40s -run TestTriage .`) failed with `panic: test timed out after 40s ... running tests: TestTriage_Hostile/self_unwrap (40s)` before the fix and passes after it.
- **Suggested action:** gin-and-fiber-engines must apply the same guard in its adapters (move `guardChain` into `srvr/engine`); also ask prblm to guard `Resolve` against cyclic chains itself (stdlib `errors.Is` has no cycle check). Follow-up target: gin-and-fiber-engines.

### Stack capture at error creation
- **Status:** open
- **Target:** unassigned
- **Context:** Returned errors carry no stack in Go; srvr records the handler's code location instead (design D8). Capturing a stack where an error is created would need a wrapping helper or an opt-in and adds cost on every error.
- **Evidence:** unverified: deliberate scope decision of the proposal (Scope by Gall's Law).
- **Suggested action:** Decide in a later change whether to offer an opt-in error wrapper that captures a stack, and how it maps to `exception.stacktrace`.

### TLS termination
- **Status:** open
- **Target:** unassigned
- **Context:** core-server serves plain HTTP; TLS is expected to terminate at a proxy or mesh. `WithListener` accepts a `tls.Listener` today, but there is no `TLSConfig` option, certificate reload or HTTPS-specific default.
- **Evidence:** unverified: deliberate scope decision of the proposal.
- **Suggested action:** Plan a change for `TLSConfig`/certificate files with safe defaults (TLS 1.2+, modern ciphers) if consumers need in-process TLS.

### HTTP/2 cleartext (h2c)
- **Status:** open
- **Target:** unassigned
- **Context:** Without TLS, net/http serves HTTP/1.1 only; gRPC-style clients behind a mesh may want h2c. Go 1.24+ supports it through `http.Server.Protocols`.
- **Evidence:** unverified: deliberate scope decision of the proposal.
- **Suggested action:** Add an opt-in `Protocols`/`EnableH2C` setting in a later change.

### Health endpoints
- **Status:** open
- **Target:** unassigned
- **Context:** srvr has `Ready()` for in-process readiness but no `/healthz`/`/readyz` handlers; consumers register their own routes (and can skip them in the access log with `SkipPaths`).
- **Evidence:** unverified: deliberate scope decision of the proposal.
- **Suggested action:** Decide whether srvr should offer opt-in liveness/readiness routes tied to `Ready()` and the stop sequence (readiness failing as draining begins).

### The cycle check crashed on an unhashable error value
- **Status:** closed: fixed in core-server (slice 8 /simplify review)
- **Target:** core-server
- **Context:** `hasCycle` used every error with a comparable type as a map key; an error struct holding a slice in an interface field is comparable by type but panics when hashed, so the guard itself would have turned a returned error into a panic record.
- **Evidence:** `triage_internal_test.go` `TestHasCycle_Unhashable` (`go test -count=1 -run TestHasCycle_Unhashable .`) failed with `panic: hash of unhashable type: []int` before the fix and passes after it.
- **Suggested action:** none.

### Verification findings folded into the plan (task group 9)
- **Status:** closed: applied to the plan; implemented by task group 9
- **Target:** core-server
- **Context:** The `core-server` verification raised C1 (a consumer handler's `Enabled`/`WithAttrs`/`WithGroup` panic kills the response), C2 (logging a typed-nil or `Error()`-panicking error fails the request), W1-W4 and S1-S6. Owner decisions: W1 - no `http.request.failed` record is ever suppressed, and recovered panics and late errors that would have been 5xx are logged at `ERROR` after the response started (the plan applies the same rule to a late 4xx: the higher level of the status sent and the status the error would have had, so a late `ErrConflict` after `201` is `WARN`); W3 - design D4 follows the code (rendering notes are dropped; the first failure with a problem is kept, a problem-less panic gives way to a later error); S4 - `LogWriteErrors()` stays process-wide and says so; S5 - a `WithListener` listener is owned by srvr and closed on every `Start` failure (the plan extends this to every path where the server closes without serving, including `Shutdown` before `Start`; `Start(nil)` and misuse errors leave it open). W2 (D5 wording) was re-checked with a probe on Go 1.27.4: `ServeMux.Handler` reports `/dir/` for `/dir` and `/a/../dir/` (307), `""` for `//nope` (307 to `/nope`), `/nope` (404) and a wrong method (405).
- **Evidence:** reproductions kept at the verification scratchpad `kept_copy.go.txt`: `TestScratch_OwnHandlerFaultyMethods` (`Get ... EOF`), `TestScratch_HostileLoggedError` (status 500), `TestScratch_LatePanicRecord` (level `INFO`, and no record with `WithAccessLogDisabled`), `TestScratch_ListenerOwnership` (listener still accepts connections after `Start` failed); D5 probe output recorded above.
- **Suggested action:** none beyond task group 9.

### Tasks completed without a real RED step (S1)
- **Status:** closed: recorded
- **Target:** core-server
- **Context:** Tasks 2.5, 3.3, 3.7, 3.9, 3.10 (partially), 5.2 and 5.3 were checked off with tests written alongside or after the code they cover, so no failing run was observed for them as AGENTS.md rule 1 requires. Mutation checks run during the verification confirmed that the tests covering these tasks fail when the code they cover is broken, so they do catch regressions. `TestShutdown_Drain` could hang until the 60s test timeout on a regression; task 9.9 bounds every wait.
- **Evidence:** verification report (mutation checks); no defect.
- **Suggested action:** none; future tasks keep the RED step.

### A srvr.problem.note event for notes and contained hook panics
- **Status:** open
- **Target:** unassigned
- **Context:** Design D4 originally said prblm notes with no problem would be logged at `WARN` as `srvr.problem.note`; the code drops rendering notes (`ErrInvalidStatus`, `ErrInvalidType`, `ErrUndocumentedType`, `ErrEncoding`, `ErrShadowedDocumentation`) and D4 now says so (owner decision W3). These notes point at consumer configuration mistakes (an invalid status mapping, an undocumented type) that nothing reports today; the entry "A panicking consumer error hook is contained silently" has the same gap. A new event is an additive log schema change (no `srvr.log.schema` bump).
- **Evidence:** unverified as a defect: behaviour per revised D4 (`access.go` `recordFailure`, `renderingNotes`).
- **Suggested action:** Owner decides whether to add a `WARN` `srvr.problem.note` record (once per note kind per process, to avoid flooding) for rendering notes and contained hook panics, with a spec scenario and a golden-record row.

### Start's "Serve returned because the stop closed the listener" branch is covered only by timing
- **Status:** open
- **Target:** unassigned
- **Context:** In `server.go` `Start`, when `http.Server.Serve` returns and the stop sequence has already begun, `served` is reset to nil (`case <-r.stopping: served = nil`, about line 183). Whether a test reaches it depends on which `select` case wins, so `go tool cover -func` reports `Start` at 96% on some runs. Found while verifying task group 9; not part of its findings, so not changed.
- **Evidence:** unverified as a defect (coverage only): `GOWORK=off go test -count=1 -coverprofile=c.out ./...` listed `server.go:183.4,183.16 1 0`.
- **Suggested action:** Decide whether to make the branch deterministic (a seam closing the listener before `Serve` returns, or restructuring so the stop sequence's own error result is the only source) and add a test, or accept the timing-dependent coverage.

### Second verification findings folded into the plan (task group 10)
- **Status:** closed: applied to the plan; implemented by task group 10
- **Target:** core-server
- **Context:** The second `core-server` verification raised C1 (`runHooks` calls a hook error's `Error()` unguarded in the stopping caller's goroutine, so a typed-nil or `Error()`-panicking hook error panics out of `Shutdown` or `Start` and every later `Shutdown` hangs), W1 (a handler writing only `103` logs `http.response.status_code` `103` although the client receives `200`), S1 (the `WithListener` wording "leaves ln open" for `ErrServerClosed`, when the listener was already closed) and S2 (ownership starts once `New` returns a server). Owner decisions kept and now stated explicitly in the specs: a late 4xx is logged at the higher of the two levels; `Shutdown` before `Start` closes the `WithListener` listener.
- **Evidence:** RED tests of task group 10 (10.1-10.3).
- **Suggested action:** none beyond task group 10.

### One unreproduced failure of the core test suite
- **Status:** closed: cause found and fixed in core-server (task group 14)
- **Target:** core-server
- **Context:** While verifying task group 10, one run of `GOWORK=off go test -count=1 -coverprofile=c.out ./...` (without `-race`) reported `FAIL github.com/kartaladev/srvr`; the output was cut by a `tail` filter, so the failing test is unknown. Another project's test binary was running on the machine at the time. 27 later full runs of the package (12 `-coverprofile ./...`, 15 `-coverprofile .`), 6 `-race` runs and `-count=50` of the group 10 tests all passed.
- **Evidence:** unverified: not reproduced; no failing test name. Task 11.4 ran the full suite without `-race` 10 more times (`GOWORK=off go test -count=1 -json ./...`, foreground, output kept as `flake-run-1.json` to `flake-run-10.json` in the session scratchpad); all 10 passed, while another project's test binaries were running on the machine. Still unconfirmed.
- **Suggested action:** If a srvr test fails again, keep the full output (`go test -json`), name the test, and reproduce it with a targeted `-run` and a high `-count` before treating it as a defect. Task group 13 (2026-11-17): one more unnamed failure. A foreground `GOWORK=off go test -count=1 -race -run 'TestListener|TestStart_|TestShutdown' .` run printed `FAIL`, but a `tail` filter cut the test name (same mistake as before). It did not come back in 4 reruns of that command with `-json`, in `-count=50` of each of `TestListener_AcceptPanics`, `TestListener_ClosePanics` and `TestMiddleware_ConstructorPanics`, or in 2 full `-race -json ./...` runs and 1 `-json -coverprofile ./...` run. Unconfirmed; the lesson stands: always use `-json` and no output filter on the first run. Resolved 2026-11-17: `GOWORK=off go test -race -count=100 -run 'TestListener_AcceptPanics|TestListener_ClosePanics|TestGuardedListener_AcceptAllocs|TestMiddleware_ConstructorPanics' .` failed 3 times with `consumerfault_test.go:230: Shutdown on a running server = closing the listener panicked: close, want <nil>`. Cause: the listener wrapper returned its stored first `Close` error again to `http.Server.Shutdown`, which reports it when it still tracks the listener (`Serve` not yet returned), so `drain` saw a forced drain. Reproduced deterministically enough by `TestListener_CloseErrors` (task 14.1); fixed by task 14.1's GREEN step. The earlier unnamed failures fit the same race in `TestListener_ClosePanics` (group 13) but cannot be confirmed after the fact.

### Lifecycle records do not pass exception.message through the error redactor
- **Status:** closed: owner decided 2026-11-17 to redact and limit lifecycle error text; planned as task group 11
- **Target:** core-server
- **Context:** Found by the /simplify altitude review of task group 10. Request failure records build `exception.message` through `errorText`, `WithErrorRedactor` and the 8 KiB message limit (`triage.go` `errorAttrs`); `server.shutdown_hook.failed` (`hooks.go` `runHooks`) and `server.failed` (`server.go` `fail`) put `errorText(err)` in a plain string attribute, so the redactor is skipped and the general `MaxAttrBytes` limit applies. `LOGGING.md` documents the redactor only in the request-event table, so whether it should apply to lifecycle records is not settled; hook errors are consumer text (for example a database close error with a connection string).
- **Evidence:** probe `TestZZProbeHookRedaction` (not kept: the fix is outside task group 10) with `WithErrorRedactor` replacing `secret` and an `AfterDrain` hook returning `errors.New("close db: secret")`, then `Shutdown` before `Start`; `GOWORK=off go test -count=1 -run TestZZProbeHookRedaction .` failed with `unredacted: close db: secret`.
- **Suggested action:** Owner decides whether lifecycle `exception.message` is redacted and limited like request records. If yes, add a spec row (server-lifecycle / Lifecycle events are logged), restore the probe as the RED test, and route all three sites through one helper (for example `(*Server).exceptionMessage(err) slog.Attr` in `triage.go`) so no call site builds the attribute by hand; document it in `LOGGING.md`.
- **Decision:** the owner decided that lifecycle records (`server.shutdown_hook.failed`, `server.failed` and any other srvr-owned lifecycle record carrying consumer error text) pass `exception.message` through `WithErrorRedactor`, contain a panicking redactor like the request path (`[redaction failed]`) and apply the same 8 KiB limit. Applied: server-lifecycle `Requirement: Lifecycle error text is redacted and limited` with its scenario, triage-error-records redactor requirement, design D8/D9, `LOGGING.md` lifecycle section, task group 11 (the probe is recreated as its RED test). No `srvr.log.schema` bump: no key is renamed or retyped.

### Third verification findings folded into the plan (task group 12)
- **Status:** closed: applied to the plan; implemented by task group 12
- **Target:** core-server
- **Context:** The third `core-server` verification raised C1 (a typed-nil `WithListener` listener, or one whose `Addr()` returns nil, makes `serve` panic while holding the state lock, so every later `Shutdown`, `Addr` or `Register` blocks; with an address it panics inside `New`), W1 (a value whose `Error()` panics with itself escapes `fmt`'s one-level guard in `runHook`, `runHooks` and `safeConfigure`) and S1 (with a negative `MaxAttrBytes` a consumer handler receives lifecycle `exception.message` as `slog.KindAny` wrapping the unexported `ownText`; regression from 5bd8e34), plus an unconfirmed documentation point (redactors and log handlers run synchronously). Owner decisions: an unusable listener address fails `Start` through `fail` and the listener is closed under the ownership rules (AGENTS rule 7); no srvr lock is held across consumer code. Planning probes found three more sites of the W1 class: `newProblems` (`fmt.Sprint` of a panicking prblm option's value), `validateListener` (`%v` of the listener's address) and `runHook`'s abandoned error (`%w` of the hook context's `Err()`, which for `BeforeDrain` comes from the consumer's `Shutdown` context). Task 10.2's audit note is corrected.
- **Evidence:** probes run with `GOWORK=off go test -count=1 -run TestProbe<name> -v .` (sources kept in the session scratchpad as `zz_probe_test.go.txt`, `zz_probe2_test.go.txt`, `zz_probe3_test.go.txt`, not in the repository): `HookPanic` `fatal error: panic while printing panic value: type srvr_test.selfPanic`; `HookErr`, `Configurer`, `PrblmOption`, `ListenerAddrCombined` each `recovered: true` (the call panicked); `TypedNilListener` and `NilAddr` (nil, panicking `Addr()`, panicking `String()`) `Start recovered: true` then `Shutdown HANG`; `ShutdownCtxErr` `Shutdown recovered: true` then `Shutdown2 HANG`; `S1` kind `Any`. A handler that panics with, or returns, such a value answered `500` with a complete failed record (prblm and srvr's request path are not affected).
- **Suggested action:** none beyond task group 12.

### A middleware constructor that panics during Start leaves the server unable to stop
- **Status:** closed: owner decided 2026-11-17 to contain it like a configurer panic; planned as task group 13
- **Target:** core-server
- **Context:** `installRoutes` (`group.go`) calls each middleware constructor through `chain` (`mw(h)`) during `Start` without a `recover`, unlike `Configure` (`safeConfigure`). A middleware whose constructor panics makes `Start` panic after `begin` moved the server to starting; nothing closes it, so a later `Shutdown` waits until its context ends. Found while planning task group 12; not one of the third verification's findings (it is panic containment, not the formatting class), so not changed.
- **Evidence:** probe `TestProbeMiddlewarePanicAtStart` (`zz_probe_test.go.txt` in the session scratchpad), `GOWORK=off go test -count=1 -run TestProbeMiddlewarePanicAtStart -v .`: `recovered: mw`, then `shutdown: context deadline exceeded` (1s deadline).
- **Suggested action:** Add a route-registration row (Faulty middleware: "panics while being applied at Start" -> `Start` returns an error wrapping `ErrRouteConfig` naming the route and configurer, with the panic value through `valueText`; a later `Shutdown` returns `nil`), recover in `chain` and route the failure through `fail`.
- **Decision:** the owner decided that a middleware constructor panicking during `Start` is contained the same way as a configurer panic: `Start` returns an `ErrRouteConfig` error with the panic value through `valueText`, the server ends closed, a passed listener is closed under the ownership rules, and a later `Shutdown` returns `nil` promptly; every other consumer callback invoked during `Start`/configure is checked for the same gap, each with a test. Applied: route-registration Requirement: A middleware constructor that panics fails Start, design D3, task 13.1.

### A listener whose Close panics leaves the server unable to stop; one whose Accept panics crashes the process
- **Status:** closed: owner decided 2026-11-17 to wrap the passed listener; planned as task group 13
- **Target:** core-server
- **Context:** `closeUnserved` and `stopRun` (`server.go`) call the listener's `Close` without a `recover`; a panic there escapes before `finish`, so the server never closes and every later `Shutdown` blocks. `http.Server.Serve` calls `Accept` on its own goroutine without a `recover`, so a panicking `Accept` kills the process. Both are faulty consumer listeners like task group 12's `Addr()` case, but outside the third verification's findings, so not changed. The `sync.Once` in `onceCloseListener` was kept (task 12.2's lock audit).
- **Evidence:** probes `TestProbeClosePanic` (`Shutdown before Start recovered: true`, then `Shutdown again HANG`) and `TestProbeAcceptPanic` (`panic: accept`, `FAIL github.com/kartaladev/srvr`), `zz_probe3_test.go.txt` in the session scratchpad.
- **Suggested action:** Owner decides the contract: guard `Close` like a `Close` error (ignored, the server still closes) and wrap the served listener so a panicking `Accept` becomes an error returned by `Serve` (then `Start` returns it, wrapping `ErrConfig` or a new sentinel); add spec rows for both.
- **Decision:** the owner decided that srvr wraps the passed listener so a panic in `Accept`, `Close` or `Addr` becomes an error. An `Accept` panic stops serving and `Start` returns it (wrapping `ErrConfig` naming `Listener`, the panic value through `valueText`) with a `server.failed` record; because the server was running, it stops in order (drain included) rather than through `fail`'s served-nothing path. A `Close` panic is treated like a `Close` error, which srvr ignores: the stop completes and the stopping call returns what it returns for a successful `Close` (`nil` when hooks and drain succeed), with no record. The wrapper adds only a deferred `recover` per `Accept`; connections are not wrapped. Applied: server-lifecycle Requirements: A passed listener whose Accept panics stops serving, and A passed listener whose Close panics never blocks the stop, the Lifecycle records row, design D1, tasks 13.3 and 13.4.

### WithLogger accepts a Logger without a handler and drops every record
- **Status:** closed: owner decided 2026-11-17 to reject it in New; planned as task group 13
- **Target:** unassigned
- **Context:** `WithLogger(&slog.Logger{})` passes the `l == nil` check, but `l.Handler()` is nil, so every call srvr makes to the base handler panics, is counted in `LogWriteErrors()` and the record is skipped: the server logs nothing, silently (AGENTS rule 7 asks construction to fail instead). Found while planning task group 12.
- **Evidence:** probe `TestProbeZeroLogger` (`zz_probe2_test.go.txt` in the session scratchpad): `New err: <nil>`.
- **Suggested action:** Reject a Logger whose `Handler()` is nil or a typed nil in `WithLogger` with `ErrConfig` naming `Logger`; add a row to structured-logging / Own handler.
- **Decision:** the owner decided that `WithLogger` rejects a Logger whose `Handler()` is nil or a typed nil with `ErrConfig` naming `Logger` (AGENTS rule 7). Applied: server-lifecycle Construction requirement and two rejected rows in Scenario: Valid and invalid configuration, design D7, task 13.2.

### A Start or Shutdown context whose methods panic leaves the server unable to stop
- **Status:** open
- **Target:** unassigned
- **Context:** Found by task 13.1's audit of consumer callbacks invoked during `Start`. srvr guards the text of a context's `Err()` value (`errorText`), but calls the consumer context's own methods (`Err`, `Done`, `Value`, through `Start`, `context.WithoutCancel`, `net.ListenConfig.Listen` and the log handler) without `recover`. A `Start` context whose `Err()` panics makes `Start` panic after `begin` moved the server to starting; nothing closes it, so a later `Shutdown` waits until its own context ends. The same likely holds for `Done()` and `Value()` and for the `Shutdown` context (not probed). Not one of the three owner decisions of task group 13, so not changed.
- **Evidence:** probe `TestProbeStartCtxErrPanics` (source kept as `zz_probe_ctx_test.go.txt` in the session scratchpad, not in the repository), `GOWORK=off go test -count=1 -run TestProbeStartCtxErrPanics -v .`: `Start: panicked: err`, then `Shutdown: context deadline exceeded` (1s deadline).
- **Suggested action:** Owner decides whether srvr contains a context whose methods panic (a `context.Context` that panics breaks every stdlib API it is passed to, so documenting it as unsupported is a reasonable answer); if contained, guard `Start`'s first `ctx.Err()` and route a panic through `fail`, and add rows to server-lifecycle / Scenario: Start fails.

### A connection from a WithListener listener whose methods panic crashes the process
- **Status:** open
- **Target:** unassigned
- **Context:** Task group 13 wraps only the passed listener's `Accept` and `Close`. Owner decision: connections are not wrapped, so nothing is added per connection. net/http calls an accepted connection's `Read` on a background goroutine (`(*connReader).backgroundRead`, `net/http/server.go:742` on Go 1.27.4) with no `recover`. A `net.Conn` from a consumer listener that panics in `Read` therefore ends the process. Panics in `c.serve`'s own goroutine are recovered by net/http. Not changed: wrapping every connection would add a per-connection allocation and per-call defers, against the decision.
- **Evidence:** probe `TestProbeConnReadPanics` (source kept as `zz_probe_conn_test.go.txt` in the session scratchpad, not in the repository), `GOWORK=off go test -count=1 -run TestProbeConnReadPanics -v .`: `panic: read` from `net/http.(*connReader).backgroundRead`, `FAIL github.com/kartaladev/srvr`.
- **Suggested action:** Owner decides between documenting it on `WithListener` (connections are the listener's responsibility, as with any `http.Server`) and an opt-in connection guard. The default should stay allocation-free.

### Errors and panics from a passed listener's Close are not logged
- **Status:** open
- **Target:** unassigned
- **Context:** Owner decision for task group 13: a panicking `Close` is handled like a `Close` error. srvr ignores both on every path (`closeUnserved`, `stopRun`, http.Server's own close), so no record is written. A consumer cannot see that its listener failed to close. Correction (task group 14): the claim that the stop result never changes did not hold. The wrapper kept the first `Close` error and returned it to every later caller; `http.Server.Shutdown` closes the listeners it still tracks and returns that error, so when `Serve` had not yet returned, `drain` treated it as a forced drain, `Shutdown` returned the listener's text and `server.stopped` was written at `WARN` with `srvr.drain.forced`. Task group 14 makes the wrapper's `Close` return nil to every caller, so the result now really is unchanged (design D1).
- **Evidence:** specified behaviour: server-lifecycle / Scenario: A listener whose Close panics (`TestListener_ClosePanics`) and Scenario: A listener whose Close returns an error (`TestListener_CloseErrors`, task 14.1, which failed before the fix).
- **Suggested action:** If operators need it, add a `WARN` lifecycle event (for example `server.listener.close_failed` with `exception.message`, built by the lifecycle error-text helper). It is a schema addition, so no `srvr.log.schema` bump.
