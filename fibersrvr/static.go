package fibersrvr

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/internal/engine"
	"github.com/kartaladev/srvr/internal/errchain"
	"github.com/kartaladev/srvr/internal/static"
	"github.com/valyala/fasthttp"
)

// Static returns a configurer serving the files of fsys under prefix, such
// as a single-page-app build directory or an embed.FS:
//
//	s.Register(fibersrvr.Static("/", site, srvr.WithSPAFallback("index.html")))
//
// It answers exactly as srvr.Static does on net/http, from the same
// resolver and with the same srvr.StaticOption values and safe defaults,
// which no option loosens: GET and HEAD of regular files with
// Content-Type, Content-Length and Last-Modified (none for a file without
// a modification time, such as an embed.FS file); a directory answered by
// its index file ("index.html", Cache-Control: no-cache) and never
// redirected, so links inside such a page must be root-relative; no
// directory listing; dotfiles, missing files and paths that cannot name a
// file inside fsys answered with the 404 problem; other methods with
// fiber's 405 problem, whose Allow lists GET, HEAD and the methods the
// path's routes serve; a file system error, panic or unseekable file with
// the safe 500 problem, the error going only to the record; only
// If-Modified-Since honoured (no ranges, ETag or compression); no SPA
// fallback unless configured. os.DirFS follows symbolic links anywhere;
// prefer os.OpenRoot(dir) and its Root.FS(), which refuses a link
// escaping the root (a 500 problem).
//
// Files are sent with fiber's Ctx.SendFile from fsys, by the name srvr's
// resolver chose, with fiber's open-file cache off (CacheDuration -1) by
// default, so a file replaced on disk is served fresh and no file stays
// open after a request; [WithSendFileCache] turns the cache on for one
// mount. Compression, byte ranges and fiber's own Cache-Control stay off.
// Because SendFile opens the file again by name, a file removed or made
// unreadable between the resolution and SendFile's open is the 404 problem,
// recorded as a request no route matched, and one removed before
// fasthttp's second open is the safe 500 problem; net/http and gin serve
// the file the resolver opened (an engine-parity exception).
//
// fiber matches routes in registration order, so the server installs the
// mounts after every other configurer, longest prefix first, as GET
// prefix (unless "/") and GET prefix+"/*". Hence:
//   - Every registered route wins over a mount, in either registration
//     order, and so does a sub-app mounted with Use. A route that repeats
//     a mount's pattern, such as Get("/*") beside Static("/"), would never
//     be served and fails Start as a duplicate (on net/http and gin the
//     consumer's catch-all wins instead).
//   - A GET or HEAD to a path only another method's route serves is the
//     mount's: beside a POST /login route, a browser's GET /login gets the
//     SPA fallback.
//   - Middleware a configurer adds with Use runs around the mounts even
//     when that configurer is registered after Static.
//   - fiber removes "." and ".." elements from the path before routing;
//     a request whose path had one is answered 404, never with a file or
//     the fallback.
//   - With fiber's UnescapePath (set through [WithFiberConfig]), fiber
//     decodes the path before routing, so an encoded slash at the end of
//     the prefix, as in GET /assets%2Fapp.js for Static("/assets"), serves
//     app.js, as on gin; without it the answer is 404, as on net/http (an
//     engine-parity exception; nothing outside fsys is reachable).
//
// A served file is recorded with http.route prefix+"/*" ("/*" for "/"), or
// prefix for the prefix itself, and no code location. A mount's 404 is
// recorded as a request no route matched and counted by
// srvr.WithUnmatchedRecordLimit, as fiber's 405; a failure consumer
// middleware reports first is not. The route listing shows GET prefix
// (unless "/") and GET prefix+"/*", after the other routes.
//
// prefix must start with "/" and have no empty, "." or ".." element, no
// whitespace or control character and none of { } * : % ? # \; one
// trailing slash is dropped. fsys must not be nil, and two mounts must not
// share a prefix. A nil option or invalid configuration, such as a
// negative srvr.StaticConfig.FiberSendFileCache, fails Start with an error
// wrapping srvr.ErrRouteConfig and a *srvr.ConfigError per invalid
// setting. Register it with [Server.Register]: its Configure
// panics, so a configurer calling it fails Start.
func Static(prefix string, fsys fs.FS, opts ...srvr.StaticOption) srvr.RouteConfigurer[fiber.Router] {
	m, err := static.New(prefix, fsys, opts...)

	return &staticConfigurer{prefix: prefix, mount: m, err: err}
}

// WithSendFileCache turns on fiber's open-file cache for one static mount,
// with cache duration d: fasthttp keeps each served file open, with its
// size and modification time, for d after its last use, instead of
// opening it on every request. It is an option of [Static], set per
// mount; srvr.StaticConfig.FiberSendFileCache sets it from configuration
// (0 there means off). It is opt-in because it loosens the default, which
// is off (fiber's CacheDuration -1), and has these costs:
//   - A file replaced on disk may be served stale, with its old content,
//     for up to d. An embed.FS cannot change, so it is unaffected. srvr's
//     resolver still checks every request against the file system, so a
//     removed file is a 404 at once.
//   - Each cached mount starts fasthttp's cache cleaner goroutine
//     (fasthttp.(*inMemoryCacheManager).handleCleanCache) on its first
//     served file, and it outlives Shutdown: fiber v3.7.0 never sets
//     fasthttp.FS.CleanStop, so nothing can stop it. A test binary
//     checking for leaked goroutines (goleak) must ignore that function.
//
// The gain is in fasthttp's own open and reads: with os.DirFS it measured
// about half the time per request (fibersrvr's BenchmarkPipeline "static
// cached" row against "static"). d must be positive: d <= 0 fails Start with a
// *srvr.ConfigError naming Static.FiberSendFileCache; leave the option
// out to keep the cache off. net/http and gin have no file cache: their
// Static fails Start on a non-zero FiberSendFileCache.
func WithSendFileCache(d time.Duration) srvr.StaticOption {
	return func(s *static.Settings) {
		if d <= 0 {
			s.Errs = append(s.Errs, errchain.NewConfigError("Static.FiberSendFileCache", d,
				"WithSendFileCache needs a positive duration; leave it out to keep the file cache off"))

			return
		}

		s.FiberSendFileCache = d
	}
}

// staticConfigurer is the fiber static mount configurer. [Server.configure]
// recognises it and installs its mount after every other configurer; its
// Configure is never called by the server.
type staticConfigurer struct {
	// prefix is the prefix as the consumer passed it, for messages.
	prefix string
	mount  *static.Mount
	// err is the configuration error NewStatic returned; the configure
	// phase reports it, so Start fails.
	err error
}

// Configure panics: a fiber static mount must be installed after every
// other route, which only [Server.Register] can do, so a consumer
// configurer calling it gets a route error naming that configurer.
func (c *staticConfigurer) Configure(fiber.Router) {
	panic(fmt.Sprintf("fibersrvr.Static(%q) is registered with Server.Register, not configured by another configurer", c.prefix))
}

// staticOwner is the configurer type every static mount is listed with.
const staticOwner = "*fibersrvr.staticConfigurer"

// staticMounts are a server's static mounts in registration order, built
// by the configure phase.
type staticMounts struct {
	mounts   []*staticMount
	prefixes static.Prefixes
	// decode says whether the mounts percent-decode the routed path: not
	// when fiber's UnescapePath already did.
	decode bool
}

// staticMount is one mount and what serving it needs.
type staticMount struct {
	mount *static.Mount
	// send is the mount's one fiber.SendFile, reused by every request so
	// fiber keeps one file handler for it.
	send fiber.SendFile
	// decode says whether the mount percent-decodes the routed path: not
	// when fiber's UnescapePath already did.
	decode bool
}

// mountFS gives a mount's file system an identity fiber can compare:
// fiber keeps one file handler per distinct SendFile and compares their
// file systems with ==, which never matches a value of a non-comparable
// type (a struct with a map field), so every request would build a
// handler. A pointer made once per mount always matches itself.
type mountFS struct{ fs.FS }

// add checks c at its position in the configure phase and adds its mount,
// or returns the route error that fails Start.
func (ms *staticMounts) add(c *staticConfigurer) error {
	if err := ms.prefixes.Add(c, c.prefix, c.mount, c.err); err != nil {
		return err
	}

	// No open-file cache (-1) unless the mount asked for one: NewStatic
	// refuses a negative duration, and with the cache off a file replaced
	// on disk is served fresh, as on net/http. No compression, ranges or
	// Cache-Control of fiber's own.
	ms.mounts = append(ms.mounts, &staticMount{
		mount:  c.mount,
		send:   fiber.SendFile{FS: &mountFS{c.mount.FS()}, CacheDuration: cmp.Or(c.mount.SendFileCache(), -1)},
		decode: ms.decode,
	})

	return nil
}

// install registers every mount on app, longest prefix first, because
// fiber matches routes in registration order: GET prefix (unless "/") and
// GET prefix+"/*".
func (ms *staticMounts) install(app *fiber.App) {
	sorted := slices.Clone(ms.mounts)
	slices.SortStableFunc(sorted, func(a, b *staticMount) int {
		return cmp.Compare(len(b.mount.Prefix()), len(a.mount.Prefix()))
	})

	for _, sm := range sorted {
		for _, p := range sm.mount.Patterns("*") {
			app.Get(routePath(p), sm.serve)
		}
	}
}

// routePath returns pattern, a mount's pattern, as a fiber route path:
// "+", a parameter marker in fiber's syntax that a valid prefix may hold,
// escaped. A valid prefix has none of fiber's other markers (":", "*").
func routePath(pattern string) string {
	return strings.ReplaceAll(pattern, "+", `\+`)
}

// staticHandlerKey is the handler key of every mount's serve method value,
// which share their code.
var staticHandlerKey = handlerKey((*staticMount)(nil).serve)

// serve answers a request routed to the mount.
func (sm *staticMount) serve(c fiber.Ctx) error {
	rest, ok := sm.rest(c)
	if !ok {
		return notFound(c)
	}

	res := sm.mount.Resolve(c.Method(), rest, c.Get(fiber.HeaderAccept))
	if res.Answer == static.Found {
		return sm.sendFile(c, res)
	}

	return engine.StaticFailure(c.Context(), res, "GET, HEAD")
}

// rest returns the request path below the mount's prefix, percent-decoded
// (design D3), or false when the path cannot name a file of the mount: a
// malformed escape, or a "." or ".." element in the path the client sent,
// which fiber's router removed before routing (a hostile path is a 404,
// never the fallback). With UnescapePath, fiber keeps a malformed escape
// as literal text in the path it routes, so the escape is looked for in
// the path the client sent. It reads the path fiber routed, c.Path(), not the
// wildcard parameter, which loses a trailing slash.
//
// The name it returns is the request's own copy: c.Path() is a view of a
// buffer fiber reuses for a later request, and the name reaches the
// consumer's fs.FS, which may keep it, and errors that outlive the
// request (a *fs.PathError in a record).
func (sm *staticMount) rest(c fiber.Ctx) (string, bool) {
	raw := c.Request().URI().PathOriginal()
	if dotSegment(raw) || (!sm.decode && malformedEscape(raw)) {
		return "", false
	}

	// The mount's patterns matched, so the path starts with the prefix, in
	// any letter case unless CaseSensitive is set.
	p := c.Path()
	rest := strings.TrimPrefix(p[min(len(sm.mount.Prefix()), len(p)):], "/")
	if !sm.decode || strings.IndexByte(rest, '%') < 0 {
		return strings.Clone(rest), true
	}

	// PathUnescape returns a new string when rest has an escape.
	decoded, err := url.PathUnescape(rest)

	return decoded, err == nil
}

// dotSegment reports whether raw, a request path as the client sent it,
// has a "." or ".." element once its valid escapes are decoded.
func dotSegment(raw []byte) bool {
	// A dot element needs a ".", sent as is or escaped as %2e.
	if bytes.IndexByte(raw, '.') < 0 && !bytes.Contains(raw, []byte("%2e")) && !bytes.Contains(raw, []byte("%2E")) {
		return false
	}

	if bytes.IndexByte(raw, '%') >= 0 {
		raw = unescapeValid(raw)
	} else if !bytes.Contains(raw, []byte("/.")) {
		return false
	}

	for seg := range bytes.SplitSeq(raw, []byte("/")) {
		if string(seg) == "." || string(seg) == ".." {
			return true
		}
	}

	return false
}

// unescapeValid returns a copy of b with every valid %XX escape decoded
// and other bytes kept, as fiber's router decodes before cleaning.
func unescapeValid(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] == '%' && i+2 < len(b) {
			if hi, ok := unhex(b[i+1]); ok {
				if lo, ok := unhex(b[i+2]); ok {
					out = append(out, hi<<4|lo)
					i += 2

					continue
				}
			}
		}

		out = append(out, b[i])
	}

	return out
}

// malformedEscape reports whether raw, a request path as the client sent
// it, has a "%" not followed by two hexadecimal digits.
func malformedEscape(raw []byte) bool {
	if bytes.IndexByte(raw, '%') < 0 {
		return false
	}

	_, err := url.PathUnescape(string(raw))

	return err != nil
}

// unhex returns the value of hexadecimal digit c, or false.
func unhex(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

// sendFile serves res, a StaticFile decision, with fiber's file server
// and the headers the decision names, set after SendFile returns because
// fasthttp's 304 resets the response (design D8, D10).
func (sm *staticMount) sendFile(c fiber.Ctx, res static.Result) error {
	// SendFile opens the file again by name (design D2).
	_ = res.File.Close()

	timed := !res.ModTime.IsZero()
	if !timed {
		// fasthttp would take the zero time as a date (H1): no
		// If-Modified-Since applies to a file without one.
		defer withoutHeader(&c.Request().Header, fiber.HeaderIfModifiedSince)()
	}

	// A status consumer middleware set before the mount does not survive
	// a served file, as before a route that sends one.
	c.Status(http.StatusOK)

	if err := c.SendFile(res.Name, sm.send); err != nil {
		return sendFileFailure(c, err)
	}

	h := &c.Response().Header

	switch status := c.Response().StatusCode(); status {
	case http.StatusOK:
		if res.ContentType != "" {
			h.SetContentType(res.ContentType)
		}

		// fasthttp buffers the headers with the start of the body, and a
		// read failing midway closes the connection without flushing them:
		// the client would see no response at all, where every engine sends
		// the 200 and a body cut short (static-files: Faulty file system).
		c.Response().ImmediateHeaderFlush = true
	case http.StatusNotModified:
		h.Set(fiber.HeaderLastModified, res.ModTime.UTC().Format(http.TimeFormat))
	default:
		// fasthttp answered a failure of its own in plain text, such as an
		// open failing after the one SendFile checks.
		return fmt.Errorf("static file %q: fiber's file server answered %d", res.Name, status)
	}

	if !timed {
		h.Del(fiber.HeaderLastModified)
	}

	setIf(h, fiber.HeaderCacheControl, res.CacheControl)
	setIf(h, fiber.HeaderVary, res.Vary)

	return nil
}

// sendFileFailure returns the error for c of err, which SendFile
// returned: its 404, a file removed since it was resolved, is a miss (the
// vanish race of design D2); any other error fails the request.
func sendFileFailure(c fiber.Ctx, err error) error {
	if fe, ok := errors.AsType[*fiber.Error](err); ok && fe.Code == http.StatusNotFound {
		return notFound(c)
	}

	return err
}

// withoutHeader removes header name from h and returns the function that
// puts its values back.
func withoutHeader(h *fasthttp.RequestHeader, name string) func() {
	v := h.Peek(name)
	if v == nil {
		return func() {}
	}

	saved := bytes.Clone(v)
	h.Del(name)

	return func() { h.SetBytesV(name, saved) }
}

// setIf sets header name to v unless v is empty.
func setIf(h *fasthttp.ResponseHeader, name, v string) {
	if v != "" {
		h.Set(name, v)
	}
}

// notFound marks the request as answered by the router (design D5) and
// returns the 404 problem.
func notFound(c fiber.Ctx) error {
	engine.MarkRouterAnswered(c.Context())

	return prblm.ErrNotFound
}
