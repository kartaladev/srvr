# Spec Delta

## MODIFIED Requirements

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

## ADDED Requirements

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
