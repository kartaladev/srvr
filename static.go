package srvr

import (
	"io/fs"
	"net/http"
	"slices"
	"strings"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/engine"
)

type (
	// StaticConfig configures a static mount; see [engine.StaticConfig]
	// for its fields and defaults. Pass it with [WithStaticConfig].
	StaticConfig = engine.StaticConfig
	// StaticOption configures a static mount; options apply in argument
	// order, so a later option wins over an earlier one.
	StaticOption = engine.StaticOption
)

// WithStaticIndexNames sets the file names that answer a directory, the
// first existing one winning (default "index.html"). Each must be a single
// file name: no "/", and not "." or "..". A requested name whose last
// element is an index name is an index file for caching (see
// [StaticConfig]).
func WithStaticIndexNames(names ...string) StaticOption {
	return func(c *StaticConfig) { c.IndexNames = names }
}

// WithStaticConfig sets every field of [StaticConfig], a zero field
// meaning its default, replacing what earlier options set; later options
// change the fields they name. The configuration is copied when [Static]
// returns, so changing c's slices afterwards changes nothing.
func WithStaticConfig(c StaticConfig) StaticOption {
	return func(dst *StaticConfig) { *dst = c }
}

// Static returns a configurer serving the files of fsys under prefix,
// such as a single-page-app build directory or an embed.FS:
//
//	s.Register(srvr.Static("/", site))
//
// It answers GET and HEAD below prefix with the regular file of that name:
// 200, Content-Type from the name's extension (mime.TypeByExtension, which
// also reads the system's MIME tables on Unix; sniffed when there is
// none), Content-Length, and Last-Modified when the file has a
// modification time (embed.FS files have none). A directory is answered by
// its index file ("index.html" unless [WithStaticIndexNames] says
// otherwise), with or without the trailing slash and never with a
// redirect, so links inside such a page must be root-relative.
//
// The defaults are safe without configuration, and no option loosens
// them:
//   - Directories are never listed.
//   - Dotfiles and anything in a dot-directory (.env, .git/config) answer
//     404 exactly as missing files do.
//   - A missing file is srvr's 404 problem for prblm.ErrNotFound, a
//     request path that cannot name a file inside fsys (a ".." element
//     once decoded, a backslash, a control character, invalid UTF-8) too.
//   - A file system error other than "does not exist", a panic, or a file
//     that cannot seek is the safe 500 problem: the error, which may name
//     the file, goes only to the http.request.failed record.
//   - Only If-Modified-Since is honoured: Range, If-Range, If-Match,
//     If-None-Match and If-Unmodified-Since are ignored, so the whole file
//     is sent and no 206, 412 or 416 answer occurs. No ETag is sent and
//     nothing is compressed.
//   - Other methods get a 405 problem whose Allow lists GET, HEAD and the
//     methods other routes serve for the path.
//
// Symbolic links are the file system's business: os.DirFS follows a link
// anywhere, even out of its directory. Prefer os.OpenRoot(dir) and its
// Root.FS(), which refuses a link that escapes the root; such a request
// gets the 500 problem and an ERROR record.
//
// Registered routes always win: the mount's patterns, prefix and
// prefix+"/{path...}", are registered for every method, so ServeMux finds
// any other route more specific, in either registration order. Only a
// method-less route with the same path, such as Mount("/", h) beside
// Static("/"), conflicts and fails Start. Among static mounts, the longest
// prefix that contains the path owns the request. A request path with ".."
// or "//" sent as a raw request target gets ServeMux's 307 redirect to the
// cleaned path before any route runs (an engine-parity exception: gin and
// fiber answer 404).
//
// A 404 or 405 of the mount is logged and observed as a request no route
// matched (no http.route) and counted by [WithUnmatchedRecordLimit],
// because with Static("/") every scanner path reaches it; a served file
// has http.route set to the mount's pattern. The mount's routes get the
// group's middleware at the time Configure runs.
//
// prefix must start with "/" and have no empty, "." or ".." element, no
// whitespace or control character and none of { } * : % ? # \; one
// trailing slash is dropped. fsys must not be nil, and two mounts must not
// share a prefix. A nil option or invalid configuration fails Start with
// an error wrapping [ErrRouteConfig] and, for each invalid setting, a
// [*ConfigError] naming it (Static.Prefix, Static.FS, StaticOption,
// Static.IndexNames).
func Static(prefix string, fsys fs.FS, opts ...StaticOption) RouteConfigurer[*Group] {
	m, err := engine.NewStatic(prefix, fsys, opts...)

	return &staticConfigurer{prefix: prefix, mount: m, err: err}
}

// staticConfigurer is the net/http static mount configurer.
type staticConfigurer struct {
	// prefix is the prefix as the consumer passed it, for messages.
	prefix string
	mount  *engine.Static
	// err is the configuration error NewStatic returned; Configure
	// reports it, so Start fails.
	err error
}

// Configure registers the mount on g, or records its configuration error.
func (c *staticConfigurer) Configure(g *Group) {
	if c.err != nil {
		g.b.errs = append(g.b.errs, engine.RouteErrorf("static %q (%s): %w", c.prefix, g.b.owner, c.err))

		return
	}

	// A valid prefix starts with "/", so joinPattern cannot fail.
	joined, _ := joinPattern(g.prefix, c.mount.Prefix())
	if len(joined) > 1 {
		joined = strings.TrimSuffix(joined, "/")
	}

	if earlier, dup := g.b.statics[joined]; dup {
		err := engine.NewConfigError("Static.Prefix", c.prefix, "is already served by another static mount ("+earlier+")")
		g.b.errs = append(g.b.errs, engine.RouteErrorf("static %q (%s): %w", c.prefix, g.b.owner, err))

		return
	}

	if g.b.statics == nil {
		g.b.statics = map[string]string{}
	}

	g.b.statics[joined] = g.b.owner

	// Every method, so that every other registered route, a Mount
	// included, is more specific on ServeMux and wins (design D4); the
	// mount answers methods other than GET and HEAD with 405 and is
	// listed as GET.
	sv := g.b.served
	h := g.b.wrap(func(w http.ResponseWriter, r *http.Request) error { return c.serve(w, r, sv) })

	if p := c.mount.Prefix(); p != "/" {
		g.addListed("", http.MethodGet, p, h, engine.Location{})
	}

	g.addListed("", http.MethodGet, c.mount.Prefix()+"/{path...}", h, engine.Location{})
}

// serve answers a request with the mount's decision; sv is what the
// server's routes serve.
func (c *staticConfigurer) serve(w http.ResponseWriter, r *http.Request, sv *served) error {
	res := c.mount.Resolve(r.Method, r.PathValue("path"))

	switch res.Answer {
	case engine.StaticFile:
		defer res.File.Close()

		setStaticHeaders(w.Header(), res)
		http.ServeContent(w, withoutPreconditions(r), res.Name, res.ModTime, res.File)

		return nil
	case engine.StaticNotFound:
		markRouterAnswered(r) // design D5: a static miss is the router's answer

		return prblm.ErrNotFound
	case engine.StaticMethodNotAllowed:
		markRouterAnswered(r)

		return prblm.New(http.StatusMethodNotAllowed, prblm.WithHeader("Allow", sv.allow(r)))
	default:
		return res.Err
	}
}

// allow returns the Allow header of a static mount's 405 for r: GET and
// HEAD, which the mount serves, and every method another route serves for
// r's path, found by asking the ServeMux which pattern matches r with that
// method. It runs only for a method the mount does not serve.
func (sv *served) allow(r *http.Request) string {
	methods := []string{http.MethodGet, http.MethodHead}
	probe := *r

	for _, m := range sv.methods {
		if m == http.MethodGet || m == http.MethodHead {
			continue
		}

		probe.Method = m
		if _, pattern := sv.mux.Handler(&probe); pattern != r.Pattern {
			methods = append(methods, m)
		}
	}

	slices.Sort(methods)

	return strings.Join(methods, ", ")
}

// setStaticHeaders sets the headers the decision of a served file names.
// They are set only once the file is open, so a problem never carries
// them (prblm keeps a Cache-Control already on the response), and before
// http.ServeContent, which then keeps them on a 304 and does not sniff a
// known type.
func setStaticHeaders(h http.Header, res engine.StaticResult) {
	setIf(h, "Content-Type", res.ContentType)
	setIf(h, "Cache-Control", res.CacheControl)
}

// setIf sets header name to v unless v is empty.
func setIf(h http.Header, name, v string) {
	if v != "" {
		h.Set(name, v)
	}
}

// preconditions are the request headers http.ServeContent would honour
// and a static mount ignores: byte ranges and every precondition except
// If-Modified-Since, so no 206, 412 or 416 answer can occur.
var preconditions = []string{"Range", "If-Range", "If-Match", "If-None-Match", "If-Unmodified-Since"}

// withoutPreconditions returns r, or a shallow copy without the
// preconditions a static mount ignores when r carries any.
func withoutPreconditions(r *http.Request) *http.Request {
	if !slices.ContainsFunc(preconditions, func(h string) bool { _, ok := r.Header[h]; return ok }) {
		return r
	}

	r2 := new(http.Request)
	*r2 = *r
	r2.Header = r.Header.Clone()

	for _, h := range preconditions {
		delete(r2.Header, h)
	}

	return r2
}
