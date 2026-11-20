// Package static resolves requests against a static file mount (an fs.FS
// under a prefix): its options and their validation, the safe path rules,
// index and SPA fallback lookup, and serving a resolved file. Every srvr
// engine uses it; mapping an answer to an error needs the request state,
// so that part is internal/engine's StaticFailure.
//
// It is internal to srvr's modules and imports only internal/errchain.
package static

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/kartaladev/srvr/internal/errchain"
)

// Config configures a static mount, the files of an fs.FS served
// under a URL prefix; a zero field means its default, so the zero value
// is the safe default configuration. Consumers use it as
// srvr.Config with srvr.WithStaticConfig, and set single fields with
// the srvr.WithStatic* and srvr.WithSPAFallback options.
type Config struct {
	// SPAFallback names the file served, with status 200, for a navigation
	// request that matches no file. "", the default, serves no fallback.
	SPAFallback string `json:"spa_fallback" yaml:"spa_fallback" env:"SPA_FALLBACK"`
	// CacheControl is the Cache-Control header of served files other than
	// index files and the fallback. "", the default, sends none: only the
	// consumer knows which file names are content-hashed.
	CacheControl string `json:"cache_control" yaml:"cache_control" env:"CACHE_CONTROL"`
	// IndexCacheControl is the Cache-Control header of index files (a
	// directory's answer, or a requested name whose last element is an
	// index name) and the fallback. "", the default, means "no-cache": a
	// client revalidates on every use, so a deploy reaches every client at
	// once while an unchanged file still gets 304.
	IndexCacheControl string `json:"index_cache_control" yaml:"index_cache_control" env:"INDEX_CACHE_CONTROL"`
	// IndexNames are the file names that answer a directory, the first
	// existing one winning. Empty, the default, means "index.html".
	IndexNames []string `json:"index_names" yaml:"index_names" env:"INDEX_NAMES"`
	// FiberSendFileCache, when positive, turns on fiber's open-file cache
	// for the mount with that cache duration (fibersrvr.WithSendFileCache
	// says what it costs). 0, the default, keeps it off: a file replaced on
	// disk is served fresh and no cache goroutine starts. A negative value
	// fails Start, and so does any non-zero value on net/http and gin,
	// which have no file cache, so the setting is never silently ignored.
	FiberSendFileCache time.Duration `json:"fiber_send_file_cache" yaml:"fiber_send_file_cache" env:"FIBER_SEND_FILE_CACHE"`
}

// Settings is what the [Option]s of one mount build: its
// configuration and the mistakes the options detect while they are
// applied. Only srvr's and the engine modules' option functions write it.
type Settings struct {
	Config
	// Errs collects the mistakes options detect while they are applied,
	// such as fibersrvr.WithSendFileCache(0). [New] reports them even
	// when a later option replaces the configuration
	// (srvr.WithStaticConfig), because a refused argument is a programming
	// error that no configuration hides.
	Errs []error
}

// Option sets fields of a [Settings]; consumers use it as
// srvr.Option. Options apply in argument order.
type Option func(*Settings)

// Mount is one static mount: a validated prefix, its file system and its
// configuration, copied when it was created. The engines build it with
// [New] and serve its [Mount.Resolve] decisions with their own file
// primitives. It is safe for concurrent use.
type Mount struct {
	prefix string
	fsys   fs.FS
	cfg    Config
	// indexCacheControl is the Cache-Control of index files and the
	// fallback: cfg.IndexCacheControl or the default.
	indexCacheControl string
}

// New returns the mount of fsys under prefix, configured by opts
// applied in order to a zero Config, or an error wrapping errchain.ErrConfig
// joining one *errchain.ConfigError per invalid setting: StaticOption (a nil
// option), Static.Prefix, Static.FS, Static.CacheControl and
// Static.IndexCacheControl (a control character other than a tab, which
// could inject a header), Static.IndexNames and Static.SPAFallback (not a
// valid fs.FS name), Static.FiberSendFileCache (a negative value; an
// engine without an open-file cache passes [RejectSendFileCache] last to
// refuse a positive one too), and the errors options collected in
// [Settings.Errs]. It reads nothing from fsys: [Mount.Check] does.
//
// A valid prefix starts with "/", has no empty, "." or ".." element, no
// whitespace or control character, and none of { } * : % ? # \, which are
// pattern syntax in some engine; one trailing slash is dropped, so
// "/assets/" is "/assets".
func New(prefix string, fsys fs.FS, opts ...Option) (*Mount, error) {
	var errs []error

	cfg, err := applyOptions(opts)
	if err != nil {
		errs = append(errs, err)
	}

	norm, err := staticPrefix(prefix)
	if err != nil {
		errs = append(errs, err)
	}

	if errchain.IsNil(fsys) {
		errs = append(errs, errchain.NewConfigError("Static.FS", fsys, "must not be nil"))
	}

	for _, err := range []error{
		validateHeaderValue("Static.CacheControl", cfg.CacheControl),
		validateHeaderValue("Static.IndexCacheControl", cfg.IndexCacheControl),
		validateIndexNames(cfg.IndexNames),
		validateFallbackName(cfg.SPAFallback),
		validateSendFileCache(cfg.FiberSendFileCache),
	} {
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	if len(cfg.IndexNames) == 0 {
		cfg.IndexNames = []string{defaultIndexName}
	}

	return &Mount{
		prefix: norm, fsys: fsys, cfg: cfg,
		indexCacheControl: cmp.Or(cfg.IndexCacheControl, defaultIndexCacheControl),
	}, nil
}

// Prefix returns the mount's normalised prefix: "/" or a path without a
// trailing slash, such as "/assets".
func (s *Mount) Prefix() string { return s.prefix }

// Patterns returns the route patterns serving the mount in an engine whose
// catch-all element is wildcard, such as "{path...}", "*filepath" or "*":
// the prefix itself unless it is "/", then the prefix followed by "/" and
// wildcard.
func (s *Mount) Patterns(wildcard string) []string {
	all := strings.TrimSuffix(s.prefix, "/") + "/" + wildcard
	if s.prefix == "/" {
		return []string{all}
	}

	return []string{s.prefix, all}
}

// SendFileCache returns the cache duration of the engine's open-file
// cache for the mount, 0 for none.
func (s *Mount) SendFileCache() time.Duration { return s.cfg.FiberSendFileCache }

// FS returns the mount's file system, for an engine whose file primitive
// opens the names [Mount.Resolve] chose itself (fiber's Ctx.SendFile).
func (s *Mount) FS() fs.FS { return s.fsys }

// applyOptions applies opts in order to a zero config and returns
// it with its slices copied, so the caller changing a slice it passed
// changes nothing; a nil option is an error naming its index, joined
// with the errors the options collected.
func applyOptions(opts []Option) (Config, error) {
	var s Settings

	for i, opt := range opts {
		if opt == nil {
			s.Errs = append(s.Errs, errchain.NewConfigError("StaticOption", i, "is nil"))

			continue
		}

		opt(&s)
	}

	s.IndexNames = slices.Clone(s.IndexNames)

	return s.Config, errors.Join(s.Errs...)
}

// staticPrefix returns prefix normalised, or an error naming
// Static.Prefix.
func staticPrefix(prefix string) (string, error) {
	invalid := func(reason string) (string, error) {
		return "", errchain.NewConfigError("Static.Prefix", prefix, reason)
	}

	switch {
	case prefix == "" || prefix[0] != '/':
		return invalid(`must start with "/"`)
	case !utf8.ValidString(prefix):
		return invalid("must be valid UTF-8")
	case strings.ContainsFunc(prefix, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }):
		return invalid("must not contain whitespace or control characters")
	case strings.ContainsAny(prefix, `{}*:%?#\`):
		return invalid(`must not contain any of { } * : % ? # \`)
	case strings.Contains(prefix, "//"):
		return invalid("must not have an empty element")
	}

	if prefix == "/" {
		return prefix, nil
	}

	norm := strings.TrimSuffix(prefix, "/")
	for elem := range strings.SplitSeq(norm[1:], "/") {
		if elem == "." || elem == ".." {
			return invalid(`must not have a "." or ".." element`)
		}
	}

	return norm, nil
}

// Check returns the error an engine's configure phase reports for a
// mount New returned with err: err itself, or [Mount.Check]. It is
// the one call every engine makes, so none forgets the start-time check.
func Check(m *Mount, err error) error {
	if err != nil {
		return err
	}

	return m.Check()
}

// Prefixes records the static mounts of an engine's configure phase
// by prefix, so two mounts never share one. The zero value is ready to
// use.
type Prefixes struct {
	// owners names the configurer of each mount, by normalised prefix.
	owners map[string]string
}

// Add checks the static configurer c, registered with prefix, whose mount
// New returned as m with err, at its position in the configure
// phase: it returns a route error naming c for err, for [Check]'s
// start-time check, or for a prefix an earlier mount took (naming that
// mount), and otherwise records m's prefix and returns nil.
func (p *Prefixes) Add(c any, prefix string, m *Mount, err error) error {
	name := fmt.Sprintf("static %q (%T)", prefix, c)

	// Reads the file system, which the consumer may fill until Start.
	if err := Check(m, err); err != nil {
		return errchain.RouteErrorf("%s: %w", name, err)
	}

	if err := p.Claim(m.Prefix(), name, prefix); err != nil {
		return errchain.RouteErrorf("%s: %w", name, err)
	}

	return nil
}

// Claim records owner as the mount serving key, a mount's full
// normalised prefix, and returns nil; when an earlier mount claimed key,
// it returns a *errchain.ConfigError naming Static.Prefix with value prefix, the
// prefix as the consumer passed it, and that mount's owner. The net/http
// engine claims the prefix joined with its group's; Add claims the
// mount's own.
func (p *Prefixes) Claim(key, owner, prefix string) error {
	if earlier, dup := p.owners[key]; dup {
		return errchain.NewConfigError("Static.Prefix", prefix, "is already served by another static mount ("+earlier+")")
	}

	if p.owners == nil {
		p.owners = map[string]string{}
	}

	p.owners[key] = owner

	return nil
}

// Len returns the number of mounts claimed so far.
func (p *Prefixes) Len() int { return len(p.owners) }

// Check reads the file system for the settings that need it, during an
// engine's configure phase: the SPA fallback must be a regular file. A
// file system that panics or fails is reported, never raised. It returns
// nil or a *errchain.ConfigError naming Static.SPAFallback.
func (s *Mount) Check() (err error) {
	name := s.cfg.SPAFallback
	if name == "" {
		return nil
	}

	defer func() {
		if v := recover(); v != nil {
			err = errchain.NewConfigError("Static.SPAFallback", name, "reading it panicked: "+errchain.ValueText(v))
		}
	}()

	info, statErr := fs.Stat(s.fsys, name)

	switch {
	case statErr != nil && missOrError(statErr) == nil:
		return errchain.NewConfigError("Static.SPAFallback", name, "does not exist in the file system")
	case statErr != nil:
		return errchain.NewConfigError("Static.SPAFallback", name, "cannot be read: "+errchain.ErrorText(statErr))
	case !info.Mode().IsRegular():
		return errchain.NewConfigError("Static.SPAFallback", name, "is not a regular file")
	default:
		return nil
	}
}

// validateHeaderValue returns an error naming field when v has a control
// character other than a tab: a CR or LF would end the header and start
// another one.
func validateHeaderValue(field, v string) error {
	if strings.ContainsFunc(v, func(r rune) bool { return r != '\t' && unicode.IsControl(r) }) {
		return errchain.NewConfigError(field, v, "must not contain control characters")
	}

	return nil
}

// validateSendFileCache returns nil or a *errchain.ConfigError naming
// Static.FiberSendFileCache for a negative d.
func validateSendFileCache(d time.Duration) error {
	if d < 0 {
		return errchain.NewConfigError("Static.FiberSendFileCache", d, "must not be negative; 0 keeps the file cache off")
	}

	return nil
}

// RejectSendFileCache is the option an engine without an open-file cache
// (net/http, gin) passes last to [New]: it refuses a positive
// Config.FiberSendFileCache, which only fiber serves, so the setting
// fails Start instead of being silently ignored. Passed last, it sees the
// configuration every other option built.
func RejectSendFileCache(s *Settings) {
	if d := s.FiberSendFileCache; d > 0 {
		s.Errs = append(s.Errs, errchain.NewConfigError("Static.FiberSendFileCache", d,
			"is supported only by fibersrvr.Static; this engine has no file cache, so it must be 0"))
	}
}

// validateFallbackName returns an error naming Static.SPAFallback when
// name is set but is not a name the mount could serve for a request (see
// Resolve rules 2 and 3), such as "../index.html" or "/index.html".
func validateFallbackName(name string) error {
	if name == "" {
		return nil
	}

	if n, dirOnly, ok := requestName(name); !ok || dirOnly || n != name {
		return errchain.NewConfigError("Static.SPAFallback", name, `must be a visible file name inside the file system, such as "index.html"`)
	}

	return nil
}

// validateIndexNames returns an error naming Static.IndexNames for the
// first name that is not one valid path element.
func validateIndexNames(names []string) error {
	for _, n := range names {
		if !isFileName(n) {
			return errchain.NewConfigError("Static.IndexNames", n, "must be a single file name, without / and not . or ..")
		}
	}

	return nil
}

// isFileName reports whether name is one valid fs.FS path element.
func isFileName(name string) bool {
	return name != "." && fs.ValidPath(name) && !strings.Contains(name, "/")
}

// defaultIndexCacheControl is the Cache-Control of index files and the
// fallback when IndexCacheControl is empty: revalidate on every use, so a
// deploy reaches every client at once while unchanged files still get 304.
const defaultIndexCacheControl = "no-cache"

// defaultIndexName answers a directory when IndexNames is empty.
const defaultIndexName = "index.html"

// Answer is how a static mount answers a request.
type Answer int

const (
	// Found serves the open file of the result.
	Found Answer = iota + 1
	// NotFound answers 404: no file, a hidden file or a path that
	// cannot name a file.
	NotFound
	// MethodNotAllowed answers 405 with Allow: GET, HEAD.
	MethodNotAllowed
	// Failed answers 500: the file system failed; Err says how.
	Failed
)

// FileContent is an open file a static mount serves: readable and
// seekable, as http.ServeContent needs.
type FileContent interface {
	fs.File
	io.Seeker
}

// Result is a static mount's decision for one request. For
// StaticFile, File is open and the caller closes it on every path.
type Result struct {
	Answer Answer
	// File is the open file to serve, nil unless Answer is StaticFile.
	File FileContent
	// Name is the served file's name in the file system.
	Name string
	// ModTime is the served file's modification time, zero when the file
	// system has none (embed.FS).
	ModTime time.Time
	// ContentType is the type of Name's extension, "" when it has none:
	// the engine then sniffs the content.
	ContentType string
	// CacheControl is the Cache-Control header to send with the file, ""
	// for none.
	CacheControl string
	// Vary is the Vary header to send with the file: "Accept" for the SPA
	// fallback, whose answer depends on it, "" otherwise.
	Vary string
	// Err is the file system's failure when Answer is StaticFailed.
	Err error
}

// Resolve decides how the mount answers method for rest, the request path
// below the prefix as the engine decoded it, without a leading slash (""
// for the prefix itself; a trailing slash names a directory), and accept,
// the request's Accept header, which only the SPA fallback reads. In
// order:
//
//  1. A method other than GET or HEAD: StaticMethodNotAllowed.
//  2. Invalid UTF-8, a backslash, a control character, or an empty, "."
//     or ".." element: StaticNotFound.
//  3. An element starting with ".": StaticNotFound, exactly as if the
//     file did not exist.
//  4. The named regular file, or for a directory the first index name
//     that is a regular file inside it, is served. A name that does not
//     exist, a regular file named with a trailing slash, a directory
//     without an index file and anything else (a device, a socket):
//     StaticNotFound.
//  5. A miss of rule 4 gets the SPA fallback when one is configured and
//     the request is a navigation: accept lists text/html with a quality
//     above zero and rest's last element has no ".". Otherwise
//     StaticNotFound. Rules 1 to 3 never fall back.
//  6. Any other file system error, and a file that cannot seek:
//     StaticFailed.
//
// It never redirects. A panic of the file system reaches the caller, whose
// request recovery answers it; no file is left open.
func (s *Mount) Resolve(method, rest, accept string) Result {
	if method != http.MethodGet && method != http.MethodHead {
		return Result{Answer: MethodNotAllowed}
	}

	name, dirOnly, ok := requestName(rest)
	if !ok {
		return Result{Answer: NotFound}
	}

	found, info, err := s.lookup(name, dirOnly)
	if err != nil {
		return staticFailure(err)
	}

	if found == "" {
		return s.miss(rest, accept)
	}

	return s.open(found, info)
}

// miss answers a request whose valid, visible path matched no file: the
// fallback for a navigation, else StaticNotFound. The fallback is opened
// first and its type read from the open file, one name lookup fewer than
// Stat then Open: its name is the consumer's, checked at Start, never the
// request's. It is read on every request, so a fallback deleted or
// replaced after Start is seen at once.
func (s *Mount) miss(rest, accept string) Result {
	name := s.cfg.SPAFallback
	if name == "" || !navigation(rest, accept) {
		return Result{Answer: NotFound}
	}

	f, err := s.fsys.Open(name)
	if err != nil {
		return openFailure(err)
	}

	info, err := f.Stat()

	switch {
	case err != nil:
		_ = f.Close()

		return staticFailure(err)
	case !info.Mode().IsRegular():
		_ = f.Close()

		return Result{Answer: NotFound} // replaced by a directory after Start
	}

	res := s.serving(name, f, info)
	if res.Answer == Found {
		res.CacheControl, res.Vary = s.indexCacheControl, "Accept"
	}

	return res
}

// navigation reports whether a request for rest with Accept header accept
// is a page navigation the SPA fallback answers: rest's last element has
// no "." (a missing app-9999.js is not a page) and accept lists text/html.
func navigation(rest, accept string) bool {
	trimmed := strings.TrimSuffix(rest, "/")
	last := trimmed[strings.LastIndexByte(trimmed, '/')+1:]

	return !strings.Contains(last, ".") && acceptsHTML(accept)
}

// acceptsHTML reports whether an Accept header lists text/html with a
// quality above zero. Media ranges are split on ",", parameters on ";",
// compared without case. */* and text/* do not count: fetch sends */* by
// default. A malformed quality does not count either.
func acceptsHTML(accept string) bool {
	for mediaRange := range strings.SplitSeq(accept, ",") {
		mediaType, params, _ := strings.Cut(mediaRange, ";")
		if !strings.EqualFold(strings.TrimSpace(mediaType), "text/html") {
			continue
		}

		if positiveQuality(params) {
			return true
		}
	}

	return false
}

// positiveQuality reports whether the parameters of a media range give it
// a quality above zero: no q parameter means 1.
func positiveQuality(params string) bool {
	for param := range strings.SplitSeq(params, ";") {
		name, value, _ := strings.Cut(param, "=")
		if strings.EqualFold(strings.TrimSpace(name), "q") {
			return positiveQValue(strings.TrimSpace(value))
		}
	}

	return true
}

// positiveQValue reports whether v is a valid RFC 9110 qvalue ("0",
// "0.5", "1", "1.000") above zero; an invalid one is not. It is parsed by
// hand so that a malformed value costs no allocation.
func positiveQValue(v string) bool {
	if v == "" || (v[0] != '0' && v[0] != '1') {
		return false
	}

	whole, frac, dotted := strings.Cut(v, ".")
	if len(whole) != 1 || (dotted && len(frac) > 3) {
		return false
	}

	positive := whole == "1"
	for _, c := range []byte(frac) {
		switch {
		case c < '0' || c > '9', whole == "1" && c != '0':
			return false
		case c != '0':
			positive = true
		}
	}

	return positive
}

// requestName returns the file system name rest names and whether it
// names a directory only (a trailing slash), or false when rest cannot
// name a file: rules 2 and 3 of Resolve.
func requestName(rest string) (name string, dirOnly, ok bool) {
	if rest == "" {
		return ".", true, true
	}

	if !utf8.ValidString(rest) || strings.ContainsFunc(rest, unsafeRune) {
		return "", false, false
	}

	name, dirOnly = strings.CutSuffix(rest, "/")
	if !fs.ValidPath(name) || hidden(name) {
		return "", false, false
	}

	return name, dirOnly, true
}

// unsafeRune reports whether r never appears in a name a static mount
// serves: a backslash, a separator on Windows, or a control character.
func unsafeRune(r rune) bool { return r == '\\' || unicode.IsControl(r) }

// hidden reports whether an element of name, a valid fs.FS path, starts
// with ".", such as .env or .git/config.
func hidden(name string) bool {
	for elem := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(elem, ".") {
			return true
		}
	}

	return false
}

// lookup returns the regular file that answers name, with its
// information, or "" for a miss, or the file system's error.
func (s *Mount) lookup(name string, dirOnly bool) (string, fs.FileInfo, error) {
	info, err := fs.Stat(s.fsys, name)
	if err != nil {
		return "", nil, missOrError(err)
	}

	switch {
	case info.Mode().IsRegular() && !dirOnly:
		return name, info, nil
	case info.IsDir():
		return s.index(name)
	default:
		return "", nil, nil
	}
}

// index returns the first index file of directory dir that is a regular
// file, with its information, or "" when there is none.
func (s *Mount) index(dir string) (string, fs.FileInfo, error) {
	for _, idx := range s.cfg.IndexNames {
		name := path.Join(dir, idx)

		info, err := fs.Stat(s.fsys, name)
		if err != nil {
			if err = missOrError(err); err != nil {
				return "", nil, err
			}

			continue
		}

		if info.Mode().IsRegular() {
			return name, info, nil
		}
	}

	return "", nil, nil
}

// isIndex reports whether name's last element is an index name.
func (s *Mount) isIndex(name string) bool {
	return slices.Contains(s.cfg.IndexNames, path.Base(name))
}

// open opens the chosen file and returns the decision serving it.
func (s *Mount) open(name string, info fs.FileInfo) Result {
	f, err := s.fsys.Open(name)
	if err != nil {
		return openFailure(err)
	}

	return s.serving(name, f, info)
}

// serving returns the decision serving f, the open file name with info,
// or closes it and fails when it cannot seek.
func (s *Mount) serving(name string, f fs.File, info fs.FileInfo) Result {
	content, ok := f.(FileContent)
	if !ok {
		_ = f.Close()

		return staticFailure(fmt.Errorf("static file %q: %T cannot seek, so it cannot be served", name, f))
	}

	res := Result{
		Answer: Found, File: content, Name: name, ModTime: info.ModTime(),
		ContentType: mime.TypeByExtension(path.Ext(name)), CacheControl: s.cfg.CacheControl,
	}

	if s.isIndex(name) {
		res.CacheControl = s.indexCacheControl
	}

	return res
}

// openFailure returns the decision for an error opening a chosen file: a
// miss (deleted since it was found) or a failure.
func openFailure(err error) Result {
	if err = missOrError(err); err != nil {
		return staticFailure(err)
	}

	return Result{Answer: NotFound}
}

// missOrError returns nil when err, a file system error, means the name
// does not exist or cannot exist, and err otherwise: fs.ErrNotExist,
// fs.ErrInvalid, and the operating system's "not a directory" (a name
// below a file, such as index.html/x) and "name too long", which os.DirFS
// returns without matching fs.ErrNotExist. An error whose chain cannot be
// walked is never a miss.
func missOrError(err error) error {
	if errchain.HasCycle(err) {
		return err
	}

	for _, miss := range missErrors {
		if errors.Is(err, miss) {
			return nil
		}
	}

	return err
}

// missErrors are the file system errors that mean a name does not exist.
var missErrors = [...]error{fs.ErrNotExist, fs.ErrInvalid, syscall.ENOTDIR, syscall.ENAMETOOLONG}

// staticFailure returns the decision for a file system failure.
func staticFailure(err error) Result {
	return Result{Answer: Failed, Err: err}
}

// ServeFile sends res, a StaticFile decision, with net/http's
// content server, as the net/http and gin engines do, and closes its file.
// The headers the decision names are set first: only once the file is
// open, so a problem never carries them (prblm keeps a Cache-Control
// already on the response), and before http.ServeContent, which then
// keeps them on a 304 and does not sniff a known type. The request is
// passed without the preconditions a static mount ignores.
func ServeFile(w http.ResponseWriter, r *http.Request, res Result) {
	defer res.File.Close()

	h := w.Header()
	setIf(h, "Content-Type", res.ContentType)
	setIf(h, "Cache-Control", res.CacheControl)
	setIf(h, "Vary", res.Vary)

	http.ServeContent(w, withoutPreconditions(r), res.Name, res.ModTime, res.File)
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
var preconditions = [...]string{"Range", "If-Range", "If-Match", "If-None-Match", "If-Unmodified-Since"}

// withoutPreconditions returns r, or a shallow copy without the
// preconditions a static mount ignores when r carries any.
func withoutPreconditions(r *http.Request) *http.Request {
	if !slices.ContainsFunc(preconditions[:], func(h string) bool { _, ok := r.Header[h]; return ok }) {
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
