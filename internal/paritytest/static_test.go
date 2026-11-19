package paritytest

import (
	"cmp"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// siteFiles is the test file tree of the static-files design ("Test
// fixture"): name to content, "" for a directory.
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

//go:embed testdata/static
var embedded embed.FS

// staticFixture is the test site, built once for every row and engine and
// only read: an fstest.MapFS, an os.DirFS of a directory holding the same
// tree (secret.txt next to it, link.txt a symbolic link to it), the
// os.Root of that directory and an embed.FS of app.js and index.html.
type staticFixture struct {
	mapSite  fstest.MapFS
	homeSite fstest.MapFS // mapSite with home.html
	dirSite  fs.FS
	root     fs.FS // nil when no symbolic link can be made here
	embedded fs.FS
}

func newStaticFixture(t *testing.T) *staticFixture {
	t.Helper()

	dir := dirSite(t)
	fx := &staticFixture{mapSite: mapSite(), homeSite: mapSite(), dirSite: os.DirFS(dir)}
	fx.homeSite["home.html"] = &fstest.MapFile{Data: []byte("<html>home</html>")}

	if err := os.Symlink(filepath.Join("..", "secret.txt"), filepath.Join(dir, "link.txt")); err == nil {
		root, err := os.OpenRoot(dir)
		must(t, err)
		t.Cleanup(func() { _ = root.Close() })

		fx.root = root.FS()
	}

	sub, err := fs.Sub(embedded, "testdata/static")
	must(t, err)

	fx.embedded = sub

	return fx
}

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

// dirSite writes the test site to a new directory, every file modified at
// siteModTime and secret.txt ("SECRET") next to it, and returns the site's
// directory.
func dirSite(t *testing.T) string {
	t.Helper()

	parent := t.TempDir()
	dir := filepath.Join(parent, "site")

	for name, data := range siteFiles {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if data == "" {
			must(t, os.MkdirAll(p, 0o755))

			continue
		}

		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, []byte(data), 0o644))
		must(t, os.Chtimes(p, siteModTime, siteModTime))
	}

	must(t, os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("SECRET"), 0o644))

	return dir
}

// faultFS is a file system whose Open and Stat may be replaced per name;
// other names go to base.
type faultFS struct {
	base fs.FS
	open map[string]func() (fs.File, error)
	stat map[string]func() (fs.FileInfo, error)
	// noStat makes Stat go through Open.
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

// fsError is a consumer error type; a typed-nil *fsError is the faulty
// file system's error of one row.
type fsError struct{}

func (*fsError) Error() string { return "fs error" }

// staticMount is one Static call: the same prefix, file system and options
// on every engine.
type staticMount struct {
	prefix string
	fsys   fs.FS
	opts   []srvr.StaticOption
	// returned, when set, runs once Static returned.
	returned func()
}

// route is a consumer route registered beside the mounts: GET path,
// answering body as plain text.
type route struct{ path, body string }

// staticPlan is what one engine's server registers, in order, and a change
// made after Start, before the request.
type staticPlan struct {
	mounts      []staticMount
	routes      []route
	routesFirst bool
	after       func(t *testing.T)
}

// mount is the plan of one Static(prefix, fsys, opts...).
func mount(prefix string, fsys fs.FS, opts ...srvr.StaticOption) staticPlan {
	return staticPlan{mounts: []staticMount{{prefix: prefix, fsys: fsys, opts: opts}}}
}

// staticSetup builds a row's plan for one engine; site is the build of the
// test site the row runs on.
type staticSetup func(t *testing.T, fx *staticFixture, site fs.FS) staticPlan

// at mounts the row's site at prefix.
func at(prefix string, opts ...srvr.StaticOption) staticSetup {
	return func(_ *testing.T, _ *staticFixture, site fs.FS) staticPlan { return mount(prefix, site, opts...) }
}

// from mounts the file system fsys picks from the fixture at prefix.
func from(fsys func(fx *staticFixture) fs.FS, prefix string, opts ...srvr.StaticOption) staticSetup {
	return func(_ *testing.T, fx *staticFixture, _ fs.FS) staticPlan { return mount(prefix, fsys(fx), opts...) }
}

// onMapSite mounts f at prefix, with the fixture's MapFS as its base.
func onMapSite(f faultFS, prefix string, opts ...srvr.StaticOption) staticSetup {
	return func(_ *testing.T, fx *staticFixture, _ fs.FS) staticPlan {
		f.base = fx.mapSite

		return mount(prefix, f, opts...)
	}
}

// changedAfterStart mounts at "/" with the SPA fallback a fresh directory
// site, whose index.html change changes after Start, or whose Open of
// index.html fails once change returns true.
func changedAfterStart(change func(t *testing.T, dir string) (broken bool)) staticSetup {
	return func(t *testing.T, _ *staticFixture, _ fs.FS) staticPlan {
		t.Helper()

		dir := dirSite(t)
		site := os.DirFS(dir)

		var broken atomic.Bool

		fsys := faultFS{base: site, open: map[string]func() (fs.File, error){"index.html": func() (fs.File, error) {
			if broken.Load() {
				return nil, &fs.PathError{Op: "open", Path: "index.html", Err: fs.ErrPermission}
			}

			return site.Open("index.html")
		}}}

		p := mount("/", fsys, srvr.WithSPAFallback("index.html"))
		p.after = func(t *testing.T) {
			t.Helper()
			broken.Store(change(t, dir))
		}

		return p
	}
}

const plainText = "text/plain; charset=utf-8"

// register registers p's mounts, each built with static, and routes on
// srv, in p's order.
func register[C any](t *testing.T, srv interface{ Register(C) error }, p staticPlan, static func(string, fs.FS, ...srvr.StaticOption) C, routes C) {
	t.Helper()

	confs := make([]C, 0, len(p.mounts)+1)
	if p.routesFirst {
		confs = append(confs, routes)
	}

	for _, m := range p.mounts {
		confs = append(confs, static(m.prefix, m.fsys, m.opts...))
		if m.returned != nil {
			m.returned()
		}
	}

	if !p.routesFirst {
		confs = append(confs, routes)
	}

	for _, c := range confs {
		must(t, srv.Register(c))
	}
}

// staticServers build one engine's server of a plan, with opts, without
// starting it.
var staticServers = map[string]func(t *testing.T, logs *logBuffer, p staticPlan, opts ...srvr.Option) server{
	"net/http": func(t *testing.T, logs *logBuffer, p staticPlan, opts ...srvr.Option) server {
		t.Helper()

		srv := newNetServer(t, logs, func(*srvr.Group) {}, opts...)
		register(t, srv, p, srvr.Static, srvr.RouteConfigurer[*srvr.Group](groupConfig(func(g *srvr.Group) {
			for _, rt := range p.routes {
				g.GET(rt.path, func(w http.ResponseWriter, _ *http.Request) error {
					w.Header().Set("Content-Type", plainText)
					_, err := io.WriteString(w, rt.body)

					return err
				})
			}
		})))

		return srv
	},
	"gin": func(t *testing.T, logs *logBuffer, p staticPlan, opts ...srvr.Option) server {
		t.Helper()

		srv := newGinServer(t, logs, func(*gin.RouterGroup) {}, opts...)
		register(t, srv, p, ginsrvr.Static, srvr.RouteConfigurer[*gin.RouterGroup](ginConfig(func(g *gin.RouterGroup) {
			for _, rt := range p.routes {
				g.GET(rt.path, func(c *gin.Context) { c.Data(http.StatusOK, plainText, []byte(rt.body)) })
			}
		})))

		return srv
	},
	"fiber": func(t *testing.T, logs *logBuffer, p staticPlan, opts ...srvr.Option) server {
		t.Helper()

		srv := newFiberServer(t, logs, idleWorkerDuration, func(fiber.Router) {}, opts...)
		register(t, srv, p, fibersrvr.Static, srvr.RouteConfigurer[fiber.Router](fiberConfig(func(router fiber.Router) {
			for _, rt := range p.routes {
				router.Get(rt.path, func(c fiber.Ctx) error {
					c.Set("Content-Type", plainText)

					return c.SendString(rt.body)
				})
			}
		})))

		return srv
	},
}

// staticServer builds engine's server of p, logging to logs, with opts,
// without starting it.
func staticServer(t *testing.T, engine string, logs *logBuffer, p staticPlan, opts ...srvr.Option) server {
	t.Helper()

	return staticServers[engine](t, logs, p, opts...)
}

// staticHeaders are the response headers the static-files spec names.
var staticHeaders = []string{
	"Allow", "Cache-Control", "Content-Length", "Content-Range", "Content-Type", "ETag", "Last-Modified", "Location", "Vary",
}

// staticResult is what one engine answered a row and logged.
type staticResult struct {
	// fields are the *srvr.ConfigError fields of a failed Start, sorted.
	fields []string

	status int
	header map[string][]string
	body   string
	// cut reports a body shorter than the response's Content-Length.
	cut bool

	msg, level any
	keys       []string
}

// staticRow is one "every engine" Examples row of the static-files spec.
type staticRow struct {
	name  string
	setup staticSetup
	// sites runs the row on both builds of the site, the MapFS and the
	// DirFS; otherwise it runs once, on the MapFS.
	sites bool
	// needsLink skips the row where no symbolic link can be made.
	needsLink   bool
	routes      []route
	routesFirst bool

	// method is the request method, GET when empty.
	method, target string
	header         map[string]string

	// unlike lists problem members gin and fiber answer differently for
	// reasons outside static files, each named by a notes.md entry; they
	// and Content-Length are left out of the comparison, every other
	// member is compared.
	unlike []string

	// status is net/http's status; fields, when set, are the
	// *srvr.ConfigError fields of net/http's Start error, and no request
	// is sent.
	status int
	fields []string
}

// TestStaticParity covers static-files: every Examples row the spec runs
// on every engine. The fixture is built once; each row starts one server
// per engine and compares the status, the headers the spec names, the
// body, and the access record's msg, level and key set with net/http's.
// net/http's status (or Start error) is checked against the row.
func TestStaticParity(t *testing.T) {
	t.Parallel()

	fx := newStaticFixture(t)
	builds := []struct {
		name string
		fsys fs.FS
	}{{"MapFS", fx.mapSite}, {"DirFS", fx.dirSite}}

	for _, tt := range staticRows() {
		sites := builds[:1]
		if tt.sites {
			sites = builds
		}

		for _, site := range sites {
			name := tt.name
			if tt.sites {
				name += "/" + site.name
			}

			t.Run(name, func(t *testing.T) {
				t.Parallel()

				if tt.needsLink && fx.root == nil {
					t.Skip("cannot create a symbolic link here")
				}

				var reference staticResult

				for i, e := range engines {
					got := runStaticRow(t, tt, e.name, fx, site.fsys)
					got.dropMembers(t, tt.unlike)

					if i == 0 {
						reference = got
						checkStaticReference(t, tt, got)

						continue
					}

					if !reflect.DeepEqual(got, reference) {
						t.Errorf("%s differs from net/http:\n%s: %+v\nnet/http: %+v", e.name, e.name, got, reference)
					}
				}
			})
		}
	}
}

// dropMembers removes members from a problem body, and Content-Length
// with them.
func (r *staticResult) dropMembers(t *testing.T, members []string) {
	t.Helper()

	if len(members) == 0 {
		return
	}

	var p map[string]any
	must(t, json.Unmarshal([]byte(r.body), &p))

	for _, m := range members {
		delete(p, m)
	}

	b, err := json.Marshal(p)
	must(t, err)

	r.body = string(b)
	delete(r.header, "Content-Length")
}

// checkStaticReference fails the test unless net/http's result matches
// the row.
func checkStaticReference(t *testing.T, tt staticRow, got staticResult) {
	t.Helper()

	if tt.fields != nil {
		want := slices.Sorted(slices.Values(tt.fields))
		if !slices.Equal(got.fields, want) {
			t.Fatalf("net/http Start error fields %q, want %q", got.fields, want)
		}

		return
	}

	if got.status != tt.status {
		t.Fatalf("net/http %s %s = %d %q, want %d", tt.method, tt.target, got.status, got.body, tt.status)
	}
}

// runStaticRow starts engine's server of row tt, sends its request and
// returns the answer and the access record.
func runStaticRow(t *testing.T, tt staticRow, engine string, fx *staticFixture, site fs.FS) staticResult {
	t.Helper()

	p := tt.setup(t, fx, site)
	p.routes, p.routesFirst = tt.routes, tt.routesFirst
	logs := &logBuffer{}
	srv := staticServer(t, engine, logs, p)

	if tt.fields != nil {
		fields := configFields(startFailure(t, srv))
		slices.Sort(fields)

		return staticResult{fields: fields}
	}

	base := run(t, srv)
	if p.after != nil {
		p.after(t)
	}

	header := map[string]string{"traceparent": traceparent}
	maps.Copy(header, tt.header)

	method := cmp.Or(tt.method, http.MethodGet)

	res, body, err := send(t, method, base+tt.target, header)
	if res == nil {
		t.Fatalf("%s: %s %s: %v", engine, method, tt.target, err)
	}

	got := staticResult{status: res.StatusCode, header: map[string][]string{}, body: string(body)}
	for _, h := range staticHeaders {
		if v := res.Header.Values(h); v != nil {
			got.header[h] = v
		}
	}

	if err != nil {
		// The body was cut short: its bytes depend on the engine's
		// buffering, the cut does not.
		got.cut, got.body = true, ""
	}

	r := one(t, logs)
	got.msg, got.level, got.keys = r["msg"], r["level"], flatKeys(r)

	return got
}

// staticRows returns every "every engine" Examples row of the static-files
// spec, named "<scenario>/<row>".
func staticRows() []staticRow {
	const (
		browser  = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
		modified = "Wed, 18 Nov 2026 10:00:00 GMT"
		cache60  = "public, max-age=60"
		forever  = "public, max-age=31536000, immutable"
	)

	html := map[string]string{"Accept": "text/html"}
	accept := func(v string) map[string]string { return map[string]string{"Accept": v} }
	dirFS := func(fx *staticFixture) fs.FS { return fx.dirSite }
	embedFS := func(fx *staticFixture) fs.FS { return fx.embedded }
	homeFS := func(fx *staticFixture) fs.FS { return fx.homeSite }
	fallback := srvr.WithSPAFallback("index.html")

	var decoded srvr.StaticConfig
	if err := json.Unmarshal([]byte(`{"spa_fallback":"index.html","cache_control":"public, max-age=60",`+
		`"index_cache_control":"no-store","index_names":["index.html"]}`), &decoded); err != nil {
		panic(err)
	}

	// mounts mounts each of ms, those without a file system on the MapFS
	// site.
	mounts := func(ms ...staticMount) staticSetup {
		return func(_ *testing.T, fx *staticFixture, _ fs.FS) staticPlan {
			out := slices.Clone(ms)
			for i := range out {
				if out[i].fsys == nil {
					out[i].fsys = fx.mapSite
				}
			}

			return staticPlan{mounts: out}
		}
	}

	// without mounts at prefix without a file system, with opts.
	without := func(prefix string, opts ...srvr.StaticOption) staticSetup {
		return func(*testing.T, *staticFixture, fs.FS) staticPlan { return mount(prefix, nil, opts...) }
	}

	api := []route{{"/api/users", "users"}}
	docs := fstest.MapFS{"guide.html": {Data: []byte("<html>docs mount</html>")}}

	// Row builders, one per scenario whose rows share a setup.
	serving := func(name, prefix, method, target string) staticRow {
		return staticRow{name: "Serving a file/" + name, setup: at(prefix), sites: true, method: method, target: target, status: 200}
	}
	missing := func(name, method, target string, opts ...srvr.StaticOption) staticRow {
		opts = append([]srvr.StaticOption{srvr.WithStaticCacheControl(cache60)}, opts...)

		return staticRow{name: "Not found/" + name, setup: at("/assets", opts...), sites: true, method: method, target: target, status: 404}
	}
	conditional := func(name string, fsys func(*staticFixture) fs.FS, header map[string]string, status int) staticRow {
		return staticRow{name: "Conditional/" + name, setup: from(fsys, "/assets"), target: "/assets/app.js", header: header, status: status}
	}
	configured := func(name string, setup staticSetup, target string) staticRow {
		return staticRow{name: "Configuration sources/" + name, setup: setup, target: target, status: 200}
	}
	rejected := func(name string, setup staticSetup, fields ...string) staticRow {
		return staticRow{name: "Rejected static configuration/" + name, setup: setup, fields: fields}
	}
	faulty := func(name string, f faultFS, status int) staticRow {
		return staticRow{name: "Faulty file system/" + name, setup: onMapSite(f, "/assets"), target: "/assets/app.js", status: status}
	}
	navigate := func(name, method, target string, header map[string]string, status int) staticRow {
		return staticRow{name: "Fallback decision/" + name, setup: at("/", fallback), method: method, target: target, header: header, status: status}
	}
	underPrefix := func(name, target string, status int) staticRow {
		return staticRow{name: "Fallback under a prefix/" + name, setup: at("/app", fallback), routes: api, target: target, header: html, status: status}
	}
	cached := func(name, target string, status int, opts ...srvr.StaticOption) staticRow {
		return staticRow{name: "Cache headers/" + name, setup: at("/", append([]srvr.StaticOption{fallback}, opts...)...), target: target, header: html, status: status}
	}
	precedence := func(name, target string, routesFirst bool, header map[string]string, status int) staticRow {
		return staticRow{
			name: "Precedence/" + name, routes: append(slices.Clone(api), route{"/app.js", "route app.js"}), routesFirst: routesFirst,
			setup:  mounts(staticMount{prefix: "/", opts: []srvr.StaticOption{fallback}}, staticMount{prefix: "/docs", fsys: docs}),
			target: target, header: header, status: status,
		}
	}

	// appJS faults app.js: open opens it, stat stats it.
	appJS := fstest.MapFS{"app.js": {Data: []byte(siteFiles["app.js"])}}
	opening := func(open func() (fs.File, error)) faultFS {
		return faultFS{open: map[string]func() (fs.File, error){"app.js": open}}
	}
	statting := func(err error) faultFS {
		return faultFS{stat: map[string]func() (fs.FileInfo, error){"app.js": func() (fs.FileInfo, error) { return nil, err }}}
	}
	// fallbackOpening faults index.html, the fallback, with Stat through
	// Open.
	fallbackOpening := func(open func() (fs.File, error)) faultFS {
		return faultFS{open: map[string]func() (fs.File, error){"index.html": open}, noStat: true}
	}

	var typedNilEmbed *embed.FS

	long := "/assets/" + strings.Repeat("a", 4096) + ".js"

	return []staticRow{
		// Scenario: Serving a file.
		serving("GET app.js", "/assets", "", "/assets/app.js"),
		serving("HEAD app.js", "/assets", http.MethodHead, "/assets/app.js"),
		serving("style.css", "/assets", "", "/assets/style.css"),
		serving("logo.svg", "/assets", "", "/assets/logo.svg"),
		serving("data.json", "/assets", "", "/assets/data.json"),
		serving("sniffed", "/assets", "", "/assets/notes"),
		serving("encoded UTF-8", "/assets", "", "/assets/caf%C3%A9.txt"),
		serving("encoded slash below the prefix", "/assets", "", "/assets/a%2Fb.txt"),
		serving("prefix with slash", "/assets", "", "/assets/"),
		serving("prefix without slash", "/assets", "", "/assets"),
		serving("index file named", "/assets", "", "/assets/index.html"),
		serving("directory", "/assets", "", "/assets/docs"),
		serving("directory with slash", "/assets", "", "/assets/docs/"),
		serving("trailing-slash prefix", "/assets/", "", "/assets/app.js"),
		serving("root", "/", "", "/"),
		serving("root nested file", "/", "", "/docs/guide.html"),
		{name: "Serving a file/embed.FS", setup: from(embedFS, "/"), target: "/app.js", status: 200},

		// Scenario: Conditional and range requests.
		conditional("not modified", dirFS, map[string]string{"If-Modified-Since": modified}, 304),
		conditional("modified since", dirFS, map[string]string{"If-Modified-Since": "Tue, 17 Nov 2026 10:00:00 GMT"}, 200),
		conditional("not a date", dirFS, map[string]string{"If-Modified-Since": "not a date"}, 200),
		conditional("embedded file", embedFS, map[string]string{"If-Modified-Since": modified}, 200),
		conditional("range", dirFS, map[string]string{"Range": "bytes=0-3"}, 200),
		conditional("unsatisfiable range", dirFS, map[string]string{"Range": "bytes=999-"}, 200),
		conditional("If-Match", dirFS, map[string]string{"If-Match": `"x"`}, 200),
		conditional("If-Unmodified-Since", dirFS, map[string]string{"If-Unmodified-Since": "Mon, 16 Nov 2026 10:00:00 GMT"}, 200),
		conditional("If-None-Match", dirFS, map[string]string{"If-None-Match": "*"}, 200),

		// Scenario: Configuration sources.
		configured("no options", at("/"), "/app.js"),
		configured("cache option", at("/", srvr.WithStaticCacheControl(forever)), "/app.js"),
		configured("cache from the config struct", at("/", srvr.WithStaticConfig(srvr.StaticConfig{CacheControl: cache60})), "/app.js"),
		configured("a later option wins", at("/", srvr.WithStaticConfig(srvr.StaticConfig{CacheControl: cache60}), srvr.WithStaticCacheControl("no-store")), "/app.js"),
		configured("the config struct replaces every field", at("/", srvr.WithStaticCacheControl("no-store"), srvr.WithStaticConfig(srvr.StaticConfig{})), "/app.js"),
		configured("index names option", from(homeFS, "/", srvr.WithStaticIndexNames("home.html")), "/"),
		configured("index names from the config struct", from(homeFS, "/", srvr.WithStaticConfig(srvr.StaticConfig{IndexNames: []string{"home.html"}})), "/"),
		configured("first existing index name wins", at("/", srvr.WithStaticIndexNames("home.html", "index.html")), "/"),
		configured("config changed after Static returned", func(_ *testing.T, fx *staticFixture, _ fs.FS) staticPlan {
			c := srvr.StaticConfig{IndexNames: []string{"index.html"}}
			p := mount("/", fx.mapSite, srvr.WithStaticConfig(c))
			p.mounts[0].returned = func() { c.IndexNames[0] = "x.html" }

			return p
		}, "/"),
		{name: "Configuration sources/config decoded from JSON", setup: at("/", srvr.WithStaticConfig(decoded)), target: "/dashboard", header: html, status: 200},

		// Scenario: Rejected static configuration.
		rejected("nil fs", without("/"), "Static.FS"),
		rejected("typed-nil embed.FS", func(*testing.T, *staticFixture, fs.FS) staticPlan { return mount("/", typedNilEmbed) }, "Static.FS"),
		rejected("empty prefix", mounts(staticMount{prefix: ""}), "Static.Prefix"),
		rejected("relative prefix", mounts(staticMount{prefix: "assets"}), "Static.Prefix"),
		rejected("prefix with a wildcard", mounts(staticMount{prefix: "/assets/{name}"}), "Static.Prefix"),
		rejected("prefix with a star", mounts(staticMount{prefix: "/assets/*"}), "Static.Prefix"),
		rejected("prefix with a parameter", mounts(staticMount{prefix: "/assets/:id"}), "Static.Prefix"),
		rejected("prefix with a dot-dot", mounts(staticMount{prefix: "/a/../b"}), "Static.Prefix"),
		rejected("prefix with an empty element", mounts(staticMount{prefix: "/a//b"}), "Static.Prefix"),
		rejected("prefix with a NUL", mounts(staticMount{prefix: "/a\x00"}), "Static.Prefix"),
		rejected("nil option", mounts(staticMount{prefix: "/", opts: []srvr.StaticOption{nil}}), "StaticOption"),
		rejected("missing fallback", at("/", srvr.WithSPAFallback("missing.html")), "Static.SPAFallback"),
		rejected("directory fallback", at("/", srvr.WithSPAFallback("docs")), "Static.SPAFallback"),
		rejected("escaping fallback", at("/", srvr.WithSPAFallback("../index.html")), "Static.SPAFallback"),
		rejected("rooted fallback", at("/", srvr.WithSPAFallback("/index.html")), "Static.SPAFallback"),
		rejected("cache control with a header injection", at("/", srvr.WithStaticCacheControl("max-age=60\r\nSet-Cookie: a=b")), "Static.CacheControl"),
		rejected("index cache control with a NUL", at("/", srvr.WithStaticIndexCacheControl("no-cache\x00")), "Static.IndexCacheControl"),
		rejected("empty index name", at("/", srvr.WithStaticIndexNames("")), "Static.IndexNames"),
		rejected("index name with a slash", at("/", srvr.WithStaticIndexNames("a/index.html")), "Static.IndexNames"),
		rejected("dot-dot index name", at("/", srvr.WithStaticIndexNames("..")), "Static.IndexNames"),
		rejected("fallback Open panics", onMapSite(fallbackOpening(func() (fs.File, error) { panic("boom") }), "/", fallback), "Static.SPAFallback"),
		rejected("fallback Open denied", onMapSite(fallbackOpening(func() (fs.File, error) {
			return nil, &fs.PathError{Op: "open", Path: "index.html", Err: fs.ErrPermission}
		}), "/", fallback), "Static.SPAFallback"),
		rejected("same prefix twice", mounts(staticMount{prefix: "/assets"}, staticMount{prefix: "/assets"}), "Static.Prefix"),
		rejected("root twice", mounts(staticMount{prefix: "/"}, staticMount{prefix: "/", fsys: docs}), "Static.Prefix"),

		// Scenario: Several invalid settings.
		{
			name: "Several invalid settings/prefix, FS and cache control", fields: []string{"Static.Prefix", "Static.FS", "Static.CacheControl"},
			setup: without("assets", srvr.WithStaticCacheControl("a\nb")),
		},

		// Scenario: Not found.
		missing("missing", "", "/assets/missing.js"),
		missing("HEAD missing", http.MethodHead, "/assets/missing.js"),
		missing("directory without index", "", "/assets/empty/"),
		missing("nested directory without index", "", "/assets/empty/x/"),
		missing("dotfile", "", "/assets/.env"),
		missing("dot-directory file", "", "/assets/.git/config"),
		missing("hidden file", "", "/assets/assets/.hidden.js"),
		missing("dot-directory", "", "/assets/.git/"),
		missing("file named as a directory", "", "/assets/app.js/"),
		missing("below a file", "", "/assets/index.html/x"),
		missing("index name missing", "", "/assets/", srvr.WithStaticIndexNames("home.html")),
		missing("4096-character name", "", long),

		// Scenario: Symbolic links.
		{name: "Symbolic links/os.Root refuses the symbolic link", setup: from(func(fx *staticFixture) fs.FS { return fx.root }, "/assets"), needsLink: true, target: "/assets/link.txt", status: 500},
		{name: "Symbolic links/os.DirFS follows the symbolic link", setup: from(dirFS, "/assets"), needsLink: true, target: "/assets/link.txt", status: 200},

		// Scenario: Faulty file system.
		faulty("permission denied from Open", opening(func() (fs.File, error) {
			return nil, &fs.PathError{Op: "open", Path: "app.js", Err: fs.ErrPermission}
		}), 500),
		faulty("error from Stat", statting(errors.New("disk on fire")), 500),
		faulty("panic in Open", opening(func() (fs.File, error) { panic("boom") }), 500),
		faulty("typed-nil error from Open", opening(func() (fs.File, error) { return nil, (*fsError)(nil) }), 500),
		faulty("file without Seek", opening(func() (fs.File, error) {
			f, err := appJS.Open("app.js")

			return noSeekFile{f}, err
		}), 500),
		faulty("Read fails after 10 bytes", opening(func() (fs.File, error) {
			f, err := appJS.Open("app.js")
			seeker, _ := f.(io.Seeker)

			return &shortReadFile{File: f, seeker: seeker, limit: 10}, err
		}), 200),
		faulty("wrapped not-exist", statting(fmt.Errorf("lookup: %w", fs.ErrNotExist)), 404),
		faulty("removed between resolution and serving", opening(func() (fs.File, error) { return nil, fs.ErrNotExist }), 404),

		// Scenario: Fallback decision.
		navigate("browser navigation", "", "/dashboard", accept(browser), 200),
		navigate("deep link", "", "/users/42/edit", html, 200),
		navigate("HEAD navigation", http.MethodHead, "/dashboard", html, 200),
		navigate("query and upper case", "", "/dashboard?tab=2", accept("TEXT/HTML"), 200),
		navigate("low quality html", "", "/dashboard", accept("application/json, text/html;q=0.1"), 200),
		navigate("directory without index", "", "/empty/", html, 200),
		navigate("no Accept", "", "/dashboard", nil, 404),
		navigate("fetch default", "", "/dashboard", accept("*/*"), 404),
		navigate("any text", "", "/dashboard", accept("text/*"), 404),
		navigate("JSON", "", "/dashboard", accept("application/json"), 404),
		navigate("html refused", "", "/dashboard", accept("text/html;q=0"), 404),
		navigate("missing asset", "", "/assets/app-9999.js", html, 404),
		navigate("dot rule", "", "/users/john.doe", html, 404),
		navigate("hidden file", "", "/.env", html, 404),
		func() staticRow {
			// fiber removes the decoded dot segments before routing, for
			// every route, so its instance is "/secret" (notes.md "fiber:
			// a problem's instance is the path with encoded dot segments
			// removed").
			r := navigate("hostile path", "", "/%2e%2e/secret", html, 404)
			r.unlike = []string{"instance"}

			return r
		}(),
		navigate("POST navigation", http.MethodPost, "/dashboard", html, 405),
		{name: "Fallback decision/no fallback configured", setup: at("/"), target: "/dashboard", header: html, status: 404},
		{
			name: "Fallback decision/fallback deleted after Start", target: "/dashboard", header: html, status: 404,
			setup: changedAfterStart(func(t *testing.T, dir string) bool {
				t.Helper()
				must(t, os.Remove(filepath.Join(dir, "index.html")))

				return false
			}),
		},
		{
			name: "Fallback decision/fallback replaced after Start", target: "/dashboard", header: html, status: 200,
			setup: changedAfterStart(func(t *testing.T, dir string) bool {
				t.Helper()
				name := filepath.Join(dir, "index.html")
				must(t, os.WriteFile(name, []byte("<html>v2</html>"), 0o644))
				// The same time on every engine, or Last-Modified differs
				// when the engines' writes straddle a second.
				must(t, os.Chtimes(name, siteModTime.Add(time.Hour), siteModTime.Add(time.Hour)))

				return false
			}),
		},
		{
			name: "Fallback decision/fallback unreadable after Start", target: "/dashboard", header: html, status: 500,
			setup: changedAfterStart(func(*testing.T, string) bool { return true }),
		},

		// Scenario: Fallback under a prefix. Outside the prefix the router
		// answers; gin's and fiber's 404 problem has no detail
		// (otel-observability notes.md "Unmatched 404 problems differ
		// between net/http and gin/fiber", deferred).
		underPrefix("below the prefix", "/app/settings", 200),
		underPrefix("the prefix itself", "/app", 200),
		func() staticRow {
			r := underPrefix("outside the prefix", "/other", 404)
			r.unlike = []string{"detail"}
			return r
		}(),
		underPrefix("the API route", "/api/users", 200),
		func() staticRow {
			r := underPrefix("outside the prefix under the API", "/api/nope", 404)
			r.unlike = []string{"detail"}

			return r
		}(),

		// Scenario: Cache headers.
		cached("root", "/", 200),
		cached("index file named", "/index.html", 200),
		cached("directory index", "/docs/", 200),
		cached("fallback", "/dashboard", 200),
		cached("other file", "/app.js", 200),
		cached("miss", "/missing.js", 404),
		cached("hashed asset", "/assets/app-3f2a.js", 200, srvr.WithStaticCacheControl(forever)),
		cached("index keeps no-cache", "/index.html", 200, srvr.WithStaticCacheControl(forever)),
		cached("miss keeps prblm's", "/missing.js", 404, srvr.WithStaticCacheControl(forever)),
		cached("index cache option", "/dashboard", 200, srvr.WithStaticIndexCacheControl("no-store")),
		cached("index cache from the config struct", "/", 200,
			srvr.WithStaticConfig(srvr.StaticConfig{SPAFallback: "index.html", IndexCacheControl: "public, max-age=0, must-revalidate"})),
		{
			name: "Cache headers/kept on 304", target: "/app.js", status: 304,
			header: map[string]string{"Accept": "text/html", "If-Modified-Since": modified},
			setup:  from(dirFS, "/", fallback, srvr.WithStaticCacheControl(cache60)),
		},
		cached("empty index cache means the default", "/", 200, srvr.WithStaticIndexCacheControl("")),

		// Scenario: Precedence.
		precedence("route, static first", "/api/users", false, html, 200),
		precedence("route, routes first", "/api/users", true, html, 200),
		precedence("route over a file, static first", "/app.js", false, html, 200),
		precedence("route over a file, routes first", "/app.js", true, html, 200),
		precedence("navigation the API does not answer falls back", "/api/nope", false, html, 200),
		precedence("API miss without text/html", "/api/nope", false, accept("application/json"), 404),
		precedence("longest prefix", "/docs/guide.html", false, html, 200),
		precedence("longest prefix miss", "/docs/missing", false, html, 404),
		precedence("shorter prefix", "/style.css", false, html, 200),
	}
}
