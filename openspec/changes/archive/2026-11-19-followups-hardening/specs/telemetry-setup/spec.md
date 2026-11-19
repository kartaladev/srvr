# Spec Delta

## MODIFIED Requirements

### Requirement: otelsetup configuration errors are typed
Every error `otelsetup.New` returns for invalid configuration SHALL contain a `*otelsetup.ConfigError` naming the field, readable with `errors.As`, and SHALL still match `errors.Is(err, otelsetup.ErrConfig)`; when several fields are invalid, each one SHALL be found. An underlying cause SHALL be kept in `Cause`, reachable with `errors.Is`/`errors.As`, without changing the text. A path `http.ServeMux` rejects by panicking with an error SHALL have that error kept as `Cause` the same way.

#### Scenario: Typed configuration errors
- **Given** `otelsetup.New` called with `<config>`
- **When** the caller reads the returned error with `errors.As` into `*otelsetup.ConfigError`
- **Then** it finds `Field` `<fields>`
- **And** `errors.Is(err, otelsetup.ErrConfig)` is true
- **And** the cause is `<cause>`: `none` means every `*ConfigError` found has a nil `Cause`; a type means `errors.As` on the returned error reaches an error of that type; a text means the `Cause` of the `*ConfigError` found has that text

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
| `Prometheus.Path` `"/metrics/{"` | `Prometheus.Path` | the text of the error `http.ServeMux` panics with when `"/metrics/{"` is registered |
| `Prometheus.Path` `"/metrics x"` | `Prometheus.Path` | the text of the error `http.ServeMux` panics with when `"/metrics x"` is registered |
