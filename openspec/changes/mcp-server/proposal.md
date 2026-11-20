# Proposal

## Why

Teams that build on srvr now also expose Model Context Protocol (MCP) servers over HTTP. Today they wire an MCP SDK handler by hand next to srvr. That handler gets none of srvr's guarantees: no rendered errors, no triage records, no shutdown behaviour, no safe defaults. Tool code also follows a different model from srvr's "configurer registers handlers, handlers return `error`". This change lets a consumer serve a stateful MCP server on a srvr engine through the same `Register`/`Configure` model, with srvr's error, logging, lifecycle and security guarantees.

## What Changes

- New module `github.com/kartaladev/srvr/mcpsrvr` built on the official Go SDK `github.com/modelcontextprotocol/go-sdk` (v1.8.0 or later). The SDK stays the protocol engine: JSON-RPC, schema inference and validation, the Streamable HTTP transport. srvr adds the HTTP mount, defaults, lifecycle, logging and the tool programming model.
- **Layer 1, mount.** An MCP endpoint built from an SDK `*mcp.Server` is registered as a route configurer, the same way `Static` is: `s.Register(mcpsrvr.Handler("/mcp", ms, opts...))` for net/http, plus a gin counterpart. A consumer can keep using the SDK API (`mcp.AddTool`) unchanged.
- **Layer 2, srvr-style capabilities.** A `ToolConfigurer` with `Configure(*mcpsrvr.Toolbox)` registers tools through `Register`, and tool handlers have the shape `func(ctx context.Context, in In) (Out, error)`. srvr renders a returned error into a tool error result. Only what prblm would expose for a 4xx problem reaches the model; anything else gets a generic message. srvr also recovers panics in tool handlers and writes a failed record with a fingerprint from the error types and the tool name.
- **Sessions.** Stateful sessions are the default. Stateless mode is an explicit opt-out. Inside a tool, `mcpsrvr.SessionFromContext(ctx)` gives the session, the same way `srvr.TraceFromContext` and `srvr.LoggerFromContext` work. Through it a tool can:
  - keep per-session state, which is released when the session closes;
  - send notifications and progress;
  - make server-to-client requests (elicitation, sampling, roots), with a timeout and a check of the client's declared capabilities.
- **Lifecycle.** srvr does the following:
  - closes open sessions in a `BeforeDrain` shutdown hook;
  - extends the write deadline for the SSE stream so the server `WriteTimeout` does not cut it;
  - caps concurrent sessions;
  - expires idle sessions;
  - shows by a goleak test that every session goroutine ends after `Shutdown`.
- **Transport errors.** Errors at the transport level, before the protocol starts (for example the session cap is reached, a session id is unknown, or the method or media type is wrong), are answered as RFC 9457 problems through prblm. JSON-RPC responses inside the protocol keep MCP's own error format. This is a deliberate, documented exception to "every error through prblm".
- **Triage records.** Access and failed records of MCP requests gain `mcp.method.name`, the tool name, the JSON-RPC request id and `mcp.session.id`, following OpenTelemetry semantic conventions where they exist.
- **Auth.** Authentication goes through the scrty bridge from `scrty-integration`: OAuth protected-resource metadata, and the endpoint collision check with `srvr.WithRouteCheck`.

## Defaults and Customization

Zero configuration (`mcpsrvr.Handler(path, server)` registered on a server) gives:
- **Sessions:** stateful, with a non-zero idle timeout, a cap on concurrent sessions and a keepalive for protocols that support it. Sessions close before the drain on every stop path. The values and their reasons are fixed in design.md and documented in godoc.
- **Protection:** DNS-rebinding protection on (SDK default) and cross-origin protection through `http.NewCrossOriginProtection`. The request body is limited.
- **No leaks to clients:**
  - Tool errors never carry `err.Error()` text, stack traces or internal paths to the model.
  - Transport-level refusals are problems with `trace_id` and no error text.
  - 5xx answers carry no internals.
- **No leaks to logs:** tool arguments, tool results, elicitation answers, sampling content, credentials and session tokens are never logged at any level.
- **Timeouts:** a server-to-client request that the client never answers fails with a typed error after a default timeout.

Opt-ins:
- `WithStateless()` / `Config{Stateless: true}` turns sessions off. `SessionFromContext` then reports no session, and server-to-client requests fail with a typed error.
- Every setting in the list above is configurable through options and a tagged `mcpsrvr.Config` that any loader can fill. Invalid values fail construction with an error naming the field and value.
- **Opt-ins that loosen a safety default.** Each has its own explicit name, settled in design:
  - turning DNS-rebinding or cross-origin protection off;
  - removing the session cap or the idle timeout;
  - removing the body limit;
  - logging tool names together with their arguments;
  - exposing error text to the model.

## Capabilities

### New Capabilities
- `mcp-endpoint`: serving an MCP server over Streamable HTTP on a srvr engine. It covers:
  - mounting as a route configurer;
  - stateful and stateless modes;
  - session cap, idle expiry and keepalive;
  - SSE write deadline;
  - shutdown behaviour;
  - origin and rebinding protection;
  - transport-level problems;
  - MCP attributes on triage records;
  - auth through the scrty bridge.
- `mcp-tools`: srvr's tool programming model:
  - `ToolConfigurer` and `Toolbox` registration;
  - error-returning handlers;
  - error rendering to tool results;
  - panic recovery;
  - failed records and fingerprints.
- `mcp-sessions`: what a tool can do with its session:
  - `SessionFromContext`;
  - per-session state and its release;
  - notifications and progress;
  - server-to-client requests with timeouts and capability checks.

### Modified Capabilities
<!-- None at planning time. The consolidation group re-checks whether `structured-logging`
     (new MCP keys on access and failed records), `route-registration` and `engine-support`
     need delta specs once `scrty-integration`, `engine-internal-split` and
     `pluggable-error-renderer` are merged. -->

## Scope by Gall's Law

The simplest version that works end to end is a net/http srvr server with `s.Register(mcpsrvr.Handler("/mcp", ms))` for an SDK server with one tool. In that version:
- a client initializes a stateful session and calls the tool;
- the access record names the MCP method and session;
- `Shutdown` closes the open session before the drain, and goleak passes.

Each later slice adds one capability to that working system:
1. net/http mount: stateful default and stateless opt-out, session cap, idle timeout, keepalive, SSE write deadline, shutdown close, origin and rebinding protection, session identity.
2. gin mount, with a parity test against slice 1 in both modes.
3. MCP attributes on access and failed records.
4. `Toolbox` configurer, error rendering, panic recovery and `SessionFromContext`.
5. Per-session state, released on session close.
6. Notifications and progress.
7. Server-to-client requests: elicitation, sampling and roots.
8. Auth through the scrty bridge.

Spikes that must run before the slice that depends on them. Each claim below is a hypothesis until a test proves it (project rule 2):
- **Before slice 4:** whether the SDK returns a tool handler's `err.Error()` to the model. This test is the RED step of slice 4.
- **Before slice 1:** whether the SDK has no limit on concurrent sessions.
- **Before slice 5:** how srvr learns that a session closed. The SDK docs show no close callback. Waiting on `ServerSession.Wait` would start a goroutine, which then needs a goleak proof.
- **Before slice 7:** how protocol version `2026-07-28` (sessionless direction, SEP-2567) changes server-to-client requests and keepalive.
- **Before slice 1:** whether the SDK writes plain-text transport errors that srvr must intercept to render them as problems.

Deliberately left for later changes:
- **Fiber.** Serving through fasthttp may buffer whole responses and break SSE streaming. That is unproven; a later change starts with a spike that tests it.
- **MCP resources and prompts** in the `Toolbox` style. Consumers can register them with the SDK API through the Layer 1 mount meanwhile.
- **OpenTelemetry spans and metrics for MCP methods.** These belong to `otelsrvr` once this change is merged.
- **Stream resumption** (the SDK's `EventStore`).

## Impact

- New module `srvr/mcpsrvr` with the SDK's dependency graph (jsonschema-go, jwt, oauth2, uritemplate, x/sync and others). Consumers who don't import it are unaffected; the core module's dependency set does not change. The design decides where the gin mount lives so that `mcpsrvr` does not import gin, either a nested module or an engine-neutral handler that the gin module adapts.
- **Compatibility decision:** this adds new public API in a new module: `Handler`, `ToolConfigurer`, `Toolbox`, `Tool`, `SessionFromContext`, `Config` and the options. It may also add engine-neutral hooks to `srvr/engine`, for example to extend a request's write deadline.
- **Compatibility decision:** this adds new log keys (`mcp.method.name`, tool name, JSON-RPC request id, `mcp.session.id`) and new failed-record events for tools. Added keys keep `srvr.log.schema`'s version; the golden-record test gains them.
- **Compatibility decision:** a documented exception to "every error response through prblm" for JSON-RPC bodies inside the protocol.
- Builds on these earlier changes and assumes the following. The consolidation group must check every name:
  - `core-server`: `Group.Mount` with prefix stripping, `RouteConfigurer`/`Register`, `WithShutdownHook` with `BeforeDrain`, the `WriteTimeout` default, the access log and failed records, and the pipeline order.
  - `gin-and-fiber-engines`: the `ginsrvr` route configurers, `engine.RequestState` and `engine.Routes`.
  - `static-files`: a configurer factory shared across engines.
  - `engine-internal-split`: where shared engine pieces live after the split.
  - `pluggable-error-renderer`: how transport-level problems are rendered.
  - `scrty-integration`: the bridge, `WithRouteCheck` collision checking, and placement of the chain before the router.
- The change order recorded in `AGENTS.md` and `openspec/config.yaml` gains `mcp-server` after `scrty-integration`. Group 1 (consolidation) makes that edit.
- Version pins: go-sdk v1.8.0 (v1.9.0 is in pre-release), Go 1.27.
