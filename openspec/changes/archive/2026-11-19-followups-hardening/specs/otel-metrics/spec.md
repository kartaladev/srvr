# Spec Delta

## MODIFIED Requirements

### Requirement: otelsrvr configuration errors are typed
Every error `otelsrvr.New` returns for invalid configuration SHALL contain a `*otelsrvr.ConfigError` naming the field, readable with `errors.As`, and SHALL still match `errors.Is(err, otelsrvr.ErrConfig)`; when several fields are invalid, each one SHALL be found. An underlying cause SHALL be kept in `Cause`, reachable with `errors.Is`/`errors.As`, without changing the text. A provider or propagator that panics with an error SHALL have that error kept as `Cause` the same way.

#### Scenario: Typed configuration errors
- **Given** `otelsrvr.New` called with `<options>`
- **When** the caller reads the returned error with `errors.As` into `*otelsrvr.ConfigError`
- **Then** it finds `Field` `<fields>`
- **And** `errors.Is(err, otelsrvr.ErrConfig)` is true
- **And** the cause is `<cause>`: `none` means every `*ConfigError` found has a nil `Cause`; otherwise `errors.Is` or `errors.As` on the returned error reaches it

##### Examples:
| options | fields | cause |
|---------|--------|-------|
| `nil` | `Option` | none |
| `WithMeterProvider(nil)` | `MeterProvider` | none |
| `WithTracerProvider(nil)` | `TracerProvider` | none |
| `WithPropagator(nil)` | `Propagator` | none |
| `WithDurationBuckets()` | `DurationBuckets` | none |
| `WithDurationBuckets(1, 0.5)` | `DurationBuckets` | none |
| `WithMeterProvider(nil)` and `WithTracerProvider(nil)` | `MeterProvider` and `TracerProvider` | none |
| `WithMeterProvider` of a meter failing to create `http.server.request.duration` with error `E` | `MeterProvider` | `E` (`errors.Is`) |
| `WithMeterProvider` of a meter failing to create `http.server.response.body.size` with error `E`, and `WithBodySizes()` | `MeterProvider` | `E` (`errors.Is`) |
| `WithMeterProvider` of a provider whose `Meter` panics with error `E` | `MeterProvider` | `E` (`errors.Is`) |
| `WithTracerProvider` of a provider whose `Tracer` panics with error `E` | `TracerProvider` | `E` (`errors.Is`) |
| `WithPropagator` of a propagator whose `Fields` panics with error `E` | `Propagator` | `E` (`errors.Is`) |
| `WithMeterProvider` of a provider whose `Meter` panics with `"boom"` | `MeterProvider` | none |
| `WithMeterProvider` of a provider whose `Meter` panics with a typed-nil `*MyError` | `MeterProvider` | none |
| `WithTracerProvider` of a provider whose `Tracer` panics with an error whose `Unwrap` returns the error itself | `TracerProvider` | none, and `New` returns within `1s` |
| `WithPropagator` of a propagator whose `Fields` panics with an error whose `Error` panics | `Propagator` | that error (`errors.Is`), and no panic escapes `New` |
| `WithMeterProvider` of a provider whose `Meter` panics with an error of an uncomparable type `sliceError{"a"}` | `MeterProvider` | that error (`errors.As` into `sliceError`), and no panic escapes `New` |
