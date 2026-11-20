package ginsrvr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/prblm/adapters/ginprblm"
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
	// statics are the static mounts the configure phase installed in gin's
	// no-route chain, nil without one; only read while serving.
	statics *staticMounts
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
		core.Warn(context.Background(), engine.WarnGinDebugMode,
			"gin runs in debug mode and prints [GIN-debug] lines to standard output; "+
				"set GIN_MODE=release or call gin.SetMode(gin.ReleaseMode) "+
				"(srvr does not change gin's process-wide mode)")
	}

	core.SetDefaultLogger()

	return s, nil
}

// WithGinEngine adds hook, which customises the gin engine at construction,
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
func WithGinEngine(hook func(*gin.Engine)) srvr.Option {
	return engine.Only(engineName, "ginsrvr.WithGinEngine", hook)
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

	opts := append(core.ProblemOptions(), prblm.AddErrorHook(clearStatusOnly))
	e.Use(requestStage, ginprblm.Middleware(opts...), guardErrors, ginprblm.Recovery(opts...))

	return e, nil
}

// applyHook runs one WithGinEngine hook under recover and rejects it when it
// added middleware or routes.
func applyHook(e *gin.Engine, h any) (err error) {
	hook, _ := h.(func(*gin.Engine))
	if hook == nil {
		return engine.NewConfigError("WithGinEngine", "<nil>", "hook is nil")
	}

	name := engine.FuncLocation(hook).Function()

	defer func() {
		if v := recover(); v != nil {
			err = engine.NewConfigError("WithGinEngine", name, "hook panicked: "+engine.ValueText(v))
		}
	}()

	handlers, routes := len(e.Handlers), len(e.Routes())

	hook(e)

	if len(e.Handlers) != handlers || len(e.Routes()) != routes {
		return engine.NewConfigError("WithGinEngine", name,
			"hook added middleware or routes, which would run outside srvr's pipeline; use Register")
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
// the matched route and its handler's location in the request state, the
// pattern of the static mount containing the path of a request no route
// matched (see [Static]), or that the router answers a request no route
// matched; and
// it sorts the Allow header gin set for a 405 (in the order its method
// trees were created), so the problem rendered inside carries it like
// net/http.
func (s *Server) requestStage(c *gin.Context) {
	allowReadOnly := false

	state, ok := engine.StateFrom(c.Request.Context())
	if ok {
		if route := c.FullPath(); route != "" {
			state.SetRoute(route)
			state.SetLocation(s.locations[handlerKey(c.Handler())])
		} else if s.statics != nil {
			// gin's no-route or no-method chain, which ends with the
			// static dispatcher after the root group's consumer
			// middleware: begin decides whether a mount answers, and only
			// the dispatcher, if it runs, can tell a miss (design D5).
			allowReadOnly = s.statics.begin(c, state)
		} else {
			// Without a static mount only srvr's stages run in gin's
			// no-route and no-method chains, so the router answers this
			// request.
			state.MarkRouterAnswered()
		}
	}

	if allow := c.Writer.Header().Get("Allow"); allow != "" {
		methods := strings.Split(allow, ", ")
		if allowReadOnly {
			methods = append(methods, http.MethodGet, http.MethodHead)
		}

		slices.Sort(methods)
		c.Writer.Header().Set("Allow", strings.Join(slices.Compact(methods), ", "))
	}

	c.Next()
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

// clearStatusOnly undoes the failure srvr's own prblm hook, which runs
// just before it (engine.Core.ProblemOptions), recorded for a status-only abort such as
// c.AbortWithStatus(401): ginprblm reports one to the hooks wrapped around
// ginprblm.ErrStatusOnly, and that request did not fail (design D2). gin's
// own 404 and 405 match ErrStatusOnly too, so a request no route matched
// keeps its failure, as does a recovered panic.
func clearStatusOnly(ctx context.Context, err error, _ *prblm.Problem) {
	state, ok := engine.StateFrom(ctx)
	if ok && state.Matched() && !state.Panicked() && errors.Is(err, ginprblm.ErrStatusOnly) {
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
	return s.lc.Start(ctx, func(
		configurers []srvr.RouteConfigurer[*gin.RouterGroup],
	) (engine.Runner, engine.Routes, error) {
		routes, err := s.configure(configurers)
		if err != nil {
			return nil, nil, err
		}

		return engine.HTTPRunner(s.core.Config(), s.handler()), routes, nil
	})
}

// Shutdown stops the server gracefully, as srvr.Server.Shutdown does.
func (s *Server) Shutdown(ctx context.Context) error { return s.lc.Shutdown(ctx) }

// configure runs each configurer on the root group under recover, checks
// the routes it added, resolves the handlers' locations and returns the
// route listing, each route attributed to the configurer that added it.
func (s *Server) configure(configurers []srvr.RouteConfigurer[*gin.RouterGroup]) (engine.Routes, error) {
	var (
		routes  engine.Routes
		statics staticMounts
	)

	before := s.engine.Routes()

	for _, c := range configurers {
		if sc, ok := c.(*staticConfigurer); ok {
			if err := statics.add(sc); err != nil {
				return nil, err
			}

			continue
		}

		if err := engine.Configure(c, &s.engine.RouterGroup); err != nil {
			return nil, err
		}

		after := s.engine.Routes()
		if err := checkHandlers(after, c); err != nil {
			return nil, err
		}

		routes = append(routes, added(before, after, fmt.Sprintf("%T", c))...)
		before = after
	}

	s.locations = make(map[uintptr]engine.Location, len(before))
	for _, r := range before {
		s.locations[handlerKey(r.HandlerFunc)] = engine.FuncLocation(r.HandlerFunc)
	}

	if len(statics.mounts) > 0 {
		// After the loop, so the rebuilt no-route and no-method chains
		// include every middleware consumers added to the root group; only
		// with a mount, so a server without one keeps gin's chains as they
		// were.
		s.engine.NoRoute(statics.serve)
		s.engine.NoMethod(statics.serve)
		s.statics = &statics
	}

	return append(engine.CollapseMethods(routes, anyMethods()), statics.routes()...), nil
}

// anyMethods returns the method set of gin's RouterGroup.Any, fresh on
// every call so no caller can change another one's set.
func anyMethods() []string {
	return []string{
		http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodHead,
		http.MethodOptions, http.MethodDelete, http.MethodConnect, http.MethodTrace,
	}
}

// added returns the routes in after that are not in before, attributed to
// owner. gin lists routes by method tree, so they are matched by method
// and path, not by position.
func added(before, after gin.RoutesInfo, owner string) engine.Routes {
	seen := make(map[string]bool, len(before))
	for _, r := range before {
		seen[r.Method+" "+r.Path] = true
	}

	var out engine.Routes

	for _, r := range after {
		if !seen[r.Method+" "+r.Path] {
			out = append(out, engine.Route{Method: r.Method, Pattern: r.Path, Configurer: owner})
		}
	}

	return out
}

// handlerKey identifies a route handler by its function pointer.
func handlerKey(h gin.HandlerFunc) uintptr { return engine.FuncPointer(h) }

// checkHandlers rejects a route with a nil handler, which gin accepts and
// would answer by panicking. It runs after each configurer, so a nil
// handler found is one that c registered.
func checkHandlers(routes gin.RoutesInfo, c srvr.RouteConfigurer[*gin.RouterGroup]) error {
	for _, r := range routes {
		if r.HandlerFunc == nil {
			return engine.RouteErrorf("%q (%T): handler is nil", r.Method+" "+r.Path, c)
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
