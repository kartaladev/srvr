# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.


### Gin handler signature contradicts config.yaml
- **Status:** closed: user accepted plain gin.HandlerFunc with c.Error (planning, 2026-11-17); config.yaml updated
- **Target:** gin-and-fiber-engines
- **Context:** Specs keep plain gin.HandlerFunc reporting errors through c.Error (matches the user's original sketch); openspec/config.yaml says handlers return error on every engine.
- **Evidence:** unverified: planning-time inconsistency found when the five changes were drafted in parallel
- **Suggested action:** Apply the user's decision on handler signature to specs and config.yaml before slice work.

### Public srvr/engine package is new core API
- **Status:** closed: user accepted public srvr/engine, unstable before v1 (planning, 2026-11-17); integration of later changes tracked in their own notes.md
- **Target:** gin-and-fiber-engines
- **Context:** Separate modules cannot import core internal/, so shared pipeline pieces become public srvr/engine. Later changes (observer, security layer, debug buffer) were planned without knowing this package.
- **Evidence:** unverified: planning-time inconsistency found when the five changes were drafted in parallel
- **Suggested action:** Confirm the package name and surface; make sure otel-observability, scrty-integration and triage-debug-buffer plug into it.

### Gin prints [GIN-debug] lines to stdout in its default mode
- **Status:** adopted by gin-and-fiber-engines (owner decision D19, 2026-11-17: one `server.config.warning` record with `srvr.warning.code` `gin_debug_mode` at construction; gin's globals untouched; gin-engine "Gin debug mode is reported", design D2/D9, task 3.10)
- **Target:** gin-and-fiber-engines
- **Context:** gin's mode is process-global (gin v1.12.0 `mode.go:48`, `debug.go:22-42`); there is no per-engine release mode, and `DebugPrintRouteFunc` is a package variable. Since srvr never calls `gin.SetMode` (owner decision), a zero-config ginsrvr in debug mode prints gin's warning and one line per route to stdout, mixed into srvr's JSON log stream. The audit removed the proposal/design claims of "release mode on srvr's engine".
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/gin/main_test.go` `TestDebugOutputAndDefaults`, `go test -count=1 -v -run TestDebugOutputAndDefaults .`: `mode: debug`, `[GIN-debug] [WARNING] Running in "debug" mode...`, `[GIN-debug] GET /x --> ... (1 handlers)`
- **Suggested action:** Owner decides: accept and document `GIN_MODE=release`; or log one `WARN` record at construction when `gin.IsDebugging()`; or (global, rejected so far) set `gin.DebugPrintRouteFunc`/`gin.DefaultWriter`.

### Fiber header limit and header timeout differ from MaxHeaderBytes and ReadHeaderTimeout
- **Status:** adopted by gin-and-fiber-engines (owner decision D20, 2026-11-17: `ReadBufferSize = min(MaxHeaderBytes, 65536)`, `fiber_header_limit_capped` warning for a consumer-set value above the cap; `ReadHeaderTimeout` and the cap are engine-parity exceptions; fiber-engine "Fiber header limit is derived and capped", task 4.12)
- **Target:** gin-and-fiber-engines
- **Context:** fasthttp has no header-read timeout (no such field in fasthttp v1.78.0 `server.go`), and a request header must fit `ReadBufferSize` (fiber default 8192, `app.go:662`), answered with 431. srvr's defaults (`ReadHeaderTimeout` 10s, `MaxHeaderBytes` 1 MiB) cannot map one to one; `ReadBufferSize` = 1 MiB would allocate that per connection. The proposal now flags this as pending.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/fib/real_test.go` `TestReal`, `go test -count=1 -v -run TestReal .`: `9000B header: 418 "EH:Request Header Fields Too Large ..."` with default config
- **Suggested action:** Owner decides per setting: map `MaxHeaderBytes` to `ReadBufferSize` (memory cost), cap it, or list both as engine-parity exceptions with spec rows; same for `ReadHeaderTimeout` (fasthttp `ReadTimeout` covers header and body).

### Fiber accepts duplicate and conflicting routes silently
- **Status:** adopted by gin-and-fiber-engines (owner decision D21, 2026-11-17: detect through `app.GetRoutes(true)` with normalised parameter names and fail `Start` with `srvr.ErrRouteConfig`; fiber-engine registration rows, design D3/D8, tasks 4.3 and 6.2)
- **Target:** gin-and-fiber-engines
- **Context:** core-server fails `Start` with `srvr.ErrRouteConfig` for conflicting patterns and gin panics on them, but fiber v3.7.0 registers `GET /users/:id`, `GET /users/:name` and a second `GET /users/:id` without error; the first wins. fiber-engine has no row for this.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/fib/main_test.go` `TestDupAndNil`, `go test -count=1 -v -run TestDupAndNil .`: `dup served body: a`, `dup panic: <nil>`
- **Suggested action:** Owner decides: detect duplicates after the configure phase from `app.GetRoutes()` (normalising parameter names) and return `srvr.ErrRouteConfig`, or add an engine-parity exception row.

### ginprblm reports status-only aborts to OnError with an unexported error
- **Status:** closed: tracked in core-server notes.md (one prblm issue, D26)
- **Target:** unassigned
- **Context:** `ginprblm.Middleware` turns `c.AbortWithStatus(401)` into a problem and calls the `OnError` hook with `errStatusOnly` (unexported, ginprblm `middleware.go`). srvr cannot match it with `errors.Is`, so classifying failed requests from hook calls would log `http.request.failed` where gin-engine expects `http.request.completed`. design D2 now classifies from `c.Errors` and recovered panics instead.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/gin/hook_test.go` `TestHookOnStatusOnly`, `go test -count=1 -v -run TestHookOnStatusOnly .`: `OnError called: err="ginprblm: response aborted with an error status and no error" type=*prblm.statusError`
- **Suggested action:** Ask prblm to export the sentinel (for example `ginprblm.ErrStatusOnly`) so the hook can tell it apart; until then keep the `c.Errors`-based classification and its test. Owner decision D26 (2026-11-17): one prblm issue covers this and the composable `OnError` request; core-server's notes.md holds the combined entry. This entry stays open until prblm ships the sentinel.

### Gin Allow order and trailing-slash redirects differ from net/http
- **Status:** adopted by gin-and-fiber-engines (owner decision D22, 2026-11-17: `Allow` sorted, `RedirectTrailingSlash` off and re-asserted after `WithEngine`; gin-engine unmatched and hook rows, design D2, tasks 3.5 and 3.12)
- **Target:** gin-and-fiber-engines
- **Context:** gin builds `Allow` in the order method trees were created, not sorted (`gin.go:738-753`), and `gin.New()` has `RedirectTrailingSlash` on, answering `GET /users/` with a `301` and an HTML body instead of a problem. Neither is pinned by engine-parity.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/gin/main_test.go` `TestUnmatched`, `go test -count=1 -v -run TestUnmatched .`: `DELETE /users -> 405 ... allow="POST, GET"` (POST registered first), `GET /users/ -> 301 ct="text/html; charset=utf-8" loc="/users"`
- **Suggested action:** Owner decides: sort `Allow` and disable `RedirectTrailingSlash` (re-asserted like the other safety settings), or add both as engine-parity exception rows.

### Fiber answers unknown methods before any middleware
- **Status:** adopted by gin-and-fiber-engines (owner decision D23, 2026-11-17: accepted as an engine-parity exception, no `RequestMethods` override; engine-parity row, design D3, task 5.4)
- **Target:** gin-and-fiber-engines
- **Context:** A method fiber does not know (for example `FOO`) gets a plain-text `501` from `defaultRequestHandler` (`router.go:1016-1020`) before middleware runs: no problem, no access record, no trace. Added as an engine-parity exception row; whether to accept it is a decision.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/fib/real_test.go` `TestReal`: `FOO method: 501 ct="text/plain; charset=utf-8" "Not Implemented" mwRan=0`
- **Suggested action:** Owner accepts the exception, or srvr sets `fiber.Config.RequestMethods` so unknown methods reach the chain and get a 405/501 problem.

### Adopted: otel-observability carry-over on Fiber route and status after Next
- **Status:** adopted by gin-and-fiber-engines
- **Target:** gin-and-fiber-engines
- **Context:** The open entry "Fiber unmatched route reads as "/" from middleware" in `openspec/changes/otel-observability/notes.md` targets this change. design D4 now reads the route only when `c.Matched()` and has the access middleware call the error handler; fiber-engine route rows omit `http.route` when unmatched. That entry's status lives in another change's file and was not edited here.
- **Evidence:** probe `/private/tmp/claude-501/-Users-zakyalvan-Documents-RND-srvr/ae329126-0c37-4115-98e4-3f992b4b4d3b/scratchpad/probes/fib/main_test.go` `TestRouteAfterNext`: `GET /nope -> resp=404 | mw: route="/" matched=false statusInMW=200`, `DELETE /users/42 -> resp=405 | mw: route="/users" matched=false statusInMW=200`
- **Suggested action:** Task 1.2 marks the otel-observability entry `adopted by gin-and-fiber-engines`.

### Planning audit 2026-11-17 decisions
- **Status:** closed: applied to proposal, specs, design and tasks during planning (2026-11-17)
- **Target:** gin-and-fiber-engines
- **Context:** The cross-change audit and the owner's decisions D8, D13 and D19-D23 changed this change. Applied: unmatched 404/405 logged as `http.request.failed` at `WARN` on every engine with parity rows (D8); config-struct entry points `srvr.WithConfig`, `WithLogConfig`, `WithAccessLogConfig`, `WithServiceConfig` applied in argument order (D13); D19-D23 as recorded on their entries. Audit gaps closed here because scrty-integration depends on them (Gall's Law): gin body limit through `http.MaxBytesReader` (design D2 no longer claims gin inherits it from `http.Server`); route listing `engine.Routes` with configurer attribution and `srvr.WithRouteCheck` (design D8, slice 6). One per-request holder `engine.RequestState` / `engine.StateFrom(ctx)` (design D7); this change adds route, matched, status, failure and trace context, and otel-observability and scrty-integration add their fields (trace source, client address, principal) to the same struct. New `server.config.warning` event (design D9), additive under the `srvr.log.schema` policy. Parity row corrected: gin, not net/http, is the engine whose `Allow` lacks `HEAD`. Consolidation task 1.5 added (D25); module setup was already the first task of each slice.
- **Evidence:** unverified: planning decisions; earlier probes are cited on the individual entries
- **Suggested action:** none; consolidation 1.3 re-checks these against merged core-server code (names of the config entry points and the request-state fields).

### Planning audit 2 2026-11-17: applied fixes
- **Status:** closed: applied during planning
- **Target:** gin-and-fiber-engines
- **Context:** The second fresh audit and the owner's A1-A4 defaults changed this change. Applied: option rows use `WithLogLevel(slog.LevelDebug)` and `srvr.WithTraceConfig` is listed (M1); new requirement "Shutdown hooks run on every engine's stop paths" with gin and fiber rows, the A4 `Start`-failure rule and tasks 2.2, 3.3, 4.2, 6.2 (M5); fiber protocol rejections (`408`, `413`, `431`) recorded from the pre-set status with a `problem` group, design D4/D7 reworded, task 4.13 over a real listener (M8); route listing lists any-method routes once with `Method` `""` (net/http `Mount` and method-less patterns, gin `Any`, fiber `All`), gin `Static` per method, path-scoped fiber `Use` excluded (M9); `WithRouteCheck(func(context.Context, engine.Routes) error)` with `Start`'s context and a copy per check, mutating and blocking rows (L7); gin aborted-request record and fiber `http.ErrAbortHandler` parity exception (A3); tasks 2.6 and 5.6 (L1); ginprblm entry closed (L5).
- **Evidence:** fiber v3.7.0 source in the module cache: `app.go:1793-1877` (`serverErrorHandler` seeds the status, replays the `Use` chain with route handlers skipped, then calls `app.ErrorHandler`), `router.go:763` (the replayed chain returns `false, nil`); prblm source: `ginprblm/recovery.go:58` passes `http.ErrAbortHandler` on, `fiberprblm/recovery.go:23` recovers it like any panic; gin v1.12.0 `routergroup.go:18-23` (`anyMethods`). No probes were run in this pass.
- **Suggested action:** none beyond the RED tests named in the tasks.

### Unverified assumptions from audit 2
- **Status:** open
- **Target:** gin-and-fiber-engines
- **Context:** Three rows rest on source reading, not a running test: (1) a header read stalled past fasthttp's `ReadTimeout` reaches `serverErrorHandler` as a timeout and is answered `408` (Protocol rejection record row 3); (2) net/http `Mount("/")` in group `/static` registers the method-less pattern `/static/`, which depends on how core-server implements `Mount`; (3) fiber `All` registers exactly `RequestMethods`, so the any-method collapse recognises it.
- **Evidence:** unverified: established from the cited source lines only.
- **Suggested action:** Tasks 4.13 and 6.1 write these rows first as RED tests; if one fails for a reason other than the missing feature, correct the row and record the actual behaviour here. Consolidation 1.3 re-checks (2) against merged core code.
