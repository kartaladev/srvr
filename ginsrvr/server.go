package ginsrvr

import (
	"context"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/prblm/ginprblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

// engineName names the gin engine in engine-specific option errors.
const engineName = "gin"

// Server is an HTTP server on the Gin engine with srvr's lifecycle,
// configuration, RFC 9457 problems and logs. Create it with [New], add
// routes with [Server.Register] and run it with [Server.Start]; its
// methods behave like those of srvr.Server and are safe for concurrent use.
type Server struct {
	core   *engine.Core
	lc     *engine.Lifecycle[srvr.RouteConfigurer[*gin.RouterGroup]]
	engine *gin.Engine
	// locations maps each route handler (by function pointer) to its code
	// location, resolved once by the configure phase and only read while
	// serving.
	locations map[uintptr]engine.Location
}

var _ srvr.RouteRegistrar[*gin.RouterGroup] = (*Server)(nil)

// New returns a server configured by opts, the options of package srvr
// plus this package's own. It does not open a listener. Invalid
// configuration returns an error wrapping srvr.ErrConfig naming every
// offending field and value, including an option meant for another engine.
func New(opts ...srvr.Option) (*Server, error) {
	core, err := engine.ResolveConfig(opts)
	if err != nil {
		return nil, err
	}

	values, err := core.EngineOptions(engineName)
	if err != nil {
		return nil, err
	}

	s := &Server{core: core, lc: engine.NewLifecycle[srvr.RouteConfigurer[*gin.RouterGroup]](core)}

	if s.engine, err = newEngine(core, values, s.requestStage); err != nil {
		return nil, err
	}

	if gin.IsDebugging() {
		core.Warn(context.Background(), engine.WarnGinDebugMode, "gin runs in debug mode and prints [GIN-debug] lines to standard output; set GIN_MODE=release or call gin.SetMode(gin.ReleaseMode) (srvr does not change gin's process-wide mode)")
	}

	return s, nil
}

// WithEngine adds hook, which customises the gin engine at construction,
// before srvr installs its pipeline. srvr first sets the engine's trusted
// proxies to none (gin trusts every proxy by default, so c.ClientIP would
// believe X-Forwarded-For from anyone); a hook may loosen that with
// SetTrustedProxies. After the hooks srvr re-asserts the settings its
// behaviour depends on (ContextWithFallback, HandleMethodNotAllowed,
// RedirectTrailingSlash off) and installs its own recovery.
//
// A hook must not add middleware or routes: they would run outside srvr's
// pipeline, so New rejects such a hook, a nil hook and a hook that panics
// with an error wrapping srvr.ErrConfig. Register routes through
// [Server.Register] instead.
func WithEngine(hook func(*gin.Engine)) srvr.Option {
	return engine.ForEngine(engineName, "ginsrvr.WithEngine", hook)
}

// newEngine returns the gin engine: trusted proxies set to none, the
// consumer's hooks applied, srvr's settings re-asserted and srvr's
// pipeline installed as its global middleware, before any consumer group:
// srvr's request stage, ginprblm's middleware, which renders recorded
// errors and statuses as problems, srvr's error guard and ginprblm's
// recovery. Both ginprblm stages are built from the server's prblm
// options, which ResolveConfig validated, so they cannot panic here.
func newEngine(core *engine.Core, hooks []any, requestStage gin.HandlerFunc) (*gin.Engine, error) {
	e := gin.New()
	_ = e.SetTrustedProxies(nil) // trusting none cannot fail: no CIDR to parse

	for _, h := range hooks {
		if err := applyHook(e, h); err != nil {
			return nil, err
		}
	}

	assertSettings(e)

	opts := core.ProblemOptions()
	e.Use(requestStage, ginprblm.Middleware(opts...), guardErrors, ginprblm.Recovery(opts...))

	return e, nil
}

// applyHook runs one WithEngine hook under recover and rejects it when it
// added middleware or routes.
func applyHook(e *gin.Engine, h any) (err error) {
	hook, _ := h.(func(*gin.Engine))
	if hook == nil {
		return engine.ConfigError("WithEngine", "<nil>", "hook is nil")
	}

	name := engine.FuncLocation(hook).Function()

	defer func() {
		if v := recover(); v != nil {
			err = engine.ConfigError("WithEngine", name, "hook panicked: "+engine.ValueText(v))
		}
	}()

	handlers, routes := len(e.Handlers), len(e.Routes())

	hook(e)

	if len(e.Handlers) != handlers || len(e.Routes()) != routes {
		return engine.ConfigError("WithEngine", name, "hook added middleware or routes, which would run outside srvr's pipeline; use Register")
	}

	return nil
}

// assertSettings sets the engine settings srvr's behaviour depends on:
// the request context behind *gin.Context (so srvr.TraceFromContext(c),
// srvr.LoggerFromContext(c) and c.Done work on it), 405 problems for
// wrong methods instead of 404, and a 404 problem for a trailing-slash
// mismatch instead of gin's HTML 301 redirect (net/http answers 404 too).
func assertSettings(e *gin.Engine) {
	e.ContextWithFallback = true
	e.HandleMethodNotAllowed = true
	e.RedirectTrailingSlash = false
}

// requestStage is srvr's outermost gin stage. Before the chain runs, so
// that a request aborted with http.ErrAbortHandler keeps them, it records
// the matched route and its handler's location in the request state; and
// it sorts the Allow header gin set for a 405 (in the order its method
// trees were created), so the problem rendered inside carries it like
// net/http.
func (s *Server) requestStage(c *gin.Context) {
	state, ok := engine.StateFrom(c.Request.Context())
	if ok {
		if route := c.FullPath(); route != "" {
			state.Route, state.Location = route, s.locations[handlerKey(c.Handler())]
		}
	}

	if allow := c.Writer.Header().Get("Allow"); allow != "" {
		methods := strings.Split(allow, ", ")
		slices.Sort(methods)
		c.Writer.Header().Set("Allow", strings.Join(methods, ", "))
	}

	c.Next()

	if ok {
		classify(c, state)
	}
}

// guardErrors replaces every error recorded in c.Errors with its guarded
// value once the chain returned, before ginprblm's middleware resolves the
// last one: prblm's resolution would loop forever on an error whose Unwrap
// chain is cyclic, which gets the safe 500 problem instead while the
// access record still describes the original error. A cyclic error passed
// to c.Error never gets here: gin's own c.Error loops on it first
// (documented in the package overview).
func guardErrors(c *gin.Context) {
	c.Next()

	for _, e := range c.Errors {
		e.Err = engine.GuardChain(e.Err)
	}
}

// classify keeps the failure ginprblm reported only when it is one: an
// error recorded with c.Error, a recovered panic, or no matched route.
// ginprblm also reports a status-only abort such as c.AbortWithStatus(401)
// to srvr's hook, with an error srvr cannot tell apart, and that request
// did not fail (design D2).
func classify(c *gin.Context, state *engine.RequestState) {
	if state.Failed() && len(c.Errors) == 0 && state.Matched() && !state.Panicked() {
		state.ClearFailure()
	}
}

// Register adds conf to the configurers run when the server starts. Each
// registration runs Configure once, in registration order, during Start,
// with the root router group. A nil or typed-nil conf returns an error
// wrapping srvr.ErrRouteConfig. Once Start was called it returns
// srvr.ErrServerStarted, or srvr.ErrServerClosed when the server stopped.
func (s *Server) Register(conf srvr.RouteConfigurer[*gin.RouterGroup]) error {
	return s.lc.Register(conf)
}

// Ready returns a channel closed once the listener is bound and the server
// accepts connections. It is never closed when Start fails.
func (s *Server) Ready() <-chan struct{} { return s.lc.Ready() }

// Addr returns the bound address once the server is ready, and the empty
// string before.
func (s *Server) Addr() string { return s.lc.Addr() }

// Start configures the routes, binds the listener, closes [Server.Ready]
// and serves until ctx is done or [Server.Shutdown] is called, then drains
// in-flight requests for at most ShutdownTimeout. It follows
// srvr.Server.Start in every other respect. A configurer that panics,
// registers conflicting routes (gin panics) or registers a route with a
// nil handler fails Start with an error wrapping srvr.ErrRouteConfig.
func (s *Server) Start(ctx context.Context) error {
	return s.lc.Start(ctx, func(configurers []srvr.RouteConfigurer[*gin.RouterGroup]) (engine.Runner, error) {
		if err := s.configure(configurers); err != nil {
			return nil, err
		}

		return engine.HTTPRunner(s.core.Config(), s.handler()), nil
	})
}

// Shutdown stops the server gracefully, as srvr.Server.Shutdown does.
func (s *Server) Shutdown(ctx context.Context) error { return s.lc.Shutdown(ctx) }

// configure runs each configurer on the root group under recover and
// checks the routes it added.
func (s *Server) configure(configurers []srvr.RouteConfigurer[*gin.RouterGroup]) error {
	for _, c := range configurers {
		if err := engine.Configure(c, &s.engine.RouterGroup); err != nil {
			return err
		}

		if err := checkHandlers(s.engine.Routes(), c); err != nil {
			return err
		}
	}

	s.locations = make(map[uintptr]engine.Location)
	for _, r := range s.engine.Routes() {
		s.locations[handlerKey(r.HandlerFunc)] = engine.FuncLocation(r.HandlerFunc)
	}

	return nil
}

// handlerKey identifies a route handler by its function pointer.
func handlerKey(h gin.HandlerFunc) uintptr { return reflect.ValueOf(h).Pointer() }

// checkHandlers rejects a route with a nil handler, which gin accepts and
// would answer by panicking. It runs after each configurer, so a nil
// handler found is one that c registered.
func checkHandlers(routes gin.RoutesInfo, c srvr.RouteConfigurer[*gin.RouterGroup]) error {
	for _, r := range routes {
		if r.HandlerFunc == nil {
			return engine.RouteError("%q (%T): handler is nil", r.Method+" "+r.Path, c)
		}
	}

	return nil
}

// handler is the gin engine wrapped in srvr's outer stages.
func (s *Server) handler() http.Handler { return s.core.StartTrace(s.stages()) }

// stages are the outer stages inside the trace context stage: the access
// record and the body limit, which gin does not inherit from http.Server
// because the core applies it as a stage too.
func (s *Server) stages() http.Handler {
	return s.core.LogAccess(engine.LimitBody(s.core.Config().MaxRequestBodyBytes, s.engine))
}
