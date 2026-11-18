package otelsetup

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// exporterOptions returns the options of one OTLP exporter, set only for
// the fields o sets so the SDK's environment fills the rest. An endpoint
// URL is a base: for http/protobuf the signal path (/v1/metrics,
// /v1/traces) is appended, as the SDK does for OTEL_EXPORTER_OTLP_ENDPOINT;
// gRPC has no path. A host:port goes to withEndpoint, which uses TLS.
func exporterOptions[O any](o OTLPConfig, signalPath string, withURL, withEndpoint func(string) O, withHeaders func(map[string]string) O) []O {
	var opts []O

	switch {
	case strings.Contains(o.Endpoint, "://"):
		url := o.Endpoint
		if o.Protocol == ProtocolHTTPProtobuf {
			url = strings.TrimSuffix(url, "/") + signalPath
		}

		opts = append(opts, withURL(url))
	case o.Endpoint != "":
		opts = append(opts, withEndpoint(o.Endpoint))
	}

	if len(o.Headers) > 0 {
		opts = append(opts, withHeaders(o.Headers))
	}

	return opts
}

// metricExporter returns the OTLP metric exporter of o.
func metricExporter(ctx context.Context, o OTLPConfig) (exp sdkmetric.Exporter, err error) {
	if o.Protocol == ProtocolGRPC {
		exp, err = otlpmetricgrpc.New(ctx, exporterOptions(o, "", otlpmetricgrpc.WithEndpointURL, otlpmetricgrpc.WithEndpoint, otlpmetricgrpc.WithHeaders)...)
	} else {
		exp, err = otlpmetrichttp.New(ctx, exporterOptions(o, "/v1/metrics", otlpmetrichttp.WithEndpointURL, otlpmetrichttp.WithEndpoint, otlpmetrichttp.WithHeaders)...)
	}

	if err != nil {
		return nil, fmt.Errorf("otelsetup: OTLP metric exporter: %w", err)
	}

	return exp, nil
}

// spanExporter returns the OTLP span exporter of o.
func spanExporter(ctx context.Context, o OTLPConfig) (exp sdktrace.SpanExporter, err error) {
	if o.Protocol == ProtocolGRPC {
		exp, err = otlptracegrpc.New(ctx, exporterOptions(o, "", otlptracegrpc.WithEndpointURL, otlptracegrpc.WithEndpoint, otlptracegrpc.WithHeaders)...)
	} else {
		exp, err = otlptracehttp.New(ctx, exporterOptions(o, "/v1/traces", otlptracehttp.WithEndpointURL, otlptracehttp.WithEndpoint, otlptracehttp.WithHeaders)...)
	}

	if err != nil {
		return nil, fmt.Errorf("otelsetup: OTLP span exporter: %w", err)
	}

	return exp, nil
}
