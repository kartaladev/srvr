# Performance

How srvr's hot paths are benchmarked and profiled, how to compare two
versions, and what CI checks on every pull request.

## What is benchmarked

| Module | Benchmark | Measures |
|---|---|---|
| `srvr` (net/http) | `BenchmarkPipeline` | one request through the full pipeline: success, handler error, unmatched path, panic, body over `MaxRequestBodyBytes`, 32-link error chain; each also as `<case> parallel` (`b.RunParallel`) |
| `ginsrvr` | `BenchmarkPipeline` | the same cases on gin |
| `fibersrvr` | `BenchmarkPipeline` | the same cases on fiber through `app.Test`, plus a success whose access record is suppressed (`SkipPaths`), except the body over the limit, which fasthttp rejects before fiber runs and `app.Test` reports as an error |
| `srvr/engine` | `BenchmarkLogHandler` | srvr's log handler: 0, 1 and 3 `With` steps, with and without a `slog.Any` value, a logger from `LoggerFromContext` used for one record, and the root logger used by concurrent requests |
| `srvr/engine` | `BenchmarkCorrelation` | grouped and ungrouped request records |
| `srvr/engine` | `BenchmarkRedactSecrets` | the default redactor over error texts and a 32 KiB stack |
| `srvr/engine` | `BenchmarkDebugBuffer` | a request logging five `DEBUG` records with the debug buffer off, on and succeeding, on and failing |
| `otelsrvr` | `BenchmarkObserver` | the observer on one request: metrics, metrics and tracing, every instrument and tracing |

Every benchmark reports `ns/op`, `B/op` and `allocs/op` and logs to
`io.Discard` through srvr's JSON handler. `otelsetup` and
`internal/paritytest` have no benchmarks.

One reference point, on an Apple M4 Pro laptop (darwin/arm64, Go 1.27,
`-count=10`; timings from another machine will differ): a request that
logs five `DEBUG` records below the `INFO` level costs 1.24 µs, 866 B and
15 allocations with the debug buffer off; 3.16 µs, 6.0 KiB and 20
allocations with it on and the request succeeding (the records are
buffered, then discarded); and 4.96 µs, 6.8 KiB and 31 allocations with it
on and the request failing (the records are written). That cost is why
the buffer is off by default.

## Running the benchmarks

From the repository root:

```sh
make bench
```

Each module runs with `GOWORK=off`, as consumers build it, and writes its
results to `bench/<module>.txt` (`bench/srvr.txt`, `bench/ginsrvr.txt`,
...). `bench/` is ignored by git. The variables narrow or extend a run:

| Variable | Default | Meaning |
|---|---|---|
| `MODULES` | every module | the module directories to run, such as `MODULES=ginsrvr` |
| `BENCH` | `.` | the `-bench` regexp |
| `BENCH_COUNT` | `6` | runs per benchmark; `benchstat` needs several |
| `BENCH_FLAGS` | | more `go test` flags, such as `-benchtime=200ms` |
| `BENCH_DIR` | `bench` | where results and profiles go, relative to the repository root |

For example, the log handler benchmarks only, ten runs each:

```sh
make bench MODULES=. BENCH=BenchmarkLogHandler BENCH_COUNT=10
```

`make bench` also works inside a module directory (`cd ginsrvr && make
bench`), writing to the root `bench/`. By hand, from a module directory:

```sh
GOWORK=off go test -run '^$' -bench . -benchmem -count=6 ./...
```

## Profiles

```sh
make profile
```

runs the benchmarks of every package that has any once, with
`-cpuprofile` and `-memprofile`. `go test` profiles one package at a time,
so the files are per package, named after its path in the repository:
`bench/srvr.cpu` and `bench/srvr.mem` for the core package,
`bench/engine.*`, `bench/ginsrvr.*`, `bench/fibersrvr.*` and
`bench/otelsrvr.*`. The test binary (`bench/<name>.test`) and the
benchmark output (`bench/<name>.profile.txt`) are kept next to them.

Reading a profile:

```sh
go tool pprof -top bench/srvr.cpu                                  # where CPU time goes
go tool pprof -top -sample_index=alloc_objects bench/srvr.mem      # what allocates most often
go tool pprof -top -sample_index=alloc_space bench/engine.mem      # what allocates most bytes
go tool pprof -list 'RedactSecrets' bench/engine.cpu               # time per source line
go tool pprof -http=localhost:8080 bench/srvr.cpu                  # flame graph in a browser
```

A profile covers every benchmark of its package. Focus on one function
with `-focus=<regexp>`, or narrow the profile to the benchmarks of
interest with `BENCH` (a package without a matching benchmark then gets an
empty profile):

```sh
go tool pprof -top -focus=RedactSecrets bench/engine.cpu
make profile MODULES=. BENCH=BenchmarkPipeline
```

## Comparing two versions

Run the same benchmarks before and after a change, on the same machine
and with the same Go version, and compare them with
[`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat):

```sh
make bench MODULES=. BENCH=BenchmarkLogHandler BENCH_COUNT=10 BENCH_DIR=bench/old
# ... make the change ...
make bench MODULES=. BENCH=BenchmarkLogHandler BENCH_COUNT=10 BENCH_DIR=bench/new
go run golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e bench/old/srvr.txt bench/new/srvr.txt
```

`benchstat` prints the median of each benchmark and the change, with a
p-value; `~` means no significant difference. Ten runs (`-count=10`) give
it enough samples. Close other programs while measuring: on a busy laptop
timings vary by more than 100% between runs, while allocation counts stay
stable.

A change made for speed is kept only when it gains 10% or more in
`allocs/op` or `B/op`, or in `ns/op` with `p < 0.05` (design D8 of the
`triage-debug-buffer` change). The comparison goes into the change's
`notes.md`.

## The CI benchmark job

On every pull request the `bench` job of `.github/workflows/ci.yml`:

1. checks out the pull request and its base (`main`) side by side;
2. runs `make bench` in both, one after the other, on the same runner,
   with `BENCH_FLAGS=-benchtime=200ms`;
3. writes `benchstat`'s comparison to the job summary;
4. runs `internal/benchgate`, which fails the job when a benchmark's
   median `allocs/op` or `B/op` rises more than 10% against `main`.

What fails and what does not:

| Change against `main` | Result |
|---|---|
| `allocs/op` or `B/op` up by 10% or less | passes |
| `allocs/op` or `B/op` up by more than 10%, or up from zero | fails, naming the benchmark and both values |
| `ns/op` up or down by more than 10% | passes, reported as a timing change |
| a benchmark only on the branch | passes, reported as new |
| a benchmark only on `main` | passes, reported as removed |

Timings never fail the job: shared CI runners differ from run to run, and
earlier measurements on one machine varied by more than 100% between runs.
Allocation counts are deterministic for a given code path, so a rise is a
real regression. The median over six runs absorbs the occasional outlier
from pooled objects.

To run the gate locally, benchmark both versions into two directories and
compare them:

```sh
cat bench/old/*.txt > bench/old.txt
cat bench/new/*.txt > bench/new.txt
GOWORK=off go run ./internal/benchgate bench/old.txt bench/new.txt
```

It exits 0 when nothing regressed, 1 when allocations regressed and 2 when
an input is missing or holds no benchmark result.
