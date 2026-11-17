package fibersrvr

import (
	"context"
	"fmt"
	"math"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/prblm/fiberprblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"github.com/valyala/fasthttp"
)

// engineName names the fiber engine in engine-specific option errors.
const engineName = "fiber"

// Server is an HTTP server on the Fiber v3 engine with srvr's lifecycle,
// configuration, RFC 9457 problems and logs. Create it with [New], add
// routes with [Server.Register] and run it with [Server.Start]; its
// methods behave like those of srvr.Server and are safe for concurrent use.
type Server struct {
	core   *engine.Core
	lc     *engine.Lifecycle[srvr.RouteConfigurer[fiber.Router]]
	app    *fiber.App
	owners routeOwners
	// locations maps each route handler (by function pointer) to its code
	// location, resolved once by the configure phase.
	locations map[uintptr]engine.Location
	// problems is fiberprblm's error handler, built from the server's
	// prblm options.
	problems fiber.ErrorHandler
	// base is the context every request context derives from; a forced
	// drain cancels it, because fiber.Ctx itself never cancels.
	base       context.Context //nolint:containedctx // the server-wide parent of request contexts.
	cancelBase context.CancelFunc
	// serving is the runner of a started server, nil before.
	serving atomic.Pointer[runner]
}

var _ srvr.RouteRegistrar[fiber.Router] = (*Server)(nil)

// New returns a server configured by opts, the options of package srvr
// plus this package's own. It does not open a listener. Invalid
// configuration returns an error wrapping srvr.ErrConfig naming every
// offending field and value, including an option meant for another engine.
func New(opts ...srvr.Option) (*Server, error) {
	core, err := engine.ResolveConfig(opts)
	if err != nil {
		return nil, err
	}

	hooks, err := core.EngineOptions(engineName)
	if err != nil {
		return nil, err
	}

	s := &Server{core: core, lc: engine.NewLifecycle[srvr.RouteConfigurer[fiber.Router]](core)}
	s.base, s.cancelBase = context.WithCancel(context.Background()) //nolint:gosec // cancelled by the runner's Drain, or never needed when the server never serves.
	s.problems = fiberprblm.ErrorHandler(core.ProblemOptions()...)

	cfg, err := s.fiberConfig(hooks)
	if err != nil {
		return nil, err
	}

	if set := core.Settings().MaxHeaderBytes; set > maxReadBufferSize {
		core.Warn(context.Background(), engine.WarnFiberHeaderLimitCapped, fmt.Sprintf(
			"MaxHeaderBytes %d is above the %d bytes fiber can buffer per connection; requests with larger headers get 431", set, maxReadBufferSize))
	}

	s.app = fiber.New(cfg)
	s.app.Hooks().OnRoute(s.owners.record)
	s.app.Use(s.requestStage, fiberprblm.Recovery())

	return s, nil
}

// WithFiberConfig adds hook, which customises the fiber.Config srvr
// derives from its own configuration (error handler, body limit, read,
// write and idle timeouts, header buffer). srvr re-asserts its error
// handler after the hooks, so errors always render as problems. Fiber's
// ListenConfig is owned by srvr (startup message off, prefork never
// enabled) and not exposed. Enabling ProxyHeader loosens a safety default:
// c.IP() then trusts a client-supplied header. A nil hook or a hook that
// panics fails New with an error wrapping srvr.ErrConfig.
func WithFiberConfig(hook func(*fiber.Config)) srvr.Option {
	return engine.ForEngine(engineName, "fibersrvr.WithFiberConfig", hook)
}

// maxReadBufferSize caps fiber's ReadBufferSize, the header limit, because
// fasthttp allocates the buffer for every connection.
const maxReadBufferSize = 64 << 10

// fiberConfig derives fiber's configuration from srvr's, applies the
// consumer's hooks and re-asserts srvr's error handler.
func (s *Server) fiberConfig(hooks []any) (fiber.Config, error) {
	c := s.core.Config()

	cfg := fiber.Config{
		ErrorHandler:   s.renderError,
		BodyLimit:      bodyLimit(c.MaxRequestBodyBytes),
		ReadTimeout:    max(c.ReadTimeout, 0),
		WriteTimeout:   max(c.WriteTimeout, 0),
		IdleTimeout:    max(c.IdleTimeout, 0),
		ReadBufferSize: min(c.MaxHeaderBytes, maxReadBufferSize),
	}

	for _, h := range hooks {
		if err := applyHook(&cfg, h); err != nil {
			return fiber.Config{}, err
		}
	}

	cfg.ErrorHandler = s.renderError

	return cfg, nil
}

// bodyLimit maps MaxRequestBodyBytes to fiber's BodyLimit, which reads
// zero and negative values as its 4 MiB default: a disabled limit becomes
// math.MaxInt.
func bodyLimit(n int64) int {
	if n < 0 || n > math.MaxInt {
		return math.MaxInt
	}

	return int(n)
}

// applyHook runs one WithFiberConfig hook under recover.
func applyHook(cfg *fiber.Config, h any) (err error) {
	hook, _ := h.(func(*fiber.Config))
	if hook == nil {
		return engine.ConfigError("WithFiberConfig", "<nil>", "hook is nil")
	}

	defer func() {
		if v := recover(); v != nil {
			err = engine.ConfigError("WithFiberConfig", engine.FuncLocation(hook).Function(), "hook panicked: "+engine.ValueText(v))
		}
	}()

	hook(cfg)

	return nil
}

// Register adds conf to the configurers run when the server starts. Each
// registration runs Configure once, in registration order, during Start,
// with the app's root router. A nil or typed-nil conf returns an error
// wrapping srvr.ErrRouteConfig. Once Start was called it returns
// srvr.ErrServerStarted, or srvr.ErrServerClosed when the server stopped.
func (s *Server) Register(conf srvr.RouteConfigurer[fiber.Router]) error {
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
// srvr.Server.Start in every other respect.
func (s *Server) Start(ctx context.Context) error {
	return s.lc.Start(ctx, func(configurers []srvr.RouteConfigurer[fiber.Router]) (engine.Runner, error) {
		if err := s.configure(configurers); err != nil {
			return nil, err
		}

		r := &runner{app: s.app, cancelBase: s.cancelBase, conns: &tracker{open: map[net.Conn]struct{}{}}}
		s.serving.Store(r)

		return r, nil
	})
}

// Shutdown stops the server gracefully, as srvr.Server.Shutdown does. When
// ctx ends before in-flight requests finish, it cancels their contexts and
// closes their connections before returning ctx's error.
func (s *Server) Shutdown(ctx context.Context) error { return s.lc.Shutdown(ctx) }

// configure runs each configurer on the app's root router under recover
// and then rejects duplicate routes, which fiber registers silently
// (merging an identical registration into the first, which wins).
func (s *Server) configure(configurers []srvr.RouteConfigurer[fiber.Router]) error {
	defer func() { s.owners.current = "" }()

	for _, c := range configurers {
		s.owners.current = fmt.Sprintf("%T", c)

		if err := engine.Configure[fiber.Router](c, s.app); err != nil {
			return err
		}
	}

	if err := s.owners.duplicate(); err != nil {
		return err
	}

	s.locations = make(map[uintptr]engine.Location)
	for _, r := range s.app.GetRoutes(true) {
		if n := len(r.Handlers); n > 0 {
			h := r.Handlers[n-1]
			s.locations[handlerKey(h)] = engine.FuncLocation(h)
		}
	}

	return nil
}

// handlerKey identifies a route handler by its function pointer.
func handlerKey(h fiber.Handler) uintptr { return engine.FuncPointer(h) }

// routeOwners records every route registration made during the configure
// phase with the configurer that made it, through fiber's OnRoute hook,
// which also fires for a registration fiber merges into an earlier route.
type routeOwners struct {
	// current is the type of the configurer running, "" outside the
	// configure phase.
	current string
	routes  []ownedRoute
}

type ownedRoute struct {
	method, path, owner string
}

// record is the app's OnRoute hook.
func (o *routeOwners) record(r fiber.Route) error {
	if o.current != "" && !routeFlag(r, "use") && !routeFlag(r, "mount") && !routeFlag(r, "autoHead") {
		o.routes = append(o.routes, ownedRoute{method: r.Method, path: r.Path, owner: o.current})
	}

	return nil
}

// routeFlag reads one of fiber.Route's unexported flags (use, mount,
// autoHead), which fiber v3 does not export but which tell a middleware or
// implicit registration from a route the consumer declared. Reading an
// unexported bool through reflect is allowed; the fiber version is pinned
// and the registration tests fail if the field disappears.
func routeFlag(r fiber.Route, name string) bool {
	f := reflect.ValueOf(r).FieldByName(name)

	return f.IsValid() && f.Kind() == reflect.Bool && f.Bool()
}

// duplicate returns an error wrapping srvr.ErrRouteConfig for the first
// route whose method and path, with parameter names normalised (":id" and
// ":name" are the same segment), repeat an earlier one.
func (o *routeOwners) duplicate() error {
	first := map[string]ownedRoute{}

	for _, r := range o.routes {
		key := r.method + " " + normalise(r.path)
		if earlier, dup := first[key]; dup {
			return engine.RouteError("%q (%s) duplicates %q (%s); fiber would serve only the first",
				r.method+" "+r.path, r.owner, earlier.method+" "+earlier.path, earlier.owner)
		}

		first[key] = r
	}

	return nil
}

// normalise replaces every parameter name in a fiber path with ":".
func normalise(path string) string {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		if strings.HasPrefix(seg, ":") {
			segments[i] = ":" + strings.TrimLeft(seg[1:], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-")
		}
	}

	return strings.Join(segments, "/")
}

// requestStage is srvr's outermost fiber stage. It gives the request a
// cancellable context derived from the server's base context, carrying
// srvr's request state; renders an error the chain returns itself, so it
// records the status the client gets; and writes the access record. Every
// value it keeps is copied out of fiber's pooled buffers first.
func (s *Server) requestStage(c fiber.Ctx) error {
	begin := time.Now()

	ctx, cancel := context.WithCancel(s.base)
	defer cancel()

	// A status already set is a request fasthttp rejected before routing
	// (413, 431, 408): fiber replays the Use chain with it and renders the
	// problem through its error handler afterwards. Normal requests start
	// at 200.
	rejected := c.Response().StatusCode()

	ctx = s.core.BeginRequest(ctx, traceparent(c))
	c.SetContext(ctx)
	state, _ := engine.StateFrom(ctx) // never nil: BeginRequest made it

	if rejected >= http.StatusBadRequest {
		s.core.RecordRejection(ctx, rejected)
	} else if err := c.Next(); err != nil {
		_ = s.renderError(c, err)

		// fiberprblm keeps a body stream that already started; the client
		// got that, not the problem.
		if c.Response().IsBodyStream() {
			state.MarkCommitted()
		}
	}

	if c.Matched() {
		route := c.Route()
		state.Route = route.Path // the app's registered string, never a pooled buffer
		if n := len(route.Handlers); n > 0 {
			state.Location = s.locations[handlerKey(route.Handlers[n-1])]
		}
	}

	s.core.WriteAccess(ctx, s.accessInfo(c, time.Since(begin)))

	return nil
}

// bodySize returns the size of the response body without reading a
// streamed one, which fasthttp would buffer whole (and an endless stream
// would never end): a stream reports its declared Content-Length, or 0
// when it has none.
func bodySize(res *fasthttp.Response) int64 {
	if res.IsBodyStream() {
		return int64(max(res.Header.ContentLength(), 0))
	}

	return int64(len(res.Body()))
}

// accessInfo copies what the access record reports out of c.
func (s *Server) accessInfo(c fiber.Ctx, d time.Duration) engine.AccessInfo {
	return engine.AccessInfo{
		Method:     c.Method(), // a stable string in fiber v3, never a pooled buffer
		Path:       strings.Clone(c.Path()),
		RawQuery:   string(c.Request().URI().QueryString()),
		RemoteAddr: c.RequestCtx().RemoteAddr().String(),
		UserAgent:  string(c.Request().Header.UserAgent()),
		Status:     c.Response().StatusCode(),
		BodySize:   bodySize(c.Response()),
		Duration:   d,
	}
}

// renderError is the app's error handler: fiberprblm's, given the error
// guarded against a cyclic chain, because fiberprblm resolves it with
// errors.As and errors.Is.
func (s *Server) renderError(c fiber.Ctx, err error) error {
	return s.problems(c, engine.GuardChain(err))
}

// traceparent returns copies of the request's traceparent header values.
func traceparent(c fiber.Ctx) []string {
	raw := c.Request().Header.PeekAll("Traceparent")
	values := make([]string, len(raw))

	for i, v := range raw {
		values[i] = string(v)
	}

	return values
}

// runner serves the fiber app on srvr's listener.
type runner struct {
	app        *fiber.App
	cancelBase context.CancelFunc
	conns      *tracker
}

// Serve serves on ln through a tracking listener, with fiber's startup
// message off. srvr builds the ListenConfig itself and never enables
// prefork, which would break the lifecycle.
func (r *runner) Serve(ln net.Listener) error {
	r.conns.Listener = ln

	return r.app.Listener(r.conns, fiber.ListenConfig{DisableStartupMessage: true})
}

// Drain waits for in-flight requests until ctx is done. fiber's
// ShutdownWithContext then stops waiting but leaves active connections
// open, so on the deadline Drain cancels every request context and closes
// the tracked connections before returning ctx's error.
func (r *runner) Drain(ctx context.Context) error {
	err := r.app.ShutdownWithContext(ctx)

	// Nothing may outlive the drain: request contexts end with it.
	r.cancelBase()

	if err != nil {
		r.conns.closeAll()
	}

	return err
}

// tracker is a listener that keeps the connections it accepted until they
// close, so a forced drain can close them.
type tracker struct {
	net.Listener

	mu   sync.Mutex
	open map[net.Conn]struct{}
}

func (t *tracker) Accept() (net.Conn, error) {
	conn, err := t.Listener.Accept()
	if err != nil {
		return nil, err
	}

	tc := &trackedConn{Conn: conn, t: t}

	t.mu.Lock()
	t.open[tc] = struct{}{}
	t.mu.Unlock()

	return tc, nil
}

// closeAll closes every connection still open.
func (t *tracker) closeAll() {
	t.mu.Lock()
	conns := make([]net.Conn, 0, len(t.open))
	for c := range t.open {
		conns = append(conns, c)
	}
	t.mu.Unlock()

	for _, c := range conns {
		_ = c.Close()
	}
}

// trackedConn removes itself from its tracker when closed.
type trackedConn struct {
	net.Conn

	t    *tracker
	once sync.Once
}

func (c *trackedConn) Close() error {
	c.once.Do(func() {
		c.t.mu.Lock()
		delete(c.t.open, c)
		c.t.mu.Unlock()
	})

	return c.Conn.Close()
}
