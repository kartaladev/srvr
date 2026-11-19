# Spec Delta

## ADDED Requirements

### Requirement: Hot paths are benchmarked and profilable
Every hot path SHALL have a benchmark reporting allocations, run under `go test -bench` in its module, and the repository SHALL provide commands that write benchmark results and CPU and memory profiles for every module without editing code.

#### Scenario: Benchmarks and profiles
- **Given** `<path>`
- **When** `make bench` or `make profile` runs from the root
- **Then** `<result>`

##### Examples:
| path | result |
|------|--------|
| the request pipeline of net/http, gin and fiber, sequential and with `b.RunParallel` | a benchmark result per engine and mode, with `ns/op`, `B/op` and `allocs/op` |
| `otelsrvr`'s observer on a request | a benchmark result with allocations |
| a request logger from `LoggerFromContext` used for one record, and the root logger used by concurrent requests | a benchmark result with allocations for each |
| panic recovery, a body over `MaxRequestBodyBytes` and an error chain of 32 links | a benchmark result with allocations for each |
| the debug buffer off, on with a successful request and on with a failing one | a benchmark result with allocations for each |
| `make profile` | a CPU and a memory profile per module, readable by `go tool pprof` |

### Requirement: Allocation regressions fail CI
A pull request SHALL fail CI when a benchmark's `allocs/op` or `B/op` rises more than 10% against `main`, measured in the same job; timing differences SHALL NOT fail CI.

#### Scenario: Benchmark comparison in CI
- **Given** a pull request whose change makes benchmark `<benchmark>` go from `<main>` to `<branch>`
- **When** the CI benchmark job compares the branch with `main`
- **Then** the job `<result>`

##### Examples:
| benchmark | main | branch | result |
|-----------|------|--------|--------|
| `BenchmarkLogHandler/1_step` | `10 allocs/op` | `10 allocs/op` | passes |
| `BenchmarkLogHandler/1_step` | `10 allocs/op` | `11 allocs/op` | passes (exactly 10%) |
| `BenchmarkLogHandler/1_step` | `10 allocs/op` | `12 allocs/op` | fails, naming the benchmark and both values |
| `BenchmarkPipeline/success` | `1333 B/op` | `1500 B/op` | fails, naming the benchmark and both values |
| `BenchmarkPipeline/success` | `3613 ns/op` | `7000 ns/op` | passes, reporting the timing change |
| a benchmark added by the branch | (none) | any | passes, reporting it as new |
| a benchmark removed by the branch | any | (none) | passes, reporting it as removed |
