package ginsrvr

import (
	"fmt"
	"io/fs"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/internal/engine"
	"github.com/kartaladev/srvr/internal/static"
)

// Static returns a configurer serving the files of fsys under prefix, such
// as a single-page-app build directory or an embed.FS:
//
//	s.Register(ginsrvr.Static("/", site, srvr.WithSPAFallback("index.html")))
//
// It answers exactly as srvr.Static does on net/http, from the same
// resolver and with the same srvr.StaticOption values and safe defaults,
// which no option loosens: GET and HEAD of regular files with
// Content-Type, Content-Length and Last-Modified; a directory answered by
// its index file ("index.html", Cache-Control: no-cache) and never
// redirected, so links inside such a page must be root-relative; no
// directory listing; dotfiles, missing files and paths that cannot name a
// file inside fsys answered with the 404 problem; other methods with a
// 405 problem with Allow: GET, HEAD; a file system error, panic or
// unseekable file with the safe 500 problem, the error going only to the
// record; only If-Modified-Since honoured (no ranges, ETag or
// compression); no SPA fallback unless configured. os.DirFS follows
// symbolic links anywhere; prefer os.OpenRoot(dir) and its Root.FS(),
// which refuses a link escaping the root (a 500 problem).
//
// A gin catch-all route cannot share a tree level with other routes, so
// the server registers no route for a mount and serves its mounts from
// gin's no-route chain. Hence, differently from net/http:
//   - Every registered route wins over a mount, in either registration
//     order; the longest prefix containing the path answers it, and a
//     path no mount contains gets gin's 404 problem.
//   - A GET or HEAD to a path only another method's route serves is the
//     mount's, as on net/http: beside a POST /login route, a browser's
//     GET /login gets the SPA fallback. Any other method gets gin's 405,
//     whose Allow inside a mount also lists GET and HEAD, sorted as on
//     net/http and fiber: beside a POST /api/users route, PUT /api/users
//     gets Allow: GET, HEAD, POST. A path no mount contains keeps gin's
//     Allow (POST).
//   - Root-group middleware (RouterGroup.Use) runs around the mounts even
//     when a configurer registered after Static adds it, and, once a
//     server has a mount, also for requests gin answers with its own 404
//     or 405.
//   - The mount replaces a no-route handler a [WithGinEngine] hook set.
//   - gin routes on the decoded path, so an encoded slash at the end of
//     the prefix, as in GET /assets%2Fapp.js for Static("/assets"), serves
//     app.js, where net/http and fiber by default answer 404 (an
//     engine-parity exception; nothing outside fsys is reachable).
//
// A served file is recorded with http.route prefix+"/*filepath"
// ("/*filepath" for "/"), or prefix for the prefix itself, and no code
// location. A mount's 404 or 405 is recorded as a request no route
// matched and counted by srvr.WithUnmatchedRecordLimit; a failure consumer
// middleware reports first is not. The route listing shows GET prefix
// (unless "/") and GET prefix+"/*filepath", after the other routes.
//
// prefix must start with "/" and have no empty, "." or ".." element, no
// whitespace or control character and none of { } * : % ? # \; one
// trailing slash is dropped. fsys must not be nil, and two mounts must not
// share a prefix. A nil option or invalid configuration, including any
// non-zero srvr.StaticConfig.FiberSendFileCache (gin has no open-file
// cache, so the fiber-only setting fails instead of being ignored), fails
// Start with an error wrapping srvr.ErrRouteConfig and a
// *srvr.ConfigError per invalid setting. Register it with
// [Server.Register]: its Configure panics, so a configurer calling it
// fails Start.
func Static(prefix string, fsys fs.FS, opts ...srvr.StaticOption) srvr.RouteConfigurer[*gin.RouterGroup] {
	// gin has no open-file cache: a positive FiberSendFileCache fails.
	m, err := static.New(prefix, fsys, append(slices.Clip(opts), static.RejectSendFileCache)...)

	return &staticConfigurer{prefix: prefix, mount: m, err: err}
}

// staticConfigurer is the gin static mount configurer. [Server.configure]
// recognises it and serves its mount from gin's no-route chain; its
// Configure is never called by the server.
type staticConfigurer struct {
	// prefix is the prefix as the consumer passed it, for messages.
	prefix string
	mount  *static.Mount
	// err is the configuration error NewStatic returned; the configure
	// phase reports it, so Start fails.
	err error
}

// Configure panics: a gin static mount is served from the server's
// no-route chain, which only [Server.Register] can install, so a consumer
// configurer calling it gets a route error naming that configurer.
func (c *staticConfigurer) Configure(*gin.RouterGroup) {
	panic(fmt.Sprintf("ginsrvr.Static(%q) is registered with Server.Register, not configured by another configurer", c.prefix))
}

// staticMounts are a server's static mounts in registration order, built
// by the configure phase.
type staticMounts struct {
	mounts   []*static.Mount
	prefixes static.Prefixes
}

// staticOwner is the configurer type every static mount is listed with.
const staticOwner = "*ginsrvr.staticConfigurer"

// add checks c at its position in the configure phase and adds its mount,
// or returns the route error that fails Start.
func (ms *staticMounts) add(c *staticConfigurer) error {
	if err := ms.prefixes.Add(c, c.prefix, c.mount, c.err); err != nil {
		return err
	}

	ms.mounts = append(ms.mounts, c.mount)

	return nil
}

// routes lists each mount as GET prefix (unless "/") and GET
// prefix+"/*filepath", in registration order.
func (ms *staticMounts) routes() engine.Routes {
	var out engine.Routes

	for _, m := range ms.mounts {
		for _, p := range m.Patterns("*filepath") {
			out = append(out, engine.Route{Method: http.MethodGet, Pattern: p, Configurer: staticOwner})
		}
	}

	return out
}

// pattern returns the gin pattern a request for path below prefix is
// recorded with: prefix itself for the prefix (other than "/"), else
// prefix+"/*filepath".
func pattern(prefix, path string) string {
	if path == prefix && prefix != "/" {
		return prefix
	}

	return strings.TrimSuffix(prefix, "/") + "/*filepath"
}

// find returns the mount with the longest prefix containing path and the
// path below that prefix, without its leading slash, or nil when no mount
// contains path. Gin hands the decoded, uncleaned path, so "//" and ".."
// elements reach the resolver, which refuses them.
func (ms *staticMounts) find(path string) (m *static.Mount, rest string) {
	for _, mount := range ms.mounts {
		if r, in := below(mount.Prefix(), path); in && (m == nil || len(mount.Prefix()) > len(m.Prefix())) {
			m, rest = mount, r
		}
	}

	return m, rest
}

// below returns the part of path below prefix, a normalised prefix, or
// false when prefix does not contain path.
func below(prefix, path string) (rest string, ok bool) {
	if path == prefix && prefix != "/" {
		return "", true
	}

	return strings.CutPrefix(path, strings.TrimSuffix(prefix, "/")+"/")
}

// begin decides, before gin's no-route or no-method chain runs, whether a
// mount answers the request: one contains its path and, in the no-method
// chain (gin preset 405, another method's route matches), its method is
// GET or HEAD (owner decision 2026-11-19). Such a request is prepared so
// consumer middleware sees it as a matched route's: the mount's pattern as
// its route (net/http records the mount's route before its middleware
// too), status 200 instead of the 404 or 405 gin presets, which ginprblm
// would also take as the status of an error without its own, and no
// Allow header from gin's 405. Other requests keep gin's answer: the
// dispatcher marks it as the router's when it runs, and a failure
// consumer middleware reports first is the middleware's. begin reports
// whether the request is gin's 405 inside a mount, whose Allow must also
// list GET and HEAD, which the mount serves there (owner decision
// 2026-11-19, item 4).
func (ms *staticMounts) begin(c *gin.Context, state *engine.RequestState) (allowReadOnly bool) {
	m, _ := ms.find(c.Request.URL.Path)
	if m == nil {
		return false
	}

	if c.Writer.Status() == http.StatusMethodNotAllowed && !readOnly(c.Request.Method) {
		return true
	}

	state.SetRoute(pattern(m.Prefix(), c.Request.URL.Path))
	c.Status(http.StatusOK)
	c.Writer.Header().Del("Allow")

	return false
}

// readOnly reports whether method is one a static mount serves.
func readOnly(method string) bool { return method == http.MethodGet || method == http.MethodHead }

// serve is the dispatcher at the end of gin's no-route and no-method
// chains. A request begin gave to a mount (its route is set) is answered
// by that mount; any other keeps gin's 404 or 405, the router's answer. A
// mount's miss and a method other than GET and HEAD are the router's
// answers too (design D5).
func (ms *staticMounts) serve(c *gin.Context) {
	state, _ := engine.StateFrom(c.Request.Context())

	m, rest := ms.find(c.Request.URL.Path)
	if m == nil || state == nil || !state.Matched() {
		engine.MarkRouterAnswered(c.Request.Context())

		return
	}

	res := m.Resolve(c.Request.Method, rest, c.GetHeader("Accept"))
	if res.Answer == static.Found {
		static.ServeFile(c.Writer, c.Request, res)

		return
	}

	_ = c.Error(engine.StaticFailure(c.Request.Context(), res, "GET, HEAD"))
	c.Abort()
}
