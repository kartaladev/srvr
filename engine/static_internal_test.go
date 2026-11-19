package engine

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"
)

// staticSite is the test file tree of the static-files design ("Test
// fixture"), as an fstest.MapFS.
func staticSite() fstest.MapFS {
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

	return fstest.MapFS{
		"index.html":         file("<html>app</html>"),
		"app.js":             file("console.log(1)"),
		"style.css":          file("body{}"),
		"logo.svg":           file("<svg/>"),
		"data.json":          file("{}"),
		"notes":              file("plain notes"),
		"café.txt":           file("café"),
		"a/b.txt":            file("b"),
		"docs/index.html":    file("<html>docs</html>"),
		"docs/guide.html":    file("<html>guide</html>"),
		"empty/x":            &fstest.MapFile{Mode: fs.ModeDir},
		"assets/app-3f2a.js": file("hashed"),
		".env":               file("SECRET=1"),
		".git/config":        file("[core]"),
		"assets/.hidden.js":  file("hidden"),
	}
}

// configFields returns the Field of every *ConfigError joined in err.
func configFields(err error) []string {
	if ce, ok := err.(*ConfigError); ok { //nolint:errorlint // walking the joined tree on purpose
		return []string{ce.Field}
	}

	var fields []string

	switch u := err.(type) { //nolint:errorlint // walking the joined tree on purpose
	case interface{ Unwrap() []error }:
		for _, m := range u.Unwrap() {
			fields = append(fields, configFields(m)...)
		}
	case interface{ Unwrap() error }:
		fields = configFields(u.Unwrap())
	}

	return fields
}

func TestNewStatic_Config(t *testing.T) {
	t.Parallel()

	var typedNil *embed.FS

	indexNames := func(names ...string) StaticOption {
		return func(c *StaticConfig) { c.IndexNames = names }
	}

	tests := []struct {
		name   string
		prefix string
		fsys   fs.FS
		opts   []StaticOption
		fields []string
	}{
		{"nil FS", "/", nil, nil, []string{"Static.FS"}},
		{"typed-nil *embed.FS", "/", typedNil, nil, []string{"Static.FS"}},
		{"empty prefix", "", staticSite(), nil, []string{"Static.Prefix"}},
		{"relative prefix", "assets", staticSite(), nil, []string{"Static.Prefix"}},
		{"wildcard prefix", "/assets/{name}", staticSite(), nil, []string{"Static.Prefix"}},
		{"star prefix", "/assets/*", staticSite(), nil, []string{"Static.Prefix"}},
		{"parameter prefix", "/assets/:id", staticSite(), nil, []string{"Static.Prefix"}},
		{"dot-dot prefix", "/a/../b", staticSite(), nil, []string{"Static.Prefix"}},
		{"empty element prefix", "/a//b", staticSite(), nil, []string{"Static.Prefix"}},
		{"NUL in prefix", "/a\x00", staticSite(), nil, []string{"Static.Prefix"}},
		{"invalid UTF-8 prefix", "/\xff", staticSite(), nil, []string{"Static.Prefix"}},
		{"space in prefix", "/a b", staticSite(), nil, []string{"Static.Prefix"}},
		{"nil option", "/", staticSite(), []StaticOption{nil}, []string{"StaticOption"}},
		{"empty index name", "/", staticSite(), []StaticOption{indexNames("")}, []string{"Static.IndexNames"}},
		{"index name with a slash", "/", staticSite(), []StaticOption{indexNames("a/index.html")}, []string{"Static.IndexNames"}},
		{"dot-dot index name", "/", staticSite(), []StaticOption{indexNames("..")}, []string{"Static.IndexNames"}},
		{"several invalid settings", "assets", nil, nil, []string{"Static.Prefix", "Static.FS"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, err := NewStatic(tt.prefix, tt.fsys, tt.opts...)
			if err == nil {
				t.Fatalf("NewStatic(%q) = %v, nil; want an error", tt.prefix, m)
			}

			if !errors.Is(err, ErrConfig) {
				t.Errorf("error %v does not wrap ErrConfig", err)
			}

			if got := configFields(err); !slices.Equal(got, tt.fields) {
				t.Errorf("fields = %v, want %v (error %v)", got, tt.fields, err)
			}
		})
	}
}

func TestNewStatic_Valid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		prefix, want string
	}{
		{"/", "/"},
		{"/assets", "/assets"},
		{"/assets/", "/assets"},
		{"/a/b", "/a/b"},
		{"/café", "/café"},
	}

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			t.Parallel()

			m, err := NewStatic(tt.prefix, staticSite())
			if err != nil {
				t.Fatalf("NewStatic(%q) = %v", tt.prefix, err)
			}

			if m.Prefix() != tt.want {
				t.Errorf("Prefix() = %q, want %q", m.Prefix(), tt.want)
			}
		})
	}
}

// faultFS is a file system whose Open and Stat may be replaced per name;
// other names go to base.
type faultFS struct {
	base fs.FS
	open map[string]func() (fs.File, error)
	stat map[string]func() (fs.FileInfo, error)
}

func (f faultFS) Open(name string) (fs.File, error) {
	if open, ok := f.open[name]; ok {
		return open()
	}

	return f.base.Open(name)
}

func (f faultFS) Stat(name string) (fs.FileInfo, error) {
	if stat, ok := f.stat[name]; ok {
		return stat()
	}

	return fs.Stat(f.base, name)
}

// noSeekFile is a file that does not implement io.Seeker.
type noSeekFile struct{ f fs.File }

func (n noSeekFile) Stat() (fs.FileInfo, error) { return n.f.Stat() }
func (n noSeekFile) Read(p []byte) (int, error) { return n.f.Read(p) }
func (n noSeekFile) Close() error               { return n.f.Close() }

// MyError is a consumer error type whose nil pointer is returned as an
// error.
type MyError struct{ msg string }

func (e *MyError) Error() string { return e.msg }

// staticDecision is the part of a StaticResult a test compares.
type staticDecision struct {
	answer       StaticAnswer
	name         string
	contentType  string
	cacheControl string
}

func decisionOf(r StaticResult) staticDecision {
	return staticDecision{r.Answer, r.Name, r.ContentType, r.CacheControl}
}

// TestStatic_Resolve covers the static-files decisions (Serving a file,
// Not found, Hostile paths, Faulty file system, Other methods) without
// HTTP: rest is the path below the prefix as the engine decoded it.
func TestStatic_Resolve(t *testing.T) {
	t.Parallel()

	site := staticSite()
	appJS := func() (fs.File, error) { return site.Open("app.js") }

	var typedNil *MyError

	served := func(name, ctype, cache string) staticDecision {
		return staticDecision{answer: StaticFile, name: name, contentType: ctype, cacheControl: cache}
	}
	notFound := staticDecision{answer: StaticNotFound}
	notAllowed := staticDecision{answer: StaticMethodNotAllowed}
	failed := staticDecision{answer: StaticFailed}

	const (
		js   = "text/javascript; charset=utf-8"
		html = "text/html; charset=utf-8"
		text = "text/plain; charset=utf-8"
	)

	tests := []struct {
		name   string
		fsys   fs.FS
		opts   []StaticOption
		method string
		rest   string
		want   staticDecision
		// errIs is a target the failed decision's error matches.
		errIs error
		// errText is text the failed decision's error contains.
		errText string
	}{
		// Serving a file.
		{name: "GET app.js", method: "GET", rest: "app.js", want: served("app.js", js, "")},
		{name: "HEAD app.js", method: "HEAD", rest: "app.js", want: served("app.js", js, "")},
		{name: "style.css", method: "GET", rest: "style.css", want: served("style.css", "text/css; charset=utf-8", "")},
		{name: "logo.svg", method: "GET", rest: "logo.svg", want: served("logo.svg", "image/svg+xml", "")},
		{name: "data.json", method: "GET", rest: "data.json", want: served("data.json", "application/json", "")},
		{name: "no extension is sniffed by the engine", method: "GET", rest: "notes", want: served("notes", "", "")},
		{name: "decoded UTF-8 name", method: "GET", rest: "café.txt", want: served("café.txt", text, "")},
		{name: "decoded slash", method: "GET", rest: "a/b.txt", want: served("a/b.txt", text, "")},
		{name: "the prefix itself", method: "GET", rest: "", want: served("index.html", html, "no-cache")},
		{name: "index file named", method: "GET", rest: "index.html", want: served("index.html", html, "no-cache")},
		{name: "directory without slash", method: "GET", rest: "docs", want: served("docs/index.html", html, "no-cache")},
		{name: "directory with slash", method: "GET", rest: "docs/", want: served("docs/index.html", html, "no-cache")},
		{name: "file in a directory", method: "GET", rest: "docs/guide.html", want: served("docs/guide.html", html, "")},
		// Not found.
		{name: "missing", method: "GET", rest: "missing.js", want: notFound},
		{name: "HEAD missing", method: "HEAD", rest: "missing.js", want: notFound},
		{name: "directory without index", method: "GET", rest: "empty/", want: notFound},
		{name: "nested directory without index", method: "GET", rest: "empty/x/", want: notFound},
		{name: "dotfile", method: "GET", rest: ".env", want: notFound},
		{name: "dot-directory file", method: "GET", rest: ".git/config", want: notFound},
		{name: "hidden file in a directory", method: "GET", rest: "assets/.hidden.js", want: notFound},
		{name: "dot-directory", method: "GET", rest: ".git/", want: notFound},
		{name: "file named as a directory", method: "GET", rest: "app.js/", want: notFound},
		{name: "below a file", method: "GET", rest: "index.html/x", want: notFound},
		{
			name: "index name missing", method: "GET", rest: "",
			opts: []StaticOption{func(c *StaticConfig) { c.IndexNames = []string{"home.html"} }}, want: notFound,
		},
		{name: "4096-character name", method: "GET", rest: strings.Repeat("a", 4096) + ".js", want: notFound},
		// Hostile paths, as decoded.
		{name: "dot-dot", method: "GET", rest: "../secret.txt", want: notFound},
		{name: "backslash", method: "GET", rest: `..\secret.txt`, want: notFound},
		{name: "NUL", method: "GET", rest: "a\x00.txt", want: notFound},
		{name: "line feed", method: "GET", rest: "a\nb", want: notFound},
		{name: "invalid UTF-8", method: "GET", rest: "\xff.txt", want: notFound},
		{name: "double-encoded dot-dot is a literal name", method: "GET", rest: "%2e%2e/secret.txt", want: notFound},
		{name: "empty first element", method: "GET", rest: "/app.js", want: notFound},
		{name: "empty inner element", method: "GET", rest: "a//b.txt", want: notFound},
		{name: "dot element", method: "GET", rest: "./app.js", want: notFound},
		// Other methods.
		{name: "POST", method: "POST", rest: "app.js", want: notAllowed},
		{name: "DELETE missing", method: "DELETE", rest: "nope", want: notAllowed},
		{name: "OPTIONS", method: "OPTIONS", rest: "", want: notAllowed},
		// Faulty file system.
		{
			name: "permission denied from Open", method: "GET", rest: "app.js", want: failed, errIs: fs.ErrPermission,
			fsys: faultFS{base: site, open: map[string]func() (fs.File, error){
				"app.js": func() (fs.File, error) { return nil, &fs.PathError{Op: "open", Path: "app.js", Err: fs.ErrPermission} },
			}},
		},
		{
			name: "error from Stat", method: "GET", rest: "app.js", want: failed, errText: "disk on fire",
			fsys: faultFS{base: site, stat: map[string]func() (fs.FileInfo, error){
				"app.js": func() (fs.FileInfo, error) { return nil, errors.New("disk on fire") },
			}},
		},
		{
			name: "typed-nil error from Open", method: "GET", rest: "app.js", want: failed, errIs: typedNil,
			fsys: faultFS{base: site, open: map[string]func() (fs.File, error){
				"app.js": func() (fs.File, error) { return nil, typedNil },
			}},
		},
		{
			name: "cyclic error from Open", method: "GET", rest: "app.js", want: failed, errText: "self",
			fsys: faultFS{base: site, open: map[string]func() (fs.File, error){
				"app.js": func() (fs.File, error) { return nil, selfError{} },
			}},
		},
		{
			name: "file without Seek", method: "GET", rest: "app.js", want: failed, errText: "engine.noSeekFile cannot seek",
			fsys: faultFS{base: site, open: map[string]func() (fs.File, error){
				"app.js": func() (fs.File, error) { f, err := appJS(); return noSeekFile{f}, err },
			}},
		},
		{
			name: "wrapped not-exist", method: "GET", rest: "app.js", want: notFound,
			fsys: faultFS{base: site, stat: map[string]func() (fs.FileInfo, error){
				"app.js": func() (fs.FileInfo, error) { return nil, fmt.Errorf("lookup: %w", fs.ErrNotExist) },
			}},
		},
		{
			name: "not a directory", method: "GET", rest: "app.js", want: notFound,
			fsys: faultFS{base: site, stat: map[string]func() (fs.FileInfo, error){
				"app.js": func() (fs.FileInfo, error) {
					return nil, &fs.PathError{Op: "stat", Path: "app.js", Err: syscall.ENOTDIR}
				},
			}},
		},
		{
			name: "name too long", method: "GET", rest: "app.js", want: notFound,
			fsys: faultFS{base: site, stat: map[string]func() (fs.FileInfo, error){
				"app.js": func() (fs.FileInfo, error) {
					return nil, &fs.PathError{Op: "stat", Path: "app.js", Err: syscall.ENAMETOOLONG}
				},
			}},
		},
		{
			name: "removed between Stat and Open", method: "GET", rest: "app.js", want: notFound,
			fsys: faultFS{base: site, open: map[string]func() (fs.File, error){
				"app.js": func() (fs.File, error) { return nil, fs.ErrNotExist },
			}},
		},
		{
			name: "error from an index Stat", method: "GET", rest: "", want: failed, errIs: fs.ErrPermission,
			fsys: faultFS{base: site, stat: map[string]func() (fs.FileInfo, error){
				"index.html": func() (fs.FileInfo, error) { return nil, fs.ErrPermission },
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := tt.fsys
			if fsys == nil {
				fsys = site
			}

			m, err := NewStatic("/assets", fsys, tt.opts...)
			if err != nil {
				t.Fatalf("NewStatic: %v", err)
			}

			res := m.Resolve(tt.method, tt.rest)
			if res.File != nil {
				defer res.File.Close()
			}

			if got := decisionOf(res); got != tt.want {
				t.Errorf("Resolve(%q, %q) = %+v, want %+v (err %v)", tt.method, tt.rest, got, tt.want, res.Err)
			}

			switch {
			case res.Answer == StaticFile && res.File == nil:
				t.Errorf("served without an open file: %+v", res)
			case res.Answer != StaticFile && res.File != nil:
				t.Errorf("answer %v keeps an open file", res.Answer)
			case res.Answer == StaticFailed && res.Err == nil:
				t.Error("failed without an error")
			case res.Answer != StaticFailed && res.Err != nil:
				t.Errorf("answer %v with error %v", res.Answer, res.Err)
			}

			if tt.errIs != nil && !errors.Is(res.Err, tt.errIs) {
				t.Errorf("error %v does not match %v", res.Err, tt.errIs)
			}

			if tt.errText != "" && (res.Err == nil || !strings.Contains(res.Err.Error(), tt.errText)) {
				t.Errorf("error %v does not contain %q", res.Err, tt.errText)
			}
		})
	}
}

// TestStatic_ResolvePanics checks that a file system's panic reaches the
// caller, whose request recovery answers it, with nothing left open.
func TestStatic_ResolvePanics(t *testing.T) {
	t.Parallel()

	fsys := faultFS{base: staticSite(), open: map[string]func() (fs.File, error){
		"app.js": func() (fs.File, error) { panic("boom") },
	}}

	m, err := NewStatic("/", fsys)
	if err != nil {
		t.Fatalf("NewStatic: %v", err)
	}

	defer func() {
		if v := recover(); v != "boom" {
			t.Errorf("recovered %v, want boom", v)
		}
	}()

	m.Resolve("GET", "app.js")
	t.Error("Resolve returned")
}

// FuzzStaticResolve checks that whatever the request path, a served name
// is a valid fs.FS path with no hidden element, never reached through a
// ".." element.
func FuzzStaticResolve(f *testing.F) {
	for _, seed := range []string{"", "app.js", "docs/", "../secret", "a/../app.js", ".env", "a\x00", "\xff", `a\b`, "docs//guide.html"} {
		f.Add(seed)
	}

	site := staticSite()
	site["secret"] = &fstest.MapFile{Data: []byte("SECRET")}

	m, err := NewStatic("/", site)
	if err != nil {
		f.Fatalf("NewStatic: %v", err)
	}

	f.Fuzz(func(t *testing.T, rest string) {
		res := m.Resolve("GET", rest)
		if res.File != nil {
			defer res.File.Close()
		}

		if res.Answer != StaticFile {
			return
		}

		if !fs.ValidPath(res.Name) {
			t.Errorf("served invalid name %q for %q", res.Name, rest)
		}

		for elem := range strings.SplitSeq(res.Name, "/") {
			if strings.HasPrefix(elem, ".") {
				t.Errorf("served hidden name %q for %q", res.Name, rest)
			}
		}

		if slices.Contains(strings.Split(rest, "/"), "..") {
			t.Errorf("served %q for %q, which has a .. element", res.Name, rest)
		}
	})
}
