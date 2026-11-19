package srvr

import (
	"io/fs"
	"net/http"
	"slices"
	"strings"

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
	return func(c *engine.StaticSettings) { c.IndexNames = names }
}

// WithSPAFallback serves file, a name inside the file system such as
// "index.html", for a navigation request that matches no file, so a
// single-page app's client-side routes (/dashboard, /users/42/edit) load
// the app. A miss is a navigation when all hold:
//   - its method is GET or HEAD;
//   - its Accept header lists text/html with a quality above zero (*/*
//     does not count: fetch sends it by default, and an API typo would get
//     HTML with status 200);
//   - the last element of its path has no "." (a missing /app-9999.js is a
//     404, not HTML; a route segment with a dot, such as /users/john.doe,
//     gets 404 too);
//   - its path is not hidden or hostile (those are always 404).
//
// The fallback is sent with status 200, its own Content-Type, the index
// Cache-Control (no-cache by default) and Vary: Accept, because the answer
// depends on Accept. Other misses get the 404 problem. Default: no
// fallback. Start fails when file is not a regular file of the file
// system; a fallback deleted after Start gives 404, and one that cannot be
// read gives the 500 problem.
func WithSPAFallback(file string) StaticOption {
	return func(c *engine.StaticSettings) { c.SPAFallback = file }
}

// WithStaticCacheControl sets the Cache-Control header of served files
// other than index files and the fallback, such as "public,
// max-age=31536000, immutable" for content-hashed build assets. Default:
// none, because only the consumer knows which names are hashed. It is
// never sent on a problem. A value with a control character other than a
// tab fails Start.
func WithStaticCacheControl(v string) StaticOption {
	return func(c *engine.StaticSettings) { c.CacheControl = v }
}

// WithStaticIndexCacheControl sets the Cache-Control header of index files
// and the fallback, replacing the default "no-cache": clients revalidate
// on every use, so a deploy reaches them at once while an unchanged file
// still gets 304. "" means the default, so an index file always carries a
// Cache-Control. A value with a control character other than a tab fails
// Start.
func WithStaticIndexCacheControl(v string) StaticOption {
	return func(c *engine.StaticSettings) { c.IndexCacheControl = v }
}

// WithStaticConfig sets every field of [StaticConfig], a zero field
// meaning its default, replacing what earlier options set; later options
// change the fields they name. The configuration is copied when [Static]
// returns, so changing c's slices afterwards changes nothing.
func WithStaticConfig(c StaticConfig) StaticOption {
	return func(s *engine.StaticSettings) { s.StaticConfig = c }
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
//   - Index files are sent with Cache-Control: no-cache, other files with
//     none ([WithStaticIndexCacheControl], [WithStaticCacheControl]); a
//     problem never carries either.
//   - No SPA fallback: a miss is a 404 until [WithSPAFallback] is set.
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
// [*ConfigError] naming it (StaticOption, Static.Prefix, Static.FS,
// Static.CacheControl, Static.IndexCacheControl, Static.IndexNames,
// Static.SPAFallback, and Static.FiberSendFileCache for any non-zero
// value: net/http has no open-file cache, so the fiber-only setting fails
// instead of being ignored). The fallback is checked at Start, when the file
// system is read for the first time; a file system that blocks there
// blocks Start.
func Static(prefix string, fsys fs.FS, opts ...StaticOption) RouteConfigurer[*Group] {
	// net/http has no open-file cache: a positive FiberSendFileCache fails.
	m, err := engine.NewStatic(prefix, fsys, append(slices.Clip(opts), engine.RejectSendFileCache)...)

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
	// Reads the file system, which the consumer may fill until Start.
	if err := engine.CheckStatic(c.mount, c.err); err != nil {
		g.b.errs = append(g.b.errs, engine.RouteErrorf("static %q (%s): %w", c.prefix, g.b.owner, err))

		return
	}

	// A valid prefix starts with "/", so joinPattern cannot fail.
	joined, _ := joinPattern(g.prefix, c.mount.Prefix())
	if len(joined) > 1 {
		joined = strings.TrimSuffix(joined, "/")
	}

	if err := g.b.statics.Claim(joined, g.b.owner, c.prefix); err != nil {
		g.b.errs = append(g.b.errs, engine.RouteErrorf("static %q (%s): %w", c.prefix, g.b.owner, err))

		return
	}

	// Every method, so that every other registered route, a Mount
	// included, is more specific on ServeMux and wins (design D4); the
	// mount answers methods other than GET and HEAD with 405 and is
	// listed as GET.
	sv := g.b.served
	h := g.b.wrap(func(w http.ResponseWriter, r *http.Request) error { return c.serve(w, r, sv) })

	for _, p := range c.mount.Patterns("{path...}") {
		g.addListed("", http.MethodGet, p, h, engine.Location{})
	}
}

// serve answers a request with the mount's decision; sv is what the
// server's routes serve.
func (c *staticConfigurer) serve(w http.ResponseWriter, r *http.Request, sv *served) error {
	switch res := c.mount.Resolve(r.Method, r.PathValue("path"), r.Header.Get("Accept")); res.Answer {
	case engine.StaticFile:
		engine.ServeStaticFile(w, r, res)

		return nil
	case engine.StaticMethodNotAllowed:
		return res.Failure(r.Context(), sv.allow(r))
	default:
		// The Allow header is read only for a 405, whose probing costs.
		return res.Failure(r.Context(), "")
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
