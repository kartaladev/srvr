# test-quality Specification

## Purpose

Guarantees about srvr's own test suites: every module's tests fail on leaked goroutines, the goroutines srvr starts are proven to end with their owner, and every deliberate long-lived goroutine is listed with a reason.

## Requirements

### Requirement: Every module's test suite fails on leaked goroutines
Every test package of every srvr module SHALL run the goroutine leak check of `go.uber.org/goleak` after its tests: a test binary whose tests pass but which still has an unexpected goroutine running SHALL fail, naming the goroutine's stack.

#### Scenario: A goroutine left running fails the module's tests
- **Given** the test binary of module `<module>`, with the leak options that module defines
- **When** a test leaves a goroutine `<goroutine>` and the leak check runs
- **Then** the check reports a leak: `<reported>`

##### Examples: per module
| module | goroutine | reported |
|--------|-----------|----------|
| `github.com/kartaladev/srvr` | blocked on a channel that is never closed | yes, naming the blocked function |
| `github.com/kartaladev/srvr` | blocked on a channel the test closes at cleanup | no |
| `github.com/kartaladev/srvr/ginsrvr` | blocked on a channel that is never closed | yes, naming the blocked function |
| `github.com/kartaladev/srvr/ginsrvr` | blocked on a channel the test closes at cleanup | no |
| `github.com/kartaladev/srvr/fibersrvr` | blocked on a channel that is never closed | yes, naming the blocked function |
| `github.com/kartaladev/srvr/fibersrvr` | blocked on a channel the test closes at cleanup | no |
| `github.com/kartaladev/srvr/otelsrvr` | blocked on a channel that is never closed | yes, naming the blocked function |
| `github.com/kartaladev/srvr/otelsrvr` | blocked on a channel the test closes at cleanup | no |
| `github.com/kartaladev/srvr/otelsetup` | blocked on a channel that is never closed | yes, naming the blocked function |
| `github.com/kartaladev/srvr/otelsetup` | blocked on a channel the test closes at cleanup | no |
| `github.com/kartaladev/srvr/internal/paritytest` | blocked on a channel that is never closed | yes, naming the blocked function |
| `github.com/kartaladev/srvr/internal/paritytest` | blocked on a channel the test closes at cleanup | no |

##### Examples: boundary
| module | goroutine | reported |
|--------|-----------|----------|
| `github.com/kartaladev/srvr` | (none) | no |
| `github.com/kartaladev/srvr` | ends on its own `20ms` after the test | no |
| `github.com/kartaladev/srvr` | sleeping in `time.Sleep` with no srvr frame, outliving goleak's retry window | yes, naming `time.Sleep` |

#### Scenario: Every test package runs the leak check
- **Given** the test packages of the repository's modules
- **When** the guard parses the test files of every package directory in the repository
- **Then** the package `<package>` is `<verdict>`

##### Examples: packages
| package | verdict |
|---------|---------|
| `github.com/kartaladev/srvr` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/internal/engine` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/internal/errchain` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/internal/static` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/internal/tracectx` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/ginsrvr` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/fibersrvr` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/otelsrvr` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/otelsetup` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/internal/paritytest` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/internal/paritytest/gincycle` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/internal/benchgate` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |

##### Examples: rejected
| package | verdict |
|---------|---------|
| a package with test files and no `TestMain` | rejected: the guard fails naming the directory |
| a package whose `TestMain` calls `m.Run` without `goleak.VerifyTestMain` | rejected: the guard fails naming the directory |
| a package without test files | accepted: nothing to check |

### Requirement: Goroutines srvr starts end when their owner stops
Every goroutine srvr or a dependency starts for a server, a listener, a provider or an exporter SHALL have ended once its owner's stop (`Shutdown`, a failed `Start`, a `Run` that returned, a serve failure after `Start` reported by `Wait`, `Setup.Shutdown`, a failed `otelsetup.New`) has returned, apart from the goroutines listed as deliberate. An owner that is never stopped SHALL be reported as a leak.

#### Scenario: Lifecycle leaves no goroutine behind
- **Given** `<owner>` configured with `<setup>`
- **When** the test runs `<lifecycle>` and then checks for goroutines started since the test began
- **Then** the leak check reports `<reported>`

##### Examples: net/http
| owner | setup | lifecycle | reported |
|-------|-------|-----------|----------|
| a net/http server | one route `GET /ok` | `Start`, `GET /ok` with keep-alive, `Shutdown` | no |
| a net/http server | a `BeforeDrain` and an `AfterDrain` hook returning `nil` | `Start`, `Shutdown` | no |
| a net/http server | `WithListener` on `127.0.0.1:0` | `Start`, `Shutdown` | no |
| a net/http server | one route `GET /ok` | `Shutdown` before `Start` | no |
| a net/http server | one route `GET /ok` | `Run` with a context cancelled after it is running | no |
| a net/http server | one route `GET /ok` | `Start`, then its context cancelled, then `Shutdown` | no |
| a net/http server | `WithListener` whose `Accept` returns a permanent error after `Start` | `Start`, the failure, `Wait` | no |
| a net/http server | one route `GET /ok` | `Start`, `Wait` from `3` goroutines, `Shutdown` | no |
| a net/http server | address already in use | `Start` fails | no |
| a net/http server | a handler that panics | `Start`, `GET /ok`, `Shutdown` | no |
| a net/http server | a handler blocking until its request context ends | `Start`, `GET /block`, `Shutdown` with a `50ms` drain deadline | no |
| a net/http server | one route `GET /ok` | `Start` and never `Shutdown` | yes, naming srvr's serve goroutine |
| a net/http server | one route `GET /ok` | `Run` in a goroutine, the leak check run while it serves, then the `Run` context cancelled | yes before the cancel, naming srvr's serve goroutine; no after `Run` returned |

##### Examples: gin and fiber
| owner | setup | lifecycle | reported |
|-------|-------|-----------|----------|
| a gin server | one route `GET /ok` | `Start`, `GET /ok` with keep-alive, `Shutdown` | no |
| a gin server | one route `GET /ok` | `Run` with a context cancelled after it is running | no |
| a gin server | one route `GET /ok` | `Start` and never `Shutdown` | yes, naming srvr's serve goroutine |
| a fiber server | one route `GET /ok` | `Start`, `GET /ok` with keep-alive, `Shutdown` | no |
| a fiber server | one route `GET /ok` | `Run` with a context cancelled after it is running | no |
| a fiber server | one route `GET /ok` | `Start` and never `Shutdown` | yes, naming srvr's serve goroutine |
##### Examples: otel
| owner | setup | lifecycle | reported |
|-------|-------|-----------|----------|
| a net/http server with `otelsrvr` | `WithTracing()` and an SDK tracer provider | `Start`, `GET /ok`, `Shutdown`, provider `Shutdown` | no |
| an `otelsetup.Setup` | Prometheus enabled on `127.0.0.1:0` | `New`, `GET /metrics`, `Shutdown` | no |
| an `otelsetup.Setup` | OTLP metrics and traces over HTTP to a test collector | `New`, one export, `Shutdown` | no |
| an `otelsetup.Setup` | OTLP metrics and traces over gRPC to an unreachable endpoint | `New`, `Shutdown` | no |
| an `otelsetup.Setup` | Prometheus on an address already in use | `New` fails | no |
| an `otelsetup.Setup` | Prometheus enabled on `127.0.0.1:0` | `New` and never `Shutdown` | yes, naming the metrics server's serve goroutine |

##### Examples: parity harness
| owner | setup | lifecycle | reported |
|-------|-------|-----------|----------|
| a fiber server of the `internal/paritytest` harness | one route `GET /ok`, fasthttp's `MaxIdleWorkerDuration` shortened to `20ms` | `Start`, `GET /ok`, `Shutdown` | no, including fasthttp's idle-worker cleaner `(*workerPool).Start.func2` |

#### Scenario: Abandoned consumer code is the only goroutine left
- **Given** `<owner>` with consumer code that ignores its context and blocks until the test releases it
- **When** srvr abandons that code at its budget and the test checks for goroutines `<release>`
- **Then** the leak check reports `<reported>`

##### Examples: abandoned
| owner | release | reported |
|-------|---------|----------|
| a `BeforeDrain` hook with a `50ms` `Shutdown` context | after releasing the hook | no |
| a `BeforeDrain` hook with a `50ms` `Shutdown` context | before releasing the hook | yes, only the goroutine running the hook |
| an `AfterDrain` hook with a `50ms` `ShutdownHookTimeout` | after releasing the hook | no |
| a route check with a `Start` context whose deadline is `20ms` | after releasing the check | no |
| a route check with a `Start` context whose deadline is `20ms` | before releasing the check | yes, only the goroutine running the check |

### Requirement: Deliberate long-lived goroutines are listed with a reason
A goroutine that outlives its test on purpose SHALL be excluded only by an ignore naming one function, defined once per module next to a comment giving the reason. Each ignore SHALL still be needed and SHALL NOT hide a server that was never stopped.

#### Scenario: Each ignore is needed and narrow
- **Given** module `<module>` whose leak options ignore `<function>` because `<reason>`
- **When** the test makes that goroutine run and checks for goroutines without and then with the module's leak options
- **Then** without the options the goroutine is reported, and with them it is `<with options>`

##### Examples: ignores
| module | function | reason | with options |
|--------|----------|--------|--------------|
| `github.com/kartaladev/srvr/fibersrvr` | `github.com/valyala/fasthttp.updateServerDate.func1` | fasthttp refreshes its `Date` header once per process and never stops | not reported |
| `github.com/kartaladev/srvr/fibersrvr` | `github.com/valyala/fasthttp.(*workerPool).Start.func2` | fasthttp's idle-worker cleaner sleeps up to `10s` (`MaxIdleWorkerDuration`'s default) before it sees `Stop`, and the package's `Example` runs a server with that default, which it cannot shorten | not reported |
| `github.com/kartaladev/srvr/internal/paritytest` | `github.com/valyala/fasthttp.updateServerDate.func1` | as in `fibersrvr` | not reported |
| `github.com/kartaladev/srvr/internal/paritytest/gincycle` | the package's named cyclic-error handler | gin's `c.Error` never returns for a cyclic chain, so the handler goroutine cannot be stopped (engine-parity documented difference) | not reported |

##### Examples: not hidden
| module | function | reason | with options |
|--------|----------|--------|--------------|
| `github.com/kartaladev/srvr/fibersrvr` | a fiber server started and never shut down | its serve and worker goroutines are not deliberate | reported, naming srvr's serve goroutine |
| `github.com/kartaladev/srvr/internal/paritytest` | a gin handler blocked on a channel never closed | only the `gincycle` binary ignores its handler | reported, naming the blocked handler |
| `github.com/kartaladev/srvr/internal/paritytest` | fasthttp's idle-worker cleaner of a fiber server left at the default `MaxIdleWorkerDuration`, after `Shutdown` | the parity binary does not ignore the cleaner: its harness shortens the duration so the cleaner ends | reported, naming `github.com/valyala/fasthttp.(*workerPool).Start.func2` |

### Requirement: Hostile inputs are fuzzed
Every parser or transform of untrusted input SHALL have a fuzz test with seeds covering its edge cases and a property it must keep, run on its seed corpus by every `go test`: `traceparent` parsing, route pattern joining, attribute cutting and the log line written for a hostile value.

#### Scenario: Fuzzed properties
- **Given** the fuzz test `<fuzz>`
- **When** `go test -run <fuzz>` runs its seeds and `go test -fuzz <fuzz> -fuzztime 30s` explores
- **Then** no input violates `<property>`

##### Examples:
| fuzz | property |
|------|----------|
| `FuzzTraceparent` | an accepted header keeps its trace id and parent id, its sampled flag equals the flags byte's low bit, and a rejected one yields a fresh valid trace |
| `FuzzJoinPattern` | no panic, the pattern starts with `/` and contains no `//` outside the host part |
| `FuzzCutString` | the result is at most the limit plus the suffix, is a prefix of the input plus the suffix, and is valid UTF-8 when the input is |
| `FuzzLogLine` | logging any string as a message and an attribute writes exactly one line that decodes as JSON back to the cut value |

### Requirement: Test hygiene is enforced by the linter
`golangci-lint run` in every module SHALL fail on a test helper without `t.Helper()`, a parallel test whose subtests are not parallel, an unchecked type assertion, `context.Background()` where `t.Context()` applies, and a core non-test import outside the standard library and prblm.

#### Scenario: Lint findings fail the run
- **Given** a module with `<code>`
- **When** `golangci-lint run ./...` runs with the repository's configuration
- **Then** it reports `<linter>`

##### Examples:
| code | linter |
|------|--------|
| a test helper taking `t *testing.T` that does not call `t.Helper()` | `thelper` |
| a `t.Parallel()` test whose `t.Run` subtests do not call `t.Parallel()` | `tparallel` |
| `v := x.(T)` in a test | `forcetypeassert` |
| `context.Background()` inside a test function body | `usetesting` |
| a core non-test file importing `go.opentelemetry.io/otel` | `depguard` |
| none of these | no finding |

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

### Requirement: Internal packages depend in one direction
The core's internal library packages SHALL import only the standard library, prblm and the internal packages listed below them in the recorded order `internal/tracectx`, `internal/errchain`, `internal/static`, `internal/engine`; no package imports `internal/engine`. Commands such as `internal/benchgate` are skipped. A guard test SHALL fail, naming both packages, when an import breaks the order.

#### Scenario: Import direction guard
- **Given** the recorded package order of the core module's internal packages
- **When** the guard lists the imports of `<package>` with `go list -deps` (or `golang.org/x/tools/go/packages`) and finds `<import>`
- **Then** the guard `<verdict>`

##### Examples: accepted
| package | import | verdict |
|---------|--------|---------|
| `github.com/kartaladev/srvr/internal/engine` | `github.com/kartaladev/srvr/internal/errchain` | passes |
| `github.com/kartaladev/srvr/internal/errchain` | `github.com/kartaladev/prblm` | passes |
| `github.com/kartaladev/srvr/internal/static` | `github.com/kartaladev/srvr/internal/errchain` | passes |
| `github.com/kartaladev/srvr` | `github.com/kartaladev/srvr/internal/engine` | passes |

##### Examples: rejected
| package | import | verdict |
|---------|--------|---------|
| `github.com/kartaladev/srvr/internal/errchain` | `github.com/kartaladev/srvr/internal/engine` | fails naming `internal/errchain` and `internal/engine` |
| `github.com/kartaladev/srvr/internal/errchain` | `github.com/kartaladev/srvr/internal/static` (a package listed above it) | fails naming both packages |
| an internal package missing from the recorded order | any srvr package | fails naming the unlisted package |
| `github.com/kartaladev/srvr/internal/static` | `github.com/kartaladev/srvr/internal/engine` | fails naming both packages |
| `github.com/kartaladev/srvr/internal/static` | `github.com/kartaladev/srvr` (the root package) | fails naming both packages |

##### Examples: boundary
| package | import | verdict |
|---------|--------|---------|
| an internal package with only `_test.go` imports of a higher package (an external `_test` package) | `github.com/kartaladev/srvr/internal/engine` | passes: test-only imports are outside the rule |
| `github.com/kartaladev/srvr/internal/engine` before any extraction (slice 2) | standard library, prblm and `internal/tracectx` | passes with a one-entry order |

### Requirement: The refactor keeps the pipeline's performance
`BenchmarkPipeline` of every engine SHALL be compared, with `benchstat` over 10 interleaved runs on one machine, between the change's base commit and the change's last slice. A regression over 5% `ns/op` with `p < 0.05`, or any extra `allocs/op`, on any row SHALL stop the work for an owner decision.

#### Scenario: Pipeline benchmark comparison
- **Given** `BenchmarkPipeline` row `<row>` on engine `<engine>` measured at the base commit and after the last slice
- **When** `benchstat` compares the two sets of 10 interleaved runs and finds `<difference>`
- **Then** the result is `<result>`

##### Examples:
| row | engine | difference | result |
|-----|--------|------------|--------|
| every row | `net/http` | `ns/op` within 5%, same `allocs/op` | passes |
| every row | `gin` | `ns/op` within 5%, same `allocs/op` | passes |
| every row | `fiber` | `ns/op` within 5%, same `allocs/op` | passes |
| any row | `net/http` | one more `allocs/op` than the base | stops for the owner, naming the row and both values |
| any row | `gin` | `ns/op` 6% over the base with `p < 0.05` | stops for the owner, naming the row and both values |
| any row | `fiber` | `ns/op` 6% over the base with `p >= 0.05` (`~`) | passes, reported as not significant |
| any row | any engine | exactly 5.0% over the base with `p < 0.05` | passes (the limit is "over 5%") |
| any row | any engine | measured while another benchmark session uses the machine | not run: the owner is asked first and the numbers are discarded |

### Requirement: The error renderer path is benchmarked against its baseline
The error path and the happy path SHALL be benchmarked on every engine (`ns/op`, `B/op`, `allocs/op`) for the change's base commit, the default renderer and a trivial custom renderer, compared with `benchstat` over 10 interleaved runs on one machine. A default-renderer regression over 5% `ns/op` on any error row, or any extra `allocs/op` on the success row, SHALL stop the work for an owner decision.

#### Scenario: Renderer benchmark comparison
- **Given** `BenchmarkPipeline` row `<row>` on engine `<engine>` measured at the base commit and with `<renderer>`
- **When** `benchstat` compares the two sets of 10 interleaved runs
- **Then** the result is `<result>`

##### Examples:
| row | engine | renderer | result |
|-----|--------|----------|--------|
| `success` | `net/http` | default | passes with the same `allocs/op` as the base |
| `success` | `gin` | default | passes with the same `allocs/op` as the base |
| `success` | `fiber` | default | passes with the same `allocs/op` as the base |
| `success` | `net/http` | default, one more `allocs/op` than the base | stops for the owner, naming the row and both values |
| `handler_error` | every engine | default, `ns/op` within 5% of the base | passes |
| `handler_error` | `gin` | default, `ns/op` 6% over the base with `p < 0.05` | stops for the owner, naming the row and both values |
| `handler_error` | `fiber` | default, `ns/op` 6% over the base with `p ≥ 0.05` (`~`) | passes, reported as not significant |
| `not_found` and `method_not_allowed` | every engine | default | passes within 5% `ns/op` |
| `panic` | every engine | default | passes within 5% `ns/op` |
| `handler_error`, `not_found`, `method_not_allowed`, `panic` | every engine | trivial custom renderer | recorded for information; no threshold |
| any row | any engine | a run while another benchmark session uses the machine | not run: the owner is asked first and the numbers are discarded |
