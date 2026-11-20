# Performance

How srvr's hot paths are benchmarked and profiled, how to compare two
versions, and what CI checks on every pull request.

## What is benchmarked

| Module | Benchmark | Measures |
|---|---|---|
| `srvr` (net/http) | `BenchmarkPipeline` | one request through the full pipeline: success, handler error, unmatched path, panic, body over `MaxRequestBodyBytes`, 32-link error chain, a success with the user agent omitted (`WithoutAccessLogUserAgent`); each also as `<case> parallel` (`b.RunParallel`) |
| `srvr` (`internal/tracectx`) | `BenchmarkNewTrace` | generating a new trace's ids, serial and parallel |
| `ginsrvr` | `BenchmarkPipeline` | the same cases on gin |
| `fibersrvr` | `BenchmarkPipeline` | the same cases on fiber through `app.Test`, plus a success whose access record is suppressed (`SkipPaths`) and an unmatched path whose record `WithUnmatchedRecordLimit` suppresses, except the body over the limit, which fasthttp rejects before fiber runs and `app.Test` reports as an error |
| `srvr/internal/engine` | `BenchmarkLogHandler` | srvr's log handler: 0, 1 and 3 `With` steps, with and without a `slog.Any` value, a logger from `LoggerFromContext` used for one record, and the root logger used by concurrent requests whose records interleave |
| `srvr/internal/engine` | `BenchmarkCorrelation` | grouped and ungrouped request records |
| `srvr/internal/errchain` | `BenchmarkRedactSecrets` | the default redactor over error texts and a 32 KiB stack |
| `srvr/internal/engine` | `BenchmarkDebugBuffer` | a request logging five `DEBUG` records with the debug buffer off, on and succeeding, on and failing; on and failing with seven attributes per record, one a `slog.Any` value; on and failing with 150 records, past the default 100 |
| `otelsrvr` | `BenchmarkObserver` | the observer on one request: metrics, metrics and tracing, every instrument and tracing |

Every benchmark reports `ns/op`, `B/op` and `allocs/op` and logs to
`io.Discard` through srvr's JSON handler. `otelsetup` and
`internal/paritytest` have no benchmarks.

One reference point, on an Apple M4 Pro laptop (darwin/arm64, Go 1.27,
`-count=10`; timings from another machine will differ): a request that
logs five `DEBUG` records below the `INFO` level costs 1.24 µs, 866 B and
15 allocations with the debug buffer off; 3.0 µs, 4.9 KiB and 18
allocations with it on and the request succeeding (the records are
buffered, then discarded); and 4.8 µs, 5.8 KiB and 29 allocations with it
on and the request failing (the records are written). That cost is why
the buffer is off by default.

## Measured decisions

Every optimisation below was measured with `benchstat` over ten runs
(design D8 rule: kept at a 10% or larger gain in `allocs/op` or `B/op`, or
in `ns/op` with `p < 0.05`). Unless noted, the timings are from the same
M4 Pro laptop, which other work was also using; allocation counts do not
depend on the machine. The full tables are in the `followups-hardening`
change's `notes.md`.

| Item | Result | Decision |
|---|---|---|
| Debug buffer kept as a ring instead of a resliced slice | `BenchmarkDebugBuffer` on-success -17% B/op and 20 -> 18 allocations; 150 records -47% B/op | applied |
| Debug buffer: size a `slog.Any` value without building its text | -2.5% B/op | not applied |
| Debug buffer: skip `slog.Record.Clone` when the record is rewritten | no change: `Clone` does not allocate | not applied |
| fiber: decide the unmatched-record limit once, skipping copies for suppressed records | 70 -> 67 allocations (-4%) | not applied |
| Skip reading the user agent when `OmitUserAgent` is set | fiber 50 -> 49 allocations; net/http and gin unchanged | not applied |
| Trace ids drawn from a pooled batch of `crypto/rand` bytes (one read per 32 traces) instead of one `crypto/rand` read per request | darwin: about 42% of `BenchmarkPipeline` CPU was in `crypto/rand`; `Pipeline/success` -12%, parallel -47%. Linux (local arm64 container, `getrandom` vDSO): at most -4%; the CI runner is unverified | applied, with the owner's acceptance of the trade-off below |
| Trace ids from pooled ChaCha8 generators seeded from `crypto/rand` | the same gain as the batch | not applied: the batch keeps `crypto/rand` as the source at a similar gain |

The trace-id batch is a security trade-off. Ids stay cryptographically
random and come only from `crypto/rand`. A process holds up to 768 bytes
of future ids per pooled batch, so a heap disclosure (a core dump, a
memory-read bug) reveals the next ids drawn from that batch. Ids also
repeat when a process or VM snapshot is restored more than once, because
the copied batches are not reseeded. Trace ids are not secrets: srvr
writes them to problem responses and logs, and W3C Trace Context asks
only that they be random and unique.

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
`bench/internal-engine.*`, `bench/ginsrvr.*`, `bench/fibersrvr.*` and
`bench/otelsrvr.*`. The test binary (`bench/<name>.test`) and the
benchmark output (`bench/<name>.profile.txt`) are kept next to them.

Reading a profile:

```sh
go tool pprof -top bench/srvr.cpu                                       # where CPU time goes
go tool pprof -top -sample_index=alloc_objects bench/srvr.mem           # what allocates most often
go tool pprof -top -sample_index=alloc_space bench/internal-engine.mem  # what allocates most bytes
go tool pprof -list 'RedactSecrets' bench/internal-errchain.cpu         # time per source line
go tool pprof -http=localhost:8080 bench/srvr.cpu                       # flame graph in a browser
```

A profile covers every benchmark of its package. Focus on one function
with `-focus=<regexp>`, or narrow the profile to the benchmarks of
interest with `BENCH` (a package without a matching benchmark then gets an
empty profile):

```sh
go tool pprof -top -focus=RedactSecrets bench/internal-errchain.cpu
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
   median `allocs/op` or `B/op` rises more than 10% against `main` and
   that unit's runs are reproducible: on each side, the largest run at
   most 10% above the smallest. Its report goes to the job log and to the
   job summary.

What fails and what does not:

| Change against `main` | Result |
|---|---|
| `allocs/op` or `B/op` up by 10% or less | passes |
| `allocs/op` or `B/op` up by more than 10%, or up from zero | fails, naming the benchmark and both values |
| the same, but the unit's runs on `main` or on the branch spread more than 10% | passes, reported as `noisy` with both ranges: not reproducible, not gated |
| `ns/op` up or down by more than 10% | passes, reported as a timing change |
| a benchmark only on the branch | passes, reported as new |
| a benchmark only on `main` | passes, reported as removed |

Timings never fail the job: shared CI runners differ from run to run, and
earlier measurements on one machine varied by more than 100% between runs.
Allocation counts are deterministic for a given code path, so a rise is a
real regression. The median over six runs absorbs the occasional outlier
from pooled objects.

A benchmark whose code path depends on how goroutines interleave is not
deterministic, and the gate checks that instead of assuming it. The
first version of the concurrent log benchmark shared the root logger's
one-entry target cache between goroutines, each with a request of its
own, so a record hit the cache only when no other goroutine had logged
since: 0 `allocs/op` at `-cpu=1`, 4 to 7 at `-cpu=4`. It failed a
docs-only pull request at 5 -> 7 `allocs/op` (+40%, and `benchstat`
called it significant), and a re-run measured 6 -> 6. The benchmark now
alternates two requests per goroutine, so every record misses the cache
(8 `allocs/op` and 464 `B/op` at every `-cpu`), and the gate reports a
unit whose runs spread more than 10% instead of failing on it: a 10%
threshold cannot tell a regression from runs that disagree by more than
that among themselves. Over 20 runs of every benchmark at `-cpu=4`, every
other benchmark, `RunParallel` rows included, kept its `allocs/op` within
one allocation and its `B/op` within 4.1% (pooled buffers, stack traces),
apart from one outlier run of fiber's `panic parallel` (+24% `B/op`).

So the gate still fails every rise above 10% of a benchmark whose runs
agree to within 10% on both sides, which is every benchmark today. It
does not fail a rise of a benchmark that has become noisy, nor of a unit
with one outlier run in that job: the rise is printed, with both medians
and ranges, in the job log and in the job summary below `benchstat`'s
table. A new benchmark must keep its allocations
independent of scheduling; one that is steady within each run but
different between two runs (one cache hit rate on `main`, another on the
branch) would still fail the gate, which only determinism prevents. See
design D10 of the `followups-hardening` change.

To run the gate locally, benchmark both versions into two directories and
compare them:

```sh
cat bench/old/*.txt > bench/old.txt
cat bench/new/*.txt > bench/new.txt
GOWORK=off go run ./internal/benchgate bench/old.txt bench/new.txt
```

It exits 0 when nothing regressed, 1 when allocations regressed and 2 when
an input is missing or holds no benchmark result.
