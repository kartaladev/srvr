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
	enginePkg = "github.com/kartaladev/srvr/internal/engine"
	corePkg   = "github.com/kartaladev/srvr"
)

// result is one benchmark's measurements in a recorded run.
type result struct {
	pkg, name         string
	ns, bytes, allocs float64
	allocsRuns        []float64 // per-run allocs/op, overriding allocs
	bytesRuns         []float64 // per-run B/op, overriding bytes
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
			allocs, bytes := r.allocs, r.bytes
			if r.allocsRuns != nil {
				allocs = r.allocsRuns[i]
			}

			if r.bytesRuns != nil {
				bytes = r.bytesRuns[i]
			}

			fmt.Fprintf(&b, "%s-4   \t  500000\t      %g ns/op\t     %g B/op\t      %g allocs/op\n", r.name, r.ns, bytes, allocs)
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
	withAllocs := func(r result, allocs float64) result {
		r.allocs = allocs

		return r
	}
	other := result{pkg: enginePkg, name: "BenchmarkRedactSecrets/no_secret", ns: 6239, bytes: 112, allocs: 3}
	parallel := func(ns, bytes float64) result {
		return result{pkg: corePkg, name: "BenchmarkPipeline/success_parallel", ns: ns, bytes: bytes, allocs: 20}
	}
	// concurrent is the parallel benchmark of CI run 44486663813 attempt 1,
	// under the name it had then, on identical code: on main its runs spread from 4 to 6 allocs/op
	// (benchstat: 5.000 ± 20%) and 300 to 390 B/op (344.5 ± 13%).
	concurrent := func(allocsRuns, bytesRuns []float64) result {
		return result{
			pkg: enginePkg, name: "BenchmarkLogHandler/root_logger_concurrent_requests", ns: 1036,
			allocsRuns: allocsRuns, bytesRuns: bytesRuns,
		}
	}
	noisyAllocs := []float64{4, 5, 5, 5, 6, 6}
	noisyBytes := []float64{300, 330, 340, 349, 360, 390}

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
			name: "identical code with a noisy parallel benchmark passes, reported as not reproducible",
			main: []result{concurrent(noisyAllocs, noisyBytes)},
			branch: []result{concurrent(
				[]float64{7, 7, 7, 7, 7, 7}, []float64{414, 416, 418, 418, 420, 422})},
			wantCode: 0,
			wantOut: []string{
				"BenchmarkLogHandler/root_logger_concurrent_requests allocs/op", "main 5 (runs 4..6)", "branch 7 (runs 7..7)",
				"BenchmarkLogHandler/root_logger_concurrent_requests B/op", "main 344.5 (runs 300..390)", "not reproducible",
			},
		},
		{
			name: "allocations up 21% in a serial benchmark fail",
			main: []result{pipeline(3613, 1333)}, branch: []result{withAllocs(pipeline(3613, 1333), 23)},
			wantCode: 1,
			wantOut:  []string{"FAIL", "BenchmarkPipeline/success", "allocs/op", "main 19", "branch 23"},
		},
		{
			name: "allocations up 15% in a reproducible parallel benchmark fail",
			main: []result{parallel(1100, 1410)}, branch: []result{withAllocs(parallel(1100, 1410), 23)},
			wantCode: 1,
			wantOut:  []string{"FAIL", "BenchmarkPipeline/success_parallel", "allocs/op", "main 20", "branch 23"},
		},
		{
			name:     "allocations doubled in a noisy parallel benchmark pass, reported as not reproducible",
			main:     []result{concurrent(noisyAllocs, noisyBytes)},
			branch:   []result{concurrent([]float64{10, 10, 10, 10, 10, 10}, noisyBytes)},
			wantCode: 0,
			wantOut:  []string{"BenchmarkLogHandler/root_logger_concurrent_requests allocs/op", "main 5 (runs 4..6)", "branch 10 (runs 10..10)", "not reproducible"},
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
// comparison: missing or empty inputs, noisy runs and the spread at which
// runs stop being gated, zero baselines and one benchmark name in two
// packages.
func TestBenchmarkComparison_Faults(t *testing.T) {
	t.Parallel()

	one := func(pkg string, allocs float64) result {
		return result{pkg: pkg, name: "BenchmarkPipeline/success", ns: 3613, bytes: 1333, allocs: allocs}
	}
	runs := func(allocs ...float64) string {
		r := one(corePkg, 0)
		r.allocsRuns = allocs

		return benchOutput(r)
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
			name:     "one noisy run of six does not fail",
			main:     benchOutput(one(corePkg, 19)),
			branch:   runs(19, 19, 19, 25, 19, 19),
			wantCode: 0,
		},
		{
			// a false failure costs more than one pull request not gating
			// one unit: the outlier is not left out of the spread.
			name:     "one outlier run of six on main reports the unit, not gated",
			main:     runs(19, 19, 19, 19, 19, 25),
			branch:   benchOutput(one(corePkg, 23)),
			wantCode: 0, wantOut: []string{"noisy", "allocs/op", "main 19 (runs 19..25)", "branch 23 (runs 23..23)"},
		},
		{
			name:     "runs spread exactly 10% are gated",
			main:     runs(20, 20, 20, 22, 22, 22),
			branch:   benchOutput(one(corePkg, 25)),
			wantCode: 1, wantOut: []string{"FAIL", "allocs/op", "main 21", "branch 25"},
		},
		{
			name:     "runs spread more than 10% are not gated",
			main:     runs(20, 20, 20, 23, 23, 23),
			branch:   benchOutput(one(corePkg, 25)),
			wantCode: 0, wantOut: []string{"noisy", "allocs/op", "main 21.5 (runs 20..23)", "branch 25 (runs 25..25)", "not gated"},
		},
		{
			name:     "runs spread up from zero are not gated",
			main:     runs(0, 0, 0, 0, 0, 1),
			branch:   benchOutput(one(corePkg, 2)),
			wantCode: 0, wantOut: []string{"noisy", "allocs/op", "main 0 (runs 0..1)", "from zero", "not gated"},
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
			main: "pkg: " + corePkg + "\nBenchmarkX-4 1 1 ns/op 10 B/op 20 allocs/op\nBenchmarkX-4 1 1 ns/op 10 B/op 22 allocs/op\n",
			// median 21 on main: 23 is 9.5% above it and passes, 25 is 19%.
			// Each side's runs spread 10% or less, so both are gated.
			branch:   "pkg: " + corePkg + "\nBenchmarkX-4 1 1 ns/op 10 B/op 24 allocs/op\nBenchmarkX-4 1 1 ns/op 10 B/op 26 allocs/op\n",
			wantCode: 1, wantOut: []string{"main 21", "branch 25"},
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
