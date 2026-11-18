# Spec Delta

## Purpose

Guarantees about srvr's own test suites: every module's tests fail on leaked goroutines, the goroutines srvr starts are proven to end with their owner, and every deliberate long-lived goroutine is listed with a reason.

## ADDED Requirements

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
| `github.com/kartaladev/srvr/engine` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/internal/tracectx` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/ginsrvr` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/fibersrvr` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/otelsrvr` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/otelsetup` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/internal/paritytest` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |
| `github.com/kartaladev/srvr/internal/paritytest/gincycle` | accepted: its `TestMain` calls `goleak.VerifyTestMain` |

##### Examples: rejected
| package | verdict |
|---------|---------|
| a package with test files and no `TestMain` | rejected: the guard fails naming the directory |
| a package whose `TestMain` calls `m.Run` without `goleak.VerifyTestMain` | rejected: the guard fails naming the directory |
| a package without test files | accepted: nothing to check |
| a test file that does not parse | rejected: the guard fails naming the file and the parse error |

### Requirement: Goroutines srvr starts end when their owner stops
Every goroutine srvr or a dependency starts for a server, a listener, a provider or an exporter SHALL have ended once its owner's stop (`Shutdown`, a failed `Start`, `Setup.Shutdown`, a failed `otelsetup.New`) has returned, apart from the goroutines listed as deliberate. An owner that is never stopped SHALL be reported as a leak.

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
| a net/http server | one route `GET /ok` | `Start` with a context cancelled after it is running | no |
| a net/http server | address already in use | `Start` fails | no |
| a net/http server | a handler that panics | `Start`, `GET /ok`, `Shutdown` | no |
| a net/http server | a handler blocking until its request context ends | `Start`, `GET /block`, `Shutdown` with a `50ms` drain deadline | no |
| a net/http server | one route `GET /ok` | `Start` and never `Shutdown` | yes, naming srvr's serve goroutine |

##### Examples: gin and fiber
| owner | setup | lifecycle | reported |
|-------|-------|-----------|----------|
| a gin server | one route `GET /ok` | `Start`, `GET /ok` with keep-alive, `Shutdown` | no |
| a gin server | one route `GET /ok` | `Start` and never `Shutdown` | yes, naming srvr's serve goroutine |
| a fiber server | one route `GET /ok` | `Start`, `GET /ok` with keep-alive, `Shutdown` | no |
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
