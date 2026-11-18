# otelsetup

Opt-in OpenTelemetry SDK and exporter wiring for applications that serve with
srvr (`github.com/kartaladev/srvr`). Libraries record through `otelsrvr`, which needs only the
OpenTelemetry API; this module picks the backend: Prometheus scraping, OTLP
export, or both. The snippets below are compiled as `Example` tests in
`example_test.go`.

## Prometheus

```go
setup, err := otelsetup.New(ctx,
	otelsetup.WithPrometheus(),
	otelsetup.WithService(srvr.ServiceConfig{Name: "registration-api", Version: "1.4.2"}),
)
obs, err := otelsrvr.New(otelsrvr.WithMeterProvider(setup.MeterProvider()))
srv, err := srvr.New(srvr.WithObserver(obs), setup.ShutdownHook())
```

Metrics are served on their own listener, `127.0.0.1:9464` at `/metrics` by
default, from a registry of the setup's own (never
`prometheus.DefaultRegisterer`).

**Kubernetes:** the default address is loopback-only, so nothing outside the
pod can scrape it. Set the address explicitly for the pod's scrape annotation
or ServiceMonitor:

```go
otelsetup.WithPrometheusConfig(otelsetup.PrometheusConfig{Enabled: true, Addr: ":9464"})
```

Binding a public address loosens an exposure default: metrics describe the
service's internals, so keep the port off public ingress. To serve metrics on a
listener of your own instead, set `DisableListener: true` and mount
`setup.MetricsHandler()`:

```go
setup, err := otelsetup.New(ctx, otelsetup.WithPrometheusConfig(otelsetup.PrometheusConfig{
	Enabled:         true,
	DisableListener: true,
}))
// in a route configurer:
g.Group("/ops").Mount("/metrics", setup.MetricsHandler())
```

The setup's own listener has a 10s read-header timeout, a 30s write timeout
and a 60s idle timeout; the handler serves at most 4 scrapes at a time and cuts
each off after 20s, because every scrape runs a full collection. These limits
are fixed; with `DisableListener` your own server sets the listener timeouts.
`Path` must start with `/` and be a pattern `http.ServeMux` accepts; an invalid
one fails `New` with `ErrConfig` before anything is bound.

## OTLP

```go
setup, err := otelsetup.New(ctx,
	otelsetup.WithOTLP(otelsetup.OTLPConfig{
		Endpoint: "http://otel-collector:4318", // http/protobuf; Protocol: "grpc" for :4317
		Metrics:  true,
		Traces:   true,
	}),
	otelsetup.WithSetGlobal(),
)
```

`Endpoint` is a base URL (`http://` plaintext, `https://` TLS; the signal path
`/v1/metrics` or `/v1/traces` is appended for http/protobuf) or a `host:port`,
which uses TLS. A URL that does not parse, has another scheme or no host, and a
`host:port` without a host or a numeric port, fail `New` with `ErrConfig`
naming `OTLP.Endpoint` (the SDK would otherwise ignore it silently and export
to its default endpoint). `Headers` carries, for example, an API key.

## Configuration sources and the environment

Every setting is an option (`WithPrometheus`, `WithPrometheusConfig`,
`WithOTLP`, `WithService`, `WithFlushTimeout`, `WithSetGlobal`, `WithLogger`)
and a field of `otelsetup.Config` (with `json`, `yaml` and `env` tags) passed
with `WithConfig`; options apply in argument order.

srvr never reads environment variables, but the OpenTelemetry SDK does for
fields you leave empty, and **a field you set always wins**:

| Field left empty | Variables the SDK reads |
|------------------|-------------------------|
| `Service.Name`, other resource attributes | `OTEL_SERVICE_NAME`, `OTEL_RESOURCE_ATTRIBUTES` |
| `OTLP.Endpoint` | `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`, `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` |
| `OTLP.Headers` | `OTEL_EXPORTER_OTLP_HEADERS` (and the per-signal variants) |
| TLS, timeouts, compression | `OTEL_EXPORTER_OTLP_INSECURE`, `OTEL_EXPORTER_OTLP_CERTIFICATE`, `OTEL_EXPORTER_OTLP_TIMEOUT`, `OTEL_EXPORTER_OTLP_COMPRESSION` |
| metric export interval | `OTEL_METRIC_EXPORT_INTERVAL` (default 60s) |
| batch span processor | `OTEL_BSP_*` |

With `OTLP.Endpoint` empty, an operator's `OTEL_EXPORTER_OTLP_ENDPOINT`
therefore decides where telemetry goes.

## Shutdown

`setup.ShutdownHook()` registers `setup.Shutdown` as a srvr `AfterDrain`
hook: after the server drained, it flushes the providers and closes the
metrics listener, bounded by `FlushTimeout` (default 5s) and srvr's
`ShutdownHookTimeout` (default 5s), on every stop path. Register it before
other slow `AfterDrain` hooks: the phase shares one budget. Calling
`setup.Shutdown` again is safe and returns nil.

## Globals and export errors

By default nothing global is changed. `WithSetGlobal()` installs the meter and
tracer providers, a W3C TraceContext plus Baggage propagator and an error
handler that logs export failures as `otel.export.failed` at WARN, at most once
per minute per error kind, to the logger given with `WithLogger` (default
`slog.Default()`). Globals are installed only after the whole setup succeeded.
