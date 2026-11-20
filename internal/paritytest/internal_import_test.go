package paritytest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// goTool returns the path of the go command, skipping the test under
// -short when it is missing (make check always has it).
func goTool(t *testing.T) string {
	t.Helper()

	path, err := exec.LookPath("go")
	if err != nil {
		if testing.Short() {
			t.Skipf("go command not found: %v", err)
		}

		t.Fatalf("go command not found: %v", err)
	}

	return path
}

// offline makes a throwaway module build from the working tree and the
// module cache only: no proxy, no checksum database, go.mod updated in
// place.
var offline = []string{"GOPROXY=off", "GOSUMDB=off", "GOFLAGS=-mod=mod"}

// goCmd runs the go command in dir with GOWORK=off, as make check does,
// plus env, and returns its combined output.
func goCmd(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), goTool(t), args...)
	cmd.Dir = dir
	cmd.Env = append(append(os.Environ(), "GOWORK=off"), env...)
	out, err := cmd.CombinedOutput()

	return string(out), err
}

// consumerModule writes a module named module into a new directory: one
// main package with source, requiring srvr replaced by the working tree.
func consumerModule(t *testing.T, root, module, source string) string {
	t.Helper()

	dir := t.TempDir()
	sum, err := os.ReadFile(filepath.Join(root, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}

	mod := "module " + module + "\n\ngo 1.27\n\n" +
		"require github.com/kartaladev/srvr v0.3.0\n\n" +
		"replace github.com/kartaladev/srvr => " + root + "\n"
	files := map[string]string{"go.mod": mod, "go.sum": string(sum), "main.go": source}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

// TestInternalImport covers engine-support / Scenario: Only srvr's modules
// import the shared pieces, every row.
func TestInternalImport(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)

	t.Run("srvr's own modules", func(t *testing.T) {
		t.Parallel()

		const engine = "github.com/kartaladev/srvr/internal/engine"
		for _, dir := range []string{"ginsrvr", "fibersrvr", "otelsrvr", "otelsetup", "internal/paritytest"} {
			t.Run(dir, func(t *testing.T) {
				t.Parallel()

				// go list fails on an import the internal rule refuses, and
				// reads the import graph without compiling.
				out, err := goCmd(t, filepath.Join(root, dir), nil, "list", "-test", "-deps", "./...")
				if err != nil {
					t.Fatalf("go list in %s: %v\n%s", dir, err, out)
				}

				if !slices.Contains(strings.Fields(out), engine) {
					t.Fatalf("%s does not import %s", dir, engine)
				}
			})
		}
	})

	tests := []struct {
		name, module, source string
		// fails is a substring of the build output when the build must
		// fail; empty when it must succeed.
		fails []string
	}{
		{
			name:   "outside module importing internal/engine",
			module: "example.com/consumer",
			source: "package main\n\nimport _ \"github.com/kartaladev/srvr/internal/engine\"\n\nfunc main() {}\n",
			fails:  []string{"use of internal package github.com/kartaladev/srvr/internal/engine not allowed"},
		},
		{
			name:   "outside module importing the removed engine path",
			module: "example.com/consumer",
			source: "package main\n\nimport _ \"github.com/kartaladev/srvr/engine\"\n\nfunc main() {}\n",
			fails:  []string{"no required module provides package", "does not contain package"},
		},
		{
			name:   "outside module using srvr only",
			module: "example.com/consumer",
			source: "package main\n\nimport (\n\t\"errors\"\n\n\t\"github.com/kartaladev/srvr\"\n)\n\n" +
				"func main() {\n\tvar c srvr.Config\n\t_, err := srvr.New(srvr.WithConfig(c))\n\t_ = errors.Is(err, srvr.ErrConfig)\n}\n",
		},
		{
			name:   "sibling path sharing the prefix",
			module: "github.com/kartaladev/srvrx",
			source: "package main\n\nimport _ \"github.com/kartaladev/srvr/internal/engine\"\n\nfunc main() {}\n",
			fails:  []string{"use of internal package", "not allowed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := consumerModule(t, root, tt.module, tt.source)
			if len(tt.fails) == 0 {
				if out, err := goCmd(t, dir, offline, "build", "./..."); err != nil {
					t.Fatalf("build failed: %v\n%s", err, out)
				}

				return
			}

			// go list reports a refused or missing import without compiling.
			out, err := goCmd(t, dir, offline, "list", "-deps", "./...")
			if err == nil {
				t.Fatalf("go list succeeded, want a failure containing %q", tt.fails)
			}

			if !slices.ContainsFunc(tt.fails, func(s string) bool { return strings.Contains(out, s) }) {
				t.Fatalf("go list output %q does not contain any of %q", out, tt.fails)
			}
		})
	}
}
