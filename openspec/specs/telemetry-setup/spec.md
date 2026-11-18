# telemetry-setup Specification

## Purpose

Defines the opt-in `otelsetup` module that wires the OpenTelemetry SDK and exporters for an application: Prometheus scraping, OTLP export, the service resource, and flushing tied to the server lifecycle.

## Requirements

### Requirement: Setup builds providers from configuration
`otelsetup.New(ctx, opts...)` SHALL build a `MeterProvider` and, when tracing export is configured, a `TracerProvider`, with a resource carrying `service.name`, `service.version` and `deployment.environment.name`, and SHALL return an error naming the field for invalid configuration.

#### Scenario: Provider construction
- **Given** `otelsetup.WithConfig(otelsetup.Config{<config>})`
- **When** `otelsetup.New` is called with it
- **Then** the result is `<result>`

##### Examples: accepted
| config | result |
|--------|--------|
| `Prometheus: {Enabled: true}` | a meter provider with a Prometheus reader and no tracer provider |
| `OTLP: {Endpoint: "http://collector:4318", Protocol: "http/protobuf", Metrics: true, Traces: true}` | meter and tracer providers with OTLP exporters |
| `Service: {Name: "registration-api", Version: "1.4.2"}`, `Prometheus: {Enabled: true}` | a resource with `service.name` `registration-api` and `service.version` `1.4.2` |
| `OTLP: {Endpoint: "", Protocol: "http/protobuf", Traces: true}` | a tracer provider whose exporter endpoint comes from the OpenTelemetry SDK (environment, else the SDK default) |
| `OTLP: {Endpoint: "collector:4318", Metrics: true}` | a meter provider with an OTLP exporter (a `host:port` without a scheme uses TLS) |

##### Examples: rejected
| config | result |
|--------|--------|
| (empty) | an error wrapping `otelsetup.ErrConfig` stating no exporter is enabled |
| `OTLP: {Endpoint: "http://collector:4318", Protocol: "thrift"}` | an error wrapping `otelsetup.ErrConfig` naming `Protocol` |
| `OTLP: {Endpoint: "http://collector:4318", Protocol: "http/protobuf"}` | an error wrapping `otelsetup.ErrConfig` stating neither `Metrics` nor `Traces` is enabled |
| `Prometheus: {Enabled: true, Addr: "nope"}` | an error wrapping `otelsetup.ErrConfig` naming `Addr` |
| `FlushTimeout: -1s` | an error wrapping `otelsetup.ErrConfig` naming `FlushTimeout` |
| `OTLP: {Endpoint: "http://collector:4318 ", Metrics: true}` (trailing space) | an error wrapping `otelsetup.ErrConfig` naming `OTLP.Endpoint` |
| `OTLP: {Endpoint: "http://[::1", Metrics: true}` | an error wrapping `otelsetup.ErrConfig` naming `OTLP.Endpoint` |
| `OTLP: {Endpoint: "ftp://collector:4318", Metrics: true}` | an error wrapping `otelsetup.ErrConfig` naming `OTLP.Endpoint` |
| `OTLP: {Endpoint: "http://", Metrics: true}` | an error wrapping `otelsetup.ErrConfig` naming `OTLP.Endpoint` |
| `OTLP: {Endpoint: "collector", Metrics: true}` | an error wrapping `otelsetup.ErrConfig` naming `OTLP.Endpoint` |
| `OTLP: {Endpoint: ":4318", Metrics: true}` | an error wrapping `otelsetup.ErrConfig` naming `OTLP.Endpoint` |
| `OTLP: {Endpoint: "collector:otlp", Metrics: true}` | an error wrapping `otelsetup.ErrConfig` naming `OTLP.Endpoint` |
| `Prometheus: {Enabled: true, Path: "/metrics/{"}` | an error wrapping `otelsetup.ErrConfig` naming `Prometheus.Path`, without panicking and with no listener left bound |
| `Prometheus: {Enabled: true, Path: "/metrics x"}` | an error wrapping `otelsetup.ErrConfig` naming `Prometheus.Path`, without panicking and with no listener left bound |

### Requirement: Setup is configurable by options, by config struct and by the SDK environment
`otelsetup.WithConfig(Config)` SHALL set every field (zero meaning default) and individual options one field each, applied in argument order. srvr SHALL NOT read environment variables itself; fields left empty SHALL fall back to what the OpenTelemetry SDK reads from its standard `OTEL_*` variables, and an explicit value SHALL always beat the environment.

#### Scenario: Configuration precedence
- **Given** `otelsetup.New` with `<options>` and environment `<env>`
- **When** setup completes
- **Then** the effective `<setting>` is `<effective>`

##### Examples:
| options | env | setting | effective |
|---------|-----|---------|-----------|
| `WithConfig(Config{OTLP: {Endpoint: "http://collector:4318", Metrics: true}})` | `OTEL_EXPORTER_OTLP_ENDPOINT=http://other:4318` | OTLP endpoint | `http://collector:4318` |
| `WithConfig(Config{OTLP: {Protocol: "http/protobuf", Metrics: true}})` | `OTEL_EXPORTER_OTLP_ENDPOINT=http://other:4318` | OTLP endpoint | `http://other:4318` |
| `WithConfig(Config{Service: {Name: "registration-api"}, Prometheus: {Enabled: true}})` | `OTEL_SERVICE_NAME=from-env` | `service.name` | `registration-api` |
| `WithPrometheus()` | `OTEL_SERVICE_NAME=from-env` | `service.name` | `from-env` |
| `WithConfig(Config{FlushTimeout: 2*time.Second, Prometheus: {Enabled: true}})`, `WithFlushTimeout(time.Second)` | (none) | flush timeout | `1s` |
| `WithFlushTimeout(time.Second)`, `WithConfig(Config{Prometheus: {Enabled: true}})` | (none) | flush timeout | `5s` |

### Requirement: Prometheus metrics are served on a separate private listener
With Prometheus enabled, setup SHALL serve the exposition format on its own listener (default `127.0.0.1:9464`, path `/metrics`) from a dedicated registry, never the global default registry, and never on the application's public listener unless the consumer mounts the handler explicitly. The listener runs unless `DisableListener` is true (zero value: listen).

#### Scenario: Scraping
- **Given** Prometheus enabled with `<config>` and a srvr server using `otelsrvr.WithMeterProvider(setup.MeterProvider())`
- **When** one request to `GET /users/42` completes and `<scrape>` is fetched
- **Then** the scrape returns status `<status>` and `<contains>`

##### Examples:
| config | scrape | status | contains |
|--------|--------|--------|----------|
| (defaults) | `http://127.0.0.1:9464/metrics` | `200` | `http_server_request_duration_seconds_count{` with `http_route="/users/{id}"` |
| `Addr: "127.0.0.1:0"` | `/metrics` on `setup.MetricsAddr()` | `200` | `http_server_request_duration_seconds_bucket` |
| `Path: "/internal/metrics"` | `/internal/metrics` | `200` | `http_server_request_duration_seconds_count` |
| (defaults) | the application listener `/metrics` | `404` | a problem body |
| `DisableListener: true` and the consumer mounts `setup.MetricsHandler()` on a group | the mounted path | `200` | `http_server_request_duration_seconds_count` |

#### Scenario: Registry isolation
- **Given** `<setup>`
- **When** both are created in the same process
- **Then** the outcome is `<outcome>`

##### Examples:
| setup | outcome |
|-------|---------|
| two `otelsetup.New` calls with Prometheus on ports `0` and `0` | both succeed, each serving only its own metrics |
| one `otelsetup.New` and an unrelated collector on `prometheus.DefaultRegisterer` | both succeed; the default registry is unchanged |
| `otelsetup.New` with Prometheus on an address already in use | an error matching `*net.OpError` and no providers left running |

### Requirement: Setup can install global providers on request
Setup SHALL NOT change global OpenTelemetry state by default; with `SetGlobal: true` it SHALL install its providers and a W3C TraceContext plus Baggage propagator as globals.

#### Scenario: Globals
- **Given** setup with `<config>`
- **When** setup completes
- **Then** `otel.GetMeterProvider()` is `<meter>` and the global propagator `<propagator>`

##### Examples:
| config | meter | propagator |
|--------|-------|------------|
| (defaults) | unchanged | unchanged |
| `SetGlobal: true` | the setup's provider | injects `traceparent` |

### Requirement: Shutdown flushes within a bound and joins the server lifecycle
`setup.Shutdown(ctx)` SHALL flush and shut down every provider and stop the metrics listener within `FlushTimeout` (default 5s) or `ctx`, whichever is earlier, and SHALL be idempotent. `setup.ShutdownHook()` SHALL return a srvr option registering `setup.Shutdown` as a core `srvr.AfterDrain` shutdown hook, so it runs once after the drain on every stop path, including every `Start` failure after the server leaves its initial state, and never for `Start(nil)` or misuse errors.

#### Scenario: Flush outcomes
- **Given** setup with an OTLP exporter whose collector `<collector>`
- **When** `setup.Shutdown` is called with a `<deadline>` deadline
- **Then** it returns `<result>` within `<bound>`

##### Examples:
| collector | deadline | result | bound |
|-----------|----------|--------|-------|
| accepts exports | `10s` | `nil` | `1s` |
| is unreachable | `10s` | an error wrapping the export error | `5s` (FlushTimeout) |
| hangs | `200ms` | an error matching `context.DeadlineExceeded` | `300ms` |
| (none, Prometheus only) | `10s` | `nil` | `1s` |

#### Scenario: Lifecycle misuse
- **Given** setup that `<state>`
- **When** `setup.Shutdown` is called
- **Then** it returns `<result>`

##### Examples:
| state | result |
|-------|--------|
| was shut down already | `nil` |
| is being shut down concurrently | `nil` once the first shutdown finishes |

#### Scenario: Ordering with the server
- **Given** a started srvr server with `setup.ShutdownHook()` and a request in flight, and an OTLP fake collector
- **When** the server stops because `<trigger>`
- **Then** the in-flight request's duration is `<exported>`
- **And** `setup.Shutdown` has run `<runs>`

##### Examples:
| trigger | exported | runs |
|---------|----------|------|
| the `Start` context is cancelled | recorded and present in the final export | once, after the drain |
| `Shutdown(ctx)` is called | recorded and present in the final export | once, after the drain |
| `Shutdown(ctx)` is called and then `setup.Shutdown(ctx)` by the consumer | recorded and present in the final export | once; the consumer's call returns `nil` |

#### Scenario: Server that never served
- **Given** a srvr server with `setup.ShutdownHook()` where `<event>`
- **When** `<then>` is called
- **Then** the setup's providers are `<providers>`
- **And** `setup.Shutdown` has run `<runs>`

##### Examples:
| event | then | providers | runs |
|-------|------|-----------|------|
| the server was never started | `Shutdown(ctx)` | shut down and the metrics listener closed | once |
| `Start` failed to bind its address | `Shutdown(ctx)` | shut down and the metrics listener closed | once, when `Start` failed; `Shutdown` adds no run |
| `Start` failed because a route configurer panicked | `Shutdown(ctx)` | shut down and the metrics listener closed | once, when `Start` failed |
| `Start` failed because a route check returned an error | `Shutdown(ctx)` | shut down and the metrics listener closed | once, when `Start` failed |
| `Start` was called with an already-cancelled context | `Shutdown(ctx)` | shut down and the metrics listener closed | once, when `Start` returned |
| `Start(nil)` returned an error wrapping `srvr.ErrConfig` | nothing else | still running | `0` times |
| a second `Start` returned `srvr.ErrServerStarted` while the first serves | nothing else | still running | `0` times |
| `Start` after `Shutdown` returned `srvr.ErrServerClosed` | nothing else | shut down by the earlier `Shutdown` | once, from the earlier `Shutdown` |

### Requirement: Export failures never fail requests
A failing or slow exporter SHALL NOT block or fail request handling. Export errors SHALL go to the global OpenTelemetry error handler; with `SetGlobal: true` setup SHALL install a handler logging them as `otel.export.failed` at `WARN`, at most once per minute per error kind. A logger whose handler panics SHALL NOT crash the process or stop later export errors from being logged: the panic is recovered and that record is dropped.

#### Scenario: Collector down during traffic
- **Given** setup with OTLP to an unreachable collector, `<config>`, and 100 requests handled
- **When** the periodic reader exports
- **Then** every request returned `<status>`
- **And** the srvr log has `<records>` `otel.export.failed` records within one minute

##### Examples:
| config | status | records |
|--------|--------|---------|
| `SetGlobal: true` | `200` | `1` |
| `SetGlobal: false` | `200` | `0` (errors reach the OpenTelemetry default handler) |

#### Scenario: Faulty logger
- **Given** setup with `SetGlobal: true` and `WithLogger` whose handler `<fault>`
- **When** `otel.Handle` is called with an export error and then with an export error of another kind
- **Then** the outcome is `<outcome>`

##### Examples:
| fault | outcome |
|-------|---------|
| panics with `"log handler boom"` | no panic escapes `otel.Handle` and the second error reaches the handler too |
| calls `otel.Handle` again from inside `Handle` | no deadlock: the nested error is handled and both calls return |

### Requirement: Invalid otelsetup options fail construction
`otelsetup.New` SHALL return an error wrapping `otelsetup.ErrConfig` that names the option's position for a nil option, never skipping it silently, and SHALL build no provider and open no listener when it fails.

#### Scenario: Nil option
- **Given** the options `<options>`
- **When** `otelsetup.New(ctx, ...)` is called
- **Then** it returns `<result>`
- **And** the metrics address `<listener>`

##### Examples:
| options | result | listener |
|---------|--------|----------|
| `WithPrometheus()`, `nil` | an error wrapping `otelsetup.ErrConfig` naming `Option` `1` | is not bound |
| `nil` | an error wrapping `otelsetup.ErrConfig` naming `Option` `0` | is not bound |
| `WithPrometheus()` | a setup and `nil` | is bound |

### Requirement: OTLP headers are copied at construction
The OTLP headers a consumer passes, through `WithOTLP` or `WithConfig`, SHALL be copied when `New` runs, so changing the caller's map afterwards never changes exported requests and never races with export goroutines.

#### Scenario: Caller mutates headers after New
- **Given** setup built with OTLP headers `m` holding `api-key: one`, exporting to a fake collector
- **When** the caller sets `<mutation>` after `New` while metrics are exported every `10ms`
- **Then** the collector receives `api-key` `one` on every export
- **And** `go test -race` reports `<race>`

##### Examples:
| mutation | race |
|----------|------|
| `m["api-key"] = "two"` | no race |
| `delete(m, "api-key")` | no race |

### Requirement: The metrics listener is closed at the shutdown deadline
When the deadline of `setup.Shutdown` passes while a scrape is still being served, setup SHALL close the metrics listener's remaining connections and wait for its serving goroutine to end before returning the deadline error.

#### Scenario: In-flight scrape at the deadline
- **Given** setup with Prometheus and a scrape `<scrape>`
- **When** `setup.Shutdown` is called with a `<deadline>` deadline
- **Then** it returns `<result>`
- **And** no goroutine of the metrics listener remains (goleak)

##### Examples:
| scrape | deadline | result |
|--------|----------|--------|
| whose client stalls reading the response | `100ms` | an error matching `context.DeadlineExceeded` |
| that finished | `1s` | `nil` |
| (none) | `1s` | `nil` |

### Requirement: Export failure text is redacted
The `exception.message` of an `otel.export.failed` record SHALL go through the same default redaction as srvr's request records, so a collector URL query value or a secret-looking `key=value` pair in an exporter error is written as `***`.

#### Scenario: Export error with a secret
- **Given** setup with `SetGlobal: true` and an OTLP exporter failing with `<error>`
- **When** the export error is reported
- **Then** the `otel.export.failed` record's `exception.message` is `<message>`

##### Examples:
| error | message |
|-------|---------|
| `Post "https://otlp.local/v1/metrics?token=abc": dial tcp: refused` | `Post "https://otlp.local/v1/metrics?token=***": dial tcp: refused` |
| `exporter: authorization: Bearer xyz rejected` | `exporter: authorization: *** rejected` |
| `context deadline exceeded` | `context deadline exceeded` |
