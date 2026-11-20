package paritytest

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

// srvrPath is the core module path; internal packages live below it.
const srvrPath = "github.com/kartaladev/srvr"

// internalOrder is the recorded direction of the core module's internal
// library packages (design D2): a package may import only those listed
// before it. internal/engine assembles the others and comes last. Commands
// (package main, such as internal/benchgate) are not layers and are not
// listed.
var internalOrder = []string{
	srvrPath + "/internal/tracectx",
	srvrPath + "/internal/errchain",
	srvrPath + "/internal/engine",
}

// checkImportDirection returns an error for every internal package of
// imports (package path to its non-test imports) that is missing from
// order or imports a srvr package not listed before it in order.
func checkImportDirection(order []string, imports map[string][]string) error {
	var errs []error

	for _, pkg := range slices.Sorted(maps.Keys(imports)) {
		if !strings.HasPrefix(pkg, srvrPath+"/internal/") {
			continue
		}

		rank := slices.Index(order, pkg)
		if rank < 0 {
			errs = append(errs, fmt.Errorf("%s is not in the recorded package order", pkg))

			continue
		}

		for _, imp := range imports[pkg] {
			if imp != srvrPath && !strings.HasPrefix(imp, srvrPath+"/") {
				continue
			}

			if i := slices.Index(order, imp); i < 0 || i >= rank {
				errs = append(errs, fmt.Errorf("%s imports %s, which is not listed below it", pkg, imp))
			}
		}
	}

	return errors.Join(errs...)
}

// coreImports lists the core module's library packages (commands are
// skipped) with their non-test imports.
func coreImports(t *testing.T) map[string][]string {
	t.Helper()

	out, err := goCmd(t, repoRoot(t), nil, "list", "-f", "{{if ne .Name \"main\"}}{{.ImportPath}} {{join .Imports \" \"}}{{end}}", "./...")
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}

	imports := map[string][]string{}
	for line := range strings.Lines(out) {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			imports[fields[0]] = fields[1:]
		}
	}

	return imports
}

// TestImportDirection covers test-quality / Scenario: Import direction
// guard, every row.
func TestImportDirection(t *testing.T) {
	t.Parallel()

	t.Run("the core module", func(t *testing.T) {
		t.Parallel()

		imports := coreImports(t)
		for _, pkg := range internalOrder {
			if _, ok := imports[pkg]; !ok {
				t.Errorf("%s does not exist", pkg)
			}
		}

		if err := checkImportDirection(internalOrder, imports); err != nil {
			t.Fatal(err)
		}
	})

	var (
		engine   = srvrPath + "/internal/engine"
		errchain = srvrPath + "/internal/errchain"
		tracectx = srvrPath + "/internal/tracectx"
		order    = []string{tracectx, errchain, engine}
	)

	tests := []struct {
		name    string
		imports map[string][]string
		// rejects names the packages the error must mention; nil
		// when the guard passes.
		rejects []string
	}{
		{"root package imports internal/engine", map[string][]string{srvrPath: {engine}}, nil},
		{
			"internal/engine imports std, prblm and tracectx (one-entry order)",
			map[string][]string{engine: {"context", "github.com/kartaladev/prblm", tracectx}},
			nil,
		},
		{"test-only imports are not listed", map[string][]string{tracectx: {"testing"}}, nil},
		{"internal/engine imports internal/errchain", map[string][]string{engine: {errchain}}, nil},
		{"internal/errchain imports prblm", map[string][]string{errchain: {"github.com/kartaladev/prblm"}}, nil},
		{"internal/errchain imports internal/engine", map[string][]string{errchain: {engine}}, []string{"internal/errchain", "internal/engine"}},
		{"leaf imports internal/engine", map[string][]string{tracectx: {engine}}, []string{"internal/tracectx", "internal/engine"}},
		{"internal package missing from the order", map[string][]string{srvrPath + "/internal/other": {engine}}, []string{"internal/other", "not in the recorded package order"}},
		{"internal package imports the root package", map[string][]string{tracectx: {srvrPath}}, []string{"internal/tracectx", srvrPath + ","}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := checkImportDirection(order, tt.imports)
			if tt.rejects == nil {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}

				return
			}

			if err == nil {
				t.Fatal("passed, want a rejection")
			}

			for _, want := range tt.rejects {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
		})
	}
}
