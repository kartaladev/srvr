# Spec Delta

## ADDED Requirements

### Requirement: otelsetup configuration errors are typed
Every error `otelsetup.New` returns for invalid configuration SHALL contain a `*otelsetup.ConfigError` naming the field, readable with `errors.As`, and SHALL still match `errors.Is(err, otelsetup.ErrConfig)`; when several fields are invalid, each one SHALL be found. An underlying cause SHALL be kept in `Cause`, reachable with `errors.Is`/`errors.As`, without changing the text.

#### Scenario: Typed configuration errors
- **Given** `otelsetup.New` called with `<config>`
- **When** the caller reads the returned error with `errors.As` into `*otelsetup.ConfigError`
- **Then** it finds `Field` `<fields>`
- **And** `errors.Is(err, otelsetup.ErrConfig)` is true
- **And** the cause is `<cause>`: `none` means every `*ConfigError` found has a nil `Cause`; otherwise `errors.As` on the returned error reaches an error of that type

##### Examples:
| config | fields | cause |
|--------|--------|-------|
| a nil `Option` | `Option` | none |
| no exporter enabled | `Exporters` | none |
| `OTLP.Protocol` `"udp"` | `OTLP.Protocol` | none |
| `OTLP` enabling neither metrics nor traces | `OTLP` | none |
| `OTLP.Endpoint` `"http://[::1"` | `OTLP.Endpoint` | `*url.Error` |
| `FlushTimeout` `-1s` | `FlushTimeout` | none |
| `Prometheus.Addr` `"localhost:99999"` | `Prometheus.Addr` | `*strconv.NumError` |
| `Prometheus.Addr` `"localhost"` | `Prometheus.Addr` | `*net.AddrError` |
| `OTLP.Endpoint` `"localhost"` | `OTLP.Endpoint` | `*net.AddrError` |
| `Prometheus.Path` `"metrics"` | `Prometheus.Path` | none |
| `FlushTimeout` `-1s` and `Prometheus.Path` `"metrics"` | `FlushTimeout` and `Prometheus.Path` | none |
