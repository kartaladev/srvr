package otelsrvr

import (
	"context"
	"errors"
	"fmt"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// ScopeName is the instrumentation scope of everything otelsrvr records.
const ScopeName = "github.com/kartaladev/srvr/otelsrvr"

// ErrConfig is wrapped by every error New returns for invalid
// configuration.
var ErrConfig = errors.New("otelsrvr: invalid configuration")

// Option configures an [Observer]. Options apply in argument order, so a
// later option wins over an earlier one.
type Option func(*settings)

// settings is what the options build.
type settings struct {
	meterProvider metric.MeterProvider
}

// WithMeterProvider records through mp instead of the global
// MeterProvider (the default, a no-op until the application installs
// one). A nil or typed-nil mp fails [New].
func WithMeterProvider(mp metric.MeterProvider) Option {
	return func(s *settings) { s.meterProvider = mp }
}

// Observer records OpenTelemetry HTTP server metrics for every request of
// a srvr server; register it with srvr.WithObserver.
type Observer struct {
	duration metric.Float64Histogram
}

var _ srvr.Observer = (*Observer)(nil)

// New returns an observer configured by opts. Invalid configuration
// returns an error wrapping [ErrConfig].
func New(opts ...Option) (*Observer, error) {
	s := settings{meterProvider: otel.GetMeterProvider()}
	for _, opt := range opts {
		opt(&s)
	}

	if engine.IsNil(s.meterProvider) {
		return nil, fmt.Errorf("%w: MeterProvider %T is nil", ErrConfig, s.meterProvider)
	}

	duration, err := s.meterProvider.Meter(ScopeName).Float64Histogram("http.server.request.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of HTTP server requests."),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: http.server.request.duration: %w", ErrConfig, err)
	}

	return &Observer{duration: duration}, nil
}

// Begin implements srvr.Observer.
func (o *Observer) Begin(ctx context.Context, info srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
	return ctx, func(res srvr.ResultInfo) {
		attrs := []attribute.KeyValue{
			semconv.HTTPRequestMethodKey.String(info.Method),
			semconv.URLScheme(info.Scheme),
			semconv.HTTPResponseStatusCode(res.Status),
		}
		if res.Route != "" {
			attrs = append(attrs, semconv.HTTPRoute(res.Route))
		}

		o.duration.Record(ctx, res.Duration.Seconds(), metric.WithAttributes(attrs...))
	}
}
