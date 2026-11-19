package engine

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
)

// StaticConfig configures a static mount, the files of an fs.FS served
// under a URL prefix; a zero field means its default, so the zero value
// is the safe default configuration. Consumers use it as
// srvr.StaticConfig with srvr.WithStaticConfig, and set single fields with
// the srvr.WithStatic* and srvr.WithSPAFallback options.
type StaticConfig struct {
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
}

// StaticOption sets fields of a [StaticConfig]; consumers use it as
// srvr.StaticOption. Options apply in argument order.
type StaticOption func(*StaticConfig)

// Static is one static mount: a validated prefix, its file system and its
// configuration, copied when it was created. The engines build it with
// [NewStatic] and serve its [Static.Resolve] decisions with their own file
// primitives. It is safe for concurrent use.
type Static struct {
	prefix string
	fsys   fs.FS
	cfg    StaticConfig
}

// NewStatic returns the mount of fsys under prefix, configured by opts
// applied in order to a zero StaticConfig, or an error wrapping ErrConfig
// joining one *ConfigError per invalid setting: Static.Prefix,
// Static.FS, StaticOption (a nil option) and Static.IndexNames. It reads
// nothing from fsys.
//
// A valid prefix starts with "/", has no empty, "." or ".." element, no
// whitespace or control character, and none of { } * : % ? # \, which are
// pattern syntax in some engine; one trailing slash is dropped, so
// "/assets/" is "/assets".
func NewStatic(prefix string, fsys fs.FS, opts ...StaticOption) (*Static, error) {
	var errs []error

	cfg, err := applyStaticOptions(opts)
	if err != nil {
		errs = append(errs, err)
	}

	norm, err := staticPrefix(prefix)
	if err != nil {
		errs = append(errs, err)
	}

	if IsNil(fsys) {
		errs = append(errs, NewConfigError("Static.FS", fsys, "must not be nil"))
	}

	if err := validateIndexNames(cfg.IndexNames); err != nil {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	if len(cfg.IndexNames) == 0 {
		cfg.IndexNames = []string{defaultIndexName}
	}

	return &Static{prefix: norm, fsys: fsys, cfg: cfg}, nil
}

// Prefix returns the mount's normalised prefix: "/" or a path without a
// trailing slash, such as "/assets".
func (s *Static) Prefix() string { return s.prefix }

// applyStaticOptions applies opts in order to a zero config and returns
// it with its slices copied, so the caller changing a slice it passed
// changes nothing; a nil option is an error naming its index.
func applyStaticOptions(opts []StaticOption) (StaticConfig, error) {
	var (
		cfg  StaticConfig
		errs []error
	)

	for i, opt := range opts {
		if opt == nil {
			errs = append(errs, NewConfigError("StaticOption", i, "is nil"))

			continue
		}

		opt(&cfg)
	}

	cfg.IndexNames = slices.Clone(cfg.IndexNames)

	return cfg, errors.Join(errs...)
}

// staticPrefix returns prefix normalised, or an error naming
// Static.Prefix.
func staticPrefix(prefix string) (string, error) {
	invalid := func(reason string) (string, error) {
		return "", NewConfigError("Static.Prefix", prefix, reason)
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

// validateIndexNames returns an error naming Static.IndexNames for the
// first name that is not one valid path element.
func validateIndexNames(names []string) error {
	for _, n := range names {
		if !isFileName(n) {
			return NewConfigError("Static.IndexNames", n, "must be a single file name, without / and not . or ..")
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

// StaticAnswer is how a static mount answers a request.
type StaticAnswer int

const (
	// StaticFile serves the open file of the result.
	StaticFile StaticAnswer = iota + 1
	// StaticNotFound answers 404: no file, a hidden file or a path that
	// cannot name a file.
	StaticNotFound
	// StaticMethodNotAllowed answers 405 with Allow: GET, HEAD.
	StaticMethodNotAllowed
	// StaticFailed answers 500: the file system failed; Err says how.
	StaticFailed
)

// StaticFileContent is an open file a static mount serves: readable and
// seekable, as http.ServeContent needs.
type StaticFileContent interface {
	fs.File
	io.Seeker
}

// StaticResult is a static mount's decision for one request. For
// StaticFile, File is open and the caller closes it on every path.
type StaticResult struct {
	Answer StaticAnswer
	// File is the open file to serve, nil unless Answer is StaticFile.
	File StaticFileContent
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
	// Err is the file system's failure when Answer is StaticFailed.
	Err error
}

// Resolve decides how the mount answers method for rest, the request path
// below the prefix as the engine decoded it, without a leading slash (""
// for the prefix itself; a trailing slash names a directory). In order:
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
//  5. Any other file system error, and a file that cannot seek:
//     StaticFailed.
//
// It never redirects. A panic of the file system reaches the caller, whose
// request recovery answers it; no file is left open.
func (s *Static) Resolve(method, rest string) StaticResult {
	if method != http.MethodGet && method != http.MethodHead {
		return StaticResult{Answer: StaticMethodNotAllowed}
	}

	name, dirOnly, ok := requestName(rest)
	if !ok {
		return StaticResult{Answer: StaticNotFound}
	}

	found, info, err := s.lookup(name, dirOnly)
	if err != nil {
		return staticFailure(err)
	}

	if found == "" {
		return StaticResult{Answer: StaticNotFound}
	}

	return s.open(found, info)
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
func (s *Static) lookup(name string, dirOnly bool) (string, fs.FileInfo, error) {
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
func (s *Static) index(dir string) (string, fs.FileInfo, error) {
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
func (s *Static) isIndex(name string) bool {
	return slices.Contains(s.cfg.IndexNames, path.Base(name))
}

// open opens the chosen file and returns the decision serving it.
func (s *Static) open(name string, info fs.FileInfo) StaticResult {
	f, err := s.fsys.Open(name)
	if err != nil {
		if err = missOrError(err); err != nil {
			return staticFailure(err)
		}

		return StaticResult{Answer: StaticNotFound}
	}

	content, ok := f.(StaticFileContent)
	if !ok {
		_ = f.Close()

		return staticFailure(fmt.Errorf("static file %q: %T cannot seek, so it cannot be served", name, f))
	}

	res := StaticResult{
		Answer: StaticFile, File: content, Name: name, ModTime: info.ModTime(),
		ContentType: mime.TypeByExtension(path.Ext(name)), CacheControl: s.cfg.CacheControl,
	}

	if s.isIndex(name) {
		res.CacheControl = cmp.Or(s.cfg.IndexCacheControl, defaultIndexCacheControl)
	}

	return res
}

// missOrError returns nil when err, a file system error, means the name
// does not exist or cannot exist, and err otherwise: fs.ErrNotExist,
// fs.ErrInvalid, and the operating system's "not a directory" (a name
// below a file, such as index.html/x) and "name too long", which os.DirFS
// returns without matching fs.ErrNotExist. An error whose chain cannot be
// walked is never a miss.
func missOrError(err error) error {
	if hasCycle(err) {
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
func staticFailure(err error) StaticResult {
	return StaticResult{Answer: StaticFailed, Err: err}
}
