package paritytest

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"go.uber.org/goleak"
)

// leakOptions are the goleak options of the paritytest test binary: the
// Date refresher fasthttp (observed with v1.78.0) starts once per process
// and never stops, as in fibersrvr. It is ignored with IgnoreAnyFunction,
// because the goroutine has time.Sleep on top, which IgnoreTopFunction
// could only match by ignoring every sleeping goroutine. fasthttp's
// idle-worker cleaner is not ignored here: fiberServer shortens its sleep
// so it ends after Shutdown. No gin handler is ignored here: the stuck
// cyclic c.Error handler runs in package gincycle, its own test binary.
var leakOptions = []goleak.Option{
	goleak.IgnoreAnyFunction(fasthttpServerDate),
}

// fasthttpServerDate is the function leakOptions ignores.
const fasthttpServerDate = "github.com/valyala/fasthttp.updateServerDate.func1"

// TestMain fails the test binary when a goroutine is still running after
// its tests passed.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m, leakOptions...) }

// findLeaks runs goleak.Find with leakOptions plus extra, without
// appending to the shared leakOptions slice.
func findLeaks(extra ...goleak.Option) error {
	return goleak.Find(slices.Concat(leakOptions, extra)...)
}

// names reports whether err is a leak report naming fn.
func names(err error, fn string) bool { return err != nil && strings.Contains(err.Error(), fn) }

// blockUntil blocks until ch is closed. It is a named function so a leak
// report names it.
func blockUntil(ch <-chan struct{}) { <-ch }

// startBlocked starts a goroutine running blockUntil and returns the
// function that ends it.
func startBlocked() (release func()) {
	ch := make(chan struct{})
	go blockUntil(ch)

	return func() { close(ch) }
}

// TestLeakCheck proves the leak check this module's test binary runs
// reports a goroutine left running. Not parallel: goleak.Find sees every
// goroutine of the process.
func TestLeakCheck(t *testing.T) {
	tests := []struct {
		name  string
		start func(t *testing.T) (release func())
		want  string // a function the report names; "" for no report
	}{
		{"blocked on a channel that is never closed", func(*testing.T) func() { return startBlocked() }, "blockUntil"},
		{"blocked on a channel the test closes at cleanup", func(t *testing.T) func() {
			t.Helper()
			t.Cleanup(startBlocked())
			return nil
		}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := goleak.IgnoreCurrent()

			var release func()
			t.Run("run", func(t *testing.T) { release = tt.start(t) })

			err := findLeaks(before)
			if release != nil {
				release()
			}

			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("leak reported: %v", err)
			case tt.want != "" && !names(err, tt.want):
				t.Fatalf("leak report = %v, want one naming %s", err, tt.want)
			}
		})
	}
}

// blockedHandler returns a gin handler that closes entered and blocks until
// release is closed. It is a named function so a leak report names it.
func blockedHandler(entered chan<- struct{}, release <-chan struct{}) gin.HandlerFunc {
	return func(*gin.Context) {
		close(entered)
		<-release
	}
}

// TestLeakIgnores covers test-quality / Scenario: Each ignore is needed and
// narrow (paritytest rows of both tables). Not parallel: goleak.Find sees
// every goroutine of the process.
func TestLeakIgnores(t *testing.T) {
	tests := []struct {
		name string
		// run starts the goroutine inside a subtest, whose cleanups stop
		// the server, and returns the function that ends it, or nil.
		run func(t *testing.T) (release func())
		// fn is the function the check without leakOptions reports.
		fn string
		// reported tells whether the check with leakOptions still reports
		// fn.
		reported bool
	}{
		{"fasthttp Date refresher", runFiber, fasthttpServerDate, false},
		{"a gin handler blocked on a channel never closed", runBlockedGin, "paritytest.blockedHandler", true},
		{"fasthttp idle-worker cleaner at the default duration", runFiberDefaultIdle, fasthttpWorkerCleaner, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := goleak.IgnoreCurrent()

			var release func()
			t.Run("run", func(t *testing.T) { release = tt.run(t) })
			if release != nil {
				defer func() {
					release()
					if err := findLeaks(before); err != nil {
						t.Errorf("goroutines did not end after the release: %v", err)
					}
				}()
			}

			if err := goleak.Find(); !names(err, tt.fn) {
				t.Fatalf("without leakOptions the check = %v, want a report naming %s", err, tt.fn)
			}

			if tt.reported {
				if err := findLeaks(before); !names(err, tt.fn) {
					t.Fatalf("with leakOptions the check = %v, want a report naming %s", err, tt.fn)
				}

				return
			}

			if err := findLeaks(before); err != nil {
				t.Fatalf("with leakOptions the check reports: %v", err)
			}

			// Without IgnoreCurrent: the Date refresher starts once per
			// process and may predate this test.
			if err := findLeaks(); names(err, tt.fn) {
				t.Fatalf("with leakOptions the check still reports %s: %v", tt.fn, err)
			}
		})
	}
}

// runFiber serves GET /ok on a fiber server, which t's cleanup stops.
func runFiber(t *testing.T) func() {
	t.Helper()

	base, _ := fiberServer(t, okFiber)
	getOK(t, base)

	return nil
}

// okFiber registers GET /ok answering 200.
func okFiber(r fiber.Router) {
	r.Get("/ok", func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
}

// getOK sends GET /ok to base and fails t unless it answers 200.
func getOK(t *testing.T, base string) {
	t.Helper()

	res, _, err := send(t, http.MethodGet, base+"/ok", nil)
	must(t, err)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /ok = %d", res.StatusCode)
	}
}

// runFiberDefaultIdle serves GET /ok, as runFiber does, on a fiber server
// left at fasthttp's default MaxIdleWorkerDuration (10s), whose idle-worker
// cleaner outlives the Shutdown t's cleanup runs. The returned function
// waits until the cleaner's next wake-up has ended it, so the test
// binary's own check does not report it.
func runFiberDefaultIdle(t *testing.T) func() {
	t.Helper()

	base, _ := fiberServerIdle(t, 0, okFiber)
	getOK(t, base)

	return func() {
		for deadline := time.Now().Add(cleanerWait); time.Now().Before(deadline); {
			if !names(findLeaks(), fasthttpWorkerCleaner) {
				return
			}
		}
	}
}

const (
	// fasthttpWorkerCleaner is fasthttp's idle-worker cleaner (v1.78.0).
	fasthttpWorkerCleaner = "github.com/valyala/fasthttp.(*workerPool).Start.func2"
	// cleanerWait bounds the wait for a cleaner at fasthttp's default
	// MaxIdleWorkerDuration (10s) to end: one full sleep plus a margin.
	cleanerWait = 10*time.Second + 2*time.Second
)

// runBlockedGin sends GET /block to a gin server whose handler blocks until
// the returned function is called; the request gives up once the handler
// is entered and t's cleanup stops the server after a 100ms drain.
func runBlockedGin(t *testing.T) func() {
	t.Helper()

	entered, release := make(chan struct{}), make(chan struct{})
	base, _ := ginServer(t, func(g *gin.RouterGroup) { g.GET("/block", blockedHandler(entered, release)) },
		srvr.WithShutdownTimeout(100*time.Millisecond))

	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
		}
		cancel()
	}()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/block", nil)
	if res, err := clientFor(req.URL.String()).Do(req); err == nil {
		_ = res.Body.Close()
		t.Fatalf("got %d, want no response", res.StatusCode)
	}

	return func() { close(release) }
}

// TestLifecycle_Goroutines covers test-quality / Scenario: Lifecycle leaves
// no goroutine behind (parity harness rows). Not parallel: goleak.Find sees
// every goroutine of the process.
func TestLifecycle_Goroutines(t *testing.T) {
	tests := []struct {
		name string
		// run starts and uses a server inside a subtest, whose cleanups
		// shut it down.
		run func(t *testing.T)
	}{
		{"a fiber server of the harness, Start, GET /ok, Shutdown", func(t *testing.T) { t.Helper(); runFiber(t) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := goleak.IgnoreCurrent()

			t.Run("run", tt.run)

			if err := findLeaks(before); err != nil {
				t.Fatalf("goroutines left after Shutdown: %v", err)
			}
		})
	}
}

// source is one test file: its name and its content.
type source struct{ name, content string }

// repoRoot returns the repository root: go test runs a package's tests in
// its directory, internal/paritytest.
func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	return root
}

// testPackages walks the repository from root and returns the test files
// of every directory that has any, by directory relative to root. It skips
// hidden directories, openspec and testdata.
func testPackages(root string) (map[string][]source, error) {
	pkgs := map[string][]source{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if name := d.Name(); path != root && (strings.HasPrefix(name, ".") || name == "openspec" || name == "testdata") {
				return filepath.SkipDir
			}

			return nil
		}

		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}

		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}

		dir := filepath.ToSlash(rel)
		pkgs[dir] = append(pkgs[dir], source{d.Name(), string(b)})

		return nil
	})

	return pkgs, err
}

// checkLeakCheck returns nil when the test files of dir run the leak check
// (a TestMain calling goleak.VerifyTestMain) or when there are none, and
// otherwise an error naming dir, or the file and parse error of a file
// that does not parse.
func checkLeakCheck(dir string, sources []source) error {
	fset := token.NewFileSet()
	files := make([]*ast.File, 0, len(sources))

	var errs []error

	for _, src := range sources {
		f, err := parser.ParseFile(fset, src.name, src.content, parser.SkipObjectResolution)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", dir, err))

			continue
		}

		files = append(files, f)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	if len(sources) == 0 || slices.ContainsFunc(files, verifiesTestMain) {
		return nil
	}

	return fmt.Errorf("%s: no TestMain calling goleak.VerifyTestMain", dir)
}

// verifiesTestMain reports whether f declares a TestMain that calls
// VerifyTestMain of go.uber.org/goleak.
func verifiesTestMain(f *ast.File) bool {
	pkg := goleakName(f)
	if pkg == "" {
		return false
	}

	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != "TestMain" || fn.Body == nil {
			continue
		}

		found := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			found = found || isVerifyCall(n, pkg)

			return !found
		})

		if found {
			return true
		}
	}

	return false
}

// isVerifyCall reports whether n is a call of pkg.VerifyTestMain.
func isVerifyCall(n ast.Node, pkg string) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return false
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "VerifyTestMain" {
		return false
	}

	id, ok := sel.X.(*ast.Ident)

	return ok && id.Name == pkg
}

// goleakName returns the name f refers to go.uber.org/goleak by, or "" when
// f does not import it.
func goleakName(f *ast.File) string {
	for _, imp := range f.Imports {
		if path, err := strconv.Unquote(imp.Path.Value); err != nil || path != "go.uber.org/goleak" {
			continue
		}

		if imp.Name != nil {
			return imp.Name.Name
		}

		return "goleak"
	}

	return ""
}

// TestEveryTestPackageChecksLeaks covers test-quality / Scenario: Every
// test package runs the leak check, all rows.
func TestEveryTestPackageChecksLeaks(t *testing.T) {
	t.Parallel()

	t.Run("packages", func(t *testing.T) {
		t.Parallel()

		pkgs, err := testPackages(repoRoot(t))
		if err != nil {
			t.Fatal(err)
		}

		// The walk must find at least these; a test package added later is
		// found too and checked like them.
		for _, dir := range []string{".", "engine", "internal/tracectx", "ginsrvr", "fibersrvr", "otelsrvr", "otelsetup", "internal/paritytest", "internal/paritytest/gincycle"} {
			if _, ok := pkgs[dir]; !ok {
				t.Errorf("the walk did not find the test package in %s", dir)
			}
		}

		for dir := range pkgs {
			t.Run(dir, func(t *testing.T) {
				t.Parallel()

				if err := checkLeakCheck(dir, pkgs[dir]); err != nil {
					t.Fatalf("rejected: %v", err)
				}
			})
		}
	})

	const verify = `package p

import (
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
`

	tests := []struct {
		name    string
		sources []source
		reject  string // a text the rejection names; "" when accepted
	}{
		{"a package whose TestMain calls goleak.VerifyTestMain", []source{{"leak_test.go", verify}}, ""},
		{"TestMain calling goleak.VerifyTestMain through an import alias", []source{{"leak_test.go", strings.ReplaceAll(strings.Replace(verify, `"go.uber.org/goleak"`, `gl "go.uber.org/goleak"`, 1), "goleak.VerifyTestMain", "gl.VerifyTestMain")}}, ""},
		{"a package with test files and no TestMain", []source{{"a_test.go", "package p\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n"}}, "synthetic"},
		{"a package whose TestMain calls m.Run without goleak.VerifyTestMain", []source{{"main_test.go", "package p\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc TestMain(m *testing.M) { os.Exit(m.Run()) }\n"}}, "synthetic"},
		{"VerifyTestMain of another package", []source{{"main_test.go", "package p\n\nimport (\n\t\"testing\"\n\n\tgoleak \"example.com/notgoleak\"\n)\n\nfunc TestMain(m *testing.M) { goleak.VerifyTestMain(m) }\n"}}, "synthetic"},
		{"a package without test files", nil, ""},
		{"a test file that does not parse", []source{{"broken_test.go", "package p\n\nfunc {"}}, "broken_test.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := checkLeakCheck("synthetic", tt.sources)

			switch {
			case tt.reject == "" && err != nil:
				t.Fatalf("rejected: %v", err)
			case tt.reject != "" && (err == nil || !strings.Contains(err.Error(), tt.reject)):
				t.Fatalf("checkLeakCheck = %v, want a rejection naming %s", err, tt.reject)
			}
		})
	}
}
