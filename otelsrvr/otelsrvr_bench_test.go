package otelsrvr_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// benchHeader is a request header for the observer benchmarks, carrying
// an incoming traceparent.
type benchHeader map[string]string

func (h benchHeader) Get(key string) string { return h[http.CanonicalHeaderKey(key)] }

func (h benchHeader) Keys() []string {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}

	return keys
}

// BenchmarkObserver measures the observer on one request, Begin and its
// end function, with the OpenTelemetry SDK's meter provider (a manual
// reader, never collected) and tracer provider (sampling every span, no
// exporter): the default duration histogram alone, with tracing (the
// span continues an incoming traceparent), and with every instrument and
// tracing.
func BenchmarkObserver(b *testing.B) {
	header := benchHeader{"Traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
	info := srvr.RequestInfo{Method: http.MethodGet, Scheme: "http", Host: "example.com", Header: header}
	result := srvr.ResultInfo{Route: "/users/{id}", Status: http.StatusOK, Duration: 3 * time.Millisecond}

	cases := []struct {
		name string
		opts []otelsrvr.Option
	}{
		{"metrics", nil},
		{"metrics and tracing", []otelsrvr.Option{otelsrvr.WithTracing()}},
		{"all instruments and tracing", []otelsrvr.Option{
			otelsrvr.WithTracing(), otelsrvr.WithActiveRequests(), otelsrvr.WithBodySizes(),
		}},
	}

	for _, bc := range cases {
		b.Run(bc.name, func(b *testing.B) {
			mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))
			tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
			b.Cleanup(func() {
				_ = mp.Shutdown(b.Context())
				_ = tp.Shutdown(b.Context())
			})

			obs, err := otelsrvr.New(append([]otelsrvr.Option{
				otelsrvr.WithMeterProvider(mp), otelsrvr.WithTracerProvider(tp),
				otelsrvr.WithPropagator(propagation.TraceContext{}),
			}, bc.opts...)...)
			if err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()

			for b.Loop() {
				_, end := obs.Begin(b.Context(), info)
				end(result)
			}
		})
	}
}
