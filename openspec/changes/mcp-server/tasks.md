# Tasks

## 1. Consolidation: refresh plan against merged changes

- [ ] 1.1 Sync with the main branch and list every change merged or archived since this change was planned (expect `engine-internal-split`, `pluggable-error-renderer`, `scrty-integration`); verify they are named in the completion note
- [ ] 1.2 Decide every open `notes.md` carry-over from earlier changes addressed to `mcp-server` or `unassigned` (adopt, defer or close). Pay particular attention to anything about `engine.RequestState` fields, the per-server problem renderer, `WithRouteCheck`, shutdown hook budgets, write deadlines and the access recorder; verify none is left undecided
- [ ] 1.3 Refresh this change's proposal, specs, design and tasks against the current main specs and merged code; verify `openspec validate mcp-server --strict` passes. Check each of these:
  - the `internal/engine` layout after `engine-internal-split`;
  - the renderer API from `pluggable-error-renderer`, for D3 and D9;
  - the scrty bridge API, `engine.Routes` and the collision check, for slice 8;
  - the `Group.Mount` prefix-stripping behaviour;
  - the `WriteTimeout` and `MaxRequestBodyBytes` defaults;
  - the golden-record test location;
  - the latest go-sdk release, re-reading `StreamableHTTPOptions`, `ServerOptions`, `ServerSession` and `RequestExtra` with gopls;
  - whether `structured-logging`, `route-registration` or `engine-support` need delta specs.

  Also add `mcp-server` after `scrty-integration` to the change order in `AGENTS.md` and `openspec/config.yaml`, and correct the proposal's log keys to match the specs (`srvr.mcp.session.ref` by default, `mcp.session.id` only by opt-in).
- [ ] 1.4 Record the consolidation outcome at the top of `openspec/changes/mcp-server/notes.md`; verify the file lists what was refreshed or states none was needed
- [ ] 1.5 Commit the consolidation as `docs(openspec): consolidate mcp-server against main`, or skip when 1.1-1.4 changed nothing; verify with `git log -1 --stat`

## 2. Slice: a stateful MCP endpoint serves an SDK tool on net/http

- [ ] 2.1 Create module `github.com/kartaladev/srvr/mcpsrvr` (go 1.27, package `mcpsrvr`) requiring go-sdk `v1.8.x`, prblm and srvr core. Add it to `go.work`, the Makefile module list and `-coverpkg` set, CI with `GOWORK=off`, and a `leak_test.go` with `goleak.VerifyTestMain`. Verify that:
  - `go build ./...` passes;
  - `go list -m all` in `mcpsrvr` shows no gin or fiber module;
  - `go list -deps .` in core shows no go-sdk package.
- [ ] 2.2 Spike S1, S4 and S6 against a bare SDK handler (design.md Risks); verify the tests `TestSDK_NoSessionLimit`, `TestSDK_RefusalContentTypes` and `TestSDK_ToolContextIsNotRequestContext` run, and record each confirmed or refuted hypothesis with the test output in `notes.md`
  - RED: three tests stating the hypotheses: more than `1000` sessions are admitted; `404`/`400`/`406`/`415`/`403` refusals are `text/plain`; `engine.StateFrom` is absent inside an SDK tool on a stateful session
  - GREEN: none; a test that passes confirms the hypothesis. A refuted hypothesis switches D2, D3 or D8 to the alternative named in design.md before 2.3 starts
  - REFACTOR: Extract Function for the shared bare-SDK harness, then /simplify
- [ ] 2.3 Mount an endpoint as a route configurer (mcp-endpoint / Scenario: Mount on an engine, net/http rows; Scenario: Broken mount fails Start, net/http rows including lifecycle misuse); verify `go test -race -run 'TestRoute_Mount|TestRoute_Broken'` passes
  - RED: tables over the net/http rows: nil server, empty, relative and wildcard paths, two mounts at one path, one endpoint mounted twice, a clash with `POST /mcp`, a nil option, two invalid fields named together, `Register` after `Start` and after `Shutdown`, and route listing `GET`/`POST`/`DELETE`. Fails because `mcpsrvr.New` does not exist
  - GREEN: `New(ms, opts...)` keeping its construction error; `(*Endpoint).Route(path)` and `Handler(path, ms, opts...)` returning `srvr.RouteConfigurer[*srvr.Group]`; `Configure` reports the error through `engine.RouteErrorf` and mounts `GET`/`POST`/`DELETE`; the mount-once flag
  - REFACTOR: Extract Function for path validation, shared with nothing yet (no Static reuse unless identical), then /simplify
- [ ] 2.4 Config struct and options (mcp-endpoint / Scenario: Invalid cap; every "config ..." and option row of the slice 2 requirements); verify `go test -race -run TestConfig` passes
  - RED: table over every option and config key in the design.md Defaults table, including later-wins ordering, zero values meaning the default, and negative values naming `MCP.<Field>`. Fails because `Config` does not exist
  - GREEN: `Config` with `json`/`yaml`/`env` tags, `WithConfig`, one option per field, `ConfigError` reuse
  - REFACTOR: Introduce Parameter Object for resolved settings, then /simplify
- [ ] 2.5 Stateful session round trip (mcp-endpoint / Scenario: Session id round trip, net/http rows; Scenario: Client ends its session, net/http rows); verify `go test -race -run 'TestSession_RoundTrip|TestSession_Delete'` passes
  - RED: tables with `initialize`, `tools/list` with a valid, missing, empty, unknown and deleted id, and `DELETE` with valid, unknown and missing ids, asserting status and `Mcp-Session-Id`. Fails because the mount serves nothing
  - GREEN: the mount wraps `mcp.NewStreamableHTTPHandler` with the resolved options
  - REFACTOR: /simplify
- [ ] 2.6 Stateless opt-out (mcp-endpoint / Scenario: Stateless mount, net/http rows); verify `go test -race -run TestStateless` passes
  - RED: table with `WithStateless()` and config `stateless: true`: `POST` without a session, then `GET` and `DELETE` answered `405` with `Allow: POST`. Fails because the option is ignored
  - GREEN: set `Stateless`; the srvr method check allows only `POST` when stateless
  - REFACTOR: /simplify
- [ ] 2.7 Transport refusals as problems (mcp-endpoint / Scenario: Refusal before the protocol, net/http rows); verify `go test -race -run TestRefusals` passes
  - RED: table with `PUT` (`405`, `Allow: DELETE, GET, POST`), `text/plain` content type (`415`), `Accept: text/html` (`406`), malformed JSON (`400` JSON-RPC `-32700`, unchanged) and an unknown tool (JSON-RPC error, unchanged), asserting the content type and no SDK text. Fails because SDK plain text passes through
  - GREEN: the srvr method check; a writer wrapper intercepting 4xx `text/plain` and rendering through the server's renderer (D3)
  - REFACTOR: Replace Conditional with Polymorphism if the pass-through/intercept switch grows, then /simplify
- [ ] 2.8 Body limit (mcp-endpoint / Scenario: Body limit, net/http rows); verify `go test -race -run TestBodyLimit` passes
  - RED: table with the default `4194304`/`4194305` and `1024`/`1025`, asserting `413` problem and no session created. Fails if the SDK's own limit answers in plain text or a session is created
  - GREEN: set the SDK's `MaxRequestBodyBytes` to the server's limit; intercept its `413`
  - REFACTOR: /simplify
- [ ] 2.9 Session cap (mcp-endpoint / Scenario: Session cap; Scenario: Concurrent initialize at the cap); verify `go test -race -run 'TestSessionCap'` passes
  - RED: tables over every cap row (default 999 and 1000, `WithMaxSessions(2)`, config, release after `DELETE`, `WithUnlimitedSessions()`, config `0` meaning default) and the concurrent rows (`10` of `50`, `1` of `2`). Fails because no cap exists (S1)
  - GREEN: the CAS limiter (D2), with release when no session is created, and release from a session watcher on close (the minimal `ServerSession.Wait` watcher, extended in slice 6)
  - REFACTOR: Extract Class `admission`, then /simplify
- [ ] 2.10 Idle expiry and keepalive (mcp-endpoint / Scenario: Idle expiry; Scenario: Keepalive); verify `go test -race -run 'TestIdle|TestKeepAlive'` passes
  - RED: tables over every idle row (with a fake clock where the SDK allows it, otherwise `100ms` durations) and every keepalive row, including a client answering 2 of 3 pings and the invalid values. Fails because `SessionTimeout` is zero and keepalive is unset
  - GREEN: map the settings onto `StreamableHTTPOptions.SessionTimeout` and `ServerOptions.KeepAlive`/`KeepAliveFailureThreshold` (a consumer-set SDK value wins: document it)
  - REFACTOR: /simplify
- [ ] 2.11 Cross-origin and rebinding protection (mcp-endpoint / Scenario: Protection, net/http rows); verify `go test -race -run TestProtection` passes
  - RED: table over the default and opt-out rows, including no `Origin` (non-browser) and `WithTrustedOrigins` with an invalid origin failing `Start`. Fails because cross-origin requests are served
  - GREEN: wrap with `http.NewCrossOriginProtection()`, a deny handler rendering a `403` problem, `AddTrustedOrigin`, the opt-out options; intercept the SDK's rebinding `403`
  - REFACTOR: /simplify
- [ ] 2.12 Event streams outlive WriteTimeout (mcp-endpoint / Scenario: Stream outlives WriteTimeout, net/http rows; Scenario: Client leaves the stream, net/http row); verify `go test -race -run 'TestStream'` passes
  - RED: with `WriteTimeout` `1s`, a notification after `2s` on the `GET` stream and a streamed tool result after `2s`; a client closing the stream leaves the session open and no goroutine behind. Fails because the stream is cut at `1s`
  - GREEN: the rolling deadline in the writer wrapper (D5); one `server.config.warning` with code `mcp_write_deadline_unsupported` when `SetWriteDeadline` returns `http.ErrNotSupported`, tested with a writer that lacks it
  - REFACTOR: /simplify
- [ ] 2.13 Shutdown closes sessions before the drain (mcp-endpoint / Scenario: Stop with open sessions, net/http rows; Scenario: Request in flight at shutdown); verify `go test -race -run 'TestShutdown'` passes under goleak
  - RED: table over every net/http row, including `Shutdown` before `Start`, a `Start` context cancelled, a tool that returns after `100ms`, and a tool that ignores its context with a `200ms` budget. Fails because open streams hold the drain until `ShutdownTimeout`
  - GREEN: a `BeforeDrain` shutdown hook registered by the route's `Configure`, closing every session through `Server.Sessions()` and waiting for the watchers within the hook budget
  - REFACTOR: /simplify
- [ ] 2.14 Godoc for `New`, `Endpoint`, `Route`, `Handler`, `Config` and every option, stating each default and its reason and naming the loosening options; a runnable net/http `Example` mounting an SDK server with one tool; a README section; verify `go test -run Example` passes and `go doc ./mcpsrvr` lists every option
- [ ] 2.15 Verify slice end to end: a client initializes, calls the SDK tool, and `Shutdown` closes the session with goleak clean. Confirm the mount dispatch, admission, refusal interception, rolling deadline, protection and shutdown hook are covered in `go tool cover -func`. Then:
  - run gopls diagnostics on changed files;
  - run `go vet ./...`, `golangci-lint run ./...`, `go test -race ./...`, `go mod tidy -diff` and `govulncheck ./...` with `GOWORK=off` in `mcpsrvr`, and `make check`;
  - run /simplify on the slice;
  - commit `feat(mcpsrvr): serve stateful MCP endpoints on net/http`.

## 3. Slice: the same endpoint mounts on gin

- [ ] 3.1 Create nested module `github.com/kartaladev/srvr/mcpsrvr/ginmcp` requiring `mcpsrvr`, `ginsrvr` and gin, with `go.work`, Makefile, CI and goleak `TestMain` entries; verify `go build ./...` passes and `go list -m all` in `mcpsrvr` still shows no gin
- [ ] 3.2 Gin mount (mcp-endpoint / every gin row of Scenario: Mount on an engine, Broken mount fails Start, Session id round trip, Client ends its session, Stateless mount, Refusal before the protocol, Body limit, Protection, Stream outlives WriteTimeout, Client leaves the stream, Stop with open sessions); verify `go test -race ./...` in `ginmcp` passes
  - RED: the gin rows as tables, using `httptest`. Fails because `ginmcp.Route` does not exist
  - GREEN: `ginmcp.Route(ep, path)` returning `srvr.RouteConfigurer[*gin.RouterGroup]`, adapting the endpoint's handler; confirm `ResponseController` reaches the connection through gin's writer (D5); otherwise the warning row applies, and the gap is recorded in `notes.md`
  - REFACTOR: Extract Function for anything duplicated with the net/http route, moved into `mcpsrvr` as unexported-but-reachable through an `internal/` package under `mcpsrvr`, then /simplify
- [ ] 3.3 Parity tests in `internal/paritytest` running one table over net/http and gin for the session round trip, stateless, refusals and shutdown; verify `go test -race ./...` in `internal/paritytest` passes
- [ ] 3.4 Godoc and a runnable gin `Example`; verify `go test -run Example` passes in `ginmcp`
- [ ] 3.5 Verify the slice end to end on gin and check `ginmcp` coverage with `go tool cover -func`. Then:
  - run gopls diagnostics;
  - run vet, lint, `go test -race`, tidy and govulncheck with `GOWORK=off` in `ginmcp`, and `make check`;
  - run /simplify;
  - commit `feat(ginmcp): mount MCP endpoints on gin`.

## 4. Slice: triage records name the MCP call

- [ ] 4.1 Record-attribute slot on `engine.RequestState` (internal, engine-neutral), appended to `http.request.completed` and `http.request.failed`; verify `go test -race -run TestRecordAttrs ./internal/engine/...` passes
  - RED: an internal test adding two attributes during a request and asserting both appear once in the access and failed records, in insertion order, escaped and cut by `MaxAttrBytes`. Fails because the slot does not exist
  - GREEN: the slot, safe for concurrent use and copied at record time
  - REFACTOR: /simplify
- [ ] 4.2 Envelope peek and MCP attributes (mcp-endpoint / Scenario: MCP attributes on the access record, all rows); verify `go test -race -run TestAccessAttrs` passes in `mcpsrvr` and `ginmcp`
  - RED: table over every row: `tools/call` with numeric and string ids, `tools/list`, a notification, a client response, a malformed body, and the `WithSessionIDInLogs()` and config opt-ins. Fails because no MCP keys are written
  - GREEN: bounded body read, minimal envelope decode, replayed body, `srvr.mcp.session.ref` from the first 16 hex characters of SHA-256, `mcp.protocol.version` from the header (D6)
  - REFACTOR: Extract Function `envelopeAttrs`, then /simplify
- [ ] 4.3 Hostile attribute values (mcp-endpoint / Scenario: Hostile values in logged attributes); verify `go test -race -run TestAccessAttrs_Hostile` passes, plus a fuzz target `FuzzEnvelope` run for `30s` without failure
  - RED: rows with a newline in the tool name, a `10000`-character id, and an object id. Fails if any record spans two lines or carries the object id
  - GREEN: id type check; reuse the existing escaping and cut
  - REFACTOR: /simplify
- [ ] 4.4 Tool traffic never logged (mcp-endpoint / Scenario: Tool traffic is never logged); verify `go test -race -run TestNoToolTraffic` passes
  - RED: at `DEBUG`, scan every record for the card number, the secret and the multi-line value. Fails if any appears
  - GREEN: none expected; fix any leak found
  - REFACTOR: /simplify
- [ ] 4.5 Golden-record update: add `mcp.method.name`, `jsonrpc.request.id`, `mcp.protocol.version`, `gen_ai.tool.name`, `srvr.mcp.session.ref` and `mcp.session.id` (opt-in) to the access and failed record goldens, with `srvr.log.schema` still `1`; document the keys in `LOGGING.md`; verify the golden test passes
- [ ] 4.6 Verify the slice end to end, check envelope and attribute coverage with `go tool cover -func`, add an MCP case to the hot-path benchmark and record its result in `docs/PERFORMANCE.md`. Then:
  - run gopls diagnostics, vet, lint, `go test -race`, tidy and govulncheck in core, `mcpsrvr` and `ginmcp`;
  - run /simplify;
  - commit `feat(mcpsrvr): name MCP calls in access and failed records`.

## 5. Slice: srvr-style tools with safe error rendering

- [ ] 5.1 Spike S2 and the invalid-arguments question: a bare `mcp.AddTool` handler returning `errors.New("pq: secret")`, and a call with `{"id":7}` against a string input; verify `TestSDK_ToolErrorText` and `TestSDK_InvalidArguments` run, and record both outcomes in `notes.md`. If invalid arguments come back as `isError` results rather than `-32602`, correct the `mcp-tools` row with `/opsx:update` before 5.4
- [ ] 5.2 Tool configurers register tools (mcp-tools / Scenario: Tools listed after Start; Scenario: Broken tool registration fails Start, all rows); verify `go test -race -run 'TestToolbox_List|TestToolbox_Broken'` passes in `mcpsrvr` and `ginmcp`
  - RED: tables over every row: listing (including an SDK-added tool next to configurer tools and no tools at all) and failures (nil and typed-nil configurers, a panicking `Configure`, a nil handler, duplicate names across configurers and against an SDK tool, empty, spaced and 129-character names, the 128-character boundary, a `string` input type, `Register` after `Start` and after `Shutdown`). Fails because `ToolConfigurer` does not exist
  - GREEN: `ToolConfigurer`, `Toolbox`, `(*Endpoint).Register`, `Tool[In, Out]` with name validation; `Configure` run under the route configurer's recovery (D7)
  - REFACTOR: Extract Function for name validation, then /simplify
- [ ] 5.3 Successful results (mcp-tools / Scenario: Successful call); verify `go test -race -run TestTool_Success` passes
  - RED: rows for an `Order` value and the zero `Order{}`, asserting `isError` false and `structuredContent`. Fails because the wrapper returns nothing
  - GREEN: the wrapper returns the output through the SDK's typed result
  - REFACTOR: /simplify
- [ ] 5.4 Invalid arguments never reach the handler (mcp-tools / Scenario: Invalid arguments); verify `go test -race -run TestTool_InvalidArguments` passes
  - RED: rows `{}`, `{"id":7}`, `"7"`, no arguments and a valid call, counting handler calls. Fails if the handler is called for an invalid row
  - GREEN: rely on SDK validation; add none unless the test shows a gap
  - REFACTOR: /simplify
- [ ] 5.5 Errors rendered without internals (mcp-tools / Scenario: Error to tool result, all three tables; Scenario: Exposing error text is an explicit opt-in); verify `go test -race -run 'TestTool_Error'` passes
  - RED: every mapped, unmapped and faulty-error row, plus the opt-in rows, asserting the text, the hidden value and `_meta.trace_id`. Fails on the S2 leak, or because no text is set
  - GREEN: the wrapper never returns its error to the SDK; it renders through `prblm.Handler.Resolve` (D9); `WithToolErrorText()` and config `expose_tool_error_text` read through the safe error-text guard
  - REFACTOR: Extract Function `toolErrorText`, then /simplify
- [ ] 5.6 Panics recovered (mcp-tools / Scenario: Panicking tool); verify `go test -race -run TestTool_Panic` passes
  - RED: rows with a string, an error, `panic(nil)` and `http.ErrAbortHandler`, followed by `tools/list` in the same session. Fails because the panic escapes to the SDK
  - GREEN: `recover` in the wrapper; a `Resolve` panic from a faulty consumer mapper is also recovered
  - REFACTOR: /simplify
- [ ] 5.7 Tool context through the request reference (mcp-tools / Scenario: Context values; Scenario: Cancellation); verify `go test -race -run 'TestTool_Context|TestTool_Cancel'` passes in `mcpsrvr` and `ginmcp`
  - RED: rows for `TraceFromContext` and `LoggerFromContext` with a given `traceparent`, cancellation by `notifications/cancelled`, session delete and an expired `Shutdown` context; plus a client-forged `X-Srvr-Mcp-Ref` that is ignored, and an empty registry after each request. Fails because the tool sees no trace (S6)
  - GREEN: the reference header and registry (D8); one `server.config.warning` with code `mcp_request_context_lost` when the lookup fails
  - REFACTOR: Extract Class `requestRefs`, then /simplify
- [ ] 5.8 Failed records and fingerprints (mcp-tools / Scenario: Failed record; Scenario: Fingerprint stability); verify `go test -race -run 'TestTool_Record|TestTool_Fingerprint'` passes
  - RED: rows for a `WARN` 4xx, an `ERROR` unmapped error and a panic with a stack, asserting `gen_ai.tool.name`, `trace_id`, `error.type` and `error.fingerprint`; fingerprint rows (equal across messages, different across tools and types). Fails because no record is written
  - GREEN: build `mcp.tool.failed` and `mcp.tool.panic.recovered` with the existing triage-record code, with the tool name in the route slot; add both events to the golden-record test and `LOGGING.md`
  - REFACTOR: /simplify
- [ ] 5.9 `SessionFromContext` (mcp-sessions / Scenario: Session lookup; Scenario: Session identity); verify `go test -race -run 'TestSessionFromContext'` passes
  - RED: rows for a stateful tool context (net/http and gin), stateless, a plain handler's context, `context.Background()` and nil; identity rows (same session equal, different sessions and re-initialized session different). Fails because the function does not exist
  - GREEN: `Session` type wrapping `*mcp.ServerSession` with `ID()`, stored in the rebuilt tool context
  - REFACTOR: /simplify
- [ ] 5.10 Godoc for `ToolConfigurer`, `Toolbox`, `Tool`, `Session` and `SessionFromContext`; a runnable `Example` with a configurer and an error-returning tool; README update; verify `go test -run Example` passes
- [ ] 5.11 Verify the slice end to end (a configurer tool returns a mapped error, and the model sees only the title and detail). Confirm the wrapper's branches, `Resolve` and fingerprinting are covered in `go tool cover -func`. Then:
  - run gopls diagnostics, vet, lint, `go test -race`, tidy and govulncheck in `mcpsrvr` and `ginmcp`;
  - run /simplify;
  - commit `feat(mcpsrvr): register error-returning tools through configurers`.

## 6. Slice: per-session state released on close

- [ ] 6.1 Spike S3: `ServerSession.Wait` returns on `DELETE`, idle expiry, keepalive failure and `Close`; verify `TestSDK_WaitReturnsOnClose` runs with one row per path, and record the outcome in `notes.md`. On a failing path, apply D10's fallback and record the decision before 6.2
- [ ] 6.2 State isolation (mcp-sessions / Scenario: State isolation); verify `go test -race -run TestSessionState_Isolation` passes
  - RED: rows for same session, another session, read before set, and `100` concurrent calls. Fails because `Set`/`Value` do not exist
  - GREEN: a mutex-guarded value map on `Session`
  - REFACTOR: /simplify
- [ ] 6.3 Release on close and close hooks (mcp-sessions / Scenario: Release on close, both tables); verify `go test -race -run TestSessionState_Release` passes under goleak
  - RED: rows for `DELETE`, idle expiry, failed keepalive, `Shutdown` and concurrent `DELETE`; faulty hooks (panic, error, block with a `1s` shutdown, nil function). Fails because nothing runs on close
  - GREEN: `OnClose`, the watcher running hooks once (`sync.Once`) under `recover` with the `5s` budget (`WithCloseHookTimeout`, config `close_hook_timeout`), and `mcp.session.close_hook.failed` records. An abandoned hook goroutine gets a named, commented `leakOptions` ignore
  - REFACTOR: Extract Function `runCloseHooks`, then /simplify
- [ ] 6.4 Using a closed session (mcp-sessions / Scenario: Using a closed session); verify `go test -race -run TestSessionState_Closed` passes
  - RED: rows for `Set`, `Value` and `OnClose` after close. Fails because writes succeed after close
  - GREEN: the closed flag; `ErrSessionClosed`
  - REFACTOR: /simplify
- [ ] 6.5 Godoc for the state methods and `ErrSessionClosed`, an `Example` using `OnClose`, and the new event in `LOGGING.md` and the goldens; verify `go test -run Example` and the golden test pass
- [ ] 6.6 Verify the slice end to end and check watcher and hook coverage in `go tool cover -func`. Then:
  - run gopls diagnostics, vet, lint, `go test -race`, tidy and govulncheck;
  - run /simplify;
  - commit `feat(mcpsrvr): keep per-session state released on close`.

## 7. Slice: tools report progress and log to the client

- [ ] 7.1 Progress (mcp-sessions / Scenario: Progress, all rows); verify `go test -race -run TestProgress` passes in `mcpsrvr` and `ginmcp`
  - RED: rows with string and numeric tokens, no total, and no token (skipped, no error). Fails because `Progress` does not exist
  - GREEN: `Session.Progress` reading the call's `progressToken` from the rebuilt context and calling `NotifyProgress`
  - REFACTOR: /simplify
- [ ] 7.2 Log messages (mcp-sessions / Scenario: Log messages to the client); verify `go test -race -run TestClientLog` passes
  - RED: rows over client levels and message levels, including level not set. Fails because `Log` does not exist
  - GREEN: `Session.Log` over `ServerSession.Log`, respecting the client's level
  - REFACTOR: /simplify
- [ ] 7.3 Notifying a gone client (mcp-sessions / Scenario: Notifying a gone client); verify `go test -race -run TestNotify_Gone` passes under goleak
  - RED: rows for a deleted session (`ErrSessionClosed`) and a dropped connection (returns within `1s`, no panic, goroutine ends). Fails if a send blocks or panics
  - GREEN: closed-session check; send bounded by the call context
  - REFACTOR: /simplify
- [ ] 7.4 Godoc and an `Example` reporting progress; verify `go test -run Example` passes
- [ ] 7.5 Verify the slice end to end and check send-path coverage. Then:
  - run gopls diagnostics, vet, lint, `go test -race`, tidy and govulncheck;
  - run /simplify;
  - commit `feat(mcpsrvr): report progress and log messages to MCP clients`.

## 8. Slice: tools make bounded requests to the client

- [ ] 8.1 Spike S5: read the go-sdk and the MCP specification for protocol `2026-07-28` (server-to-client requests, keepalive, sessions) and test what the SDK does for each on that protocol; verify `TestSDK_Protocol20260728` runs, record the findings in `notes.md`, and add any needed `mcp-sessions` rows with `/opsx:update` before 8.2
- [ ] 8.2 Answered requests (mcp-sessions / Scenario: Client request outcomes, answered rows); verify `go test -race -run TestClientRequest_Answered` passes in `mcpsrvr` and `ginmcp`
  - RED: rows for `Elicit` accept and decline, `CreateMessage` and `ListRoots`. Fails because the methods do not exist
  - GREEN: `Session.Elicit`, `Session.CreateMessage` and `Session.ListRoots` over the SDK methods
  - REFACTOR: /simplify
- [ ] 8.3 Refused and failed requests (mcp-sessions / Scenario: Client request outcomes, refused or failed rows and invalid configuration); verify `go test -race -run TestClientRequest_Failed` passes under goleak
  - RED: rows for missing capabilities (nothing sent), the timeout by option and config, client cancel, `Shutdown`, a JSON-RPC error, a malformed result, stateless `ErrNoSession`, and a negative timeout failing `Start`. Fails because requests are sent unchecked and unbounded
  - GREEN: the capability check from `ClientCapabilities()`, `context.WithTimeout`, mapping to `ErrClientTimeout`, `ErrClientCapability` and `ErrNoSession` (D11)
  - REFACTOR: Extract Function `clientCall` shared by the three methods, then /simplify
- [ ] 8.4 Client request errors returned by a tool (mcp-sessions / Scenario: Client request errors returned by a tool); verify `go test -race -run TestClientRequest_ToolResult` passes
  - RED: rows for a timed-out `Elicit` (`Gateway Timeout`) and a missing capability (`Internal Server Error`). Fails if the mapping differs
  - GREEN: none expected beyond the error wrapping in 8.3
  - REFACTOR: /simplify
- [ ] 8.5 Client answers never logged (mcp-sessions / Scenario: Privacy of client answers); verify `go test -race -run TestClientRequest_Privacy` passes
  - RED: at `DEBUG`, scan every record for the card number, `patient 42` and `/home/alice`. Fails if any appears
  - GREEN: failed client requests are logged by method and outcome only
  - REFACTOR: /simplify
- [ ] 8.6 Godoc and an `Example` with an elicitation; verify `go test -run Example` passes
- [ ] 8.7 Verify the slice end to end and check the capability, timeout and cancel branches in `go tool cover -func`. Then:
  - run gopls diagnostics, vet, lint, `go test -race`, tidy and govulncheck;
  - run /simplify;
  - commit `feat(mcpsrvr): make bounded requests from tools to MCP clients`.

## 9. Slice: MCP endpoints behind the scrty bridge

- [ ] 9.1 Authenticated endpoint (mcp-endpoint / Scenario: Authenticated endpoint, all rows); verify `go test -race -run TestAuth` passes in `mcpsrvr` and `ginmcp`
  - RED: rows with a valid token, no token and an expired token, asserting the status, `WWW-Authenticate` and the open session count. Fails if a refused request admits a session
  - GREEN: none expected beyond placement; document the wiring and fix admission ordering if the test fails
  - REFACTOR: /simplify
- [ ] 9.2 Collision with a scrty endpoint (mcp-endpoint / Scenario: Collision with a scrty endpoint); verify `go test -race -run TestAuth_Collision` passes
  - RED: rows with a non-colliding and a colliding endpoint. Fails if the MCP routes are missing from `engine.Routes`
  - GREEN: none expected; fix the route listing if needed
  - REFACTOR: /simplify
- [ ] 9.3 A README section and a runnable `Example` with the bridge, including the OAuth protected-resource metadata endpoint scrty serves; verify `go test -run Example` passes
- [ ] 9.4 Verify the slice end to end. Then:
  - run gopls diagnostics, vet, lint, `go test -race`, tidy and govulncheck in every touched module, and `make check` and `make cover`;
  - add the release pair `mcpsrvr` and `mcpsrvr/ginmcp` to `RELEASING.md` and `CHANGELOG.md`;
  - run /simplify;
  - commit `feat(mcpsrvr): protect MCP endpoints with the security bridge`.

## Workflow follow-up

- Archive the change after the project's review requirements are satisfied.
- Verify the archived result, and open follow-up changes for the fiber mount, MCP resources and prompts in the `Toolbox` style, and MCP spans and metrics in `otelsrvr`.
