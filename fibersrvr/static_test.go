package fibersrvr_test

import (
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

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
	"github.com/kartaladev/srvr/internal/engine"
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

// statics adapts static configurers to the registrations of a fiber
// server.
type statics = []srvr.RouteConfigurer[fiber.Router]

// TestStatic_ConfigErrors covers static-files / Scenario: Rejected static
// configuration, Scenario: Several invalid settings and Scenario: Rejected
// file cache configuration on fiber.
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
		{"nil FS", statics{fibersrvr.Static("/", nil)}, []string{"Static.FS"}, ""},
		{"typed-nil *embed.FS", statics{fibersrvr.Static("/", typedNil)}, []string{"Static.FS"}, ""},
		{"empty prefix", statics{fibersrvr.Static("", site)}, []string{"Static.Prefix"}, ""},
		{"relative prefix", statics{fibersrvr.Static("assets", site)}, []string{"Static.Prefix"}, ""},
		{"wildcard prefix", statics{fibersrvr.Static("/assets/{name}", site)}, []string{"Static.Prefix"}, ""},
		{"star prefix", statics{fibersrvr.Static("/assets/*", site)}, []string{"Static.Prefix"}, ""},
		{"parameter prefix", statics{fibersrvr.Static("/assets/:id", site)}, []string{"Static.Prefix"}, ""},
		{"dot-dot prefix", statics{fibersrvr.Static("/a/../b", site)}, []string{"Static.Prefix"}, ""},
		{"empty element prefix", statics{fibersrvr.Static("/a//b", site)}, []string{"Static.Prefix"}, ""},
		{"NUL in prefix", statics{fibersrvr.Static("/a\x00", site)}, []string{"Static.Prefix"}, ""},
		{"nil option", statics{fibersrvr.Static("/", site, nil)}, []string{"StaticOption"}, ""},
		{"missing fallback", statics{fibersrvr.Static("/", site, srvr.WithSPAFallback("missing.html"))}, []string{"Static.SPAFallback"}, ""},
		{"directory fallback", statics{fibersrvr.Static("/", site, srvr.WithSPAFallback("docs"))}, []string{"Static.SPAFallback"}, ""},
		{
			"fallback leaving the file system",
			statics{fibersrvr.Static("/", site, srvr.WithSPAFallback("../index.html"))},
			[]string{"Static.SPAFallback"},
			"",
		},
		{"absolute fallback", statics{fibersrvr.Static("/", site, srvr.WithSPAFallback("/index.html"))}, []string{"Static.SPAFallback"}, ""},
		{
			"header injection in CacheControl",
			statics{fibersrvr.Static("/", site, srvr.WithStaticCacheControl("max-age=60\r\nSet-Cookie: a=b"))},
			[]string{"Static.CacheControl"},
			"",
		},
		{
			"NUL in IndexCacheControl",
			statics{fibersrvr.Static("/", site, srvr.WithStaticIndexCacheControl("no-cache\x00"))},
			[]string{"Static.IndexCacheControl"},
			"",
		},
		{"empty index name", statics{fibersrvr.Static("/", site, srvr.WithStaticIndexNames(""))}, []string{"Static.IndexNames"}, ""},
		{
			"index name with a slash",
			statics{fibersrvr.Static("/", site, srvr.WithStaticIndexNames("a/index.html"))},
			[]string{"Static.IndexNames"},
			"",
		},
		{"dot-dot index name", statics{fibersrvr.Static("/", site, srvr.WithStaticIndexNames(".."))}, []string{"Static.IndexNames"}, ""},
		{"fallback Open panics", statics{
			fibersrvr.Static("/", failingIndex(site, func() (fs.File, error) { panic("boom") }), srvr.WithSPAFallback("index.html")),
		}, []string{"Static.SPAFallback"}, ""},
		{"fallback Open permission denied", statics{
			fibersrvr.Static("/", failingIndex(site, func() (fs.File, error) { return nil, fs.ErrPermission }), srvr.WithSPAFallback("index.html")),
		}, []string{"Static.SPAFallback"}, ""},
		{"same prefix twice", statics{fibersrvr.Static("/assets", site), fibersrvr.Static("/assets", site)}, []string{"Static.Prefix"}, ""},
		{
			"same prefix twice, one with a trailing slash",
			statics{fibersrvr.Static("/assets", site), fibersrvr.Static("/assets/", site)},
			[]string{"Static.Prefix"},
			"",
		},
		{"root twice with other file systems", statics{fibersrvr.Static("/", site), fibersrvr.Static("/", fstest.MapFS{})}, []string{"Static.Prefix"}, ""},
		{"several invalid settings", statics{fibersrvr.Static("assets", nil, srvr.WithStaticCacheControl("a\nb"))}, []string{
			"Static.Prefix", "Static.FS", "Static.CacheControl",
		}, ""},
		{"WithSendFileCache(0)", statics{fibersrvr.Static("/", site, fibersrvr.WithSendFileCache(0))}, []string{"Static.FiberSendFileCache"}, "0s"},
		{
			"WithSendFileCache(-time.Second)",
			statics{fibersrvr.Static("/", site, fibersrvr.WithSendFileCache(-time.Second))},
			[]string{"Static.FiberSendFileCache"},
			"-1s",
		},
		{
			"WithSendFileCache(0), then a zero config struct",
			statics{fibersrvr.Static("/", site, fibersrvr.WithSendFileCache(0), srvr.WithStaticConfig(srvr.StaticConfig{}))},
			[]string{"Static.FiberSendFileCache"},
			"0s",
		},
		{
			"negative config field",
			statics{fibersrvr.Static("/", site, srvr.WithStaticConfig(srvr.StaticConfig{FiberSendFileCache: -time.Second}))},
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

			if !strings.Contains(err.Error(), "*fibersrvr.staticConfigurer") {
				t.Errorf("Start = %v, want it to name the static configurer", err)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			if err := srv.Shutdown(ctx); err != nil {
				t.Errorf("Shutdown = %v, want nil", err)
			}
		})
	}
}

// TestStatic_DuplicatePrefix checks that the error for a second mount of
// a prefix names the earlier mount, as on net/http and gin.
func TestStatic_DuplicatePrefix(t *testing.T) {
	t.Parallel()

	srv, _ := newServer(t)
	mustNil(t, srv.Register(fibersrvr.Static("/assets", mapSite())))
	mustNil(t, srv.Register(fibersrvr.Static("/assets/", mapSite())))

	err := startErr(t, srv)
	want := `static "/assets/" (*fibersrvr.staticConfigurer): ` +
		`srvr: invalid configuration: Static.Prefix /assets/: is already served by another static mount (static "/assets" (*fibersrvr.staticConfigurer))`
	if err == nil || !strings.HasSuffix(err.Error(), want) {
		t.Errorf("Start = %v, want it to end with %s", err, want)
	}
}

// selfStatic is a consumer configurer that calls a static configurer's
// Configure itself instead of registering it.
type selfStatic struct{ fsys fs.FS }

func (s selfStatic) Configure(r fiber.Router) { fibersrvr.Static("/", s.fsys).Configure(r) }

// TestStatic_Lifecycle covers static-files / Scenario: Static configurer
// lifecycle misuse on fiber.
func TestStatic_Lifecycle(t *testing.T) {
	t.Parallel()

	t.Run("Register after Start", func(t *testing.T) {
		t.Parallel()

		srv, _ := newServer(t)
		base := start(t, srv)

		if err := srv.Register(fibersrvr.Static("/", mapSite())); !errors.Is(err, srvr.ErrServerStarted) {
			t.Errorf("Register = %v, want ErrServerStarted", err)
		}

		if res := do(t, http.MethodGet, base+"/app.js", nil, nil); res.status != http.StatusNotFound {
			t.Errorf("GET /app.js = %d %q, want 404: no route is added", res.status, res.body)
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

		for _, text := range []string{"fibersrvr_test.selfStatic", "fibersrvr.Static", "Server.Register"} {
			if !strings.Contains(err.Error(), text) {
				t.Errorf("Start = %v, want it to name %s", err, text)
			}
		}
	})
}

// TestStatic_Conflicts covers static-files / Scenario: Conflicts with
// other routes on fiber: a consumer route with a mount's method and path
// is a duplicate fiber would never serve, so Start fails (documented
// parity exception), whatever the registration order. A route the mount's
// patterns do not repeat, such as GET /, starts and wins.
func TestStatic_Conflicts(t *testing.T) {
	t.Parallel()

	answerRoute := routes(func(r fiber.Router) { r.Get("/*", answer("route")) })

	tests := []struct {
		name  string
		confs statics
		// duplicate is the duplicate route Start names, "" when it starts.
		duplicate string
	}{
		{"catch-all, static first", statics{fibersrvr.Static("/", mapSite()), answerRoute}, `"GET /*"`},
		{"catch-all, route first", statics{answerRoute, fibersrvr.Static("/", mapSite())}, `"GET /*"`},
		{
			"the prefix itself", statics{
				fibersrvr.Static("/assets", mapSite()), routes(func(r fiber.Router) { r.Get("/assets", answer("route")) }),
			}, `"GET /assets"`,
		},
		{"GET / beside Static(\"/\")", statics{fibersrvr.Static("/", mapSite()), routes(func(r fiber.Router) { r.Get("/", answer("route")) })}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t)
			for _, c := range tt.confs {
				mustNil(t, srv.Register(c))
			}

			if tt.duplicate == "" {
				base := start(t, srv)
				if res := do(t, http.MethodGet, base+"/", nil, nil); res.body != "route" {
					t.Errorf("GET / = %d %q, want the route's answer", res.status, res.body)
				}

				if res := do(t, http.MethodGet, base+"/app.js", nil, nil); res.body != "console.log(1)" {
					t.Errorf("GET /app.js = %d %q, want the file", res.status, res.body)
				}

				return
			}

			err := startErr(t, srv)
			if !errors.Is(err, srvr.ErrRouteConfig) || !strings.Contains(err.Error(), tt.duplicate) ||
				!strings.Contains(err.Error(), "*fibersrvr.staticConfigurer") {
				t.Errorf("Start = %v, want a route error naming %s and the static configurer", err, tt.duplicate)
			}
		})
	}
}

// staticApp is a fiber server that answers requests through app.Test,
// configured with confs on its first request.
type staticApp struct {
	srv   *fibersrvr.Server
	confs statics
	logs  *logBuffer
}

// newStaticApp returns a staticApp with server options opts and the
// configurers confs registered in order.
func newStaticApp(t *testing.T, opts []srvr.Option, confs ...srvr.RouteConfigurer[fiber.Router]) *staticApp {
	t.Helper()

	srv, logs := newServer(t, opts...)

	return &staticApp{srv: srv, confs: confs, logs: logs}
}

// staticServer returns a staticApp with the configurers confs.
func staticServer(t *testing.T, confs ...srvr.RouteConfigurer[fiber.Router]) *staticApp {
	t.Helper()

	return newStaticApp(t, nil, confs...)
}

// get answers a GET without headers.
func (a *staticApp) get(t *testing.T, target string) response {
	t.Helper()

	return serve(t, a.srv, http.MethodGet, target, nil, nil, a.confs...)
}

// sites are the two builds of the test site most rows run on.
var sites = []struct {
	name  string
	build func(t *testing.T) fs.FS
}{
	{"MapFS", func(*testing.T) fs.FS { return mapSite() }},
	{"DirFS", func(t *testing.T) fs.FS { t.Helper(); return os.DirFS(dirSite(t)) }},
}

// TestStatic_Serve covers static-files / Scenario: Serving a file on fiber.
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

				app := staticServer(t, fibersrvr.Static(tt.prefix, fsys))
				res := serve(t, app.srv, tt.method, tt.path, nil, nil, app.confs...)
				checkServed(t, res, tt.ctype, siteFiles[tt.file], site.name == "DirFS")

				if tt.method == http.MethodHead && res.header.Get("Content-Length") != "14" {
					t.Errorf("HEAD Content-Length = %q, want 14", res.header.Get("Content-Length"))
				}
			})
		}
	}

	t.Run("embed.FS", func(t *testing.T) {
		t.Parallel()

		app := staticServer(t, fibersrvr.Static("/", embedSite(t)))
		checkServed(t, app.get(t, "/app.js"), js, "console.log(1)", false)
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

// TestStatic_Names checks names fiber's router and file server handle
// specially: a "%" in a file name, encoded once, with and without fiber's
// UnescapePath (which decodes the path before the mount sees it, so the
// mount must not decode it again), a prefix matched in another letter
// case (fiber routes without case by default), and a prefix with "+", a
// parameter marker in fiber's route syntax.
func TestStatic_Names(t *testing.T) {
	t.Parallel()

	site := mapSite()
	site["100%.txt"] = &fstest.MapFile{Data: []byte("percent")}
	site["a b.txt"] = &fstest.MapFile{Data: []byte("space")}

	unescape := fibersrvr.WithFiberConfig(func(c *fiber.Config) { c.UnescapePath = true })

	tests := []struct {
		name, prefix, path string
		opts               []srvr.Option
		status             int
		body               string
	}{
		{name: "percent in a name", prefix: "/assets", path: "/assets/100%25.txt", status: 200, body: "percent"},
		{name: "percent in a name with UnescapePath", prefix: "/assets", path: "/assets/100%25.txt", opts: []srvr.Option{unescape}, status: 200, body: "percent"},
		{name: "space in a name", prefix: "/assets", path: "/assets/a%20b.txt", status: 200, body: "space"},
		{name: "space in a name with UnescapePath", prefix: "/assets", path: "/assets/a%20b.txt", opts: []srvr.Option{unescape}, status: 200, body: "space"},
		{name: "double-encoded dot segments with UnescapePath", prefix: "/assets", path: "/assets/%252e%252e/secret.txt", opts: []srvr.Option{unescape}, status: 404},
		{name: "prefix in another letter case, as fiber routes", prefix: "/assets", path: "/ASSETS/app.js", status: 200, body: "console.log(1)"},
		{name: "a name keeps its letter case", prefix: "/assets", path: "/ASSETS/APP.js", status: 404},
		{name: "plus in the prefix", prefix: "/c++", path: "/c++/app.js", status: 200, body: "console.log(1)"},
		{name: "plus in the prefix, the prefix itself", prefix: "/c++", path: "/c++", status: 200, body: "<html>app</html>"},
		{name: "plus in the prefix does not match other paths", prefix: "/c++", path: "/cxx/app.js", status: 404},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newStaticApp(t, tt.opts, fibersrvr.Static(tt.prefix, site))
			res := app.get(t, tt.path)

			if res.status != tt.status || (tt.body != "" && res.body != tt.body) {
				t.Errorf("GET %s = %d %q, want %d %q", tt.path, res.status, res.body, tt.status, tt.body)
			}
		})
	}
}

// TestStatic_Conditional covers static-files / Scenario: Conditional and
// range requests on fiber.
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

			app := staticServer(t, fibersrvr.Static("/assets", fsys))
			res := serve(t, app.srv, http.MethodGet, "/assets/app.js", nil, tt.header, app.confs...)

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
// sources on fiber.
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
			conf := fibersrvr.Static("/", site, opts...)

			if after != nil {
				after()
			}

			app := staticServer(t, conf)
			path := cmp.Or(tt.path, "/")
			res := serve(t, app.srv, http.MethodGet, path, nil, map[string]string{"Accept": "text/html"}, app.confs...)

			if res.status != http.StatusOK || res.body != tt.body || res.header.Get("Cache-Control") != tt.cache {
				t.Errorf("GET %s = %d %q Cache-Control %q, want 200 %q %q", path, res.status, res.body,
					res.header.Get("Cache-Control"), tt.body, tt.cache)
			}
		})
	}
}

// answer returns a fiber handler writing body with status 200.
func answer(body string) fiber.Handler {
	return func(c fiber.Ctx) error { return c.SendString(body) }
}

// TestStatic_Fallback covers static-files / Scenario: Fallback decision
// and Scenario: Fallback under a prefix on fiber.
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

			api := routes(func(r fiber.Router) { r.Get("/api/users", answer("users")) })
			app := staticServer(t, fibersrvr.Static(cmp.Or(tt.prefix, "/"), mapSite(), opts...), api)

			header := map[string]string{}
			if tt.accept != "" {
				header["Accept"] = tt.accept
			}

			res := serve(t, app.srv, tt.method, tt.path, nil, header, app.confs...)
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
// decision, the "fallback after Start" rows, on fiber.
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

			srv, logs := newServer(t)
			mustNil(t, srv.Register(fibersrvr.Static("/", fsys, srvr.WithSPAFallback("index.html"))))
			base := start(t, srv)

			// Served once before the change, so a cache would hold it.
			if res := do(t, http.MethodGet, base+"/dashboard", nil, map[string]string{"Accept": "text/html"}); res.body != "<html>app</html>" {
				t.Fatalf("GET /dashboard before the change = %d %q", res.status, res.body)
			}

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
// fiber.
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

			app := staticServer(t, fibersrvr.Static("/", site, append([]srvr.StaticOption{srvr.WithSPAFallback("index.html")}, tt.opts...)...))
			header := map[string]string{"Accept": "text/html"}
			maps.Copy(header, tt.header)
			res := serve(t, app.srv, http.MethodGet, tt.path, nil, header, app.confs...)

			if res.status != tt.status || res.header.Get("Cache-Control") != tt.cache {
				t.Errorf("GET %s = %d Cache-Control %q, want %d %q", tt.path, res.status, res.header.Get("Cache-Control"), tt.status, tt.cache)
			}
		})
	}
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

// TestStatic_NotFound covers static-files / Scenario: Not found on fiber.
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
				app := staticServer(t, fibersrvr.Static("/assets", fsys, opts...))
				checkNotFound(t, serve(t, app.srv, tt.method, tt.path, nil, nil, app.confs...), tt.path, tt.method == http.MethodHead)
			})
		}
	}
}

// TestStatic_Hostile covers static-files / Scenario: Hostile paths on
// fiber, each target sent as the raw request target over a connection,
// with fiber's default configuration and with UnescapePath, which
// decodes the path before the mount sees it.
func TestStatic_Hostile(t *testing.T) {
	t.Parallel()

	configs := []struct {
		name string
		opts []srvr.Option
	}{
		{"default", nil},
		{"UnescapePath", []srvr.Option{fibersrvr.WithFiberConfig(func(c *fiber.Config) { c.UnescapePath = true })}},
	}

	tests := []struct {
		name, target string
	}{
		{"encoded dot segments", "/assets/%2e%2e/secret.txt"},
		{"encoded slash after dot-dot", "/assets/..%2Fsecret.txt"},
		{"encoded backslash", "/assets/..%5Csecret.txt"},
		{"NUL", "/assets/a%00.txt"},
		{"line feed", "/assets/a%0Ab"},
		{"invalid UTF-8", "/assets/%ff.txt"},
		{"malformed escape", "/assets/%zz"},
		{"double-encoded dot segments", "/assets/%252e%252e/secret.txt"},
		{"raw dot segments", "/assets/../secret.txt"},
		{"empty element", "/assets//app.js"},
	}

	site := os.DirFS(dirSite(t)) // read only: built once for every row

	for _, cfg := range configs {
		// One server per configuration, which every row only reads; it
		// stops when the test ends, after its rows.
		srv, _ := newServer(t, cfg.opts...)
		mustNil(t, srv.Register(fibersrvr.Static("/assets", site, srvr.WithSPAFallback("index.html"))))
		start(t, srv)

		for _, tt := range tests {
			t.Run(cfg.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				res := rawRequest(t, srv.Addr(), http.MethodGet, tt.target, "Accept: text/html\r\n", nil)

				if res.status != http.StatusNotFound || res.header.Get("Location") != "" {
					t.Errorf("status %d Location %q, want 404 without a redirect (body %q)", res.status, res.header.Get("Location"), res.body)
				}

				if strings.Contains(res.body, "SECRET") || strings.Contains(res.body, "<html>app</html>") {
					t.Errorf("body %q serves a file", res.body)
				}
			})
		}
	}
}

// TestStatic_Methods covers static-files / Scenario: Other methods on
// fiber: the router answers other methods with its 405, whose Allow lists
// GET and HEAD of the mount and the methods of the path's routes, and a
// GET or HEAD to a path only another method's route serves is the
// mount's. A server without a mount keeps fiber's answers.
func TestStatic_Methods(t *testing.T) {
	t.Parallel()

	created := routes(func(r fiber.Router) {
		created := func(c fiber.Ctx) error { return c.SendStatus(http.StatusCreated) }
		r.Post("/api/users", created)
		r.Post("/login", created)
		r.Post("/app.js", created)
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
		{name: "another method of a route", method: "PUT", path: "/api/users", route: true, status: 405, allow: "GET, HEAD, POST"},
		{
			name: "GET navigation to a POST-only path falls back", method: "GET", path: "/api/users", accept: "text/html", route: true,
			status: 200, body: "<html>app</html>",
		},
		{name: "GET login page beside POST /login", method: "GET", path: "/login", accept: "text/html", route: true, status: 200, body: "<html>app</html>"},
		{name: "HEAD navigation to a POST-only path", method: "HEAD", path: "/api/users", accept: "text/html", route: true, status: 200},
		{name: "GET to a POST-only path without text/html", method: "GET", path: "/api/users", accept: "application/json", route: true, status: 404},
		{name: "GET to a POST-only path that is a file", method: "GET", path: "/app.js", route: true, status: 200, body: "console.log(1)"},
		{name: "POST outside every mount", prefix: "/assets", method: "POST", path: "/nope", status: 404},
		{
			name: "GET to a POST-only path outside every mount", prefix: "/assets", method: "GET", path: "/api/users", accept: "text/html",
			route: true, status: 405, allow: "POST",
		},
		{name: "GET to a POST-only path without a mount", prefix: "-", method: "GET", path: "/api/users", accept: "text/html", route: true, status: 405, allow: "POST"},
		{name: "a path without a route or a mount", prefix: "-", method: "GET", path: "/app.js", status: 404},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var confs statics
			if tt.prefix != "-" {
				confs = statics{fibersrvr.Static(cmp.Or(tt.prefix, "/"), mapSite(), srvr.WithSPAFallback("index.html"))}
			}

			if tt.route {
				confs = append(confs, created)
			}

			header := map[string]string{}
			if tt.accept != "" {
				header["Accept"] = tt.accept
			}

			app := staticServer(t, confs...)
			res := serve(t, app.srv, tt.method, tt.path, nil, header, app.confs...)

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

// opensAfter returns site whose app.js opens normally n times, then with
// open: a file removed or broken after the mount resolved it, while
// fiber's file server opens it again by name (design D2).
func opensAfter(site fs.FS, n int64, open func() (fs.File, error)) fs.FS {
	var opened atomic.Int64

	return faultFS{base: site, open: map[string]func() (fs.File, error){"app.js": func() (fs.File, error) {
		if opened.Add(1) > n {
			return open()
		}

		return site.Open("app.js")
	}}}
}

// TestStatic_FaultyFS covers static-files / Scenario: Faulty file system
// on fiber, plus the races fiber's reopening by name adds: the file
// removed or broken between the mount's open and fiber's file server's
// two opens.
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
			check: recordHas("error.type", "*fibersrvr_test.MyError"),
		},
		{
			name: "file without Seek", status: 500, level: "ERROR",
			fsys: opening(func() (fs.File, error) {
				f, err := site.Open("app.js")
				return noSeekFile{f}, err
			}),
			check: messageContains("fibersrvr_test.noSeekFile cannot seek"), hidden: "seek",
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
		{
			// The vanish race of design D2: SendFile's 404 is a miss.
			name: "removed between the mount's Open and fiber's", status: 404, level: "WARN",
			fsys:  opensAfter(site, 1, func() (fs.File, error) { return nil, fs.ErrNotExist }),
			check: recordHas("http.route", nil),
		},
		{
			// fasthttp answers any open failure of its own with 404.
			name: "unreadable between the mount's Open and fiber's", status: 404, level: "WARN",
			fsys:  opensAfter(site, 1, func() (fs.File, error) { return nil, fs.ErrPermission }),
			check: recordHas("http.route", nil),
		},
		{
			// fasthttp opens the file a second time for its reader and answers
			// a failure there with a plain-text 500.
			name: "removed between fiber's two opens", status: 500, level: "ERROR",
			fsys:  opensAfter(site, 2, func() (fs.File, error) { return nil, fs.ErrNotExist }),
			check: messageContains("500"), hidden: "Internal Server Error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := staticServer(t, fibersrvr.Static("/assets", tt.fsys))
			res := app.get(t, "/assets/app.js")

			if res.status != tt.status || res.problem == nil {
				t.Fatalf("status %d body %q, want a %d problem", res.status, res.body, tt.status)
			}

			if tt.status == 500 && (res.problem.Title != "Internal Server Error" || res.problem.Detail != "" ||
				res.problem.Instance != "/assets/app.js") {
				t.Errorf("problem = %+v, want the safe 500 problem", res.problem)
			}

			if tt.hidden != "" && strings.Contains(strings.ReplaceAll(res.body, `"title":"Internal Server Error"`, ""), tt.hidden) {
				t.Errorf("body %q contains %q", res.body, tt.hidden)
			}

			rec := app.logs.waitRecord(t, "http.request.failed")
			if rec["level"] != tt.level {
				t.Errorf("level = %v, want %s", rec["level"], tt.level)
			}

			if fn, ok := rec["code.function.name"]; ok {
				t.Errorf("code.function.name = %v, want none: the static mount is srvr's code", fn)
			}

			tt.check(t, rec)
		})
	}
}

// TestStatic_FaultyFSAfterStart covers the Faulty file system row whose
// Read fails after the response started, on fiber: the response is cut
// short and the request is recorded as completed.
func TestStatic_FaultyFSAfterStart(t *testing.T) {
	t.Parallel()

	site := mapSite()
	fsys := faultFS{base: site, open: map[string]func() (fs.File, error){"app.js": func() (fs.File, error) {
		f, err := site.Open("app.js")
		seeker, _ := f.(io.Seeker)

		return &shortReadFile{File: f, seeker: seeker, limit: 10}, err
	}}}

	srv, logs := newServer(t)
	mustNil(t, srv.Register(fibersrvr.Static("/assets", fsys)))
	base := start(t, srv)

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
// fiber.
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

			app := staticServer(t, fibersrvr.Static("/assets", tt.fsys(t, dir)))
			res := app.get(t, "/assets/link.txt")

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

			rec := app.logs.waitRecord(t, "http.request.failed")
			if len(chainOf(rec)) == 0 || rec["level"] != "ERROR" || rec["exception.message"] == nil {
				t.Errorf("record %v, want an ERROR with error.chain and exception.message", rec)
			}
		})
	}
}

// TestStatic_Precedence covers static-files / Scenario: Precedence on
// fiber.
func TestStatic_Precedence(t *testing.T) {
	t.Parallel()

	docs := fstest.MapFS{"guide.html": {Data: []byte("<html>docs mount</html>")}}
	api := routes(func(r fiber.Router) {
		r.Get("/api/users", answer("users"))
		r.Get("/app.js", answer("route app.js"))
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
		{"longest prefix registered first", "/docs/guide.html", true, 200, "<html>docs mount</html>", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := fibersrvr.Static("/", mapSite(), srvr.WithSPAFallback("index.html"))
			confs := statics{root, fibersrvr.Static("/docs", docs), api}
			if tt.routesFirst {
				confs = statics{api, fibersrvr.Static("/docs", docs), root}
			}

			app := staticServer(t, confs...)
			res := serve(t, app.srv, http.MethodGet, tt.path, nil, map[string]string{"Accept": cmp.Or(tt.accept, "text/html")}, app.confs...)

			if res.status != tt.status || (tt.body != "" && res.body != tt.body) {
				t.Errorf("GET %s = %d %q, want %d %q", tt.path, res.status, res.body, tt.status, tt.body)
			}
		})
	}
}

// staticTraceparent is the traceparent the static access rows send.
const staticTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// TestStatic_Access covers static-files / Scenario: Static access records
// on fiber.
func TestStatic_Access(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, path, msg, level, route string
		status                          float64
	}{
		{"GET", "/assets/app.js", "http.request.completed", "INFO", "/assets/*", 200},
		{"GET", "/assets", "http.request.completed", "INFO", "/assets", 200},
		{"GET", "/assets/settings", "http.request.completed", "INFO", "/assets/*", 200},
		{"GET", "/assets/missing.js", "http.request.failed", "WARN", "", 404},
		{"POST", "/assets/app.js", "http.request.failed", "WARN", "", 405},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			dir := dirSite(t)
			app := staticServer(t, fibersrvr.Static("/assets", os.DirFS(dir), srvr.WithSPAFallback("index.html")))
			serve(t, app.srv, tt.method, tt.path, nil, map[string]string{"Accept": "text/html", "traceparent": staticTraceparent}, app.confs...)

			rec := app.logs.waitRecord(t, tt.msg)
			route, _ := rec["http.route"].(string)

			if rec["level"] != tt.level || route != tt.route || rec["http.response.status_code"] != tt.status ||
				rec["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
				t.Errorf("record %v, want level %s route %q status %v and the request's trace", rec, tt.level, tt.route, tt.status)
			}

			if _, ok := rec["code.function.name"]; ok {
				t.Errorf("record %v names a code location: the static mount is srvr's code", rec)
			}

			if strings.Contains(app.logs.String(), dir) {
				t.Errorf("the log names the file system path %q:\n%s", dir, app.logs.String())
			}
		})
	}
}

// staticClock is a server clock that never moves, so the unmatched-record
// limit never refills during a test.
func staticClock(s *engine.Settings) { s.Now = func() time.Time { return time.Unix(0, 0) } }

// TestStatic_Unmatched covers static-files / Scenario: Static misses and
// the unmatched-record limit on fiber, and that a server without a static
// mount keeps fiber's answers and records.
func TestStatic_Unmatched(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		// mw is consumer middleware the root router uses before the static
		// configurer, nil for none.
		mw fiber.Handler
		// noStatic registers a GET /x route instead of the static mount.
		noStatic bool
		status   int
		written  int
		// routeless says the records must have no http.route.
		routeless bool
	}{
		{name: "static misses", method: "GET", status: 404, written: 1},
		{name: "wrong method", method: "PUT", status: 405, written: 1},
		{name: "root middleware calling Next", method: "GET", status: 404, written: 1, mw: func(c fiber.Ctx) error { return c.Next() }},
		{
			name: "root middleware answering 404 before the static mount", method: "GET", status: 404, written: 3,
			mw: func(fiber.Ctx) error { return prblm.ErrNotFound },
		},
		{
			name: "root middleware answering 200 itself after a static miss", method: "GET", status: 200, written: 3, routeless: true,
			mw: func(c fiber.Ctx) error {
				if err := c.Next(); err != nil {
					c.Status(http.StatusOK)

					return c.SendString("custom")
				}

				return nil
			},
		},
		{name: "without a static mount", method: "GET", noStatic: true, status: 404, written: 1},
		{name: "without a static mount, wrong method", method: "PUT", noStatic: true, status: 404, written: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var confs statics
			if tt.mw != nil {
				confs = append(confs, routes(func(r fiber.Router) { r.Use(tt.mw) }))
			}

			if tt.noStatic {
				confs = append(confs, routes(func(r fiber.Router) { r.Get("/x", ok) }))
			} else {
				confs = append(confs, fibersrvr.Static("/", mapSite()))
			}

			app := newStaticApp(t, []srvr.Option{srvr.WithUnmatchedRecordLimit(1, time.Minute), staticClock}, confs...)
			for _, p := range []string{"/nope-1.js", "/nope-2.js", "/nope-3.js"} {
				if res := serve(t, app.srv, tt.method, p, nil, nil, app.confs...); res.status != tt.status || (tt.status >= 400) != (res.problem != nil) {
					t.Fatalf("%s %s = %d %q, want %d, a problem when it fails", tt.method, p, res.status, res.body, tt.status)
				}
			}

			var written int
			for _, r := range app.logs.access(t) {
				if r["http.response.status_code"] == float64(tt.status) {
					written++
				}

				if route, ok := r["http.route"]; ok && tt.routeless {
					t.Errorf("record has http.route %v, want none for a request no route answered:\n%s", route, app.logs.String())
				}
			}

			if written != tt.written {
				t.Errorf("%d access records with status %d, want %d:\n%s", written, tt.status, tt.written, app.logs.String())
			}
		})
	}
}

// TestStatic_Observer covers static-files / Scenario: Observers see static
// requests on fiber, with a recording observer. A static miss is observed
// as a request no route matched: no route and no error, as for fiber's
// own 404 (design D5).
func TestStatic_Observer(t *testing.T) {
	t.Parallel()

	obs := &recordingObserver{}
	app := newStaticApp(t, []srvr.Option{srvr.WithObserver(obs)}, fibersrvr.Static("/assets", mapSite()))
	app.get(t, "/assets/app.js")
	app.get(t, "/assets/missing.js")

	_, results := obs.seen()
	if len(results) != 2 {
		t.Fatalf("%d observed requests, want 2", len(results))
	}

	got := []string{results[0].Route + " " + strconv.Itoa(results[0].Status), results[1].Route + " " + strconv.Itoa(results[1].Status)}
	slices.Sort(got)

	if want := []string{" 404", "/assets/* 200"}; !slices.Equal(got, want) {
		t.Errorf("observed %q, want %q", got, want)
	}

	for _, r := range results {
		if r.Status == http.StatusNotFound && r.Err != nil {
			t.Errorf("static miss observed with Err %v, want nil as for router answers", r.Err)
		}
	}
}

// TestStatic_Middleware covers static-files / Scenario: Consumer
// middleware around static files on fiber, plus the fiber specifics:
// middleware a configurer registered after the static configurer applies
// too (mounts are installed last), and middleware behaves on a mount as
// on a route.
func TestStatic_Middleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mw   fiber.Handler
		// after adds the middleware after the static configurer.
		after  bool
		path   string
		status int
		check  func(t *testing.T, res response, logs *logBuffer)
	}{
		{
			name: "returns ErrUnauthenticated", mw: func(fiber.Ctx) error { return prblm.ErrUnauthenticated },
			status: 401, check: func(t *testing.T, res response, logs *logBuffer) {
				t.Helper()
				if res.problem == nil {
					t.Errorf("body %q, want a problem", res.body)
				}
				recordHas("level", "WARN")(t, logs.waitRecord(t, "http.request.failed"))
			},
		},
		{
			name: "panics", mw: func(fiber.Ctx) error { panic("boom") }, status: 500,
			check: func(t *testing.T, _ response, logs *logBuffer) {
				t.Helper()
				recordHas("error.kind", "panic")(t, logs.waitRecord(t, "http.request.failed"))
			},
		},
		{
			name: "sets a header and calls Next", mw: func(c fiber.Ctx) error { c.Set("X-Mw", "1"); return c.Next() }, status: 200,
			check: checkMwFile,
		},
		{
			name: "added after the static configurer", after: true, mw: func(c fiber.Ctx) error { c.Set("X-Mw", "1"); return c.Next() },
			status: 200, check: checkMwFile,
		},
		{
			name: "sets a status and calls Next, as before a route", mw: func(c fiber.Ctx) error { c.Status(http.StatusTeapot); return c.Next() },
			status: 200, check: func(t *testing.T, res response, logs *logBuffer) {
				t.Helper()
				if res.body != "console.log(1)" {
					t.Errorf("body %q, want the file", res.body)
				}
				recordHas("http.route", "/assets/*")(t, logs.waitRecord(t, "http.request.completed"))
			},
		},
		{
			name: "sees the route after Next", mw: func(c fiber.Ctx) error {
				err := c.Next()
				c.Set("X-Route", c.Route().Path)

				return err
			},
			status: 200, check: func(t *testing.T, res response, _ *logBuffer) {
				t.Helper()
				if res.header.Get("X-Route") != "/assets/*" {
					t.Errorf("X-Route %q, want /assets/*", res.header.Get("X-Route"))
				}
			},
		},
		{
			name: "runs for a path no mount contains", mw: func(c fiber.Ctx) error { c.Set("X-Mw", "1"); return c.Next() },
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

			use := routes(func(r fiber.Router) { r.Use(tt.mw); r.Get("/route", ok) })
			confs := statics{use, fibersrvr.Static("/assets", mapSite())}
			if tt.after {
				confs = statics{fibersrvr.Static("/assets", mapSite()), use}
			}

			app := staticServer(t, confs...)
			res := app.get(t, cmp.Or(tt.path, "/assets/app.js"))

			if res.status != tt.status {
				t.Fatalf("status %d, want %d (body %q)", res.status, tt.status, res.body)
			}

			tt.check(t, res, app.logs)

			if n := len(app.logs.access(t)); n != 1 {
				t.Errorf("%d access records, want 1:\n%s", n, app.logs.String())
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
// on fiber: the mounts' entries come after the routes, longest prefix
// first, whatever the registration order.
func TestStatic_Routes(t *testing.T) {
	t.Parallel()

	const owner = "*fibersrvr.staticConfigurer"

	tests := []struct {
		name   string
		confs  statics
		listed engine.Routes
	}{
		{"root", statics{fibersrvr.Static("/", mapSite())}, engine.Routes{{Method: "GET", Pattern: "/*", Configurer: owner}}},
		{"prefix", statics{fibersrvr.Static("/assets", mapSite())}, engine.Routes{
			{Method: "GET", Pattern: "/assets", Configurer: owner},
			{Method: "GET", Pattern: "/assets/*", Configurer: owner},
		}},
		{"after the routes", statics{fibersrvr.Static("/assets", mapSite()), &UserRoutes{func(r fiber.Router) { r.Get("/x", ok) }}}, engine.Routes{
			{Method: "GET", Pattern: "/x", Configurer: "*fibersrvr_test.UserRoutes"},
			{Method: "GET", Pattern: "/assets", Configurer: owner},
			{Method: "GET", Pattern: "/assets/*", Configurer: owner},
		}},
		{"longest prefix first", statics{fibersrvr.Static("/", mapSite()), fibersrvr.Static("/assets", mapSite())}, engine.Routes{
			{Method: "GET", Pattern: "/assets", Configurer: owner},
			{Method: "GET", Pattern: "/assets/*", Configurer: owner},
			{Method: "GET", Pattern: "/*", Configurer: owner},
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
// serving on fiber.
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

		srv, logs := newServer(t)
		mustNil(t, srv.Register(fibersrvr.Static("/assets", newSite())))
		base := start(t, srv)

		conn, err := new(net.Dialer).DialContext(t.Context(), "tcp", srv.Addr())
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

		if res := do(t, http.MethodGet, base+"/assets/app.js", nil, nil); res.status != http.StatusOK {
			t.Errorf("GET after the disconnect = %d, want 200", res.status)
		}
	})

	t.Run("50 concurrent clients", func(t *testing.T) {
		t.Parallel()

		srv, _ := newServer(t)
		mustNil(t, srv.Register(fibersrvr.Static("/assets", newSite())))
		base := start(t, srv)

		var wg sync.WaitGroup
		for i := range 50 {
			wg.Go(func() {
				path, want := "/assets/app.js", "console.log(1)"
				if i%2 == 0 {
					path, want = "/assets/big.bin", string(big)
				}

				if res := do(t, http.MethodGet, base+path, nil, nil); res.status != http.StatusOK || res.body != want {
					t.Errorf("GET %s = %d with %d bytes, want 200 and the whole file", path, res.status, len(res.body))
				}
			})
		}
		wg.Wait()
	})
}

// TestStatic_SubApp checks that the routes of a sub-app mounted with Use
// win over a static mount, in either registration order, as registered
// routes do (design D4).
func TestStatic_SubApp(t *testing.T) {
	t.Parallel()

	mountSub := routes(func(r fiber.Router) {
		sub := fiber.New()
		sub.Get("/app.js", answer("sub app.js"))
		r.Use("/", sub)
	})

	for _, tt := range []struct {
		name  string
		confs statics
	}{
		{"static first", statics{fibersrvr.Static("/", mapSite()), mountSub}},
		{"sub-app first", statics{mountSub, fibersrvr.Static("/", mapSite())}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := staticServer(t, tt.confs...)
			if res := app.get(t, "/app.js"); res.body != "sub app.js" {
				t.Errorf("GET /app.js = %d %q, want the sub-app's answer", res.status, res.body)
			}

			if res := app.get(t, "/style.css"); res.body != "body{}" {
				t.Errorf("GET /style.css = %d %q, want the file", res.status, res.body)
			}
		})
	}
}

// TestStatic_MiddlewareAsOnRoute checks that consumer middleware on the
// root router behaves on a static mount as on a matched route: the same
// status and the same access record, the route's value aside (slice 4's
// gin lessons, checked on fiber). A served file is a streamed body, so an
// error middleware returns after it keeps the file, as for a route that
// streams (engine-parity: a streaming body has started and an error
// follows), and as net/http and gin keep a file ServeContent wrote.
func TestStatic_MiddlewareAsOnRoute(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	tests := []struct {
		name   string
		mw     fiber.Handler
		status int
		// static is the static mount's status where it differs, with the
		// record marked srvr.response.committed; 0 when it is status.
		static int
	}{
		{"sets a status and returns without Next", func(c fiber.Ctx) error { c.Status(http.StatusUnauthorized); return nil }, 401, 0},
		{"sends a status without Next", func(c fiber.Ctx) error { return c.SendStatus(http.StatusUnauthorized) }, 401, 0},
		{"returns an error without Next", func(fiber.Ctx) error { return boom }, 500, 0},
		{"writes the status it sees", func(c fiber.Ctx) error { return c.SendStatus(c.Response().StatusCode()) }, 200, 0},
		{"calls Next and returns an error", func(c fiber.Ctx) error { _ = c.Next(); return boom }, 500, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := staticServer(t,
				routes(func(r fiber.Router) { r.Use(tt.mw); r.Get("/route", ok) }),
				fibersrvr.Static("/assets", mapSite()))

			want := map[string]int{"/route": tt.status, "/assets/app.js": cmp.Or(tt.static, tt.status)}
			for _, p := range []string{"/route", "/assets/app.js"} {
				if res := app.get(t, p); res.status != want[p] {
					t.Errorf("GET %s = %d %q, want %d", p, res.status, res.body, want[p])
				}
			}

			recs := app.logs.access(t)
			if len(recs) != 2 {
				t.Fatalf("%d access records, want 2:\n%s", len(recs), app.logs.String())
			}

			keys := []string{"msg", "level", "error.type"}
			if tt.static == 0 {
				keys = append(keys, "http.response.status_code", "srvr.response.committed")
			} else if recs[1]["srvr.response.committed"] != true {
				t.Errorf("static record srvr.response.committed = %v, want true", recs[1]["srvr.response.committed"])
			}

			for _, key := range keys {
				if recs[0][key] != recs[1][key] {
					t.Errorf("%s: route %v, static %v, want the same", key, recs[0][key], recs[1][key])
				}
			}

			if _, routed := recs[0]["http.route"]; routed != (recs[1]["http.route"] != nil) {
				t.Errorf("http.route = %v and %v, want both or neither", recs[0]["http.route"], recs[1]["http.route"])
			}
		})
	}
}
