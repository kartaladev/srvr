# Spec Delta

## ADDED Requirements

### Requirement: otelsetup configuration errors are typed
Every error `otelsetup.New` returns for invalid configuration SHALL contain a `*otelsetup.ConfigError` naming the field, readable with `errors.As`, and SHALL still match `errors.Is(err, otelsetup.ErrConfig)`; when several fields are invalid, each one SHALL be found.

#### Scenario: Typed configuration errors
- **Given** `otelsetup.New` called with `<config>`
- **When** the caller reads the returned error with `errors.As` into `*otelsetup.ConfigError`
- **Then** it finds `Field` `<fields>`
- **And** `errors.Is(err, otelsetup.ErrConfig)` is true

##### Examples:
| config | fields |
|--------|--------|
| a nil `Option` | `Option` |
| no exporter enabled | `Exporters` |
| `OTLP.Protocol` `"udp"` | `OTLP.Protocol` |
| `OTLP` enabling neither metrics nor traces | `OTLP` |
| `OTLP.Endpoint` `"http://[::1"` | `OTLP.Endpoint` |
| `FlushTimeout` `-1s` | `FlushTimeout` |
| `Prometheus.Addr` `"localhost:99999"` | `Prometheus.Addr` |
| `Prometheus.Path` `"metrics"` | `Prometheus.Path` |
| `FlushTimeout` `-1s` and `Prometheus.Path` `"metrics"` | `FlushTimeout` and `Prometheus.Path` |
