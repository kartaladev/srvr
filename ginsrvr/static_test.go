package ginsrvr_test

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"github.com/kartaladev/srvr/ginsrvr"
)

// siteFiles is the test file tree of the static-files design ("Test
// fixture"): name to content, "" for a directory. It copies the root
// module's fixture: a separate module cannot import its test files.
var siteFiles = map[string]string{
	"index.html":         "<html>app</html>",
	"app.js":             "console.log(1)",
	"style.css":          "body{}",
	"logo.svg":           "<svg/>",
	"data.json":          "{}",
	"notes":              "plain notes, no extension",
	"café.txt":           "café",
	"a/b.txt":            "b",
	"docs/index.html":    "<html>docs</html>",
	"docs/guide.html":    "<html>guide</html>",
	"empty/x":            "",
	"assets/app-3f2a.js": "hashed",
	".env":               "SECRET=1",
	".git/config":        "[core]",
	"assets/.hidden.js":  "hidden",
}

// siteModTime is the modification time of every file of a directory site.
var siteModTime = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

// mapSite returns the test site as an fstest.MapFS, whose files have no
// modification time.
func mapSite() fstest.MapFS {
	m := fstest.MapFS{}
	for name, data := range siteFiles {
		if data == "" {
			m[name] = &fstest.MapFile{Mode: fs.ModeDir | 0o755}

			continue
		}

		m[name] = &fstest.MapFile{Data: []byte(data), Mode: 0o644}
	}

	return m
}

// dirSite writes the test site to a new directory, with every file
// modified at siteModTime and secret.txt ("SECRET") next to it, outside
// it, and returns the site's directory.
func dirSite(t *testing.T) string {
	t.Helper()

	parent := t.TempDir()
	dir := filepath.Join(parent, "site")

	for name, data := range siteFiles {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if data == "" {
			mustNil(t, os.MkdirAll(p, 0o755))

			continue
		}

		mustNil(t, os.MkdirAll(filepath.Dir(p), 0o755))
		mustNil(t, os.WriteFile(p, []byte(data), 0o644))
		mustNil(t, os.Chtimes(p, siteModTime, siteModTime))
	}

	mustNil(t, os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("SECRET"), 0o644))

	return dir
}

//go:embed testdata/static
var embedded embed.FS

// embedSite returns the embedded site (app.js and index.html), whose files
// have no modification time.
func embedSite(t *testing.T) fs.FS {
	t.Helper()

	sub, err := fs.Sub(embedded, "testdata/static")
	mustNil(t, err)

	return sub
}

// configFields returns the Field of every *srvr.ConfigError in err's
// tree, in order.
func configFields(err error) []string {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs := joined.Unwrap()
		fields := make([]string, 0, len(errs))
		for _, e := range errs {
			fields = append(fields, configFields(e)...)
		}

		return fields
	}

	if ce, ok := err.(*srvr.ConfigError); ok { //nolint:errorlint // walking the tree on purpose
		return []string{ce.Field}
	}

	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return configFields(wrapped.Unwrap())
	}

	return nil
}

// faultFS is a file system whose Open and Stat may be replaced per name;
// other names go to base.
type faultFS struct {
	base fs.FS
	open map[string]func() (fs.File, error)
	stat map[string]func() (fs.FileInfo, error)
	// noStat hides Stat, so fs.Stat goes through Open.
	noStat bool
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

	if f.noStat {
		file, err := f.Open(name)
		if err != nil {
			return nil, err
		}
		defer file.Close()

		return file.Stat()
	}

	return fs.Stat(f.base, name)
}

// failingIndex returns site whose index.html is opened by open, with Stat
// going through Open.
func failingIndex(site fs.FS, open func() (fs.File, error)) fs.FS {
	return faultFS{base: site, open: map[string]func() (fs.File, error){"index.html": open}, noStat: true}
}

// statics adapts static configurers to the registrations of a gin server.
type statics = []srvr.RouteConfigurer[*gin.RouterGroup]

// TestStatic_ConfigErrors covers static-files / Scenario: Rejected static
// configuration, Scenario: Several invalid settings and Scenario: Rejected
// file cache configuration on gin, which has no file cache: a non-zero
// StaticConfig.FiberSendFileCache fails Start instead of being ignored.
func TestStatic_ConfigErrors(t *testing.T) {
	t.Parallel()

	var typedNil *embed.FS

	site := mapSite()

	tests := []struct {
		name        string
		configurers statics
		fields      []string
		value       string // the first *ConfigError's Value; "" for any
	}{
		{"nil FS", statics{ginsrvr.Static("/", nil)}, []string{"Static.FS"}, ""},
		{"typed-nil *embed.FS", statics{ginsrvr.Static("/", typedNil)}, []string{"Static.FS"}, ""},
		{"empty prefix", statics{ginsrvr.Static("", site)}, []string{"Static.Prefix"}, ""},
		{"relative prefix", statics{ginsrvr.Static("assets", site)}, []string{"Static.Prefix"}, ""},
		{"wildcard prefix", statics{ginsrvr.Static("/assets/{name}", site)}, []string{"Static.Prefix"}, ""},
		{"star prefix", statics{ginsrvr.Static("/assets/*", site)}, []string{"Static.Prefix"}, ""},
		{"parameter prefix", statics{ginsrvr.Static("/assets/:id", site)}, []string{"Static.Prefix"}, ""},
		{"dot-dot prefix", statics{ginsrvr.Static("/a/../b", site)}, []string{"Static.Prefix"}, ""},
		{"empty element prefix", statics{ginsrvr.Static("/a//b", site)}, []string{"Static.Prefix"}, ""},
		{"NUL in prefix", statics{ginsrvr.Static("/a\x00", site)}, []string{"Static.Prefix"}, ""},
		{"nil option", statics{ginsrvr.Static("/", site, nil)}, []string{"StaticOption"}, ""},
		{"missing fallback", statics{ginsrvr.Static("/", site, srvr.WithSPAFallback("missing.html"))}, []string{"Static.SPAFallback"}, ""},
		{"directory fallback", statics{ginsrvr.Static("/", site, srvr.WithSPAFallback("docs"))}, []string{"Static.SPAFallback"}, ""},
		{
			"fallback leaving the file system",
			statics{ginsrvr.Static("/", site, srvr.WithSPAFallback("../index.html"))},
			[]string{"Static.SPAFallback"},
			"",
		},
		{"absolute fallback", statics{ginsrvr.Static("/", site, srvr.WithSPAFallback("/index.html"))}, []string{"Static.SPAFallback"}, ""},
		{
			"header injection in CacheControl",
			statics{ginsrvr.Static("/", site, srvr.WithStaticCacheControl("max-age=60\r\nSet-Cookie: a=b"))},
			[]string{"Static.CacheControl"},
			"",
		},
		{
			"NUL in IndexCacheControl",
			statics{ginsrvr.Static("/", site, srvr.WithStaticIndexCacheControl("no-cache\x00"))},
			[]string{"Static.IndexCacheControl"},
			"",
		},
		{"empty index name", statics{ginsrvr.Static("/", site, srvr.WithStaticIndexNames(""))}, []string{"Static.IndexNames"}, ""},
		{
			"index name with a slash",
			statics{ginsrvr.Static("/", site, srvr.WithStaticIndexNames("a/index.html"))},
			[]string{"Static.IndexNames"},
			"",
		},
		{"dot-dot index name", statics{ginsrvr.Static("/", site, srvr.WithStaticIndexNames(".."))}, []string{"Static.IndexNames"}, ""},
		{"fallback Open panics", statics{
			ginsrvr.Static("/", failingIndex(site, func() (fs.File, error) { panic("boom") }), srvr.WithSPAFallback("index.html")),
		}, []string{"Static.SPAFallback"}, ""},
		{"fallback Open permission denied", statics{
			ginsrvr.Static("/", failingIndex(site, func() (fs.File, error) { return nil, fs.ErrPermission }), srvr.WithSPAFallback("index.html")),
		}, []string{"Static.SPAFallback"}, ""},
		{"same prefix twice", statics{ginsrvr.Static("/assets", site), ginsrvr.Static("/assets", site)}, []string{"Static.Prefix"}, ""},
		{
			"same prefix twice, one with a trailing slash",
			statics{ginsrvr.Static("/assets", site), ginsrvr.Static("/assets/", site)},
			[]string{"Static.Prefix"},
			"",
		},
		{"root twice with other file systems", statics{ginsrvr.Static("/", site), ginsrvr.Static("/", fstest.MapFS{})}, []string{"Static.Prefix"}, ""},
		{"several invalid settings", statics{ginsrvr.Static("assets", nil, srvr.WithStaticCacheControl("a\nb"))}, []string{
			"Static.Prefix", "Static.FS", "Static.CacheControl",
		}, ""},
		{
			"FiberSendFileCache 1h0m0s",
			statics{ginsrvr.Static("/", site, srvr.WithStaticConfig(srvr.StaticConfig{FiberSendFileCache: time.Hour}))},
			[]string{"Static.FiberSendFileCache"},
			"1h0m0s",
		},
		{
			"FiberSendFileCache -1s",
			statics{ginsrvr.Static("/", site, srvr.WithStaticConfig(srvr.StaticConfig{FiberSendFileCache: -time.Second}))},
			[]string{"Static.FiberSendFileCache"},
			"-1s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t)
			for _, c := range tt.configurers {
				mustNil(t, srv.Register(c))
			}

			err := startErr(t, srv)
			if !errors.Is(err, srvr.ErrRouteConfig) || !errors.Is(err, srvr.ErrConfig) {
				t.Fatalf("Start = %v, want an error wrapping ErrRouteConfig and ErrConfig", err)
			}

			if ce, ok := errors.AsType[*srvr.ConfigError](err); !ok || (tt.value != "" && ce.Value != tt.value) {
				t.Errorf("Start = %v, want a *ConfigError with Value %q", err, tt.value)
			}

			if got := configFields(err); !slices.Equal(got, tt.fields) {
				t.Errorf("fields = %v, want %v (Start = %v)", got, tt.fields, err)
			}

			if !strings.Contains(err.Error(), "*ginsrvr.staticConfigurer") {
				t.Errorf("Start = %v, want it to name the static configurer", err)
			}

			select {
			case <-srv.Ready():
				t.Error("Ready was closed")
			default:
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			if err := srv.Shutdown(ctx); err != nil {
				t.Errorf("Shutdown = %v, want nil", err)
			}
		})
	}
}

// selfStatic is a consumer configurer that calls a static configurer's
// Configure itself instead of registering it.
type selfStatic struct{ fsys fs.FS }

func (s selfStatic) Configure(g *gin.RouterGroup) { ginsrvr.Static("/", s.fsys).Configure(g) }

// TestStatic_Lifecycle covers static-files / Scenario: Static configurer
// lifecycle misuse on gin.
func TestStatic_Lifecycle(t *testing.T) {
	t.Parallel()

	t.Run("Register after Shutdown", func(t *testing.T) {
		t.Parallel()

		srv, _ := newServer(t)
		start(t, srv)
		mustNil(t, srv.Shutdown(t.Context()))

		if err := srv.Register(ginsrvr.Static("/", mapSite())); !errors.Is(err, srvr.ErrServerClosed) {
			t.Errorf("Register = %v, want ErrServerClosed", err)
		}
	})

	t.Run("Configure called by a consumer configurer", func(t *testing.T) {
		t.Parallel()

		srv, _ := newServer(t)
		mustNil(t, srv.Register(selfStatic{mapSite()}))

		err := startErr(t, srv)
		if !errors.Is(err, srvr.ErrRouteConfig) {
			t.Fatalf("Start = %v, want an error wrapping ErrRouteConfig", err)
		}

		for _, text := range []string{"ginsrvr_test.selfStatic", "ginsrvr.Static", "Server.Register"} {
			if !strings.Contains(err.Error(), text) {
				t.Errorf("Start = %v, want it to name %s", err, text)
			}
		}
	})
}

// staticServer starts a gin server with the configurers registered in
// order and returns its base URL and log.
func staticServer(t *testing.T, configurers ...srvr.RouteConfigurer[*gin.RouterGroup]) (string, *logBuffer) {
	t.Helper()

	srv, logs := newServer(t)
	for _, c := range configurers {
		mustNil(t, srv.Register(c))
	}

	return start(t, srv), logs
}

// sites are the two builds of the test site most rows run on.
var sites = []struct {
	name  string
	build func(t *testing.T) fs.FS
}{
	{"MapFS", func(*testing.T) fs.FS { return mapSite() }},
	{"DirFS", func(t *testing.T) fs.FS { t.Helper(); return os.DirFS(dirSite(t)) }},
}

// TestStatic_Serve covers static-files / Scenario: Serving a file on gin.
func TestStatic_Serve(t *testing.T) {
	t.Parallel()

	const (
		js   = "text/javascript; charset=utf-8"
		html = "text/html; charset=utf-8"
		text = "text/plain; charset=utf-8"
	)

	tests := []struct {
		name, prefix, method, path, ctype string
		// file is the served file of siteFiles.
		file string
	}{
		{"file", "/assets", "GET", "/assets/app.js", js, "app.js"},
		{"HEAD", "/assets", "HEAD", "/assets/app.js", js, ""},
		{"css", "/assets", "GET", "/assets/style.css", "text/css; charset=utf-8", "style.css"},
		{"svg", "/assets", "GET", "/assets/logo.svg", "image/svg+xml", "logo.svg"},
		{"json", "/assets", "GET", "/assets/data.json", "application/json", "data.json"},
		{"sniffed", "/assets", "GET", "/assets/notes", text, "notes"},
		{"encoded UTF-8", "/assets", "GET", "/assets/caf%C3%A9.txt", text, "café.txt"},
		{"encoded slash", "/assets", "GET", "/assets/a%2Fb.txt", text, "a/b.txt"},
		{"prefix with slash", "/assets", "GET", "/assets/", html, "index.html"},
		{"prefix without slash", "/assets", "GET", "/assets", html, "index.html"},
		{"index file named", "/assets", "GET", "/assets/index.html", html, "index.html"},
		{"directory", "/assets", "GET", "/assets/docs", html, "docs/index.html"},
		{"directory with slash", "/assets", "GET", "/assets/docs/", html, "docs/index.html"},
		{"trailing-slash prefix", "/assets/", "GET", "/assets/app.js", js, "app.js"},
		{"root", "/", "GET", "/", html, "index.html"},
		{"root nested file", "/", "GET", "/docs/guide.html", html, "docs/guide.html"},
	}

	for _, site := range sites {
		fsys := site.build(t) // read only: built once for every row

		for _, tt := range tests {
			t.Run(site.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				base, _ := staticServer(t, ginsrvr.Static(tt.prefix, fsys))
				res := do(t, tt.method, base+tt.path, nil, nil)
				checkServed(t, res, tt.ctype, siteFiles[tt.file], site.name == "DirFS")

				if tt.method == http.MethodHead && res.header.Get("Content-Length") != "14" {
					t.Errorf("HEAD Content-Length = %q, want 14", res.header.Get("Content-Length"))
				}
			})
		}
	}

	t.Run("embed.FS", func(t *testing.T) {
		t.Parallel()

		base, _ := staticServer(t, ginsrvr.Static("/", embedSite(t)))
		checkServed(t, get(t, base+"/app.js"), js, "console.log(1)", false)
	})
}

// checkServed fails the test unless res is a 200 with ctype and body, no
// ETag or Location, and a Last-Modified header exactly when modified.
func checkServed(t *testing.T, res response, ctype, body string, modified bool) {
	t.Helper()

	if res.status != http.StatusOK || res.header.Get("Content-Type") != ctype || res.body != body {
		t.Fatalf("got %d %q %q, want 200 %q %q", res.status, res.header.Get("Content-Type"), res.body, ctype, body)
	}

	for _, h := range []string{"ETag", "Location"} {
		if v := res.header.Get(h); v != "" {
			t.Errorf("%s = %q, want none", h, v)
		}
	}

	if got := res.header.Get("Last-Modified"); (got != "") != modified {
		t.Errorf("Last-Modified = %q, want it set: %v", got, modified)
	}
}

// TestStatic_Conditional covers static-files / Scenario: Conditional and
// range requests on gin.
func TestStatic_Conditional(t *testing.T) {
	t.Parallel()

	const (
		modified = "Wed, 18 Nov 2026 10:00:00 GMT"
		whole    = "console.log(1)"
	)

	tests := []struct {
		name     string
		embedded bool
		header   map[string]string
		status   int
		body     string
		// lastModified is the Last-Modified header wanted, "" for none.
		lastModified string
	}{
		{"not modified", false, map[string]string{"If-Modified-Since": modified}, 304, "", modified},
		{"modified since", false, map[string]string{"If-Modified-Since": "Tue, 17 Nov 2026 10:00:00 GMT"}, 200, whole, modified},
		{"not a date", false, map[string]string{"If-Modified-Since": "not a date"}, 200, whole, modified},
		{"embedded file", true, map[string]string{"If-Modified-Since": modified}, 200, whole, ""},
		{"range", false, map[string]string{"Range": "bytes=0-3"}, 200, whole, modified},
		{"unsatisfiable range", false, map[string]string{"Range": "bytes=999-"}, 200, whole, modified},
		{"If-Match", false, map[string]string{"If-Match": `"x"`}, 200, whole, modified},
		{"If-Unmodified-Since", false, map[string]string{"If-Unmodified-Since": "Mon, 16 Nov 2026 10:00:00 GMT"}, 200, whole, modified},
		{"If-None-Match", false, map[string]string{"If-None-Match": "*"}, 200, whole, modified},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := embedSite(t)
			if !tt.embedded {
				fsys = os.DirFS(dirSite(t))
			}

			base, _ := staticServer(t, ginsrvr.Static("/assets", fsys))
			res := do(t, http.MethodGet, base+"/assets/app.js", nil, tt.header)

			if res.status != tt.status || res.body != tt.body {
				t.Errorf("status %d body %q, want %d %q", res.status, res.body, tt.status, tt.body)
			}

			if got := res.header.Get("Last-Modified"); got != tt.lastModified {
				t.Errorf("Last-Modified = %q, want %q", got, tt.lastModified)
			}

			if got := res.header.Get("Content-Range"); got != "" {
				t.Errorf("Content-Range = %q, want none", got)
			}
		})
	}
}

// TestStatic_ConfigSources covers static-files / Scenario: Configuration
// sources on gin.
func TestStatic_ConfigSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// home adds home.html to the site.
		home bool
		// opts returns the options and a change made after Static returned.
		opts  func() ([]srvr.StaticOption, func())
		body  string
		cache string
		// path is the request path, "/" when empty; the request accepts
		// text/html.
		path string
	}{
		{
			name: "no options", body: "console.log(1)", path: "/app.js",
			opts: func() ([]srvr.StaticOption, func()) { return nil, nil },
		},
		{
			name: "cache option", body: "console.log(1)", path: "/app.js", cache: "public, max-age=31536000, immutable",
			opts: func() ([]srvr.StaticOption, func()) {
				return []srvr.StaticOption{srvr.WithStaticCacheControl("public, max-age=31536000, immutable")}, nil
			},
		},
		{
			name: "cache from the config struct", body: "console.log(1)", path: "/app.js", cache: "public, max-age=60",
			opts: func() ([]srvr.StaticOption, func()) {
				return []srvr.StaticOption{srvr.WithStaticConfig(srvr.StaticConfig{CacheControl: "public, max-age=60"})}, nil
			},
		},
		{
			name: "a later option wins over the config struct", body: "console.log(1)", path: "/app.js", cache: "no-store",
			opts: func() ([]srvr.StaticOption, func()) {
				return []srvr.StaticOption{
					srvr.WithStaticConfig(srvr.StaticConfig{CacheControl: "public, max-age=60"}), srvr.WithStaticCacheControl("no-store"),
				}, nil
			},
		},
		{
			name: "the config struct replaces every field", body: "console.log(1)", path: "/app.js",
			opts: func() ([]srvr.StaticOption, func()) {
				return []srvr.StaticOption{srvr.WithStaticCacheControl("no-store"), srvr.WithStaticConfig(srvr.StaticConfig{})}, nil
			},
		},
		{
			name: "config decoded from JSON", body: "<html>app</html>", path: "/dashboard", cache: "no-store",
			opts: func() ([]srvr.StaticOption, func()) {
				var c srvr.StaticConfig
				if err := json.Unmarshal([]byte(`{"spa_fallback":"index.html","cache_control":"public, max-age=60",`+
					`"index_cache_control":"no-store","index_names":["index.html"]}`), &c); err != nil {
					panic(err)
				}

				return []srvr.StaticOption{srvr.WithStaticConfig(c)}, nil
			},
		},
		{
			name: "index names option", home: true, body: "<html>home</html>", cache: "no-cache",
			opts: func() ([]srvr.StaticOption, func()) {
				return []srvr.StaticOption{srvr.WithStaticIndexNames("home.html")}, nil
			},
		},
		{
			name: "index names from the config struct", home: true, body: "<html>home</html>", cache: "no-cache",
			opts: func() ([]srvr.StaticOption, func()) {
				return []srvr.StaticOption{srvr.WithStaticConfig(srvr.StaticConfig{IndexNames: []string{"home.html"}})}, nil
			},
		},
		{
			name: "first existing index name wins", body: "<html>app</html>", cache: "no-cache",
			opts: func() ([]srvr.StaticOption, func()) {
				return []srvr.StaticOption{srvr.WithStaticIndexNames("home.html", "index.html")}, nil
			},
		},
		{
			name: "config changed after Static returned", body: "<html>app</html>", cache: "no-cache",
			opts: func() ([]srvr.StaticOption, func()) {
				c := srvr.StaticConfig{IndexNames: []string{"index.html"}}

				return []srvr.StaticOption{srvr.WithStaticConfig(c)}, func() { c.IndexNames[0] = "x.html" }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			site := mapSite()
			if tt.home {
				site["home.html"] = &fstest.MapFile{Data: []byte("<html>home</html>")}
			}

			opts, after := tt.opts()
			conf := ginsrvr.Static("/", site, opts...)

			if after != nil {
				after()
			}

			base, _ := staticServer(t, conf)
			path := cmp.Or(tt.path, "/")
			res := do(t, http.MethodGet, base+path, nil, map[string]string{"Accept": "text/html"})

			if res.status != http.StatusOK || res.body != tt.body || res.header.Get("Cache-Control") != tt.cache {
				t.Errorf("GET %s = %d %q Cache-Control %q, want 200 %q %q", path, res.status, res.body,
					res.header.Get("Cache-Control"), tt.body, tt.cache)
			}
		})
	}
}

// answer returns a gin handler writing body with status 200.
func answer(body string) gin.HandlerFunc {
	return func(c *gin.Context) { c.String(http.StatusOK, body) }
}

// TestStatic_Fallback covers static-files / Scenario: Fallback decision
// and Scenario: Fallback under a prefix on gin.
func TestStatic_Fallback(t *testing.T) {
	t.Parallel()

	const browser = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

	tests := []struct {
		name, method, path, accept string
		// prefix is the mount's prefix, "/" when empty.
		prefix string
		// noFallback leaves the fallback unconfigured.
		noFallback bool
		status     int
		// body is the body wanted for 200, "" for none.
		body string
		// allow is the Allow header wanted for 405.
		allow string
		// cache and vary are the headers wanted on a 200.
		cache, vary string
	}{
		{name: "browser navigation", method: "GET", path: "/dashboard", accept: browser, status: 200, body: "<html>app</html>", cache: "no-cache", vary: "Accept"},
		{name: "deep link", method: "GET", path: "/users/42/edit", accept: "text/html", status: 200, body: "<html>app</html>", cache: "no-cache", vary: "Accept"},
		{name: "HEAD navigation", method: "HEAD", path: "/dashboard", accept: "text/html", status: 200, cache: "no-cache", vary: "Accept"},
		{name: "query and upper case", method: "GET", path: "/dashboard?tab=2", accept: "TEXT/HTML", status: 200, body: "<html>app</html>", cache: "no-cache", vary: "Accept"},
		{
			name: "low quality html", method: "GET", path: "/dashboard", accept: "application/json, text/html;q=0.1", status: 200,
			body: "<html>app</html>", cache: "no-cache", vary: "Accept",
		},
		{name: "directory without index", method: "GET", path: "/empty/", accept: "text/html", status: 200, body: "<html>app</html>", cache: "no-cache", vary: "Accept"},
		{name: "no Accept", method: "GET", path: "/dashboard", status: 404},
		{name: "fetch default", method: "GET", path: "/dashboard", accept: "*/*", status: 404},
		{name: "any text", method: "GET", path: "/dashboard", accept: "text/*", status: 404},
		{name: "JSON", method: "GET", path: "/dashboard", accept: "application/json", status: 404},
		{name: "html refused", method: "GET", path: "/dashboard", accept: "text/html;q=0", status: 404},
		{name: "missing asset", method: "GET", path: "/assets/app-9999.js", accept: "text/html", status: 404},
		{name: "dot rule", method: "GET", path: "/users/john.doe", accept: "text/html", status: 404},
		{name: "hidden file", method: "GET", path: "/.env", accept: "text/html", status: 404},
		{name: "hostile path", method: "GET", path: "/%2e%2e/secret", accept: "text/html", status: 404},
		{name: "POST navigation", method: "POST", path: "/dashboard", accept: "text/html", status: 405, allow: "GET, HEAD"},
		{name: "no fallback configured", method: "GET", path: "/dashboard", accept: "text/html", noFallback: true, status: 404},
		{
			name: "under a prefix", prefix: "/app", method: "GET", path: "/app/settings", accept: "text/html", status: 200,
			body: "<html>app</html>", cache: "no-cache", vary: "Accept",
		},
		{
			name: "the prefix itself is the index", prefix: "/app", method: "GET", path: "/app", accept: "text/html", status: 200,
			body: "<html>app</html>", cache: "no-cache",
		},
		{name: "outside the prefix", prefix: "/app", method: "GET", path: "/other", accept: "text/html", status: 404},
		{name: "outside the prefix under the API", prefix: "/app", method: "GET", path: "/api/nope", accept: "text/html", status: 404},
		{name: "the API route", prefix: "/app", method: "GET", path: "/api/users", accept: "text/html", status: 200, body: "users"},
		{name: "a prefix sharing the mount's leading characters", prefix: "/app", method: "GET", path: "/apple", accept: "text/html", status: 404},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var opts []srvr.StaticOption
			if !tt.noFallback {
				opts = append(opts, srvr.WithSPAFallback("index.html"))
			}

			api := routes(func(g *gin.RouterGroup) { g.GET("/api/users", answer("users")) })
			base, _ := staticServer(t, ginsrvr.Static(cmp.Or(tt.prefix, "/"), mapSite(), opts...), api)

			header := map[string]string{}
			if tt.accept != "" {
				header["Accept"] = tt.accept
			}

			res := do(t, tt.method, base+tt.path, nil, header)
			if res.status != tt.status || (tt.body != "" && res.body != tt.body) || res.header.Get("Allow") != tt.allow {
				t.Fatalf("%s %s = %d %q Allow %q, want %d %q %q", tt.method, tt.path, res.status, res.body,
					res.header.Get("Allow"), tt.status, tt.body, tt.allow)
			}

			if tt.status != http.StatusOK && res.problem == nil {
				t.Errorf("body %q, want a problem", res.body)
			}

			if tt.status == http.StatusOK && tt.body != "users" &&
				(res.header.Get("Cache-Control") != tt.cache || res.header.Get("Vary") != tt.vary) {
				t.Errorf("Cache-Control %q Vary %q, want %q %q", res.header.Get("Cache-Control"), res.header.Get("Vary"), tt.cache, tt.vary)
			}

			if tt.vary != "" && res.header.Get("Content-Type") != "text/html; charset=utf-8" {
				t.Errorf("fallback Content-Type = %q, want the fallback's", res.header.Get("Content-Type"))
			}
		})
	}
}

// TestStatic_FallbackAfterStart covers static-files / Scenario: Fallback
// decision, the "fallback after Start" rows, on gin.
func TestStatic_FallbackAfterStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// change changes the site after Start, or breaks the fallback's
		// Open.
		change func(t *testing.T, dir string, broken *atomic.Bool)
		status int
		body   string
		// failed wants an ERROR http.request.failed record.
		failed bool
	}{
		{"deleted", func(t *testing.T, dir string, _ *atomic.Bool) {
			t.Helper()
			mustNil(t, os.Remove(filepath.Join(dir, "index.html")))
		}, 404, "", false},
		{"replaced", func(t *testing.T, dir string, _ *atomic.Bool) {
			t.Helper()
			mustNil(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>v2</html>"), 0o644))
		}, 200, "<html>v2</html>", false},
		{"unreadable", func(_ *testing.T, _ string, broken *atomic.Bool) { broken.Store(true) }, 500, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := dirSite(t)
			site := os.DirFS(dir)

			var broken atomic.Bool

			fsys := faultFS{base: site, open: map[string]func() (fs.File, error){"index.html": func() (fs.File, error) {
				if broken.Load() {
					return nil, &fs.PathError{Op: "open", Path: "index.html", Err: fs.ErrPermission}
				}

				return site.Open("index.html")
			}}}

			base, logs := staticServer(t, ginsrvr.Static("/", fsys, srvr.WithSPAFallback("index.html")))
			tt.change(t, dir, &broken)

			res := do(t, http.MethodGet, base+"/dashboard", nil, map[string]string{"Accept": "text/html"})
			if res.status != tt.status || (tt.body != "" && res.body != tt.body) {
				t.Fatalf("GET /dashboard = %d %q, want %d %q", res.status, res.body, tt.status, tt.body)
			}

			if tt.failed {
				recordHas("level", "ERROR")(t, logs.waitRecord(t, "http.request.failed"))
			}
		})
	}
}

// TestStatic_CacheHeaders covers static-files / Scenario: Cache headers on
// gin.
func TestStatic_CacheHeaders(t *testing.T) {
	t.Parallel()

	const immutable = "public, max-age=31536000, immutable"

	tests := []struct {
		name   string
		opts   []srvr.StaticOption
		path   string
		header map[string]string
		// dir serves the site from a directory, whose files have a
		// modification time.
		dir    bool
		status int
		cache  string
	}{
		{name: "root", path: "/", status: 200, cache: "no-cache"},
		{name: "index file named", path: "/index.html", status: 200, cache: "no-cache"},
		{name: "directory index", path: "/docs/", status: 200, cache: "no-cache"},
		{name: "fallback", path: "/dashboard", status: 200, cache: "no-cache"},
		{name: "other file", path: "/app.js", status: 200, cache: ""},
		{name: "miss", path: "/missing.js", status: 404, cache: "no-store"},
		{
			name: "hashed asset", opts: []srvr.StaticOption{srvr.WithStaticCacheControl(immutable)}, path: "/assets/app-3f2a.js",
			status: 200, cache: immutable,
		},
		{
			name: "index keeps no-cache", opts: []srvr.StaticOption{srvr.WithStaticCacheControl(immutable)}, path: "/index.html",
			status: 200, cache: "no-cache",
		},
		{
			name: "miss keeps prblm's", opts: []srvr.StaticOption{srvr.WithStaticCacheControl(immutable)}, path: "/missing.js",
			status: 404, cache: "no-store",
		},
		{
			name: "index cache option", opts: []srvr.StaticOption{srvr.WithStaticIndexCacheControl("no-store")}, path: "/dashboard",
			status: 200, cache: "no-store",
		},
		{
			name: "index cache from the config struct", path: "/", status: 200, cache: "public, max-age=0, must-revalidate",
			opts: []srvr.StaticOption{
				srvr.WithStaticConfig(srvr.StaticConfig{SPAFallback: "index.html", IndexCacheControl: "public, max-age=0, must-revalidate"}),
			},
		},
		{
			name: "kept on 304", opts: []srvr.StaticOption{srvr.WithStaticCacheControl("public, max-age=60")}, path: "/app.js", dir: true,
			header: map[string]string{"If-Modified-Since": siteModTime.Format(http.TimeFormat)}, status: 304, cache: "public, max-age=60",
		},
		{
			name: "empty index cache means the default", opts: []srvr.StaticOption{srvr.WithStaticIndexCacheControl("")}, path: "/",
			status: 200, cache: "no-cache",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var site fs.FS = mapSite()
			if tt.dir {
				site = os.DirFS(dirSite(t))
			}

			base, _ := staticServer(t, ginsrvr.Static("/", site, append([]srvr.StaticOption{srvr.WithSPAFallback("index.html")}, tt.opts...)...))
			header := map[string]string{"Accept": "text/html"}
			maps.Copy(header, tt.header)
			res := do(t, http.MethodGet, base+tt.path, nil, header)

			if res.status != tt.status || res.header.Get("Cache-Control") != tt.cache {
				t.Errorf("GET %s = %d Cache-Control %q, want %d %q", tt.path, res.status, res.header.Get("Cache-Control"), tt.status, tt.cache)
			}
		})
	}
}

// rawGet sends target as the raw request target of a GET to base, so no
// client cleans or re-encodes it, and returns the response.
func rawGet(t *testing.T, base, target string, header map[string]string) response {
	t.Helper()

	conn, err := new(net.Dialer).DialContext(t.Context(), "tcp", strings.TrimPrefix(base, "http://"))
	mustNil(t, err)

	defer conn.Close()

	var req strings.Builder
	fmt.Fprintf(&req, "GET %s HTTP/1.1\r\nHost: test\r\nConnection: close\r\n", target)
	for k, v := range header {
		fmt.Fprintf(&req, "%s: %s\r\n", k, v)
	}
	req.WriteString("\r\n")

	_, err = io.WriteString(conn, req.String())
	mustNil(t, err)

	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	mustNil(t, err)

	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	mustNil(t, err)

	return response{status: res.StatusCode, header: res.Header, body: string(b)}
}

// checkNotFound fails the test unless res is srvr's 404 problem for
// instance, with prblm's Cache-Control.
func checkNotFound(t *testing.T, res response, instance string, head bool) {
	t.Helper()

	if res.status != http.StatusNotFound || res.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status %d Cache-Control %q, want 404 no-store (body %.200q)", res.status, res.header.Get("Cache-Control"), res.body)
	}

	if head {
		return
	}

	p := res.problem
	if p == nil {
		t.Fatalf("body %q is not a problem", res.body)
	}

	traceID, _ := p.Ext("trace_id")
	if id, _ := traceID.(string); p.Title != "Not Found" || p.Detail != "The requested resource was not found." ||
		p.Instance != instance || len(id) != 32 {
		t.Errorf("problem = %+v, want srvr's 404 problem for %q", p, instance)
	}
}

// TestStatic_NotFound covers static-files / Scenario: Not found on gin.
func TestStatic_NotFound(t *testing.T) {
	t.Parallel()

	long := "/assets/" + strings.Repeat("a", 4096) + ".js"

	tests := []struct {
		name, method, path string
		opts               []srvr.StaticOption
	}{
		{"missing", "GET", "/assets/missing.js", nil},
		{"HEAD missing", "HEAD", "/assets/missing.js", nil},
		{"directory without index", "GET", "/assets/empty/", nil},
		{"nested directory without index", "GET", "/assets/empty/x/", nil},
		{"dotfile", "GET", "/assets/.env", nil},
		{"dot-directory file", "GET", "/assets/.git/config", nil},
		{"hidden file", "GET", "/assets/assets/.hidden.js", nil},
		{"dot-directory", "GET", "/assets/.git/", nil},
		{"file named as a directory", "GET", "/assets/app.js/", nil},
		{"below a file", "GET", "/assets/index.html/x", nil},
		{"index name missing", "GET", "/assets/", []srvr.StaticOption{srvr.WithStaticIndexNames("home.html")}},
		{"4096-character name", "GET", long, nil},
	}

	for _, site := range sites {
		fsys := site.build(t) // read only: built once for every row

		for _, tt := range tests {
			t.Run(site.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				opts := append([]srvr.StaticOption{srvr.WithStaticCacheControl("public, max-age=60")}, tt.opts...)
				base, _ := staticServer(t, ginsrvr.Static("/assets", fsys, opts...))
				checkNotFound(t, do(t, tt.method, base+tt.path, nil, nil), tt.path, tt.method == http.MethodHead)
			})
		}
	}
}

// TestStatic_Hostile covers static-files / Scenario: Hostile paths on gin,
// plus request targets whose path is not absolute.
func TestStatic_Hostile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, target string
		// prefix is the mount's prefix, "/assets" when empty.
		prefix string
	}{
		{name: "encoded dot segments", target: "/assets/%2e%2e/secret.txt"},
		{name: "encoded slash after dot-dot", target: "/assets/..%2Fsecret.txt"},
		{name: "encoded backslash", target: "/assets/..%5Csecret.txt"},
		{name: "NUL", target: "/assets/a%00.txt"},
		{name: "line feed", target: "/assets/a%0Ab"},
		{name: "invalid UTF-8", target: "/assets/%ff.txt"},
		{name: "double-encoded dot segments", target: "/assets/%252e%252e/secret.txt"},
		{name: "raw dot segments", target: "/assets/../secret.txt"},
		{name: "empty element", target: "/assets//app.js"},
		{name: "absolute-form target without a path", target: "http://test", prefix: "/"},
		{name: "absolute-form target without a path, prefixed mount", target: "http://test"},
	}

	site := os.DirFS(dirSite(t)) // read only: built once for every row

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, _ := staticServer(t, ginsrvr.Static(cmp.Or(tt.prefix, "/assets"), site, srvr.WithSPAFallback("index.html")))
			res := rawGet(t, base, tt.target, map[string]string{"Accept": "text/html"})

			if res.status != http.StatusNotFound || res.header.Get("Location") != "" {
				t.Errorf("status %d Location %q, want 404 without a redirect (body %q)", res.status, res.header.Get("Location"), res.body)
			}

			if strings.Contains(res.body, "SECRET") || strings.Contains(res.body, "<html>app</html>") {
				t.Errorf("body %q serves a file", res.body)
			}
		})
	}
}

// TestStatic_Methods covers static-files / Scenario: Other methods on gin,
// with its gin rows for GET and HEAD to a path only another method's route
// serves (owner decision 2026-11-19): they fall through to the mount as on
// net/http, every other method keeps gin's 405, and so does a path no
// mount contains or a server without a mount.
func TestStatic_Methods(t *testing.T) {
	t.Parallel()

	created := routes(func(g *gin.RouterGroup) {
		g.POST("/api/users", func(c *gin.Context) { c.Status(http.StatusCreated) })
		g.POST("/login", func(c *gin.Context) { c.Status(http.StatusCreated) })
		g.POST("/app.js", func(c *gin.Context) { c.Status(http.StatusCreated) })
	})
	// gate answers 418 if consumer middleware ever runs for a request gin
	// answers itself; registered only without a mount, whose server keeps
	// gin's 405 chain without consumer middleware.
	gate := routes(func(g *gin.RouterGroup) {
		g.Use(func(c *gin.Context) {
			if c.FullPath() == "" {
				c.AbortWithStatus(http.StatusTeapot)
			}
		})
	})

	tests := []struct {
		name, method, path, accept string
		// prefix is the mount's prefix, "/" when empty; "-" for no mount.
		prefix string
		route  bool
		status int
		allow  string
		// body is the body wanted for 200, "" for none.
		body string
	}{
		{name: "POST to a file", method: "POST", path: "/app.js", status: 405, allow: "GET, HEAD"},
		{name: "DELETE to a missing path", method: "DELETE", path: "/nope", status: 405, allow: "GET, HEAD"},
		{name: "OPTIONS to the root", method: "OPTIONS", path: "/", status: 405, allow: "GET, HEAD"},
		{name: "a route's own method", method: "POST", path: "/api/users", route: true, status: 201},
		{name: "another method of a route (documented exception)", method: "PUT", path: "/api/users", route: true, status: 405, allow: "POST"},
		{
			name: "GET navigation to a POST-only path falls back", method: "GET", path: "/api/users", accept: "text/html", route: true,
			status: 200, body: "<html>app</html>",
		},
		{name: "GET login page beside POST /login", method: "GET", path: "/login", accept: "text/html", route: true, status: 200, body: "<html>app</html>"},
		{name: "HEAD navigation to a POST-only path", method: "HEAD", path: "/api/users", accept: "text/html", route: true, status: 200},
		{name: "GET to a POST-only path without text/html", method: "GET", path: "/api/users", accept: "application/json", route: true, status: 404},
		{name: "GET to a POST-only path that is a file", method: "GET", path: "/app.js", route: true, status: 200, body: "console.log(1)"},
		{
			name: "GET to a POST-only path outside every mount", prefix: "/assets", method: "GET", path: "/api/users", accept: "text/html",
			route: true, status: 405, allow: "POST",
		},
		{name: "GET to a POST-only path without a mount", prefix: "-", method: "GET", path: "/api/users", accept: "text/html", route: true, status: 405, allow: "POST"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			confs := statics{gate}
			if tt.prefix != "-" {
				confs = statics{ginsrvr.Static(cmp.Or(tt.prefix, "/"), mapSite(), srvr.WithSPAFallback("index.html"))}
			}

			if tt.route {
				confs = append(confs, created)
			}

			header := map[string]string{}
			if tt.accept != "" {
				header["Accept"] = tt.accept
			}

			base, _ := staticServer(t, confs...)
			res := do(t, tt.method, base+tt.path, nil, header)

			if res.status != tt.status || res.header.Get("Allow") != tt.allow || (tt.body != "" && res.body != tt.body) {
				t.Fatalf("%s %s = %d %q Allow %q, want %d %q %q", tt.method, tt.path, res.status, res.body, res.header.Get("Allow"),
					tt.status, tt.body, tt.allow)
			}

			if tt.status >= http.StatusBadRequest && tt.method != http.MethodHead && (res.problem == nil || res.problem.Status != tt.status) {
				t.Errorf("problem = %+v, want a %d problem", res.problem, tt.status)
			}
		})
	}
}

// noSeekFile is a file that does not implement io.Seeker.
type noSeekFile struct{ f fs.File }

func (n noSeekFile) Stat() (fs.FileInfo, error) { return n.f.Stat() }
func (n noSeekFile) Read(p []byte) (int, error) { return n.f.Read(p) }
func (n noSeekFile) Close() error               { return n.f.Close() }

// shortReadFile is a seekable file whose Read fails once limit bytes were
// read.
type shortReadFile struct {
	fs.File

	seeker      io.Seeker
	limit, read int
}

func (s *shortReadFile) Read(p []byte) (int, error) {
	if s.read >= s.limit {
		return 0, errors.New("read failed")
	}

	p = p[:min(len(p), s.limit-s.read)]
	n, err := s.File.Read(p)
	s.read += n

	return n, err
}

func (s *shortReadFile) Seek(offset int64, whence int) (int64, error) {
	return s.seeker.Seek(offset, whence)
}

// messageContains checks that a record's exception.message contains text.
func messageContains(text string) func(*testing.T, map[string]any) {
	return func(t *testing.T, rec map[string]any) {
		t.Helper()

		if msg, _ := rec["exception.message"].(string); !strings.Contains(msg, text) {
			t.Errorf("exception.message %q does not contain %q", msg, text)
		}
	}
}

// recordHas checks that a record's key holds value.
func recordHas(key string, value any) func(*testing.T, map[string]any) {
	return func(t *testing.T, rec map[string]any) {
		t.Helper()

		if rec[key] != value {
			t.Errorf("%s = %v, want %v", key, rec[key], value)
		}
	}
}

// TestStatic_FaultyFS covers static-files / Scenario: Faulty file system
// on gin.
func TestStatic_FaultyFS(t *testing.T) {
	t.Parallel()

	site := mapSite()
	opening := func(open func() (fs.File, error)) fs.FS {
		return faultFS{base: site, open: map[string]func() (fs.File, error){"app.js": open}}
	}

	var typedNil *MyError

	tests := []struct {
		name   string
		fsys   fs.FS
		status int
		level  string
		// check checks the failed record.
		check func(t *testing.T, rec map[string]any)
		// hidden is text the response must not contain.
		hidden string
	}{
		{
			name: "permission denied from Open", status: 500, level: "ERROR",
			fsys: opening(func() (fs.File, error) {
				return nil, &fs.PathError{Op: "open", Path: "app.js", Err: fs.ErrPermission}
			}),
			check: messageContains("permission denied"), hidden: "permission",
		},
		{
			name: "error from Stat", status: 500, level: "ERROR",
			fsys: faultFS{base: site, stat: map[string]func() (fs.FileInfo, error){
				"app.js": func() (fs.FileInfo, error) { return nil, errors.New("disk on fire") },
			}},
			check: messageContains("disk on fire"), hidden: "disk on fire",
		},
		{
			name: "panic in Open", status: 500, level: "ERROR",
			fsys:  opening(func() (fs.File, error) { panic("boom") }),
			check: recordHas("error.kind", "panic"), hidden: "boom",
		},
		{
			name: "typed-nil error from Open", status: 500, level: "ERROR",
			fsys:  opening(func() (fs.File, error) { return nil, typedNil }),
			check: recordHas("error.type", "*ginsrvr_test.MyError"),
		},
		{
			name: "file without Seek", status: 500, level: "ERROR",
			fsys: opening(func() (fs.File, error) {
				f, err := site.Open("app.js")
				return noSeekFile{f}, err
			}),
			check: messageContains("ginsrvr_test.noSeekFile cannot seek"), hidden: "seek",
		},
		{
			name: "wrapped not-exist", status: 404, level: "WARN",
			fsys: faultFS{base: site, stat: map[string]func() (fs.FileInfo, error){
				"app.js": func() (fs.FileInfo, error) { return nil, fmt.Errorf("lookup: %w", fs.ErrNotExist) },
			}},
			check: func(t *testing.T, rec map[string]any) {
				t.Helper()
				if msg, _ := rec["exception.message"].(string); strings.Contains(msg, "lookup") {
					t.Errorf("exception.message %q names the file system's error", msg)
				}
			},
		},
		{
			name: "removed between Stat and Open", status: 404, level: "WARN",
			fsys:  opening(func() (fs.File, error) { return nil, fs.ErrNotExist }),
			check: recordHas("http.response.status_code", float64(404)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, logs := staticServer(t, ginsrvr.Static("/assets", tt.fsys))
			res := get(t, base+"/assets/app.js")

			if res.status != tt.status || res.problem == nil {
				t.Fatalf("status %d body %q, want a %d problem", res.status, res.body, tt.status)
			}

			if tt.status == 500 && (res.problem.Title != "Internal Server Error" || res.problem.Detail != "" ||
				res.problem.Instance != "/assets/app.js") {
				t.Errorf("problem = %+v, want the safe 500 problem", res.problem)
			}

			if tt.hidden != "" && strings.Contains(res.body, tt.hidden) {
				t.Errorf("body %q contains %q", res.body, tt.hidden)
			}

			rec := logs.waitRecord(t, "http.request.failed")
			if rec["level"] != tt.level {
				t.Errorf("level = %v, want %s", rec["level"], tt.level)
			}

			tt.check(t, rec)
		})
	}
}

// TestStatic_FaultyFSAfterStart covers the Faulty file system row whose
// Read fails after the response started, on gin: the response is cut
// short and the request is recorded as completed.
func TestStatic_FaultyFSAfterStart(t *testing.T) {
	t.Parallel()

	site := mapSite()
	fsys := faultFS{base: site, open: map[string]func() (fs.File, error){"app.js": func() (fs.File, error) {
		f, err := site.Open("app.js")
		seeker, _ := f.(io.Seeker)

		return &shortReadFile{File: f, seeker: seeker, limit: 10}, err
	}}}

	base, logs := staticServer(t, ginsrvr.Static("/assets", fsys))

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/assets/app.js", nil)
	mustNil(t, err)

	res, err := clientFor(base).Do(req)
	mustNil(t, err)

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || res.ContentLength != 14 || len(body) >= 14 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("status %d Content-Length %d body %q err %v, want 200, 14, a cut body and an unexpected EOF",
			res.StatusCode, res.ContentLength, body, err)
	}

	rec := logs.waitRecord(t, "http.request.completed")
	if rec["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", rec["level"])
	}
}

// chainOf returns a record's error.chain.
func chainOf(rec map[string]any) []string {
	raw, _ := rec["error.chain"].([]any)
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i], _ = v.(string)
	}

	return out
}

// TestStatic_Symlinks covers static-files / Scenario: Symbolic links on
// gin.
func TestStatic_Symlinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		fsys   func(t *testing.T, dir string) fs.FS
		status int
	}{
		{"os.Root refuses the escape", func(t *testing.T, dir string) fs.FS {
			t.Helper()

			root, err := os.OpenRoot(dir)
			mustNil(t, err)
			t.Cleanup(func() { _ = root.Close() })

			return root.FS()
		}, 500},
		{"os.DirFS follows the link", func(_ *testing.T, dir string) fs.FS { return os.DirFS(dir) }, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := dirSite(t)
			if err := os.Symlink(filepath.Join("..", "secret.txt"), filepath.Join(dir, "link.txt")); err != nil {
				t.Skipf("cannot create a symbolic link here: %v", err)
			}

			base, logs := staticServer(t, ginsrvr.Static("/assets", tt.fsys(t, dir)))
			res := get(t, base+"/assets/link.txt")

			if res.status != tt.status {
				t.Fatalf("status %d body %q, want %d", res.status, res.body, tt.status)
			}

			if tt.status == 200 {
				if res.body != "SECRET" {
					t.Errorf("body %q, want SECRET", res.body)
				}

				return
			}

			if strings.Contains(res.body, "SECRET") || strings.Contains(res.body, dir) ||
				res.problem == nil || res.problem.Detail != "" {
				t.Errorf("body %q, want the safe 500 problem", res.body)
			}

			rec := logs.waitRecord(t, "http.request.failed")
			if len(chainOf(rec)) == 0 || rec["level"] != "ERROR" || rec["exception.message"] == nil {
				t.Errorf("record %v, want an ERROR with error.chain and exception.message", rec)
			}
		})
	}
}

// TestStatic_Precedence covers static-files / Scenario: Precedence on gin.
func TestStatic_Precedence(t *testing.T) {
	t.Parallel()

	docs := fstest.MapFS{"guide.html": {Data: []byte("<html>docs mount</html>")}}
	api := routes(func(g *gin.RouterGroup) {
		g.GET("/api/users", answer("users"))
		g.GET("/app.js", answer("route app.js"))
	})

	tests := []struct {
		name, path  string
		routesFirst bool
		status      int
		body        string
		accept      string
	}{
		{"route, static first", "/api/users", false, 200, "users", ""},
		{"route, routes first", "/api/users", true, 200, "users", ""},
		{"route over a file, static first", "/app.js", false, 200, "route app.js", ""},
		{"route over a file, routes first", "/app.js", true, 200, "route app.js", ""},
		{"navigation the API does not answer falls back", "/api/nope", false, 200, "<html>app</html>", ""},
		{"API miss without text/html", "/api/nope", false, 404, "", "application/json"},
		{"longest prefix", "/docs/guide.html", false, 200, "<html>docs mount</html>", ""},
		{"longest prefix miss", "/docs/missing", false, 404, "", ""},
		{"shorter prefix", "/style.css", false, 200, "body{}", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := ginsrvr.Static("/", mapSite(), srvr.WithSPAFallback("index.html"))
			confs := statics{root, ginsrvr.Static("/docs", docs), api}
			if tt.routesFirst {
				confs = statics{api, root, ginsrvr.Static("/docs", docs)}
			}

			base, _ := staticServer(t, confs...)
			res := do(t, http.MethodGet, base+tt.path, nil, map[string]string{"Accept": cmp.Or(tt.accept, "text/html")})

			if res.status != tt.status || (tt.body != "" && res.body != tt.body) {
				t.Errorf("GET %s = %d %q, want %d %q", tt.path, res.status, res.body, tt.status, tt.body)
			}
		})
	}
}

// TestStatic_Conflicts covers static-files / Scenario: Conflicts with
// other routes on gin: a consumer catch-all is a registered route, so the
// server starts and the route answers every GET.
func TestStatic_Conflicts(t *testing.T) {
	t.Parallel()

	base, _ := staticServer(t, ginsrvr.Static("/", mapSite()), routes(func(g *gin.RouterGroup) { g.GET("/*any", answer("route")) }))

	for _, p := range []string{"/app.js", "/", "/nope"} {
		if res := get(t, base+p); res.status != http.StatusOK || res.body != "route" {
			t.Errorf("GET %s = %d %q, want the route's answer", p, res.status, res.body)
		}
	}
}

// staticTraceparent is the traceparent the static access rows send.
const staticTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// TestStatic_Access covers static-files / Scenario: Static access records
// on gin.
func TestStatic_Access(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, path, msg, level, route string
		status                          float64
	}{
		{"GET", "/assets/app.js", "http.request.completed", "INFO", "/assets/*filepath", 200},
		{"GET", "/assets", "http.request.completed", "INFO", "/assets", 200},
		{"GET", "/assets/", "http.request.completed", "INFO", "/assets/*filepath", 200},
		{"GET", "/assets/settings", "http.request.completed", "INFO", "/assets/*filepath", 200},
		{"GET", "/assets/missing.js", "http.request.failed", "WARN", "", 404},
		{"POST", "/assets/app.js", "http.request.failed", "WARN", "", 405},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			dir := dirSite(t)
			base, logs := staticServer(t, ginsrvr.Static("/assets", os.DirFS(dir), srvr.WithSPAFallback("index.html")))
			do(t, tt.method, base+tt.path, nil, map[string]string{"Accept": "text/html", "traceparent": staticTraceparent})

			rec := logs.waitRecord(t, tt.msg)
			route, _ := rec["http.route"].(string)

			if rec["level"] != tt.level || route != tt.route || rec["http.response.status_code"] != tt.status ||
				rec["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
				t.Errorf("record %v, want level %s route %q status %v and the request's trace", rec, tt.level, tt.route, tt.status)
			}

			if _, ok := rec["code.function.name"]; ok {
				t.Errorf("record %v names a code location: the static mount is srvr's code", rec)
			}

			if strings.Contains(logs.String(), dir) {
				t.Errorf("the log names the file system path %q:\n%s", dir, logs.String())
			}
		})
	}
}

// staticClock is a server clock that never moves, so the unmatched-record
// limit never refills during a test.
func staticClock(s *engine.Settings) { s.Now = func() time.Time { return time.Unix(0, 0) } }

// TestStatic_Unmatched covers static-files / Scenario: Static misses and
// the unmatched-record limit on gin, and that a server without a static
// mount keeps gin's no-route chain (structured-logging: root-group
// middleware runs only for matched routes).
func TestStatic_Unmatched(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		// mw is consumer middleware added to the root group before the
		// static configurer, nil for none.
		mw gin.HandlerFunc
		// noStatic registers a GET /x route instead of the static mount.
		noStatic bool
		// posts registers POST routes for the requested paths, so gin
		// routes the GET requests to its no-method chain.
		posts   bool
		status  int
		written int
		// routeless says the records must have no http.route.
		routeless bool
	}{
		{name: "static misses", method: "GET", status: 404, written: 1},
		{name: "wrong method", method: "PUT", status: 405, written: 1},
		{name: "static misses of paths only POST routes serve", method: "GET", posts: true, status: 404, written: 1},
		{
			name: "root-group middleware answering 404 before the static mount", method: "GET", status: 404, written: 3,
			mw: func(c *gin.Context) { _ = c.AbortWithError(http.StatusNotFound, prblm.ErrNotFound) },
		},
		{
			name: "root-group middleware calling Next", method: "GET", status: 404, written: 1,
			mw: func(c *gin.Context) { c.Next() },
		},
		{
			name: "root-group middleware answering 200 itself after a static miss", method: "GET", status: 200, written: 3, routeless: true,
			mw: func(c *gin.Context) {
				c.Next()
				if len(c.Errors) > 0 {
					c.Errors = c.Errors[:0]
					c.String(http.StatusOK, "custom")
				}
			},
		},
		{
			name: "without a static mount, root-group middleware never runs", method: "GET", status: 404, written: 1, noStatic: true,
			mw: func(c *gin.Context) {
				_ = c.AbortWithError(http.StatusTeapot, errors.New("group middleware ran for an unmatched path"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, logs := newServer(t, srvr.WithUnmatchedRecordLimit(1, time.Minute), staticClock)
			if tt.mw != nil {
				mustNil(t, srv.Register(routes(func(g *gin.RouterGroup) { g.Use(tt.mw) })))
			}

			conf := ginsrvr.Static("/", mapSite())
			if tt.noStatic {
				conf = routes(func(g *gin.RouterGroup) { g.GET("/x", ok) })
			}

			mustNil(t, srv.Register(conf))

			if tt.posts {
				mustNil(t, srv.Register(routes(func(g *gin.RouterGroup) {
					for _, p := range []string{"/nope-1.js", "/nope-2.js", "/nope-3.js"} {
						g.POST(p, ok)
					}
				})))
			}

			base := start(t, srv)
			for _, p := range []string{"/nope-1.js", "/nope-2.js", "/nope-3.js"} {
				if res := do(t, tt.method, base+p, nil, nil); res.status != tt.status || (tt.status >= 400) != (res.problem != nil) {
					t.Fatalf("%s %s = %d %q, want %d, a problem when it fails", tt.method, p, res.status, res.body, tt.status)
				}
			}

			mustNil(t, srv.Shutdown(t.Context()))

			var written int
			for _, r := range logs.access(t) {
				if r["http.response.status_code"] == float64(tt.status) {
					written++
				}

				if route, ok := r["http.route"]; ok && tt.routeless {
					t.Errorf("record has http.route %v, want none for a static miss:\n%s", route, logs.String())
				}
			}

			if written != tt.written {
				t.Errorf("%d access records with status %d, want %d:\n%s", written, tt.status, tt.written, logs.String())
			}
		})
	}
}

// TestStatic_Observer covers static-files / Scenario: Observers see static
// requests on gin, with a recording observer. A static miss is observed
// as a request no route matched: no route and no error, as for gin's own
// 404 (design D5, followups-hardening's "consumer code ran" rule).
func TestStatic_Observer(t *testing.T) {
	t.Parallel()

	obs := &recordingObserver{}
	srv, _ := newServer(t, srvr.WithObserver(obs))
	mustNil(t, srv.Register(ginsrvr.Static("/assets", mapSite())))

	base := start(t, srv)
	get(t, base+"/assets/app.js")
	get(t, base+"/assets/missing.js")

	eventually(t, func() bool { _, results := obs.seen(); return len(results) == 2 }, "two observed requests")

	_, results := obs.seen()
	got := []string{results[0].Route + " " + strconv.Itoa(results[0].Status), results[1].Route + " " + strconv.Itoa(results[1].Status)}
	slices.Sort(got)

	if want := []string{" 404", "/assets/*filepath 200"}; !slices.Equal(got, want) {
		t.Errorf("observed %q, want %q", got, want)
	}

	for _, r := range results {
		if r.Status == http.StatusNotFound && r.Err != nil {
			t.Errorf("static miss observed with Err %v, want nil as for router answers", r.Err)
		}
	}
}

// TestStatic_Middleware covers static-files / Scenario: Consumer
// middleware around static files on gin, plus the gin specifics: root-group
// middleware added after the static configurer applies too, and once a
// mount exists it also runs for requests no mount contains.
func TestStatic_Middleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mw   gin.HandlerFunc
		// after adds the middleware after the static configurer.
		after  bool
		path   string
		status int
		check  func(t *testing.T, res response, logs *logBuffer)
	}{
		{
			name: "AbortWithError 401", mw: func(c *gin.Context) { _ = c.AbortWithError(http.StatusUnauthorized, prblm.ErrUnauthenticated) },
			status: 401, check: func(t *testing.T, res response, logs *logBuffer) {
				t.Helper()
				if res.problem == nil {
					t.Errorf("body %q, want a problem", res.body)
				}
				recordHas("http.route", "/assets/*filepath")(t, logs.waitRecord(t, "http.request.failed"))
			},
		},
		{
			name: "AbortWithStatus 401, as for a matched route", mw: func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) },
			status: 401, check: func(t *testing.T, _ response, logs *logBuffer) {
				t.Helper()
				recordHas("http.route", "/assets/*filepath")(t, logs.waitRecord(t, "http.request.completed"))
			},
		},
		{
			name: "panics", mw: func(*gin.Context) { panic("boom") }, status: 500,
			check: func(t *testing.T, _ response, logs *logBuffer) {
				t.Helper()
				recordHas("error.kind", "panic")(t, logs.waitRecord(t, "http.request.failed"))
			},
		},
		{
			name: "sets a header and calls Next", mw: func(c *gin.Context) { c.Header("X-Mw", "1"); c.Next() }, status: 200,
			check: checkMwFile,
		},
		{
			name: "added after the static configurer", after: true, mw: func(c *gin.Context) { c.Header("X-Mw", "1"); c.Next() },
			status: 200, check: checkMwFile,
		},
		{
			// Pinned limitation (notes.md): outside every mount no route is
			// recorded, so a status-only abort counts as a failure there,
			// where it is a completed request on a route or a mount.
			name: "AbortWithStatus 401 on a path no mount contains", mw: func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) },
			path: "/other", status: 401, check: func(t *testing.T, _ response, logs *logBuffer) {
				t.Helper()
				recordHas("http.route", nil)(t, logs.waitRecord(t, "http.request.failed"))
			},
		},
		{
			name: "runs for a path no mount contains", mw: func(c *gin.Context) { c.Header("X-Mw", "1"); c.Next() },
			path: "/other", status: 404, check: func(t *testing.T, res response, logs *logBuffer) {
				t.Helper()
				if res.header.Get("X-Mw") != "1" || res.problem == nil {
					t.Errorf("X-Mw %q body %q, want 1 and a problem", res.header.Get("X-Mw"), res.body)
				}
				recordHas("level", "WARN")(t, logs.waitRecord(t, "http.request.failed"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			use := routes(func(g *gin.RouterGroup) { g.Use(tt.mw) })
			confs := statics{use, ginsrvr.Static("/assets", mapSite())}
			if tt.after {
				confs = statics{ginsrvr.Static("/assets", mapSite()), use}
			}

			base, logs := staticServer(t, confs...)
			res := get(t, base+cmp.Or(tt.path, "/assets/app.js"))

			if res.status != tt.status {
				t.Fatalf("status %d, want %d (body %q)", res.status, tt.status, res.body)
			}

			tt.check(t, res, logs)

			eventually(t, func() bool { return len(logs.access(t)) == 1 }, "one access record")
			time.Sleep(duplicateWindow)

			if n := len(logs.access(t)); n != 1 {
				t.Errorf("%d access records, want 1:\n%s", n, logs.String())
			}
		})
	}
}

// checkMwFile checks that the response is app.js with header X-Mw: 1.
func checkMwFile(t *testing.T, res response, _ *logBuffer) {
	t.Helper()

	if res.body != "console.log(1)" || res.header.Get("X-Mw") != "1" {
		t.Errorf("body %q X-Mw %q, want the file and 1", res.body, res.header.Get("X-Mw"))
	}
}

// TestStatic_Routes covers static-files / Scenario: Static route listing
// on gin: the mount's entries come after the routes, whatever the
// registration order.
func TestStatic_Routes(t *testing.T) {
	t.Parallel()

	const owner = "*ginsrvr.staticConfigurer"

	tests := []struct {
		name   string
		confs  statics
		listed engine.Routes
	}{
		{"root", statics{ginsrvr.Static("/", mapSite())}, engine.Routes{{Method: "GET", Pattern: "/*filepath", Configurer: owner}}},
		{"prefix", statics{ginsrvr.Static("/assets", mapSite())}, engine.Routes{
			{Method: "GET", Pattern: "/assets", Configurer: owner},
			{Method: "GET", Pattern: "/assets/*filepath", Configurer: owner},
		}},
		{"after the routes", statics{ginsrvr.Static("/assets", mapSite()), &UserRoutes{func(g *gin.RouterGroup) { g.GET("/x", ok) }}}, engine.Routes{
			{Method: "GET", Pattern: "/x", Configurer: "*ginsrvr_test.UserRoutes"},
			{Method: "GET", Pattern: "/assets", Configurer: owner},
			{Method: "GET", Pattern: "/assets/*filepath", Configurer: owner},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got engine.Routes

			srv, _ := newServer(t, recordRoutes(&got))
			for _, c := range tt.confs {
				mustNil(t, srv.Register(c))
			}

			start(t, srv)

			if !slices.Equal(got, tt.listed) {
				t.Errorf("routes = %v, want %v", got, tt.listed)
			}
		})
	}
}

// TestStatic_Partial covers static-files / Scenario: Partial failure while
// serving on gin.
func TestStatic_Partial(t *testing.T) {
	t.Parallel()

	big := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB

	newSite := func() fstest.MapFS {
		site := mapSite()
		site["big.bin"] = &fstest.MapFile{Data: big}

		return site
	}

	t.Run("client closes mid-body", func(t *testing.T) {
		t.Parallel()

		base, logs := staticServer(t, ginsrvr.Static("/assets", newSite()))

		conn, err := new(net.Dialer).DialContext(t.Context(), "tcp", strings.TrimPrefix(base, "http://"))
		mustNil(t, err)

		_, err = io.WriteString(conn, "GET /assets/big.bin HTTP/1.1\r\nHost: test\r\n\r\n")
		mustNil(t, err)

		_, err = io.ReadFull(conn, make([]byte, 64<<10))
		mustNil(t, err)
		mustNil(t, conn.Close())

		rec := logs.waitRecord(t, "http.request.completed")
		if rec["http.response.status_code"] != float64(200) {
			t.Errorf("record %v, want status 200", rec)
		}

		if res := get(t, base+"/assets/app.js"); res.status != http.StatusOK {
			t.Errorf("GET after the disconnect = %d, want 200", res.status)
		}
	})

	t.Run("50 concurrent clients", func(t *testing.T) {
		t.Parallel()

		base, _ := staticServer(t, ginsrvr.Static("/assets", newSite()))

		var wg sync.WaitGroup
		for i := range 50 {
			wg.Go(func() {
				path, want := "/assets/app.js", "console.log(1)"
				if i%2 == 0 {
					path, want = "/assets/big.bin", string(big)
				}

				if res := get(t, base+path); res.status != http.StatusOK || res.body != want {
					t.Errorf("GET %s = %d with %d bytes, want 200 and the whole file", path, res.status, len(res.body))
				}
			})
		}
		wg.Wait()
	})
}

// TestStatic_HookNoRoute pins that a static mount replaces the no-route
// handlers a WithGinEngine hook installed: gin keeps one no-route chain,
// and the static dispatcher takes it (documented on Static).
func TestStatic_HookNoRoute(t *testing.T) {
	t.Parallel()

	hook := ginsrvr.WithGinEngine(func(e *gin.Engine) { e.NoRoute(answer("hook")) })

	tests := []struct {
		name   string
		static bool
		body   string
	}{
		{"without a static mount the hook answers", false, "hook"},
		{"a static mount replaces the hook", true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t, hook)
			if tt.static {
				mustNil(t, srv.Register(ginsrvr.Static("/assets", mapSite())))
			}

			res := get(t, start(t, srv)+"/other")
			if tt.body != "" && res.body != tt.body {
				t.Errorf("GET /other = %d %q, want %q", res.status, res.body, tt.body)
			}

			if tt.body == "" && (res.status != http.StatusNotFound || res.problem == nil) {
				t.Errorf("GET /other = %d %q, want the 404 problem", res.status, res.body)
			}
		})
	}
}

// TestStatic_MiddlewareStatus checks that consumer middleware sees a
// static mount's request as it sees a matched route's: the same status
// before Next, and the same answer when it aborts without one.
func TestStatic_MiddlewareStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mw     gin.HandlerFunc
		status int
	}{
		{"aborts", func(c *gin.Context) { c.Abort() }, 200},
		{"records an error and aborts", func(c *gin.Context) { _ = c.Error(errors.New("boom")); c.Abort() }, 500},
		{"writes the status it sees", func(c *gin.Context) { c.AbortWithStatus(c.Writer.Status()) }, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, _ := staticServer(t,
				routes(func(g *gin.RouterGroup) { g.Use(tt.mw); g.GET("/route", ok) }),
				ginsrvr.Static("/assets", mapSite()))

			for _, p := range []string{"/route", "/assets/app.js"} {
				if res := get(t, base+p); res.status != tt.status {
					t.Errorf("GET %s = %d %q, want %d", p, res.status, res.body, tt.status)
				}
			}
		})
	}
}

// TestStatic_DuplicatePrefix checks that the error for a second mount of
// a prefix names the earlier mount, as on net/http.
func TestStatic_DuplicatePrefix(t *testing.T) {
	t.Parallel()

	srv, _ := newServer(t)
	mustNil(t, srv.Register(ginsrvr.Static("/assets", mapSite())))
	mustNil(t, srv.Register(ginsrvr.Static("/assets/", mapSite())))

	err := startErr(t, srv)
	want := `static "/assets/" (*ginsrvr.staticConfigurer): ` +
		`srvr: invalid configuration: Static.Prefix /assets/: is already served by another static mount (static "/assets" (*ginsrvr.staticConfigurer))`
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("Start = %v, want it to end with %s", err, want)
	}
}
