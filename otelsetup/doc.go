// Package otelsetup wires the OpenTelemetry SDK and exporters for an
// application that serves with srvr: a MeterProvider (and, with trace
// export, a TracerProvider) with a resource naming the service, a
// Prometheus endpoint on a private listener, and flushing tied to the
// server's shutdown. Libraries use package otelsrvr, which needs only the
// OpenTelemetry API; applications add this package to pick a backend.
//
//	setup, err := otelsetup.New(ctx, otelsetup.WithPrometheus(),
//		otelsetup.WithService(srvr.ServiceConfig{Name: "registration-api"}))
//	if err != nil { ... }
//	obs, err := otelsrvr.New(otelsrvr.WithMeterProvider(setup.MeterProvider()))
//	srv, err := srvr.New(srvr.WithObserver(obs), setup.ShutdownHook())
//
// # Defaults
//
// Nothing is enabled by default: New fails with [ErrConfig] until an
// exporter is. Prometheus listens on 127.0.0.1:9464 at /metrics, loopback
// only so metrics are never public by accident; in Kubernetes set
// PrometheusConfig.Addr to ":9464" so the pod can be scraped. It uses a
// registry of its own, never prometheus.DefaultRegisterer, so two setups
// in one process do not collide. Global OpenTelemetry state is left
// untouched unless [WithSetGlobal] asks for it, and then it is changed
// only after the whole setup succeeded. Shutdown is bounded by
// FlushTimeout (default 5s).
//
// # Environment
//
// srvr never reads environment variables itself, but the OpenTelemetry
// SDK does for fields left empty, and an explicit field always wins:
// OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES for the resource (srvr's
// service fields are merged over them), and the OTEL_EXPORTER_OTLP_*
// variables for OTLP fields left empty.
package otelsetup
