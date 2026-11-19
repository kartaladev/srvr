package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordedHeader is the header go test prints before a package's
// benchmark results, as recorded on a GitHub-hosted Ubuntu runner.
const recordedHeader = "goos: linux\ngoarch: amd64\npkg: %s\ncpu: AMD EPYC 7763 64-Core Processor\n"

const (
	enginePkg = "github.com/kartaladev/srvr/engine"
	corePkg   = "github.com/kartaladev/srvr"
)

// result is one benchmark's measurements in a recorded run.
type result struct {
	pkg, name         string
	ns, bytes, allocs float64
	allocsRuns        []float64 // per-run allocs/op, overriding allocs
}

// benchOutput renders results as `go test -bench . -benchmem -count=6`
// prints them, one package header per package, with GOMAXPROCS 4.
func benchOutput(results ...result) string {
	var b strings.Builder

	pkg := ""

	for _, r := range results {
		if r.pkg != pkg {
			pkg = r.pkg
			fmt.Fprintf(&b, recordedHeader, pkg)
		}

		for i := range 6 {
			allocs := r.allocs
			if r.allocsRuns != nil {
				allocs = r.allocsRuns[i]
			}

			fmt.Fprintf(&b, "%s-4   \t  500000\t      %g ns/op\t     %g B/op\t      %g allocs/op\n", r.name, r.ns, r.bytes, allocs)
		}
	}

	fmt.Fprintf(&b, "PASS\nok  \t%s\t12.345s\n", pkg)

	return b.String()
}

// gate writes main and branch to files, runs benchgate on them and
// returns its exit code and output.
func gate(t *testing.T, main, branch string) (int, string) {
	t.Helper()

	dir := t.TempDir()
	mainFile, branchFile := filepath.Join(dir, "main.txt"), filepath.Join(dir, "branch.txt")

	for file, content := range map[string]string{mainFile: main, branchFile: branch} {
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	code := run([]string{mainFile, branchFile}, &out)

	return code, out.String()
}

// TestBenchmarkComparison covers test-quality / Scenario: Benchmark
// comparison in CI, one case per Examples row.
func TestBenchmarkComparison(t *testing.T) {
	t.Parallel()

	logHandler := func(allocs float64) result {
		return result{pkg: enginePkg, name: "BenchmarkLogHandler/1_step", ns: 398, bytes: 640, allocs: allocs}
	}
	pipeline := func(ns, bytes float64) result {
		return result{pkg: corePkg, name: "BenchmarkPipeline/success", ns: ns, bytes: bytes, allocs: 19}
	}
	other := result{pkg: enginePkg, name: "BenchmarkRedactSecrets/no_secret", ns: 6239, bytes: 112, allocs: 3}

	tests := []struct {
		name         string
		main, branch []result
		wantCode     int
		wantOut      []string
	}{
		{
			name: "allocations unchanged passes",
			main: []result{logHandler(10)}, branch: []result{logHandler(10)},
			wantCode: 0,
		},
		{
			name: "allocations up exactly 10% passes",
			main: []result{logHandler(10)}, branch: []result{logHandler(11)},
			wantCode: 0,
		},
		{
			name: "allocations up 20% fails",
			main: []result{logHandler(10)}, branch: []result{logHandler(12)},
			wantCode: 1,
			wantOut:  []string{"FAIL", "BenchmarkLogHandler/1_step", "allocs/op", "main 10", "branch 12"},
		},
		{
			name: "bytes up 12.5% fails",
			main: []result{pipeline(3613, 1333)}, branch: []result{pipeline(3613, 1500)},
			wantCode: 1,
			wantOut:  []string{"FAIL", "BenchmarkPipeline/success", "B/op", "main 1333", "branch 1500"},
		},
		{
			name: "time up 94% passes, reported",
			main: []result{pipeline(3613, 1333)}, branch: []result{pipeline(7000, 1333)},
			wantCode: 0,
			wantOut:  []string{"BenchmarkPipeline/success", "ns/op", "main 3613", "branch 7000", "not gated"},
		},
		{
			name: "a benchmark the branch adds passes, reported as new",
			main: []result{logHandler(10)}, branch: []result{logHandler(10), other},
			wantCode: 0,
			wantOut:  []string{"new", "BenchmarkRedactSecrets/no_secret"},
		},
		{
			name: "a benchmark the branch removes passes, reported as removed",
			main: []result{logHandler(10), other}, branch: []result{logHandler(10)},
			wantCode: 0,
			wantOut:  []string{"removed", "BenchmarkRedactSecrets/no_secret"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, out := gate(t, benchOutput(tt.main...), benchOutput(tt.branch...))
			if code != tt.wantCode {
				t.Fatalf("exit code %d, want %d; output:\n%s", code, tt.wantCode, out)
			}

			if tt.wantCode == 0 && strings.Contains(out, "FAIL") {
				t.Errorf("a passing comparison reports a failure:\n%s", out)
			}

			for _, want := range tt.wantOut {
				if !strings.Contains(out, want) {
					t.Errorf("output does not contain %q:\n%s", want, out)
				}
			}
		})
	}
}

// TestBenchmarkComparison_Faults covers what can go wrong around the
// comparison: missing or empty inputs, noisy runs, zero baselines and one
// benchmark name in two packages.
func TestBenchmarkComparison_Faults(t *testing.T) {
	t.Parallel()

	one := func(pkg string, allocs float64) result {
		return result{pkg: pkg, name: "BenchmarkPipeline/success", ns: 3613, bytes: 1333, allocs: allocs}
	}
	noBenchmarks := "goos: linux\ngoarch: amd64\npkg: " + corePkg + "\nPASS\nok  \t" + corePkg + "\t0.1s\n"

	tests := []struct {
		name         string
		main, branch string
		wantCode     int
		wantOut      []string
	}{
		{
			name: "main has no results", main: noBenchmarks, branch: benchOutput(one(corePkg, 19)),
			wantCode: 2, wantOut: []string{"main.txt", "no benchmark results"},
		},
		{
			name: "branch has no results", main: benchOutput(one(corePkg, 19)), branch: "",
			wantCode: 2, wantOut: []string{"branch.txt", "no benchmark results"},
		},
		{
			name: "one noisy run of six does not fail",
			main: benchOutput(one(corePkg, 19)),
			branch: benchOutput(result{
				pkg: corePkg, name: "BenchmarkPipeline/success", ns: 3613, bytes: 1333,
				allocsRuns: []float64{19, 19, 19, 25, 19, 19},
			}),
			wantCode: 0,
		},
		{
			name: "allocations from zero fail",
			main: benchOutput(one(corePkg, 0)), branch: benchOutput(one(corePkg, 1)),
			wantCode: 1, wantOut: []string{"FAIL", "allocs/op", "main 0", "branch 1"},
		},
		{
			name:     "the same name in two packages is compared per package",
			main:     benchOutput(one(corePkg, 19), one("github.com/kartaladev/srvr/ginsrvr", 30)),
			branch:   benchOutput(one(corePkg, 19), one("github.com/kartaladev/srvr/ginsrvr", 40)),
			wantCode: 1, wantOut: []string{"FAIL", "srvr/ginsrvr", "main 30", "branch 40"},
		},
		{
			name: "an unreadable input fails", main: benchOutput(one(corePkg, 19)), branch: strings.Repeat("x", 1<<17),
			wantCode: 2, wantOut: []string{"branch.txt", "too long"},
		},
		{
			name:     "results without -benchmem are not gated",
			main:     benchOutput(one(corePkg, 19)),
			branch:   "pkg: " + corePkg + "\nBenchmarkPipeline/success-4 \t 500000\t 3613 ns/op\n",
			wantCode: 0,
		},
		{
			name: "an even number of runs compares the mean of the middle two",
			main: "pkg: " + corePkg + "\nBenchmarkX-4 1 1 ns/op 10 B/op 10 allocs/op\nBenchmarkX-4 1 1 ns/op 10 B/op 12 allocs/op\n",
			// median 11 on main: 12 is 9.1% above it and passes, 13 is 18.2%.
			branch:   "pkg: " + corePkg + "\nBenchmarkX-4 1 1 ns/op 10 B/op 12 allocs/op\nBenchmarkX-4 1 1 ns/op 10 B/op 14 allocs/op\n",
			wantCode: 1, wantOut: []string{"main 11", "branch 13"},
		},
		{
			name: "malformed result lines are ignored",
			main: benchOutput(one(corePkg, 19)),
			branch: benchOutput(one(corePkg, 19)) +
				"BenchmarkPipeline/success-4 many 1 ns/op 1 B/op 99 allocs/op\n" +
				"BenchmarkPipeline/success-4 5 x ns/op 1 B/op 99 allocs/op\n" +
				"BenchmarkPipeline/success-4 5 1 ns/op 1\n",
			wantCode: 0,
		},
		{
			name:     "a log line between results is ignored",
			main:     benchOutput(one(corePkg, 19)),
			branch:   strings.Replace(benchOutput(one(corePkg, 19)), "cpu:", "    bench_test.go:12: warming up\ncpu:", 1),
			wantCode: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			code, out := gate(t, tt.main, tt.branch)
			if code != tt.wantCode {
				t.Fatalf("exit code %d, want %d; output:\n%s", code, tt.wantCode, out)
			}

			for _, want := range tt.wantOut {
				if !strings.Contains(out, want) {
					t.Errorf("output does not contain %q:\n%s", want, out)
				}
			}
		})
	}

	t.Run("arguments", func(t *testing.T) {
		t.Parallel()

		missing := filepath.Join(t.TempDir(), "missing.txt")

		for _, args := range [][]string{nil, {"one.txt"}, {"a", "b", "c"}, {missing, missing}} {
			var out bytes.Buffer
			if code := run(args, &out); code != 2 {
				t.Errorf("run(%q) = %d, want 2; output:\n%s", args, code, out.String())
			}
		}
	})
}
