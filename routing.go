package srvr

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/kartaladev/srvr/engine"
)

// HandlerFunc handles a request and reports failure by returning an error,
// which the server renders as an RFC 9457 problem.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Middleware wraps a handler. Start calls it once per route to build the
// route's handler: a nil result or a panic there fails Start with an error
// wrapping [ErrRouteConfig] naming the route. The handler it returns runs
// inside srvr's panic recovery, so a panic while serving is answered with a
// 500 problem.
type Middleware func(next http.Handler) http.Handler

// route is a route buffered by a Group until the server installs it.
type route struct {
	method  string
	pattern string
	// handler serves the route, before middleware.
	handler http.Handler
	// middleware is the group's middleware when the route was registered,
	// outermost first.
	middleware []Middleware
	// owner is the Go type of the configurer that registered the route.
	owner string
	// location is the handler's code location, resolved at registration.
	location engine.Location
	// listMethod is the method the route listing shows, the route's own
	// unless its registration said otherwise (a static mount, registered
	// for every method, is listed as GET).
	listMethod string
}

// key is the ServeMux pattern of the route.
func (rt route) key() string {
	if rt.method == "" {
		return rt.pattern
	}

	return rt.method + " " + rt.pattern
}

// builder collects what the groups of one Start register.
type builder struct {
	routes []route
	errs   []error
	// owner is the Go type of the configurer running Configure.
	owner string
	// statics records the joined prefix of every static mount registered
	// so far, with the configurer that registered it.
	statics engine.StaticPrefixes
	// served is what the installed routes serve, filled in by the
	// server once they are installed; static mounts read it while
	// serving.
	served *served
	// wrap turns an error-returning handler into an http.Handler that
	// renders its errors as problems.
	wrap func(func(http.ResponseWriter, *http.Request) error) http.Handler
}

// Group registers routes on the net/http engine. A configurer receives the
// root group in Configure. Paths use the patterns of [http.ServeMux]
// without the method, which the registering method supplies; handlers
// return errors, which the server renders as problems.
//
// A group's prefix and a route's path are joined into one pattern:
// duplicate slashes collapse, the path's trailing slash keeps its ServeMux
// meaning (a subtree), and the wildcards {name}, {name...} and {$} are kept.
// Prefixes and paths start with "/"; an empty path means the prefix itself.
// Mistakes are reported by Start as errors wrapping [ErrRouteConfig].
//
// Middleware added with [Group.Use] applies, outermost first, to the routes
// registered on the group or its sub-groups after the call, as in gin and
// fiber; routes registered before do not receive it. A sub-group starts
// with its parent's middleware at the time it is created.
type Group struct {
	b          *builder
	prefix     string
	middleware []Middleware
}

// Group returns a sub-group whose routes are registered under prefix,
// joined to this group's prefix, with this group's current middleware
// followed by mw.
func (g *Group) Group(prefix string, mw ...Middleware) *Group {
	if !isPath(prefix) {
		g.b.errs = append(g.b.errs, engine.RouteErrorf("group prefix %q does not start with \"/\"", prefix))
	}

	sub := &Group{b: g.b, prefix: collapseSlashes(g.prefix + prefix), middleware: slices.Clone(g.middleware)}
	sub.Use(mw...)

	return sub
}

// Use adds mw, in order, to the middleware of the routes registered on g
// and its sub-groups from now on.
func (g *Group) Use(mw ...Middleware) {
	for i, m := range mw {
		if m == nil {
			g.b.errs = append(g.b.errs, engine.RouteErrorf("middleware %d added to group %q is nil", i, g.prefix))
		}
	}

	// A new backing array, so sub-groups and recorded routes that share
	// the old one never see the addition.
	g.middleware = slices.Concat(g.middleware, mw)
}

// Handle registers h for method and path, for example
// g.Handle(http.MethodGet, "/items/{id}", h).
func (g *Group) Handle(method, path string, h HandlerFunc) {
	var hh http.Handler
	if h != nil {
		hh = g.b.wrap(h)
	}

	g.add(method, path, hh, engine.FuncLocation(h))
}

// Mount routes every method for path to h, a plain [http.Handler] such as
// [http.FileServerFS]. h receives the group's middleware and its panics are
// recovered like other routes; it sees the request path without the group
// prefix, as with [http.StripPrefix], so g.Group("/static").Mount("/",
// files) serves /static/a.txt from a.txt. Its errors are its own: a status
// it writes is sent as is.
func (g *Group) Mount(path string, h http.Handler) {
	var loc engine.Location
	if !engine.IsNil(h) {
		loc = engine.HandlerLocation(h)
		if n := segments(g.prefix); n > 0 {
			h = stripSegments(n, h)
		}
	}

	g.add("", path, h, loc)
}

// segments returns the number of path segments in a group prefix.
func segments(prefix string) int {
	return len(strings.FieldsFunc(prefix, func(r rune) bool { return r == '/' }))
}

// stripSegments returns h serving requests with the first n path segments
// removed, as http.StripPrefix removes a literal prefix. Counting segments
// rather than matching text also strips prefixes with wildcards, such as
// /orgs/{org}, which the request fills with its own values.
func stripSegments(n int, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r2 := new(http.Request)
		*r2 = *r
		u := *r.URL
		u.Path = cutSegments(r.URL.Path, n)
		if r.URL.RawPath != "" {
			u.RawPath = cutSegments(r.URL.RawPath, n)
		}
		r2.URL = &u
		h.ServeHTTP(w, r2)
	})
}

// cutSegments removes the first n segments of p, keeping a leading slash.
func cutSegments(p string, n int) string {
	rest := strings.TrimPrefix(p, "/")
	for range n {
		_, after, found := strings.Cut(rest, "/")
		if !found {
			return "/"
		}

		rest = after
	}

	return "/" + rest
}

// served is what a server's installed routes serve: the ServeMux and the
// methods routes registered, sorted. Static mounts read it to answer 405
// with the methods other routes serve for the path.
type served struct {
	mux     *http.ServeMux
	methods []string
}

// add records a route for method ("" for every method) and path.
func (g *Group) add(method, path string, h http.Handler, loc engine.Location) {
	g.addListed(method, method, path, h, loc)
}

// addListed records a route for method and path, listed with listMethod.
func (g *Group) addListed(method, listMethod, path string, h http.Handler, loc engine.Location) {
	pattern, err := joinPattern(g.prefix, path)
	if err != nil {
		g.b.errs = append(g.b.errs, err)

		return
	}

	rt := route{method: method, listMethod: listMethod, pattern: pattern, middleware: g.middleware, owner: g.b.owner, location: loc}
	if engine.IsNil(h) {
		g.b.errs = append(g.b.errs, engine.RouteErrorf("%q (%s): handler is nil", rt.key(), rt.owner))

		return
	}

	rt.handler = h
	g.b.routes = append(g.b.routes, rt)
}

// GET registers h for GET requests to path.
func (g *Group) GET(path string, h HandlerFunc) { g.Handle(http.MethodGet, path, h) }

// POST registers h for POST requests to path.
func (g *Group) POST(path string, h HandlerFunc) { g.Handle(http.MethodPost, path, h) }

// PUT registers h for PUT requests to path.
func (g *Group) PUT(path string, h HandlerFunc) { g.Handle(http.MethodPut, path, h) }

// PATCH registers h for PATCH requests to path.
func (g *Group) PATCH(path string, h HandlerFunc) { g.Handle(http.MethodPatch, path, h) }

// DELETE registers h for DELETE requests to path.
func (g *Group) DELETE(path string, h HandlerFunc) { g.Handle(http.MethodDelete, path, h) }

// joinPattern joins a group prefix (already joined from its parents) and a
// route path into a ServeMux path pattern. path.Join is not used: it drops
// the trailing slash that gives a pattern its subtree meaning.
func joinPattern(prefix, path string) (string, error) {
	if !isPath(path) {
		return "", engine.RouteErrorf("path %q does not start with \"/\"", path)
	}

	joined := collapseSlashes(prefix + path)
	if joined == "" {
		joined = "/"
	}

	return joined, nil
}

// isPath reports whether p is empty or starts with "/". A path without the
// leading slash is relative or names a host, which a group never means.
func isPath(p string) bool { return p == "" || p[0] == '/' }

// collapseSlashes replaces every run of slashes in p with one slash.
func collapseSlashes(p string) string {
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}

	return p
}

// installRoutes registers routes on mux in order, in their middleware. A
// pattern ServeMux rejects fails with an error wrapping ErrRouteConfig;
// for a conflict it names both routes and the configurers that registered
// them, from srvr's own record rather than ServeMux's panic text.
func installRoutes(mux *http.ServeMux, routes []route) error {
	for i, rt := range routes {
		h, err := chain(rt.handler, rt.middleware)
		if err != nil {
			return engine.RouteErrorf("%q (%s): %w", rt.key(), rt.owner, err)
		}

		h = withRoute(rt, h)

		v := tryHandle(mux, rt.key(), h)
		if v == nil {
			continue
		}

		for _, earlier := range routes[:i] {
			if conflicts(earlier.key(), rt.key()) {
				return engine.RouteErrorf("%q (%s) conflicts with %q (%s): %v", rt.key(), rt.owner, earlier.key(), earlier.owner, v)
			}
		}

		return engine.RouteErrorf("%q (%s): %v", rt.key(), rt.owner, v)
	}

	return nil
}

// conflicts reports whether ServeMux refuses pattern b after pattern a,
// which it accepted, while accepting b alone.
func conflicts(a, b string) bool {
	if tryHandle(http.NewServeMux(), b, http.NotFoundHandler()) != nil {
		return false // b is invalid on its own
	}

	mux := http.NewServeMux()
	_ = tryHandle(mux, a, http.NotFoundHandler())

	return tryHandle(mux, b, http.NotFoundHandler()) != nil
}

// tryHandle registers h for pattern on mux and returns the value ServeMux
// panicked with, or nil.
func tryHandle(mux *http.ServeMux, pattern string, h http.Handler) (panicked any) {
	defer func() { panicked = recover() }()

	mux.Handle(pattern, h)

	return nil
}

// chain wraps h in middleware, the first outermost. A middleware that
// returns a nil handler or panics is an error; the panic value's text is
// read through valueText.
func chain(h http.Handler, middleware []Middleware) (http.Handler, error) {
	for i, mw := range slices.Backward(middleware) {
		var err error
		if h, err = applyMiddleware(mw, h); err != nil {
			return nil, fmt.Errorf("middleware %d %w", i, err)
		}
	}

	return h, nil
}

// applyMiddleware returns mw(h), or an error when mw panics or returns a
// nil handler.
func applyMiddleware(mw Middleware, h http.Handler) (wrapped http.Handler, err error) {
	defer func() {
		if v := recover(); v != nil {
			wrapped, err = nil, errors.New("panicked: "+engine.ValueText(v))
		}
	}()

	if wrapped = mw(h); engine.IsNil(wrapped) {
		return nil, errors.New("returned a nil handler")
	}

	return wrapped, nil
}

// withRoute returns h recording the route's path pattern (http.route) and
// handler location on the request before the route's middleware runs.
func withRoute(rt route, h http.Handler) http.Handler {
	pattern, loc := rt.pattern, rt.location

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if req, ok := engine.StateFrom(r.Context()); ok {
			req.SetRoute(pattern)
			req.SetLocation(loc)
		}

		h.ServeHTTP(w, r)
	})
}

// RouteConfigurer contributes routes to a server. G is the routing engine's
// group type: *Group for the net/http engine of this package. A configurer
// written for one engine does not compile against another.
type RouteConfigurer[G any] interface {
	// Configure adds routes to group. It runs once per registration, in
	// registration order, when the server starts.
	Configure(group G)
}

// RouteRegistrar accepts route configurers for a server whose engine uses
// the group type G.
type RouteRegistrar[G any] interface {
	// Register adds conf to the configurers run when the server starts.
	Register(conf RouteConfigurer[G]) error
}
