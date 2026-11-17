// Package fibersrvr runs srvr servers on the Fiber v3 engine: the same
// lifecycle, configuration (every srvr option and config struct), RFC 9457
// problems rendered by github.com/kartaladev/prblm/fiberprblm, trace
// context and log records as srvr's net/http engine.
//
// Routes are contributed by srvr.RouteConfigurer[fiber.Router] values
// registered with [Server.Register]; handlers return errors, which srvr
// renders as problems with the request's trace_id (a *fiber.Error code is
// a status hint; its message is never sent). Duplicate routes, which fiber
// accepts silently and of which it serves only the first, fail Start. They
// are compared as fiber matches them: parameter names ignored, letter case
// ignored unless the app's CaseSensitive is on, a trailing slash ignored
// unless its StrictRouting is on (both as set through [WithFiberConfig]),
// and routes of mounted sub-apps at their full mounted path.
//
// srvr derives fiber's configuration from its own (error handler, body
// limit, read, write and idle timeouts, header buffer) and owns the
// ListenConfig: the startup message is off and prefork is never enabled,
// because it forks processes. Use [WithFiberConfig] to customise the rest.
//
// # Request context
//
// fiber.Ctx is pooled and reused once the handler returns, and it never
// cancels. srvr gives every request a context of its own,
// c.Context(): srvr.TraceFromContext and srvr.LoggerFromContext accept c
// or c.Context(), and c.Context() is cancelled when a drain deadline forces
// the server to stop. Pass c.Context(), never c, to work that outlives the
// handler. srvr copies every value it keeps out of fiber's buffers, so
// Immutable is not required.
//
// # Documented differences from net/http
//
//   - fasthttp does not notice a client that disconnects mid-request; the
//     handler is not told.
//   - The header limit is fiber's ReadBufferSize, allocated per
//     connection: MaxHeaderBytes is capped at 64 KiB (a larger configured
//     value writes one fiber_header_limit_capped warning), so headers
//     between 64 KiB and MaxHeaderBytes get a 431 problem on fiber only.
//   - fasthttp has no header-only read timeout: ReadHeaderTimeout is not
//     applied, and ReadTimeout bounds reading headers and body together.
//   - A request with an unknown method (for example FOO) gets fiber's
//     plain-text 501 before any middleware: no problem, no access record.
//   - A panic with http.ErrAbortHandler is recovered like any panic (a 500
//     problem); fasthttp has no abort sentinel and would crash on a
//     re-raised panic.
//   - fasthttp rejects an oversized body (413), oversized headers (431)
//     and a read timeout (408) before routing; srvr logs them as failed
//     requests from that status, without http.route, and the body limit
//     trips before the handler runs.
//   - A handler that sets a buffered body and then returns an error gets
//     the problem instead of that body; a streaming body that has started
//     is kept.
//   - A path-scoped Use (for example Use("/assets", static)) is middleware
//     to fiber and is not a declared route. A sub-app mounted with
//     Use(prefix, subApp) is different: its routes, at any mount depth, are
//     listed for route checks at their full mounted path and attributed to
//     the configurer that mounted it, and take part in the duplicate check;
//     its own middleware is not listed. srvr reads them from fiber's
//     unexported fields when Start runs, so routes added to a sub-app after
//     Start are not listed.
package fibersrvr
