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
//
// # Attributes
//
// Data points carry only http.request.method (unknown methods as _OTHER),
// http.route (omitted when no route matched), http.response.status_code
// (omitted for a request aborted with http.ErrAbortHandler), url.scheme
// and, for a failed request or any 5xx, error.type: semconv.ErrorType of
// the error the handler returned, "panic" for a recovered panic, or the
// status code for a failure without an error value (a 5xx written
// directly, no route matched). Raw paths, query strings, client and
// server addresses and user agents are never attributes. srvr's logs keep
// their own error.type, the Go type of the error, so a log record and a
// data point for the same returned error may show different values by
// design: logs need the precise type, metrics a bounded one.
//
// http.server.active_requests ([WithActiveRequests]) and the request and
// response body size histograms ([WithBodySizes]) are opt-in.
//
// # Tracing
//
// [WithTracing] starts one SERVER span per request, named "{method}
// {route}" ("{method}" when unmatched, "HTTP" for an unknown method), with
// the metric attributes and status Error only for a 5xx or a recovered
// panic, which also adds an exception event carrying the panic value's Go
// type and no message. url.path and http.request.method_original are added
// only when srvr's access log includes the path
// (srvr.WithAccessLogIncludePath), the same privacy switch. The remote
// parent is extracted with [WithPropagator]'s propagator, or the global
// one read per request; when the global propagator declares no fields (the
// no-op default) W3C TraceContext is used, so enabling tracing never drops
// an incoming traceparent srvr would honour. With
// srvr.WithIgnoreIncomingTraceContext nothing is extracted. With tracing
// on, srvr's trace_id and span_id in logs, problems and
// srvr.TraceFromContext are the server span's (the observer is a
// srvr.TraceSource), and handler spans started from the request context
// are its children.
//
// The observer runs on the request path, synchronously, like otelhttp:
// recording a value is cheap, and exporting is left to the SDK's periodic
// readers and batch processors, which run in the background. Configure
// those, not otelsrvr, when export is slow.
//
// # Dependencies
//
// The non-test packages of this module import the OpenTelemetry API only.
// Its go.mod still requires the OpenTelemetry SDK (go.opentelemetry.io/otel/sdk
// and sdk/metric), because its own tests record through it, so a module
// that requires otelsrvr gains that SDK version as a minimum in its module
// graph and go.sum, although no SDK package is compiled into its binary.
// Two tests enforce the rule: TestNoSDKDependency in this module fails when
// a non-test package of otelsrvr imports go.opentelemetry.io/otel/sdk or
// go.opentelemetry.io/otel/exporters (it does not check go.mod or test
// packages), and TestCoreHasNoOTel in the core module fails when any core
// package, test packages included, imports go.opentelemetry.io.
package otelsrvr
