package fibersrvr

import (
	"context"
	"fmt"
	"maps"
	"math"
	"net"
	"net/http"
	"reflect"
	"runtime/debug"
	"slices"
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
	// fields are the fiber internals configure checks: fiberFields(),
	// replaced only by tests that inject a renamed field.
	fields []fiberField
	// locations maps each route handler (by function pointer) to its code
	// location, resolved once by the configure phase.
	locations map[uintptr]engine.Location
	// problems is fiberprblm's error handler, built from the server's
	// prblm options.
	problems fiber.ErrorHandler
	// base is the context every request context derives from; a forced
	// drain cancels it, because fiber.Ctx itself never cancels.
	base       context.Context // the server-wide parent of request contexts
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

	s := &Server{core: core, lc: engine.NewLifecycle[srvr.RouteConfigurer[fiber.Router]](core), fields: fiberFields()}
	// cancelBase is called by the runner's Drain, or never needed when the
	// server never serves.
	s.base, s.cancelBase = context.WithCancel(context.Background()) //nolint:gosec // see the comment above.
	s.problems = fiberprblm.ErrorHandler(core.ProblemOptions()...)

	cfg, err := s.fiberConfig(hooks)
	if err != nil {
		return nil, err
	}

	if set := core.MaxHeaderBytesSet(); set > maxReadBufferSize {
		core.Warn(context.Background(), engine.WarnFiberHeaderLimitCapped, fmt.Sprintf(
			"MaxHeaderBytes %d is above the %d bytes fiber can buffer per connection; "+
				"requests with larger headers get 431",
			set, maxReadBufferSize))
	}

	s.app = fiber.New(cfg)
	s.app.Hooks().OnRoute(s.owners.record)
	s.app.Use(s.requestStage, fiberprblm.Recovery())
	core.SetDefaultLogger()

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
	return engine.Only(engineName, "fibersrvr.WithFiberConfig", hook)
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
		return engine.NewConfigError("WithFiberConfig", "<nil>", "hook is nil")
	}

	defer func() {
		if v := recover(); v != nil {
			err = engine.NewConfigError("WithFiberConfig", engine.FuncLocation(hook).Function(),
				"hook panicked: "+engine.ValueText(v))
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
	return s.lc.Start(ctx, func(configurers []srvr.RouteConfigurer[fiber.Router]) (engine.Runner, engine.Routes, error) {
		routes, err := s.configure(configurers)
		if err != nil {
			return nil, nil, err
		}

		r := &runner{app: s.app, cancelBase: s.cancelBase, conns: &tracker{open: map[net.Conn]struct{}{}}}
		s.serving.Store(r)

		return r, routes, nil
	})
}

// Shutdown stops the server gracefully, as srvr.Server.Shutdown does. When
// ctx ends before in-flight requests finish, it cancels their contexts and
// closes their connections before returning ctx's error.
func (s *Server) Shutdown(ctx context.Context) error { return s.lc.Shutdown(ctx) }

// configure runs each configurer on the app's root router under recover,
// rejects duplicate routes, which fiber registers silently (merging an
// identical registration into the first, which wins), and returns the
// route listing. The duplicate check is srvr's own route check and runs
// before the consumer's.
func (s *Server) configure(configurers []srvr.RouteConfigurer[fiber.Router]) (engine.Routes, error) {
	if err := checkFields(s.fields); err != nil {
		return nil, err
	}

	defer func() { s.owners.current = "" }()

	for _, c := range configurers {
		s.owners.current = fmt.Sprintf("%T", c)
		before := len(firstStack(s.app))

		if err := engine.Configure[fiber.Router](c, s.app); err != nil {
			return nil, err
		}

		s.owners.recordMounts(s.app, before)
	}

	if err := s.owners.addMounted(); err != nil {
		return nil, err
	}

	cfg := s.app.Config()
	if err := s.owners.duplicate(&cfg); err != nil {
		return nil, err
	}

	registered := s.app.GetRoutes(true)

	s.locations = make(map[uintptr]engine.Location, len(registered))
	for _, r := range registered {
		if n := len(r.Handlers); n > 0 {
			h := r.Handlers[n-1]
			s.locations[handlerKey(h)] = engine.FuncLocation(h)
		}
	}

	return engine.CollapseMethods(s.owners.routes, cfg.RequestMethods), nil
}

// handlerKey identifies a route handler by its function pointer.
func handlerKey(h fiber.Handler) uintptr { return engine.FuncPointer(h) }

// routeOwners records every route registration made during the configure
// phase with the configurer that made it, through fiber's OnRoute hook,
// which also fires for a registration fiber merges into an earlier route.
// fiber does not fire OnRoute for a mounted sub-app, so mounts are found in
// the app's stack after each configurer (recordMounts) and their routes
// read once the configure phase is over (addMounted).
type routeOwners struct {
	// current is the type of the configurer running, "" outside the
	// configure phase.
	current string
	routes  engine.Routes
	mounts  []mount
}

// mount is a sub-app mounted by a configurer.
type mount struct {
	// group is the mount route's *fiber.Group, read through reflection.
	group      reflect.Value
	prefix     string
	configurer string
}

// firstStack returns app's first method stack, nil when it has none.
func firstStack(app *fiber.App) []*fiber.Route {
	if st := app.Stack(); len(st) > 0 {
		return st[0]
	}

	return nil
}

// recordMounts attributes the mount routes appended to app's first method
// stack since it had length from to the configurer running. fiber files a
// mount in every method stack, so one stack sees each mount once, and a
// configurer only appends to it.
func (o *routeOwners) recordMounts(app *fiber.App, from int) {
	for _, r := range firstStack(app)[from:] {
		if v := reflect.ValueOf(r).Elem(); routeMount.set(v) {
			o.mounts = append(o.mounts, mount{group: v.FieldByName(routeGroup.name), prefix: r.Path, configurer: o.current})
		}
	}
}

// addMounted adds the routes of every recorded sub-app, at its full
// mounted path and attributed to the configurer that mounted it. Routes
// added to a sub-app after it was mounted are included, because they are
// read now.
func (o *routeOwners) addMounted() error {
	return readInternals(func() error {
		for _, m := range o.mounts {
			app, err := subApp(m.group)
			if err != nil {
				return err
			}

			if err := o.walk(app, m.prefix, m.configurer, map[uintptr]bool{}); err != nil {
				return err
			}
		}

		return nil
	})
}

// readInternals runs read, which reads fiber's unexported fields, under
// recover: a panic, a fiber change the type checks missed, returns an
// error wrapping srvr.ErrRouteConfig instead of crashing Start.
func readInternals(read func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = internalsError("reading fiber's internals panicked: %s", engine.ValueText(v))
		}
	}()

	return read()
}

// checkFields returns an error wrapping srvr.ErrRouteConfig, naming the
// field and the fiber version, for the first of fields that fiber does not
// declare with the expected type. configure runs it over the server's fields once
// per Start, mounted sub-app or not, so a fiber version that changed them
// fails Start instead of listing fewer or wrong routes.
func checkFields(fields []fiberField) error {
	for _, f := range fields {
		if err := f.check(); err != nil {
			return err
		}
	}

	return nil
}

// fiberField is an unexported fiber field srvr reads: a route flag or a
// field the sub-app walk follows.
type fiberField struct {
	owner reflect.Type
	name  string
	want  reflect.Type
}

// The unexported fiber fields srvr reads: fiberFields lists them all for
// checkFields; TestMountFields pins them.
var (
	routeUse      = routeFlag("use")
	routeMount    = routeFlag("mount")
	routeAutoHead = routeFlag("autoHead")
	routeGroup    = fiberField{reflect.TypeFor[fiber.Route](), "group", reflect.TypeFor[*fiber.Group]()}
	groupApp      = fiberField{reflect.TypeFor[fiber.Group](), "app", reflect.TypeFor[*fiber.App]()}
	appStack      = fiberField{reflect.TypeFor[fiber.App](), "stack", reflect.TypeFor[[][]*fiber.Route]()}
)

// fiberFields returns a fresh list of every unexported fiber field srvr
// reads, so no caller can change the list another one checks.
func fiberFields() []fiberField {
	return []fiberField{routeUse, routeMount, routeAutoHead, routeGroup, groupApp, appStack}
}

// check returns an error wrapping srvr.ErrRouteConfig when f's owner type
// has no field f.name of type f.want.
func (f fiberField) check() error {
	got, ok := f.owner.FieldByName(f.name)
	if !ok {
		return f.unreadable("is missing")
	}

	return f.typed(got.Type)
}

// routeFlag is the unexported bool field name of fiber.Route.
func routeFlag(name string) fiberField {
	return fiberField{reflect.TypeFor[fiber.Route](), name, reflect.TypeFor[bool]()}
}

// read returns field f of struct value v, or an error wrapping
// srvr.ErrRouteConfig when it is missing or not of the expected type.
func (f fiberField) read(v reflect.Value) (reflect.Value, error) {
	got := v.FieldByName(f.name)
	if !got.IsValid() {
		return got, f.unreadable("is missing")
	}

	return got, f.typed(got.Type())
}

// typed returns an error wrapping srvr.ErrRouteConfig when got is not the
// type f expects.
func (f fiberField) typed(got reflect.Type) error {
	if got != f.want {
		return f.unreadable("has type " + got.String() + ", want " + f.want.String())
	}

	return nil
}

// unreadable returns the error for a field srvr cannot read.
func (f fiberField) unreadable(problem string) error {
	return internalsError("field %s.%s %s", f.owner, f.name, problem)
}

// internalsError returns an error wrapping srvr.ErrRouteConfig for fiber
// internals srvr cannot read, naming the fiber version.
func internalsError(format string, args ...any) error {
	return engine.RouteErrorf("fiber %s: cannot read fiber's internals for the route listing: %s",
		fiberVersion(), fmt.Sprintf(format, args...))
}

// subApp returns the *fiber.App behind group, a *fiber.Group value, or
// the zero Value when the group is nil.
func subApp(group reflect.Value) (reflect.Value, error) {
	if !group.IsValid() {
		return reflect.Value{}, routeGroup.unreadable("is missing")
	}

	if err := routeGroup.typed(group.Type()); err != nil {
		return reflect.Value{}, err
	}

	if group.IsNil() {
		return reflect.Value{}, nil
	}

	return groupApp.read(group.Elem())
}

// walk appends the routes of app, a *fiber.App value, as fiber composes
// them when it flattens mounted apps at startup (mount.go
// processSubAppsRoutes): paths joined to prefix, middleware and implicit
// HEAD routes skipped, nested mounts followed. It only reads fiber's
// unexported fields (App.stack, Route.group, Group.app, pinned by
// TestMountFields), checking each one's type before using it, and stops at
// a mount cycle. A field that is missing or of another type, as a fiber
// upgrade can make it, returns an error wrapping srvr.ErrRouteConfig.
func (o *routeOwners) walk(app reflect.Value, prefix, configurer string, onPath map[uintptr]bool) error {
	if app.Kind() != reflect.Pointer || app.IsNil() || onPath[app.Pointer()] {
		return nil
	}

	onPath[app.Pointer()] = true
	defer delete(onPath, app.Pointer())

	stack, err := appStack.read(app.Elem())
	if err != nil {
		return err
	}

	walked := map[uintptr]bool{} // mount groups of this app: one per mount, filed in every stack

	for i := range stack.Len() {
		for j := range stack.Index(i).Len() {
			rp := stack.Index(i).Index(j)
			if rp.IsNil() {
				continue
			}

			r := rp.Elem()

			switch {
			case declared(r):
				o.routes = append(o.routes, engine.Route{
					Method:     r.FieldByName("Method").String(),
					Pattern:    groupPath(prefix, r.FieldByName("Path").String()),
					Configurer: configurer,
				})
			case routeMount.set(r):
				if err := o.walkMount(r, prefix, configurer, walked, onPath); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// walkMount follows the mount route r, a fiber.Route value, into the
// mounted app its group holds, once per group: walked records the groups
// of the current app already followed, onPath the apps being walked.
func (o *routeOwners) walkMount(r reflect.Value, prefix, configurer string, walked, onPath map[uintptr]bool) error {
	g, err := routeGroup.read(r)
	if err != nil {
		return err
	}

	if g.IsNil() || walked[g.Pointer()] {
		return nil
	}

	walked[g.Pointer()] = true

	sub, err := groupApp.read(g.Elem())
	if err != nil {
		return err
	}

	return o.walk(sub, groupPath(prefix, r.FieldByName("Path").String()), configurer, onPath)
}

// fiberModule is the module path of the fiber version srvr reads.
const fiberModule = "github.com/gofiber/fiber/v3"

// fiberVersion returns the version of fiber linked into the binary, from
// its build information, or "unknown" when that is unavailable.
func fiberVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}

	return moduleVersion(info.Deps, fiberModule)
}

// moduleVersion returns the version of module path among deps, following
// a replacement, or "unknown" when it is absent or has none.
func moduleVersion(deps []*debug.Module, path string) string {
	for _, m := range deps {
		if m == nil || m.Path != path {
			continue
		}

		if m.Replace != nil {
			m = m.Replace
		}

		if m.Version != "" {
			return m.Version
		}
	}

	return "unknown"
}

// groupPath joins a mount prefix and a sub-app route path as fiber's
// getGroupPath (helpers.go) does.
func groupPath(prefix, path string) string {
	if path == "" {
		return prefix
	}

	if path[0] != '/' {
		path = "/" + path
	}

	return strings.TrimRight(prefix, "/") + path
}

// record is the app's OnRoute hook.
func (o *routeOwners) record(r fiber.Route) error {
	if o.current != "" && declared(reflect.ValueOf(r)) {
		o.routes = append(o.routes, engine.Route{Method: r.Method, Pattern: r.Path, Configurer: o.current})
	}

	return nil
}

// declared reports whether r, a fiber.Route value, is a route the consumer
// declared rather than middleware (use), a mounted sub-app (mount) or an
// implicit HEAD route (autoHead). fiber v3 does not export these flags;
// reading an unexported bool through reflect is allowed, and checkFields
// fails Start before any is read when a fiber version changed them.
func declared(r reflect.Value) bool {
	return !routeUse.set(r) && !routeMount.set(r) && !routeAutoHead.set(r)
}

// set reports whether bool field f of struct value v is true. checkFields
// has proven the field exists as a bool before any is read; the false for
// a missing field is only a backstop.
func (f fiberField) set(v reflect.Value) bool {
	b := v.FieldByName(f.name)

	return b.IsValid() && b.Kind() == reflect.Bool && b.Bool()
}

// duplicate returns an error wrapping srvr.ErrRouteConfig for the first
// route whose method and path, compared as fiber with cfg matches them
// (see matchKey), repeat an earlier one.
func (o *routeOwners) duplicate(cfg *fiber.Config) error {
	first := make(map[string]engine.Route, len(o.routes))

	for _, r := range o.routes {
		key := r.Method + " " + matchKey(r.Pattern, cfg)
		if earlier, dup := first[key]; dup {
			return engine.RouteErrorf("%q (%s) duplicates %q (%s); fiber would serve only the first",
				r.Method+" "+r.Pattern, r.Configurer, earlier.Method+" "+earlier.Pattern, earlier.Configurer)
		}

		first[key] = r
	}

	return nil
}

// matchKey returns the key fiber compares a route path by: the clean form
// of fiber v3.7.0's normalizeRoutePattern for a non-Use route (a leading
// "/" added, ASCII letters lowercased unless cfg.CaseSensitive, trailing
// slashes trimmed to at least "/" unless cfg.StrictRouting, escape
// characters removed), with every parameter name dropped first, because
// ":id" and ":name" match the same requests.
func matchKey(path string, cfg *fiber.Config) string {
	if path == "" || path[0] != '/' {
		path = "/" + path
	}

	path = dropParamNames(path)

	if !cfg.CaseSensitive {
		path = asciiLower(path)
	}

	if !cfg.StrictRouting && len(path) > 1 {
		if path = strings.TrimRight(path, "/"); path == "" {
			path = "/"
		}
	}

	return fiber.RemoveEscapeChar(path)
}

// asciiLower lowercases ASCII letters only, as fiber's router does.
func asciiLower(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return 'A' <= r && r <= 'Z' }) {
		return s
	}

	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}

	return string(b)
}

// dropParamNames replaces every parameter name in a fiber path with ":".
func dropParamNames(path string) string {
	if !strings.Contains(path, ":") {
		return path
	}

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
// srvr's request state; begins the server's observers, whose end
// functions the access record runs with the final status; renders an
// error the chain returns itself, so it records the status the client
// gets; and writes the access record. Every value it keeps is copied out
// of fiber's pooled buffers first.
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
	state, _ := engine.StateFrom(ctx) // never nil: BeginRequest made it
	if s.core.Observed() {            // requestInfo copies, which a server without observers skips
		ctx = s.core.BeginObservers(ctx, s.requestInfo(c))
	}
	c.SetContext(ctx)

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
		state.SetRoute(route.Path) // the app's registered string, never a pooled buffer
		if n := len(route.Handlers); n > 0 {
			state.SetLocation(s.locations[handlerKey(route.Handlers[n-1])])
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
		// A negative Content-Length is unknown (-1) or chunked (-2).
		RequestBodySize: int64(max(c.Request().Header.ContentLength(), 0)),
		Duration:        d,
	}
}

// requestInfo copies what observers learn before routing out of c.
func (s *Server) requestInfo(c fiber.Ctx) engine.RequestInfo {
	// c.Method is a stable string in fiber v3, never a pooled buffer;
	// NewRequestInfo copies the path when it keeps it.
	return s.core.NewRequestInfo(c.Method(), strings.Clone(c.Scheme()), string(c.Request().Host()), c.Path(),
		fiberHeader{&c.Request().Header})
}

// fiberHeader reads fasthttp request headers for observers, during Begin
// only; values are copied out of fasthttp's buffers.
type fiberHeader struct{ h *fasthttp.RequestHeader }

func (f fiberHeader) Get(key string) string { return string(f.h.Peek(key)) }

func (f fiberHeader) Keys() []string {
	var keys []string
	for k := range f.h.All() {
		keys = append(keys, string(k))
	}

	return keys
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
	conns := slices.Collect(maps.Keys(t.open))
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
