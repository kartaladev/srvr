# Proposal

## Why

`core-server` gives teams one server library, but only on the net/http engine. Many services in the organisation already use Gin or Fiber v3 and would otherwise keep their own lifecycle, error and logging code, so incidents would still be triaged from inconsistent logs. This change adds Gin and Fiber v3 engines that behave like the net/http engine: same lifecycle, same RFC 9457 problems, same log schema, same trace correlation.

## What Changes

- New module `github.com/kartaladev/srvr/ginsrvr` (gin v1.12.0, `github.com/kartaladev/prblm/ginprblm`): `ginsrvr.New(opts...)`, `Register(srvr.RouteConfigurer[*gin.RouterGroup]) error`, handlers are plain `gin.HandlerFunc` that report failures through `c.Error(err)` (rendered by ginprblm).
- New module `github.com/kartaladev/srvr/fibersrvr` (fiber v3.7.0, `github.com/kartaladev/prblm/fiberprblm`): `fibersrvr.New(opts...)`, `Register(srvr.RouteConfigurer[fiber.Router]) error`, handlers are `func(fiber.Ctx) error`.
- Both engines expose the same `Start(ctx)`, `Shutdown(ctx)`, `Ready()`, `Addr()` semantics, the same configuration structs and options (`Config`, `LogConfig`, `AccessLogConfig`, `TraceConfig`, `ServiceConfig`, `WithProblemOptions`, `WithErrorHook`, `WithErrorRedactor`), and the same errors (`srvr.ErrConfig`, `srvr.ErrRouteConfig`, `srvr.ErrServerStarted`, `srvr.ErrServerClosed`).
- Engine-neutral pieces of the core (lifecycle state machine, config resolution, trace context, logging pipeline, access-record and triage-record builders) are exported from the core module for engine modules under a clearly marked, documented engine-support package, not duplicated.
- One per-request state, `engine.RequestState` (fetched with `engine.StateFrom(ctx)`), shared by every engine and extended by later changes instead of new holders.
- A route listing, `engine.Routes` (`{Method, Pattern, Configurer}` per declared route), passed after the configure phase to checks installed with the new core option `srvr.WithRouteCheck`; a failing check fails `Start`.
- A `server.config.warning` log event (`srvr.warning.code`, `srvr.warning.message`) for configurations that work but are likely unintended.
- A cross-engine parity suite: one Examples table, one row per engine, asserting identical problem bodies, statuses, headers and log records; documented exceptions where an engine cannot match.
- **Compatibility decision:** this adds public API to the core module (the engine-support package and `srvr.WithRouteCheck`) and an additive log event (no `srvr.log.schema` bump) and two new modules with their own dependency sets. The core module's dependency set is unchanged (standard library plus prblm).

## Defaults and Customization

Zero configuration on either engine gives exactly the `core-server` defaults: `:8080`, the same timeouts, 1 MiB headers, 4 MiB bodies, 15s drain, RFC 9457 problems with `trace_id` and no 5xx detail, JSON access records to stdout at `INFO`, no bodies, credentials, query, path or client address in logs, honoured `traceparent`. Unmatched paths and methods are logged as `http.request.failed` at `WARN` on every engine. Fiber exceptions (decided): fasthttp has no header-read timeout, so `ReadHeaderTimeout` is not applied and `ReadTimeout` covers headers and body; the header limit is `ReadBufferSize`, set to `MaxHeaderBytes` capped at 64 KiB (allocated per connection), so headers between 64 KiB and `MaxHeaderBytes` get `431` on fiber only; unknown methods get fiber's plain `501` before any middleware.

Engine-specific defaults srvr sets, each overridable:
- Gin: gin's global mode left alone (srvr never calls `gin.SetMode`; gin has no per-engine mode, so in its default debug mode gin itself prints `[GIN-debug]` lines to stdout; srvr writes one `gin_debug_mode` warning record at construction telling consumers to set `GIN_MODE=release`), `ContextWithFallback` on (request context reaches `c`), `HandleMethodNotAllowed` on (405 instead of 404), `RedirectTrailingSlash` off (a `404` problem instead of an HTML `301`), `Allow` sorted like net/http, request bodies limited with `http.MaxBytesReader` at `MaxRequestBodyBytes`, gin's own logger and recovery not installed, gin's trusted proxies set to none (gin trusts every proxy by default).
- Fiber: `ErrorHandler` from fiberprblm, `BodyLimit` from `MaxRequestBodyBytes` (unlimited mapped explicitly, since Fiber treats `<= 0` as 4 MiB), read/write/idle timeouts from `Config`, `ReadBufferSize` from `MaxHeaderBytes` capped at 64 KiB, duplicate routes rejected at `Start` (fiber accepts them silently), `Immutable` off (srvr copies the values it keeps), startup banner off, fiber's proxy header off.

Opt-ins: an engine customisation hook (`ginsrvr.WithEngine(func(*gin.Engine))`, `fibersrvr.WithFiberConfig(func(*fiber.Config))`) applied before srvr's mandatory settings; settings srvr needs for correctness (error handler, recovery, context fallback, 405 handling) are re-asserted after it runs, and a gin hook that registers routes or middleware (which would run outside srvr's pipeline) is rejected at construction. Enabling fiber `Immutable` or gin trusted proxies is allowed and named as **loosening** for the proxy case.

## Capabilities

### New Capabilities
- `engine-support`: the engine-neutral lifecycle, configuration, trace and logging contract that every engine module implements, exposed by the core for engine modules.
- `gin-engine`: serving routes through Gin with srvr's lifecycle, problems and logs, including Gin's quirks.
- `fiber-engine`: serving routes through Fiber v3 with srvr's lifecycle, problems and logs, including Fiber's quirks.
- `engine-parity`: the guarantee that net/http, Gin and Fiber produce identical client-visible and log-visible behaviour, and the documented exceptions.

### Modified Capabilities
<!-- none: core-server is archived and its capabilities (openspec/specs) keep their requirements unchanged for net/http; the new core behaviour (route listing, route checks, configuration warnings, consumer-fault containment on every engine) is specified in engine-support -->

## Scope by Gall's Law

The simplest working version: the Gin engine serving one route with a problem error, an access record with `trace_id`, and clean shutdown, built on engine-neutral pieces lifted out of the working net/http engine (only what Gin needs). Then Fiber end to end on the same pieces, then the parity suite, then the route listing that scrty-integration's collision check consumes.

Deliberately left for later: metrics and tracing on any engine (`otel-observability`), scrty middleware placement (`scrty-integration`), debug buffering (`triage-debug-buffer`), Gin `UseH2C`, Fiber prefork (never enabled: prefork forks processes and breaks `Start`/`Shutdown` semantics; it lives in fiber's `ListenConfig`, which srvr owns and does not expose).

## Impact

- New modules `ginsrvr` and `fibersrvr`; their consumers pull in gin or fiber/fasthttp respectively, never both.
- New exported engine-support API in the core; changes to it are compatibility decisions for every engine.
- Builds on `core-server`. Assumptions the consolidation group must check: `Server` lifecycle states and errors (`ErrServerStarted`, `ErrServerClosed`, `ErrConfig`, `ErrRouteConfig`); `Register` returning `error` and `Configure` running during `Start`; `Config`/`LogConfig`/`AccessLogConfig`/`TraceConfig`/`ServiceConfig` names and defaults and their entry points (`srvr.WithConfig`, `srvr.WithLogConfig`, `srvr.WithAccessLogConfig`, `srvr.WithServiceConfig`); core D5's per-request holder (moved into `engine.RequestState`); unmatched 404/405 logged as `http.request.failed` at `WARN`; the `srvr.log.schema` policy (additive keys keep the version); event names (`http.request.completed`, `http.request.failed`, `server.*`) and attribute keys; the `trace_id` problem member; srvr owning prblm's `OnError` with `WithErrorHook` for consumers; triage record builder and fingerprint inputs (method, route, error-type chain); `TraceFromContext` and `LoggerFromContext` accessors. Consolidation (2026-11-17) confirmed each against the merged code and added the guards `core-server` grew during verification, which the engines reuse through `engine` (design D10): guarded value text, the cyclic-chain guard, the guarded listener and address check, no lock held across consumer code, redacted and capped lifecycle error text, never-suppressed failure records, the containing log handler and the process-wide `LogWriteErrors`.
