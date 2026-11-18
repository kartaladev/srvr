# Spec Delta

## ADDED Requirements

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
