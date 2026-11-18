package otelsetup

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// isURL reports whether an OTLP endpoint is a URL rather than a host:port.
func isURL(endpoint string) bool { return strings.Contains(endpoint, "://") }

// checkEndpoint reports why a non-empty OTLP endpoint is unusable: a URL
// must parse and have scheme http or https and a host; anything else must
// be a host:port with a host and a numeric port. It is checked here
// because the SDK's WithEndpointURL silently ignores a URL it cannot parse
// and exports to its default endpoint instead.
func checkEndpoint(endpoint string) error {
	if !isURL(endpoint) {
		host, err := splitHostPort(endpoint)
		if err == nil && host == "" {
			err = errors.New("host is empty")
		}

		return err
	}

	u, err := url.Parse(endpoint)
	switch {
	case err != nil:
		return err
	case u.Scheme != "http" && u.Scheme != "https":
		return fmt.Errorf("scheme %q is not http or https", u.Scheme)
	case u.Hostname() == "":
		return errors.New("host is empty")
	}

	return nil
}

// exporterFuncs holds the option constructors of one OTLP exporter
// package, whose option type is O.
type exporterFuncs[O any] struct {
	url      func(string) O            // WithEndpointURL
	endpoint func(string) O            // WithEndpoint
	headers  func(map[string]string) O // WithHeaders
}

// exporterOptions returns the options of one OTLP exporter, set only for
// the fields o sets so the SDK's environment fills the rest. An endpoint
// URL is a base: for http/protobuf the signal path (/v1/metrics,
// /v1/traces) is appended, as the SDK does for OTEL_EXPORTER_OTLP_ENDPOINT;
// gRPC has no path. A host:port goes to with.endpoint, which uses TLS.
func exporterOptions[O any](o OTLPConfig, signalPath string, with exporterFuncs[O]) []O {
	var opts []O

	switch {
	case isURL(o.Endpoint):
		url := o.Endpoint
		if o.Protocol == ProtocolHTTPProtobuf {
			url = strings.TrimSuffix(url, "/") + signalPath
		}

		opts = append(opts, with.url(url))
	case o.Endpoint != "":
		opts = append(opts, with.endpoint(o.Endpoint))
	}

	if len(o.Headers) > 0 {
		opts = append(opts, with.headers(o.Headers))
	}

	return opts
}

// metricExporter returns the OTLP metric exporter of o.
func metricExporter(ctx context.Context, o OTLPConfig) (exp sdkmetric.Exporter, err error) {
	if o.Protocol == ProtocolGRPC {
		exp, err = otlpmetricgrpc.New(ctx, exporterOptions(o, "", exporterFuncs[otlpmetricgrpc.Option]{
			url: otlpmetricgrpc.WithEndpointURL, endpoint: otlpmetricgrpc.WithEndpoint, headers: otlpmetricgrpc.WithHeaders,
		})...)
	} else {
		exp, err = otlpmetrichttp.New(ctx, exporterOptions(o, "/v1/metrics", exporterFuncs[otlpmetrichttp.Option]{
			url: otlpmetrichttp.WithEndpointURL, endpoint: otlpmetrichttp.WithEndpoint, headers: otlpmetrichttp.WithHeaders,
		})...)
	}

	if err != nil {
		return nil, fmt.Errorf("otelsetup: OTLP metric exporter: %w", err)
	}

	return exp, nil
}

// spanExporter returns the OTLP span exporter of o.
func spanExporter(ctx context.Context, o OTLPConfig) (exp sdktrace.SpanExporter, err error) {
	if o.Protocol == ProtocolGRPC {
		exp, err = otlptracegrpc.New(ctx, exporterOptions(o, "", exporterFuncs[otlptracegrpc.Option]{
			url: otlptracegrpc.WithEndpointURL, endpoint: otlptracegrpc.WithEndpoint, headers: otlptracegrpc.WithHeaders,
		})...)
	} else {
		exp, err = otlptracehttp.New(ctx, exporterOptions(o, "/v1/traces", exporterFuncs[otlptracehttp.Option]{
			url: otlptracehttp.WithEndpointURL, endpoint: otlptracehttp.WithEndpoint, headers: otlptracehttp.WithHeaders,
		})...)
	}

	if err != nil {
		return nil, fmt.Errorf("otelsetup: OTLP span exporter: %w", err)
	}

	return exp, nil
}
