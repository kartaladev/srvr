# Spec Delta

## MODIFIED Requirements

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

