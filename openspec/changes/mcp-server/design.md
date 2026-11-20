# Design

## Context

See proposal.md - Why. This change assumes the following from earlier changes. The consolidation group checks each assumption against the merged code.

- From `engine-internal-split` (being applied on 2026-11-21):
  - The shared engine package is `github.com/kartaladev/srvr/internal/engine`, with siblings `internal/errchain`, `internal/static` and `internal/tracectx`.
  - Go's internal rule is by import path. `github.com/kartaladev/srvr/mcpsrvr` is under the srvr path, so it may import them even as a separate module, as `ginsrvr` does.
- From `core-server`:
  - `Group.Mount(path, http.Handler)` routes every method, strips the group prefix, applies group middleware and recovers panics.
  - `RouteConfigurer[G]` and `Register`.
  - `WithShutdownHook` with `BeforeDrain` (inside the drain budget) and `AfterDrain`, run on every stop path.
  - `Config.WriteTimeout` defaults to 60s, and `MaxRequestBodyBytes` defaults to 4 MiB (`413` problem).
  - The access recorder (`internal/engine/access.go`) implements `Unwrap`, `FlushError` and `Hijack`, so `http.ResponseController` reaches the connection.
  - `engine.RequestState` exists, reachable through `engine.StateFrom(ctx)`. It holds route, status, failure, trace and logger.
  - The log schema rule: added keys keep `srvr.log.schema` `1`, and a golden-record test pins every event.
- From `gin-and-fiber-engines`:
  - `ginsrvr` accepts `RouteConfigurer[*gin.RouterGroup]`.
  - gin applies `MaxRequestBodyBytes` with `http.MaxBytesReader`.
  - `engine.Routes` attributes routes to configurers, and `WithRouteCheck` runs after the configure phase.
- From `static-files`: a configurer factory per engine that keeps its construction error and reports it from `Configure`, so `Start` fails with `ErrRouteConfig`. This change reuses that pattern.
- From `pluggable-error-renderer`: srvr renders problems through one per-server renderer that engine code can call for an error outside a handler's return path. The MCP mount uses it for transport refusals. If the merged API differs, consolidation adapts D3 without changing specs.
- From `scrty-integration`: the bridge's chain sits before the router; `WithRouteCheck` runs the collision check; `engine.RequestState` gains principal and client fields.

SDK facts, read from `github.com/modelcontextprotocol/go-sdk` v1.8.0 docs (2026-11-21):
- **Constructors:** `mcp.NewStreamableHTTPHandler(getServer func(*http.Request) *mcp.Server, *StreamableHTTPOptions)`.
- **`StreamableHTTPOptions`:** `Stateless`, `JSONResponse`, `Logger`, `EventStore`, `SessionTimeout` (zero means never), `DisableLocalhostProtection`, `MaxRequestBodyBytes` (default 4 MiB) and `PropagateRequestCancellation`. The `CrossOriginProtection` field is deprecated in favour of wrapping the handler.
- **`ServerOptions`:** `KeepAlive` and `KeepAliveFailureThreshold` (protocols before `2026-07-28` only), `InitializedHandler`, `GetSessionID` and `SupportedProtocolVersions`.
- **Middleware:** `Server.AddReceivingMiddleware(...mcp.Middleware)` wraps every incoming method.
- **Sessions:** `Server.Sessions()` iterates live sessions. `ServerSession` has `ID`, `Close`, `Wait`, `NotifyProgress`, `Log`, `Elicit`, `CreateMessage`, `CreateMessageWithTools` and `ListRoots`.
- **Tools:** `mcp.AddTool[In, Out](s, *Tool, ToolHandlerFor[In, Out])` infers the input schema from `In`. `ToolHandlerFor` is `func(ctx, *CallToolRequest, In) (*CallToolResult, Out, error)`.
- **Requests:** `ServerRequest` exposes `Session`, `Params`, `Extra` (with `Extra.Header`, the HTTP request header), `ClientCapabilities()` and `ClientInfo()`.

prblm fact (v0.3.0): `(*prblm.Handler).Resolve(ctx, err) *prblm.Problem` maps an error to a problem without writing a response.

## Goals / Non-Goals

**Goals:**
- The SDK stays the only MCP protocol implementation. srvr adds a mount, defaults, lifecycle, logging and a tool model on top of it, and never parses JSON-RPC beyond the envelope it needs for log attributes.
- One endpoint object serves both layers. A consumer who starts with an SDK-only server can add `Toolbox` configurers later without changing the mount.
- The core, `ginsrvr` and `fibersrvr` never import the SDK.

**Non-Goals:**
- A fiber mount, MCP resources and prompts in the `Toolbox` style, OpenTelemetry spans and metrics for MCP, and stream resumption (`EventStore`). See proposal.md - Scope.
- Changing tool registration at runtime (`tools/list_changed` from srvr). Tools are fixed when `Start` configures them.

## Slices

Every slice ends green under `-race` and goleak, with `make check` passing for the modules it touches.

1. **net/http mount, stateful, end to end.**
   - What works: the `mcpsrvr` module, `mcpsrvr.New(ms, opts...)` returning an `*Endpoint`, and `ep.Route(path)` / `mcpsrvr.Handler(path, ms, opts...)` as a `RouteConfigurer[*srvr.Group]`. Stateful sessions by default and the stateless opt-out; the session cap (D2), idle timeout and keepalive; srvr-owned transport refusals and interception of SDK refusals as problems (D3); cross-origin and rebinding protection (D4); rolling write deadlines for event streams (D5); the `BeforeDrain` session close; the config struct and its validation.
   - Spikes run first: S1 (SDK session limit), S4 (SDK plain-text refusals) and S6 (whether a tool's context reaches the HTTP request). See Risks.
   - Specs satisfied: all of `mcp-endpoint` except "Triage records name the MCP call" and "Endpoint is protected through the security bridge", net/http rows only.
   - Hot paths: mount dispatch, session admission and release, refusal rendering, origin and host checks, write-deadline extension, shutdown close, config validation.
2. **gin mount.**
   - What works: nested module `mcpsrvr/ginmcp` with `ginmcp.Route(ep, path)` as a `RouteConfigurer[*gin.RouterGroup]`, reusing the same `*Endpoint`.
   - Specs satisfied: every gin row of the slice 1 requirements, and parity tests in `internal/paritytest` for both modes.
   - Hot paths: the gin route adapter, and `ResponseController` through gin's writer (D5).
3. **Triage attributes.**
   - What works: envelope peeking (D6), the record-attribute slot in `engine.RequestState`, session references, and the opt-in raw session id.
   - Specs satisfied: `mcp-endpoint` "Triage records name the MCP call" (all scenarios).
   - Hot paths: envelope peek and replay, attribute construction and escaping, the golden-record update.
4. **`Toolbox` configurer and error rendering.**
   - The S2 spike's test (whether the SDK leaks `err.Error()`) is this slice's RED step.
   - What works: `ToolConfigurer`, `Toolbox` and `mcpsrvr.Tool[In, Out]` (D7); the request-context bridge (D8); error and panic rendering through `Resolve` (D9); failed records and fingerprints; `SessionFromContext`.
   - Specs satisfied: all of `mcp-tools`, and `mcp-sessions` "A tool finds its session through its context".
   - Hot paths: the tool wrapper (success, error, typed-nil error, panicking `Error()`, panic), `Resolve`, fingerprinting, configurer registration and validation.
5. **Per-session state.**
   - The S3 spike (close detection through `ServerSession.Wait`) runs first.
   - What works: the session value store, `OnClose` hooks with budget and recovery, and `ErrSessionClosed`.
   - Specs satisfied: `mcp-sessions` "Per-session state is isolated and released on close".
   - Hot paths: the session watcher goroutine and its end, hook running, and store access under concurrency.
6. **Notifications and progress.**
   - What works: `Session.Progress` and `Session.Log`, skipping when there is no token or the level is too low, and `ErrSessionClosed`.
   - Specs satisfied: `mcp-sessions` "Tools report progress and log to the client".
   - Hot paths: the send path and its closed-session branch.
7. **Requests to the client.**
   - The S5 spike (protocol `2026-07-28` semantics) runs first; it may add spec rows for that protocol before coding starts.
   - What works: `Session.Elicit`, `Session.CreateMessage` and `Session.ListRoots` with capability checks, the timeout and cancellation; `ErrClientCapability`, `ErrClientTimeout` and `ErrNoSession`; privacy of answers.
   - Specs satisfied: `mcp-sessions` "Tools make bounded requests to the client" and "Client answers are never logged".
   - Hot paths: the capability check, the timeout and cancel branches, and error mapping into tool results.
8. **Auth through the scrty bridge.**
   - What works: the endpoint runs behind the bridge's chain like any route; the endpoint's routes are in `engine.Routes`, so the bridge's collision check sees them; refusals release no session slot because no session was admitted.
   - Specs satisfied: `mcp-endpoint` "Endpoint is protected through the security bridge".
   - Hot paths: none new; this slice covers the existing chain placement with MCP rows.

Every slice produces a releasable module. Slices 1-3 are useful with SDK-registered tools alone. Slice 4 does not need slices 5-7, and each of slices 5-7 adds methods to a `Session` type that already works.

## Decisions

### D1. Module layout: `mcpsrvr` for net/http, nested `mcpsrvr/ginmcp` for gin

`github.com/kartaladev/srvr/mcpsrvr` depends on srvr core, `internal/engine` (allowed by the import path), prblm and the go-sdk. The gin adapter lives in a nested module, `github.com/kartaladev/srvr/mcpsrvr/ginmcp`, which adds gin and `ginsrvr`. A later `fibermcp` would sit beside it.

Alternatives:
- Putting gin support inside `mcpsrvr` forces gin on every net/http user.
- `ginsrvr.MCP(...)` forces the SDK on every gin user.
- One module per engine with no shared `mcpsrvr` duplicates the session logic.

Failure modes:
- A version skew between `mcpsrvr` and `ginmcp`: `ginmcp` requires the matching `mcpsrvr` version, and RELEASING.md gains the pair to tag.
- `internal/engine` changes break `mcpsrvr`: it is released from the same repository and covered by `make check`, so a break fails CI.

### D2. One `*Endpoint` owns the SDK server, sessions and configurers

`mcpsrvr.New(ms *mcp.Server, opts ...Option) *Endpoint` keeps any construction error, the same way `Static` does, and reports it from the route's `Configure`, so `Start` fails with `ErrRouteConfig` naming the field (`MCP.Server`, `MCP.MaxSessions`, ...). `ep.Register(ToolConfigurer)` collects configurers until the mount is configured. An `*Endpoint` may be mounted once; a second mount fails `Start` naming the endpoint.

Session admission:
- The mount wraps the SDK handler.
- A `POST` without `Mcp-Session-Id` is a possible `initialize`. The mount takes a slot from a counting limiter (CAS on an `atomic.Int64` against the cap) before calling the SDK. If the cap is reached, it answers `503` (D3) without calling the SDK.
- If the SDK answers without an `Mcp-Session-Id` header (the initialize failed or the message was not an initialize), the slot is released when the request ends.
- Otherwise the slot belongs to the new session. A receiving middleware sees the `initialize` method with `req.Session` and starts the session's watcher (D10), which releases the slot when the session ends.

Alternatives:
- Counting `Server.Sessions()` on every initialize is O(n) and racy: two initializers both see `cap-1`.
- Relying on the SDK has no cap (S1).

Failure modes:
- Concurrent initializes at the cap: CAS admits exactly `cap` (spec scenario "Concurrent initialize at the cap").
- An initialize that panics inside the SDK: the slot is released in a `defer`, and the panic is recovered by the route's recovery.
- A client that initializes but never sends `notifications/initialized`: the slot is held until idle expiry, so it stays bounded.
- A nil `*mcp.Server`: construction error `MCP.Server`.
- A nil option: construction error `MCPOption`.

### D3. Transport refusals: srvr checks first, then intercepts SDK plain text

srvr itself refuses, before the SDK sees the request:
- a method other than `GET`, `POST` or `DELETE` (`405`, with `Allow`);
- a session cap reached (`503`);
- a cross-origin request (`403`);
- a body that is too large (`413`, through the server's existing limit).

For the SDK's own refusals (unknown session `404`, missing session `400`, `406`, `415`, and the SDK's rebinding `403`), the mount wraps the writer. When the SDK writes a status of 400 or above with a non-JSON, non-event-stream content type, the wrapper discards the SDK body and renders a problem with that status through the server's renderer. A JSON-RPC body (`application/json`) passes through unchanged, including a `400` parse error.

Alternatives:
- Re-implementing every SDK check would drift from the SDK's behaviour.
- Passing SDK text through breaks "every refusal is a problem" and may leak internals.

Failure modes:
- The SDK changes its content type or starts writing JSON problems itself: an S4 test pins the current behaviour and fails on change.
- The SDK writes a body before the status: the wrapper treats an implicit `200` as pass-through.
- The SDK flushes after a 4xx: the wrapper swallows the flush.

### D4. Protection: SDK rebinding check plus `http.CrossOriginProtection`

- **Rebinding:** the SDK's localhost check stays on. `WithoutRebindingProtection()` or config `disable_rebinding_protection` sets `DisableLocalhostProtection`.
- **Cross-origin:** the mount wraps the SDK handler in the standard library's `http.NewCrossOriginProtection()`, with a deny handler that renders a `403` problem.
  - `WithTrustedOrigins(...)` calls `AddTrustedOrigin`. An invalid origin is a construction error naming `MCP.TrustedOrigins` and the value.
  - `WithoutCrossOriginProtection()` or config `disable_cross_origin_protection` removes the wrapper.
- Non-browser clients, which send no `Sec-Fetch-Site` or `Origin`, pass, as the standard library defines.

Failure modes:
- A reverse proxy rewrites `Host` to loopback: documented, and covered by a rebinding test row with a forwarded host.
- An empty trusted origin: construction error.

### D5. Event streams: rolling write deadline

For a `GET` stream, and for any response whose `Content-Type` is `text/event-stream`, the writer wrapper calls `http.NewResponseController(w).SetWriteDeadline(now + WriteTimeout)` before each write and flush. Each write is still bounded, but the stream as a whole is not. A JSON response keeps the server's single deadline.

Alternatives:
- Clearing the deadline lets a stalled reader hold the goroutine forever.
- Raising `WriteTimeout` server-wide loosens a default for every route.

Failure modes:
- A writer that does not support deadlines (`http.ErrNotSupported`, for example gin's writer without `Unwrap`): one `server.config.warning` record with code `mcp_write_deadline_unsupported`, and the stream is cut at `WriteTimeout`. Slice 2 checks gin by test.
- A stalled client: the per-write deadline fails the write, the SDK sees the error, and the session survives for reconnection.

### D6. Log attributes from a peeked envelope

The mount reads the `POST` body, already bounded by the server's limit, into a buffer. It decodes only `{"method", "id", "params": {"name"}}` into a small struct, then hands the SDK a replayed body. The values go into a new internal record-attribute slot on `engine.RequestState`, which the access and failed records append.

- `jsonrpc.request.id` keeps only string or integer ids; other id types are left out.
- `srvr.mcp.session.ref` is the first 16 hex characters of the SHA-256 of the session id.
- `mcp.protocol.version` comes from the `MCP-Protocol-Version` header.
- Every value goes through the existing escaping and `MaxAttrBytes` cut.

Alternatives:
- An SDK receiving middleware runs in the session's context, not the HTTP request's (S6), so it cannot reach the request's record reliably.
- Full JSON-RPC parsing in srvr duplicates the SDK.

Failure modes:
- Malformed JSON: no MCP attributes; the SDK answers its parse error.
- Batched or array bodies: no MCP attributes.
- A body read error: the SDK sees the same error through the replay.
- The double read costs at most one body copy per request. The hot-path benchmark gains an MCP case.

### D7. `Toolbox` and the generic `Tool` function

Go methods cannot have type parameters, so tools are added with a free function:

```go
mcpsrvr.Tool[In, Out any](t *Toolbox, name, description string, h func(context.Context, In) (Out, error), opts ...ToolOption)
```

It validates the name (1-128 characters of `[A-Za-z0-9_.-]`), checks for a nil handler and duplicates across configurers, then calls `mcp.AddTool` with a wrapper. `Configure` runs once inside the route's `Configure`, under the same panic recovery and owner naming as route configurers. Tools added directly through the SDK coexist; a name clash between the two is detected through `tools/list` at `Start` and fails it.

Failure modes: a nil configurer, a typed-nil configurer, a panicking `Configure`, a nil handler, a bad name, or an input type that is not an object. Each fails `Start` with a named error (see the `mcp-tools` spec).

### D8. Tool context through a request reference header

The SDK's tool context in a stateful session is not guaranteed to come from the HTTP request (S6). Before calling the SDK, the mount:
1. deletes any client-sent `X-Srvr-Mcp-Ref` header;
2. sets the header to a random 128-bit reference;
3. registers `ref -> RequestState` in the endpoint until the request ends.

The tool wrapper reads `req.Extra.Header`, looks up the reference and rebuilds a context carrying the request's trace, logger and state, cancelled by either the SDK's context or the request's.

Alternatives:
- Correlating by session and JSON-RPC id breaks on id reuse.
- Trusting the SDK's context fails S6 for older protocols.

Failure modes:
- A missing or unknown reference (an SDK change, or a call that outlived its request): the tool runs with a fresh trace, and one `server.config.warning` with code `mcp_request_context_lost` is written per endpoint. A test pins the normal path.
- A client forges the header: it is deleted before the SDK sees the request.
- A registry leak: entries are removed in a `defer`, and a test asserts the registry is empty after a request.

### D9. Tool errors rendered through `prblm.Handler.Resolve`

The wrapper never returns its error to the SDK; it returns `(*CallToolResult{IsError: true}, zero Out, nil)`.
- **Text:** `Resolve(ctx, err)` gives the problem. For a status below 500 the text is the `title`, plus `": " + detail` when set; for 500 and above it is `Internal Server Error`.
- **`_meta`:** carries `trace_id`.
- **Error text opt-in:** `WithToolErrorText()` or config `expose_tool_error_text` uses `err.Error()` read through srvr's safe error-text guard.
- **Panics:** recovered in the wrapper, which writes `mcp.tool.panic.recovered` with a stack.
- **Records:** built with the existing triage-record code. The route slot holds the tool name, so the fingerprint is computed from the error types and the tool name.

Failure modes:
- A typed-nil error, or an `Error()` that panics: the existing guards handle both.
- A `Resolve` that panics, through a faulty consumer mapper: recovered, and the result is `Internal Server Error`.
- `panic(nil)`: Go 1.21+ turns it into `*runtime.PanicNilError`, which is handled as a panic.

### D10. Session watcher and close hooks

One goroutine per session waits on `ServerSession.Wait()` (spike S3 confirms it returns on `DELETE`, idle expiry, keepalive failure and `Close`). It then:
- releases the admission slot;
- drops the session's values;
- runs the `OnClose` hooks in registration order, each under `recover` with a 5s budget.

At shutdown, a `BeforeDrain` hook closes every session through `Server.Sessions()` and waits for the watchers until the hook's budget (the drain deadline) ends. A test proves every watcher ends.

Alternatives:
- Polling `Sessions()` delays release and costs O(n) per tick.
- If S3 fails (`Wait` does not return on some path), the fallback is the receiving middleware plus idle accounting in srvr. That would be decided before slice 5 starts and recorded in notes.md.

Failure modes:
- A blocking hook: abandoned at its budget, with one `mcp.session.close_hook.failed` record. Its goroutine is excused in `leakOptions` with a named comment, the same way core excuses abandoned shutdown hooks.
- A panicking hook: recovered and logged; later hooks still run.
- Concurrent `DELETE` and expiry: the watcher runs once (`sync.Once`).

### D11. Requests to the client

The `Session` methods wrap the SDK's `ServerSession` methods:
1. Check `ClientCapabilities()` for the capability, returning `ErrClientCapability` naming it and sending nothing.
2. Apply `context.WithTimeout(ctx, ClientRequestTimeout)` (default 60s).
3. Map a deadline to `ErrClientTimeout` wrapping `context.DeadlineExceeded`, which prblm maps to `504 Gateway Timeout`, so a tool returning the error yields that text.

In stateless mode, `SessionFromContext` returns ok `false` and the package-level helpers return `ErrNoSession`. Answers are values returned to the tool and are never handed to a logger.

Failure modes: a missing capability, a timeout, cancellation, shutdown, a JSON-RPC error from the client, and a malformed result. Each is covered by a spec row.

## Defaults

| Setting | Default | Why | Override (option / config key) |
|---|---|---|---|
| Session mode | stateful | the owner needs sessions (2026-11-21) | `WithStateless()` / `stateless` |
| Session cap | `1000` | bounds memory against initialize floods; a 0 config value means the default | `WithMaxSessions(n)` / `max_sessions`; `WithUnlimitedSessions()` (loosens) |
| Idle timeout | `30m` | the SDK's zero means sessions never close | `WithSessionIdleTimeout(d)` / `session_idle_timeout`; `WithoutSessionIdleTimeout()` (loosens) |
| Keepalive | `30s`, `3` failures | detects half-open clients before the idle timeout | `WithKeepAlive(d, n)` / `keepalive`, `keepalive_failures` |
| Client request timeout | `60s` | an unanswered elicitation must not block a tool forever | `WithClientRequestTimeout(d)` / `client_request_timeout` |
| Close-hook budget | `5s` | bounds slow cleanup without blocking shutdown | `WithCloseHookTimeout(d)` / `close_hook_timeout` |
| Rebinding protection | on | spec security best practice | `WithoutRebindingProtection()` / `disable_rebinding_protection` (loosens) |
| Cross-origin protection | on | blocks browser cross-site calls | `WithTrustedOrigins(...)` / `trusted_origins`; `WithoutCrossOriginProtection()` / `disable_cross_origin_protection` (loosens) |
| Body limit | the server's `MaxRequestBodyBytes` | one limit per server | the server option |
| Tool error text | hidden | no internals to the model | `WithToolErrorText()` / `expose_tool_error_text` (loosens) |
| Raw session id in logs | off | a session id works like a bearer handle | `WithSessionIDInLogs()` / `log_session_id` (loosens) |

Every config key is in `mcpsrvr.Config` with `json`, `yaml` and `env` tags, passed with `mcpsrvr.WithConfig(Config)`; options apply in argument order. A negative duration or count fails construction, reported at `Start`, naming `MCP.<Field>` and the value.

## Hot Paths

- **Mount dispatch:** method check → origin check → body peek → admission → reference header → SDK → writer wrapper (refusal interception, rolling deadline) → slot release, reference removal. Every branch has a spec row: wrong method, cross-origin, too large, cap reached, SDK refusal, JSON-RPC error, stream, JSON, panic.
- **Tool wrapper:** context rebuild (reference found or lost) → recover → handler → success, error (mapped, unmapped, typed nil, panicking `Error()`), or panic → `Resolve` → result → record.
- **Session lifecycle:** admission and release, watcher start and end, hook running (ok, error, panic, block), shutdown close.
- **Record construction:** MCP attribute construction and escaping, session reference hashing, and the golden-record keys for `http.request.completed`, `http.request.failed`, `mcp.tool.failed`, `mcp.tool.panic.recovered` and `mcp.session.close_hook.failed`.
- **Start:** config validation, mount-once check, configurer registration with tool validation.

Coverage is checked with `make cover`. The module is added to the Makefile's module list and to the coverage `-coverpkg` set.

## Risks / Trade-offs

Spikes. Each is a hypothesis until a test proves it (project rule 2). Each test stays in the suite as its slice's RED step.

- [S1, before slice 1] The SDK has no session limit → a test opens more than the planned cap with a bare SDK handler. If the SDK has one, D2 configures it instead.
- [S2, before slice 4] The SDK sends `err.Error()` to the model → a bare `mcp.AddTool` handler returning `errors.New("pq: secret")`; the test asserts the text is absent. D9 holds either way, because the wrapper never returns its error.
- [S3, before slice 5] `ServerSession.Wait` returns on every close path → one test per path. A failure triggers D10's fallback.
- [S4, before slice 1] The SDK's transport refusals are plain text → one test per refusal pins the status and content type that D3 intercepts.
- [S5, before slice 7] Protocol `2026-07-28` changes server-to-client requests and keepalive → read the SDK and spec, and add spec rows for that protocol before coding slice 7.
- [S6, before slice 1] The SDK's tool context is not the HTTP request's → a test reads `engine.StateFrom` inside an SDK tool. If the context does carry the request, D8 becomes a fallback path only.
- [Spec row] Invalid tool arguments are expected as JSON-RPC `-32602`; protocol `2025-11-25` may report them as `isError` tool results. S2's test also checks this, and the spec row is corrected through `/opsx:update` if needed.

Other risks:
- [SDK minor releases change behaviour] → pin `v1.8.x`; the S-tests in the suite catch drift on upgrade.
- [The body peek doubles memory per request] → bounded by `MaxRequestBodyBytes`; covered by a benchmark.
- [`ginmcp` and `mcpsrvr` drift] → parity tests in `internal/paritytest` run both engines from one table.
- [Stateful sessions do not scale horizontally without sticky routing] → documented in the package godoc. Stateless mode is the answer for load-balanced deployments.

## Migration Plan

This is additive. A new module and a nested module are released with their own tags (`mcpsrvr/v0.1.0`, `mcpsrvr/ginmcp/v0.1.0`). No existing API, default or log key changes. Rollback means not importing the module. Consolidation (task group 1) adds `mcp-server` after `scrty-integration` in `AGENTS.md` and `openspec/config.yaml`, and adds the modules to `go.work`, the Makefile and RELEASING.md.

## Open Questions

- The exact SHA-256 prefix length for `srvr.mcp.session.ref`. 16 hex characters is planned; a longer prefix changes no spec row.
- Whether `ginmcp` should also take a `*gin.Engine` hook, as `ginsrvr.WithGinEngine` does. Nothing in the specs needs it.
