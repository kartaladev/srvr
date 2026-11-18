// Package otelsrvr records OpenTelemetry HTTP server metrics for a srvr
// server on any engine (net/http, gin or fiber), through the OpenTelemetry
// API only: it never imports the SDK or an exporter, so a library can add
// it without choosing a backend. Applications install an SDK
// MeterProvider themselves, or with package otelsetup.
//
// Register it as a srvr observer:
//
//	obs, err := otelsrvr.New()
//	if err != nil { ... }
//	srv, err := srvr.New(srvr.WithObserver(obs))
//
// Every finished request records one http.server.request.duration value
// in seconds, with instrumentation scope [ScopeName], following the
// OpenTelemetry semantic conventions v1.43. By default it records through
// the global MeterProvider, which is a no-op until the application sets
// one with otel.SetMeterProvider; [WithMeterProvider] injects another.
package otelsrvr
