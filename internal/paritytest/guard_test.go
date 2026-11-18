package paritytest

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

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
