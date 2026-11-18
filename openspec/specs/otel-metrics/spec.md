# otel-metrics Specification

## Purpose

Defines the OpenTelemetry HTTP server metrics recorded by the `otelsrvr` module, their names, units, attributes and cardinality bounds, using the OpenTelemetry API only.

## Requirements

### Requirement: Request duration is recorded
With `otelsrvr.New()` installed as an observer, every finished request SHALL record one `http.server.request.duration` histogram value in seconds, through the configured `MeterProvider` (default: the global one), with instrumentation scope `github.com/kartaladev/srvr/otelsrvr`.

#### Scenario: Duration per engine
- **Given** a `<engine>` server with `otelsrvr.New(otelsrvr.WithMeterProvider(mp))` where `mp` uses a manual reader
- **When** a client sends `GET /users/42` to route `GET /users/{id}` in that engine's syntax that writes `200`
- **Then** the reader collects `http.server.request.duration` with unit `s` and count `1`
- **And** the data point has `http.route` `<route>` and `http.response.status_code` `200`

##### Examples:
| engine | route |
|--------|-------|
| net/http | `/users/{id}` |
| gin | `/users/:id` |
| fiber | `/users/:id` |

#### Scenario: Provider selection
- **Given** `<config>`
- **When** a request is handled
- **Then** the duration is recorded on `<provider>`

##### Examples:
| config | provider |
|--------|----------|
| `otelsrvr.New()` with a global provider set by `otel.SetMeterProvider` | the global provider |
| `otelsrvr.New()` with no global provider set | the no-op global provider, and the request succeeds |
| `otelsrvr.New(otelsrvr.WithMeterProvider(mp))` | `mp` |
| `otelsrvr.New(otelsrvr.WithMeterProvider(nil))` | none; `New` returns an error wrapping `otelsrvr.ErrConfig` |

### Requirement: Attributes have bounded cardinality
Duration data points SHALL carry only `http.request.method`, `http.route` (omitted when unmatched), `http.response.status_code`, `url.scheme` and, for failed requests, `error.type`: `semconv.ErrorType` of the returned error, `panic` for a recovered panic, or the status code for a failure without an error value (5xx written directly, unmatched 404/405). Unknown methods SHALL be `_OTHER`; raw paths SHALL never be attributes.

#### Scenario: Attribute values
- **Given** a server with routes `GET /users/{id}` and `POST /users`
- **When** a client sends `<request>` and the outcome is `<outcome>`
- **Then** the data point has `http.request.method` `<method>`, `http.route` `<route>` and `error.type` `<error_type>`

##### Examples: normal
| request | outcome | method | route | error_type |
|---------|---------|--------|-------|------------|
| `GET /users/42` | `200` | `GET` | `/users/{id}` | (none) |
| `GET /users/43` | `200` | `GET` | `/users/{id}` | (none) |
| `POST /users` | handler returns `prblm.ErrConflict` | `POST` | `/users` | `*errors.errorString` |
| `POST /users` | handler returns `errors.New("x")` | `POST` | `/users` | `*errors.errorString` |
| `POST /users` | handler returns `fmt.Errorf("insert: %w", &net.OpError{Op: "dial", Err: errors.New("refused")})` | `POST` | `/users` | `*net.OpError` |
| `POST /users` | handler returns `fmt.Errorf("insert: %w", dbErr)` where `dbErr` has `ErrorType() string` returning `db_timeout` | `POST` | `/users` | `db_timeout` |
| `POST /users` | handler returns `errors.Join(errors.New("x"), errors.New("y"))` | `POST` | `/users` | `*errors.joinError` |
| `POST /users` | handler returns `errors.Join(errors.New("x"), dbErr)` | `POST` | `/users` | `db_timeout` |
| `POST /users` | handler panics | `POST` | `/users` | `panic` |
| `POST /users` | handler panics with `http.ErrAbortHandler` (a recovered panic: connection aborted, no `http.response.status_code`, core logs `srvr.response.aborted` `true`) | `POST` | `/users` | `panic` |
| `GET /users/42` | handler writes `503` and returns `nil` | `GET` | `/users/{id}` | `503` |

##### Examples: hostile
| request | outcome | method | route | error_type |
|---------|---------|--------|-------|------------|
| `GET /random/a1b2c3` | `404` | `GET` | (none) | `404` |
| `PROPFIND /users` | `405` | `_OTHER` | (none) | `405` |
| `XYZZY /users/42` | `405` | `_OTHER` | (none) | `405` |
| `get /users/42` | `405` | `_OTHER` | (none) | `405` |
| `QUERY /users` | `405` | `QUERY` | (none) | `405` |

#### Scenario: Cardinality under path scanning
- **Given** a server with route `GET /users/{id}`
- **When** clients send `<requests>`
- **Then** the number of distinct `http.route` values collected is `<distinct>`

##### Examples:
| requests | distinct |
|----------|----------|
| 10000 requests to distinct random paths | `0` (unmatched requests carry no `http.route`) |
| 10000 requests to `/users/<n>` with distinct `<n>` | `1` |

### Requirement: Histogram buckets follow semantic conventions
The duration histogram SHALL use the semantic-convention advisory boundaries `0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10` by default, and the consumer SHALL be able to replace them, by option or config, with strictly increasing, positive, finite boundaries.

#### Scenario: Bucket boundaries
- **Given** `otelsrvr.New` with `<buckets>`
- **When** the meter is created
- **Then** the result is `<result>`

##### Examples:
| buckets | result |
|---------|--------|
| (none) | the semantic-convention boundaries |
| `WithDurationBuckets(0.1, 1, 10)` | boundaries `0.1, 1, 10` |
| `WithConfig(otelsrvr.Config{DurationBuckets: []float64{0.1, 1, 10}})` | boundaries `0.1, 1, 10` |
| `WithDurationBuckets(1, 0.1)` | an error wrapping `otelsrvr.ErrConfig` |
| `WithDurationBuckets()` | an error wrapping `otelsrvr.ErrConfig` |
| `WithDurationBuckets(-1, 1)` | an error wrapping `otelsrvr.ErrConfig` |
| `WithDurationBuckets(1, 1)` | an error wrapping `otelsrvr.ErrConfig` |
| `WithDurationBuckets(0.1, math.NaN())` | an error wrapping `otelsrvr.ErrConfig` |
| `WithDurationBuckets(0.1, math.Inf(1))` | an error wrapping `otelsrvr.ErrConfig` |

### Requirement: Optional metrics are opt-in
`http.server.active_requests` (UpDownCounter, attributes `http.request.method` and `url.scheme`) and `http.server.request.body.size` / `http.server.response.body.size` (histograms in bytes) SHALL be recorded only when enabled by option or config.

#### Scenario: Optional instruments
- **Given** `otelsrvr.New` with `<config>`
- **When** a request with a 5-byte body is answered with a 12-byte body and the reader collects
- **Then** the collected instruments are `<instruments>`

##### Examples:
| config | instruments |
|--------|-------------|
| (none) | `http.server.request.duration` |
| `WithActiveRequests()` | `http.server.request.duration`, `http.server.active_requests` with value `0` after completion |
| `WithBodySizes()` | `http.server.request.duration`, `http.server.request.body.size` sum `5`, `http.server.response.body.size` sum `12` |
| `WithConfig(otelsrvr.Config{ActiveRequests: true, BodySizes: true})` | all four instruments |

#### Scenario: Active requests under concurrency
- **Given** `otelsrvr.New(otelsrvr.WithActiveRequests())` and `<n>` concurrent requests held open by the handler
- **When** the reader collects before and after they are released
- **Then** `http.server.active_requests` is `<during>` then `<after>`

##### Examples:
| n | during | after |
|---|--------|-------|
| `50` | `50` | `0` |
| `50`, every handler panics | `50` | `0` |

### Requirement: otelsrvr is configurable by options and by config struct
`otelsrvr.WithConfig(otelsrvr.Config)` SHALL set every field of the config, a zero field meaning its default, and individual options SHALL set one field each. Options SHALL apply in argument order, so a later option overrides an earlier one. An invalid value from either source SHALL fail `otelsrvr.New` with an error wrapping `otelsrvr.ErrConfig`.

#### Scenario: Configuration sources
- **Given** `otelsrvr.New(<options>)`
- **When** the observer is built
- **Then** the effective `<setting>` is `<effective>`

##### Examples:
| options | setting | effective |
|---------|---------|-----------|
| (none) | duration buckets | the semantic-convention boundaries |
| `WithConfig(otelsrvr.Config{DurationBuckets: []float64{0.1, 1}})`, `WithDurationBuckets(5, 10)` | duration buckets | `5, 10` |
| `WithDurationBuckets(5, 10)`, `WithConfig(otelsrvr.Config{})` | duration buckets | the semantic-convention boundaries |
| `WithConfig(otelsrvr.Config{Tracing: true})` | tracing | on |
| `WithTracing()`, `WithConfig(otelsrvr.Config{BodySizes: true})` | tracing | off |
| `WithConfig(otelsrvr.Config{DurationBuckets: []float64{1, 0.1}})` | (construction) | an error wrapping `otelsrvr.ErrConfig` naming `DurationBuckets` |

### Requirement: Instrument creation failures do not fail serving
If the meter returns an error creating an instrument, `otelsrvr.New` SHALL return that error wrapped in `otelsrvr.ErrConfig`; recording through a provider that has been shut down SHALL NOT fail requests.

#### Scenario: Provider faults
- **Given** `<provider>`
- **When** `<action>`
- **Then** the outcome is `<outcome>`

##### Examples:
| provider | action | outcome |
|----------|--------|---------|
| a meter that fails to create the histogram | `otelsrvr.New` is called | an error wrapping `otelsrvr.ErrConfig` |
| an SDK provider already shut down | a client sends `GET /ping` | status `200` |
