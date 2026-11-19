// Command benchgate compares two `go test -bench -benchmem` outputs, the
// base branch's and a pull request's, and fails when a benchmark's
// allocations rise more than 10%: CI's allocation regression gate (see
// docs/PERFORMANCE.md).
//
// Usage:
//
//	benchgate main.txt branch.txt
//
// Each file holds the output of one or more packages' benchmarks, as
// `make bench` writes it, usually with -count=6. A benchmark is compared
// by package and name, using the median of its runs. It fails when the
// median allocs/op or B/op of the branch is more than 10% above main's; a
// rise from zero fails. A unit whose runs on either side spread more than
// 10% (the largest more than 10% above the smallest) is not reproducible:
// a rise of it is reported as noisy and does not fail, because the gate
// cannot tell it from the runs' own noise. A parallel benchmark whose
// allocations depend on how goroutines interleave is the known case.
// Timing changes above 10% are reported but never fail: shared CI runners
// vary too much for timings to gate. Benchmarks only on one side are
// reported as new or removed.
//
// The exit code is 0 when nothing regressed, 1 when an allocation
// regressed and 2 when an input cannot be read or holds no benchmark
// result.
package main

import (
	"bufio"
	"cmp"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// maxRise is the largest rise, in percent, that passes.
const maxRise = 10

// gated are the units that fail the comparison; timed is reported only.
var (
	gated = []string{"allocs/op", "B/op"}
	timed = "ns/op"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout)) }

// run compares the files args names and writes its report to out,
// returning the exit code.
func run(args []string, out io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(out, "usage: benchgate main.txt branch.txt")

		return 2
	}

	var sides [2]results

	for i, file := range args {
		res, err := parseFile(file)
		if err != nil {
			fmt.Fprintln(out, err)

			return 2
		}

		sides[i] = res
	}

	if compare(sides[0], sides[1], out) {
		return 1
	}

	return 0
}

// key names a benchmark: its package and its name without the GOMAXPROCS
// suffix.
type key struct{ pkg, name string }

func (k key) String() string { return k.pkg + " " + k.name }

// results holds every run's value of every unit, per benchmark.
type results map[key]map[string][]float64

// parseFile reads the benchmark results of file.
func parseFile(file string) (results, error) {
	f, err := os.Open(file) //nolint:gosec // G304: the file to read is the command's argument.
	if err != nil {
		return nil, err
	}
	defer f.Close()

	res, err := parse(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}

	if len(res) == 0 {
		return nil, fmt.Errorf("%s: no benchmark results", file)
	}

	return res, nil
}

// procs is the GOMAXPROCS suffix go test adds to a benchmark name.
var procs = regexp.MustCompile(`-\d+$`)

// parse reads go test benchmark output: "pkg:" lines set the package of
// the result lines after them; every other line is ignored.
func parse(r io.Reader) (results, error) {
	res := results{}
	pkg := ""

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if p, ok := strings.CutPrefix(line, "pkg: "); ok {
			pkg = strings.TrimSpace(p)

			continue
		}

		name, values, ok := parseResult(line)
		if !ok {
			continue
		}

		k := key{pkg, procs.ReplaceAllString(name, "")}
		if res[k] == nil {
			res[k] = map[string][]float64{}
		}

		for unit, v := range values {
			res[k][unit] = append(res[k][unit], v)
		}
	}

	return res, sc.Err()
}

// parseResult parses a result line, "BenchmarkX-4  1000  12.5 ns/op  64
// B/op  2 allocs/op", into the name and the value of each unit.
func parseResult(line string) (string, map[string]float64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 || len(fields)%2 != 0 || !strings.HasPrefix(fields[0], "Benchmark") {
		return "", nil, false
	}

	if _, err := strconv.ParseUint(fields[1], 10, 64); err != nil {
		return "", nil, false // not an iteration count
	}

	values := map[string]float64{}

	for i := 2; i < len(fields); i += 2 {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return "", nil, false
		}

		values[fields[i+1]] = v
	}

	return fields[0], values, true
}

// compare writes the comparison of branch with base to out and reports
// whether an allocation regressed.
func compare(base, branch results, out io.Writer) bool {
	failed := false

	for _, k := range sortedKeys(base, branch) {
		was, now := base[k], branch[k]

		switch {
		case was == nil:
			fmt.Fprintf(out, "new      %s\n", k)
		case now == nil:
			fmt.Fprintf(out, "removed  %s\n", k)
		default:
			for _, unit := range gated {
				m, b, ok := medians(was, now, unit)
				if !ok || rise(m, b) <= maxRise {
					continue
				}

				if !reproducible(was[unit]) || !reproducible(now[unit]) {
					fmt.Fprintf(out, "noisy    %s %s: main %g (runs %s), branch %g (runs %s), %s, runs spread more than %d%%: not reproducible, not gated\n",
						k, unit, m, span(was[unit]), b, span(now[unit]), percent(m, b), maxRise)

					continue
				}

				failed = true

				fmt.Fprintf(out, "FAIL     %s %s: main %g, branch %g (%s), more than %d%%\n",
					k, unit, m, b, percent(m, b), maxRise)
			}

			if m, b, ok := medians(was, now, timed); ok && max(rise(m, b), rise(b, m)) > maxRise {
				fmt.Fprintf(out, "timing   %s %s: main %g, branch %g (%s), not gated\n", k, timed, m, b, percent(m, b))
			}
		}
	}

	if failed {
		fmt.Fprintf(out, "\nallocations rose more than %d%% against main\n", maxRise)
	}

	return failed
}

// sortedKeys returns the benchmarks of either side, sorted.
func sortedKeys(sides ...results) []key {
	var keys []key
	for _, side := range sides {
		keys = slices.AppendSeq(keys, maps.Keys(side))
	}

	slices.SortFunc(keys, func(a, b key) int { return cmp.Or(cmp.Compare(a.pkg, b.pkg), cmp.Compare(a.name, b.name)) })

	return slices.Compact(keys)
}

// medians returns the medians of unit on both sides, when both have it.
func medians(base, branch map[string][]float64, unit string) (float64, float64, bool) {
	if len(base[unit]) == 0 || len(branch[unit]) == 0 {
		return 0, 0, false
	}

	return median(base[unit]), median(branch[unit]), true
}

// median returns the median of values, which is not empty.
func median(values []float64) float64 {
	s := slices.Sorted(slices.Values(values))
	n := len(s)

	if n%2 == 1 {
		return s[n/2]
	}

	return (s[n/2-1] + s[n/2]) / 2
}

// reproducible reports whether runs, one side's values of a unit, spread
// maxRise or less: only then can a rise above maxRise against the other
// side be told from the noise of the runs themselves.
func reproducible(runs []float64) bool {
	return rise(slices.Min(runs), slices.Max(runs)) <= maxRise
}

// span formats the range of runs, which is not empty.
func span(runs []float64) string {
	return fmt.Sprintf("%g..%g", slices.Min(runs), slices.Max(runs))
}

// rise returns how much branch is above base, in percent; a rise from
// zero is infinite.
func rise(base, branch float64) float64 {
	switch {
	case branch <= base:
		return 0
	case base == 0:
		return math.Inf(1)
	default:
		return (branch - base) * 100 / base
	}
}

// percent formats the change from base to branch.
func percent(base, branch float64) string {
	if base == 0 {
		return "from zero"
	}

	return fmt.Sprintf("%+.1f%%", (branch-base)*100/base)
}
