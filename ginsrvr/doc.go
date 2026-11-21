// Package ginsrvr runs srvr servers on the Gin engine: the same lifecycle,
// configuration (every srvr option and config struct), RFC 9457 problems
// rendered by github.com/kartaladev/prblm/adapters/ginprblm, trace context and
// log records as srvr's net/http engine.
//
// Routes are contributed by srvr.RouteConfigurer[*gin.RouterGroup] values
// registered with [Server.Register]. Handlers stay plain gin.HandlerFunc
// and report failures with c.Error(err) or c.AbortWithError: srvr renders
// the last recorded error as a problem with the request's trace_id. A
// status-only abort such as c.AbortWithStatus(401), which ginprblm reports
// as ginprblm.ErrStatusOnly, is answered with a problem too, but is not
// logged as a failed request (a status-only 5xx is logged as
// http.request.completed at ERROR).
//
// With srvr.WithErrorRenderer, srvr's own error middleware and recovery
// replace ginprblm's: the consumer's renderer answers the last recorded
// error, a recovered panic (a *srvr.PanicError) and the router's 404 and
// 405 (srvr.ErrNotFound, *srvr.MethodNotAllowedError), including a static
// mount's; a status the handler set before c.AbortWithError is replaced
// by the renderer's. A status-only answer is not an error answer: any
// 4xx or 5xx status sent with an empty body and no recorded error (an
// abort such as c.AbortWithStatus(401), but also c.Status(404) with
// nothing written) is sent bare, the renderer is not called, and the
// error hook receives it as ginprblm.ErrStatusOnly with that status.
//
// srvr builds the engine with gin.New and installs its own pipeline before
// any consumer group: trace context, access record, body limit
// (MaxRequestBodyBytes, through http.MaxBytesReader), problem rendering and
// panic recovery; gin's Logger and Recovery are never installed. It turns
// ContextWithFallback and HandleMethodNotAllowed on and
// RedirectTrailingSlash off, so srvr.TraceFromContext(c) and
// srvr.LoggerFromContext(c) work on *gin.Context, a wrong method gets a 405
// problem with a sorted Allow header and a trailing-slash mismatch gets a
// 404 problem, as on net/http. It trusts no proxy for c.ClientIP. Use
// [WithGinEngine] to customise the engine.
//
// # Gin's global mode
//
// Gin's mode is process-wide and srvr never changes it. In gin's default
// debug mode gin itself prints [GIN-debug] lines to standard output, mixed
// into the JSON log stream; srvr writes one server.config.warning record
// with srvr.warning.code gin_debug_mode when a server is constructed in
// debug mode. Set GIN_MODE=release in the environment, or call
// gin.SetMode(gin.ReleaseMode) before New.
//
// # Documented differences from net/http
//
//   - Gin does not route HEAD to GET handlers: HEAD to a GET-only route is
//     a 405 problem, and Allow never lists HEAD.
//   - A handler that writes a success body and later calls c.Error keeps
//     its response; the failed record has srvr.response.committed true.
//   - When the client has disconnected and the handler reports the
//     context's cancellation, nothing is written (net/http writes the
//     problem to the closed connection).
//   - Do not pass an error whose Unwrap chain is cyclic (an error that
//     unwraps to itself) to c.Error: gin's c.Error calls errors.As on it
//     before srvr sees it and never returns, so the request never
//     completes. On net/http and fiber such an error gets a 500 problem.
//     An error with a cyclic chain that reaches c.Errors in any other way
//     is still answered with the safe 500 problem.
//   - [Static] mounts are served from gin's no-route and no-method chains,
//     so root-group middleware runs around the mounts even when added
//     after Static, and, once a server has a mount, also for requests gin
//     answers with its own 404 or 405.
package ginsrvr
