# Spec Delta

## MODIFIED Requirements

### Requirement: Allocation regressions fail CI
A pull request SHALL fail CI when a benchmark's `allocs/op` or `B/op` rises more than 10% against `main`, measured in the same job. A unit whose runs on either side spread more than 10% is not reproducible: its rise SHALL be reported, not fail CI. Timing differences SHALL NOT fail CI.

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
| `BenchmarkLogHandler/root_logger_concurrent_requests` (parallel), identical code (CI run 44486663813) | runs from `4` to `6 allocs/op` and from `300` to `390 B/op` (medians `5` and `344.5`) | `7 allocs/op` and `414` to `422 B/op` (median `418`) | passes, reporting both units as not reproducible |
| `BenchmarkPipeline/success` (serial) | `19 allocs/op` | `23 allocs/op` | fails, naming the benchmark and both values |
| `BenchmarkPipeline/success_parallel` (parallel, runs reproducible) | `20 allocs/op` | `23 allocs/op` | fails, naming the benchmark and both values |
| `BenchmarkLogHandler/root_logger_concurrent_requests` (parallel, runs spread more than 10%) | runs from `4` to `6 allocs/op` | `10 allocs/op` | passes, reporting it as not reproducible |
| a benchmark added by the branch | (none) | any | passes, reporting it as new |
| a benchmark removed by the branch | any | (none) | passes, reporting it as removed |
