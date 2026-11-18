# Spec Delta

## ADDED Requirements

### Requirement: otelsrvr configuration errors are typed
Every error `otelsrvr.New` returns for invalid configuration SHALL contain a `*otelsrvr.ConfigError` naming the field, readable with `errors.As`, and SHALL still match `errors.Is(err, otelsrvr.ErrConfig)`; when several fields are invalid, each one SHALL be found.

#### Scenario: Typed configuration errors
- **Given** `otelsrvr.New` called with `<options>`
- **When** the caller reads the returned error with `errors.As` into `*otelsrvr.ConfigError`
- **Then** it finds `Field` `<fields>`
- **And** `errors.Is(err, otelsrvr.ErrConfig)` is true

##### Examples:
| options | fields |
|---------|--------|
| `nil` | `Option` |
| `WithMeterProvider(nil)` | `MeterProvider` |
| `WithTracerProvider(nil)` | `TracerProvider` |
| `WithPropagator(nil)` | `Propagator` |
| `WithDurationBuckets()` | `DurationBuckets` |
| `WithDurationBuckets(1, 0.5)` | `DurationBuckets` |
| `WithMeterProvider(nil)` and `WithTracerProvider(nil)` | `MeterProvider` and `TracerProvider` |
