package otelsrvr

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/semconv/v1.43.0/httpconv"
)

// ScopeName is the instrumentation scope of everything otelsrvr records.
const ScopeName = "github.com/kartaladev/srvr/otelsrvr"

// ErrConfig is wrapped by every error New returns for invalid
// configuration.
var ErrConfig = errors.New("otelsrvr: invalid configuration")

// Option configures an [Observer]. Options apply in argument order, so a
// later option wins over an earlier one.
type Option func(*settings)

// Config holds the otelsrvr settings that a configuration loader can
// fill; a zero field means its default. Pass it with [WithConfig].
type Config struct {
	// Tracing turns on server spans. Default false: spans cost more and
	// need a backend.
	Tracing bool `json:"tracing" yaml:"tracing" env:"TRACING"`
	// DurationBuckets are the bucket boundaries of
	// http.server.request.duration, in seconds: strictly increasing,
	// positive and finite. Default (nil): the semantic-convention advisory
	// boundaries, so dashboards stay comparable. An empty, non-nil list is
	// invalid.
	DurationBuckets []float64 `json:"duration_buckets" yaml:"duration_buckets" env:"DURATION_BUCKETS"`
	// ActiveRequests records http.server.active_requests. Default false:
	// the instrument is Development (opt-in) in semantic conventions and
	// adds series.
	ActiveRequests bool `json:"active_requests" yaml:"active_requests" env:"ACTIVE_REQUESTS"`
	// BodySizes records http.server.request.body.size and
	// http.server.response.body.size. Default false, for the same reason.
	BodySizes bool `json:"body_sizes" yaml:"body_sizes" env:"BODY_SIZES"`
}

// semconvBuckets are the advisory boundaries of http.server.request.duration
// in semantic conventions v1.43. httpconv keeps its copy unexported, so
// this one must follow it on a semconv upgrade (TestBuckets pins it).
var semconvBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}

// settings is what the options build.
type settings struct {
	Config

	meterProvider metric.MeterProvider
}

// WithConfig sets every field of [Config] from c, a zero field meaning its
// default, replacing what earlier options set.
func WithConfig(c Config) Option {
	return func(s *settings) {
		s.Config = c
		s.DurationBuckets = slices.Clone(c.DurationBuckets)
	}
}

// WithDurationBuckets replaces the duration histogram's bucket boundaries
// (see [Config.DurationBuckets]).
func WithDurationBuckets(bounds ...float64) Option {
	return func(s *settings) { s.DurationBuckets = append([]float64{}, bounds...) }
}

// WithActiveRequests records http.server.active_requests (see
// [Config.ActiveRequests]).
func WithActiveRequests() Option {
	return func(s *settings) { s.ActiveRequests = true }
}

// WithBodySizes records the request and response body size histograms
// (see [Config.BodySizes]).
func WithBodySizes() Option {
	return func(s *settings) { s.BodySizes = true }
}

// WithTracing turns on server spans (see [Config.Tracing]).
func WithTracing() Option {
	return func(s *settings) { s.Tracing = true }
}

// validate reports every invalid field.
func (s *settings) validate() error {
	var errs []error

	if engine.IsNil(s.meterProvider) {
		errs = append(errs, fmt.Errorf("%w: MeterProvider %T is nil", ErrConfig, s.meterProvider))
	}

	if err := validateBuckets(s.DurationBuckets); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// validateBuckets checks that b, when set, is non-empty, strictly
// increasing, positive and finite.
func validateBuckets(b []float64) error {
	if b == nil {
		return nil
	}

	if len(b) == 0 {
		return fmt.Errorf("%w: DurationBuckets is empty", ErrConfig)
	}

	for i, v := range b {
		switch {
		case math.IsNaN(v) || math.IsInf(v, 0) || v <= 0:
			return fmt.Errorf("%w: DurationBuckets %v: boundary %v is not positive and finite", ErrConfig, b, v)
		case i > 0 && v <= b[i-1]:
			return fmt.Errorf("%w: DurationBuckets %v: boundaries are not strictly increasing", ErrConfig, b)
		}
	}

	return nil
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
	config Config
	// duration is always recorded; active, requestSize and responseSize
	// are nil unless enabled.
	duration                  metric.Float64Histogram
	active                    metric.Int64UpDownCounter
	requestSize, responseSize metric.Int64Histogram
}

var _ srvr.Observer = (*Observer)(nil)

// New returns an observer configured by opts. Invalid configuration, or a
// meter that fails to create an instrument, returns an error wrapping
// [ErrConfig].
func New(opts ...Option) (*Observer, error) {
	s := settings{meterProvider: otel.GetMeterProvider()}
	for _, opt := range opts {
		opt(&s)
	}

	if err := s.validate(); err != nil {
		return nil, err
	}

	if s.DurationBuckets == nil {
		s.DurationBuckets = slices.Clone(semconvBuckets)
	}

	return newObserver(s.meterProvider, s.Config)
}

// newObserver creates the instruments cfg enables on mp's meter. A
// provider or meter that panics fails it like one that returns an error.
func newObserver(mp metric.MeterProvider, cfg Config) (o *Observer, err error) {
	defer func() {
		if v := recover(); v != nil {
			o, err = nil, fmt.Errorf("%w: MeterProvider %T panicked: %s", ErrConfig, mp, engine.ValueText(v))
		}
	}()

	meter := mp.Meter(ScopeName)
	o = &Observer{config: cfg}

	// Names, units and descriptions come from semconv's httpconv, so a
	// semconv upgrade cannot leave them behind.
	duration := httpconv.ServerRequestDuration{}
	if o.duration, err = meter.Float64Histogram(duration.Name(),
		metric.WithUnit(duration.Unit()),
		metric.WithDescription(duration.Description()),
		metric.WithExplicitBucketBoundaries(cfg.DurationBuckets...),
	); err != nil {
		return nil, instrumentError(duration.Name(), err)
	}

	if cfg.ActiveRequests {
		active := httpconv.ServerActiveRequests{}
		if o.active, err = meter.Int64UpDownCounter(active.Name(),
			metric.WithUnit(active.Unit()), metric.WithDescription(active.Description()),
		); err != nil {
			return nil, instrumentError(active.Name(), err)
		}
	}

	if cfg.BodySizes {
		req, res := httpconv.ServerRequestBodySize{}, httpconv.ServerResponseBodySize{}
		if o.requestSize, err = meter.Int64Histogram(req.Name(),
			metric.WithUnit(req.Unit()), metric.WithDescription(req.Description()),
		); err != nil {
			return nil, instrumentError(req.Name(), err)
		}

		if o.responseSize, err = meter.Int64Histogram(res.Name(),
			metric.WithUnit(res.Unit()), metric.WithDescription(res.Description()),
		); err != nil {
			return nil, instrumentError(res.Name(), err)
		}
	}

	return o, nil
}

// instrumentError wraps the failure to create instrument name.
func instrumentError(name string, err error) error {
	return fmt.Errorf("%w: creating %s: %w", ErrConfig, name, err)
}

// Begin implements srvr.Observer.
func (o *Observer) Begin(ctx context.Context, info srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
	method, scheme := methodAttr(info.Method), info.Scheme

	var active metric.AddOption
	if o.active != nil {
		active = metric.WithAttributeSet(attribute.NewSet(method, semconv.URLScheme(scheme)))
		o.active.Add(ctx, 1, active)
	}

	return ctx, func(res srvr.ResultInfo) {
		if o.active != nil {
			o.active.Add(ctx, -1, active)
		}

		attrs := metric.WithAttributes(attributes(method, scheme, res)...)
		o.duration.Record(ctx, res.Duration.Seconds(), attrs)

		if o.requestSize != nil {
			o.requestSize.Record(ctx, res.RequestBodySize, attrs)
			o.responseSize.Record(ctx, res.ResponseBodySize, attrs)
		}
	}
}

// attributes returns the bounded attributes of a finished request:
// method, scheme, the status unless the request was aborted, the route
// unless none matched, and error.type for a failed request or any 5xx.
func attributes(method attribute.KeyValue, scheme string, res srvr.ResultInfo) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 5)
	attrs = append(attrs, method, semconv.URLScheme(scheme))

	if !res.Aborted {
		attrs = append(attrs, semconv.HTTPResponseStatusCode(res.Status))
	}

	if res.Route != "" {
		attrs = append(attrs, semconv.HTTPRoute(res.Route))
	}

	if t := errorType(res); t != "" {
		attrs = append(attrs, semconv.ErrorTypeKey.String(t))
	}

	return attrs
}

// knownMethods are the methods semantic conventions name; any other,
// including a different case, is _OTHER.
var knownMethods = map[string]attribute.KeyValue{
	http.MethodConnect: semconv.HTTPRequestMethodConnect,
	http.MethodDelete:  semconv.HTTPRequestMethodDelete,
	http.MethodGet:     semconv.HTTPRequestMethodGet,
	http.MethodHead:    semconv.HTTPRequestMethodHead,
	http.MethodOptions: semconv.HTTPRequestMethodOptions,
	http.MethodPatch:   semconv.HTTPRequestMethodPatch,
	http.MethodPost:    semconv.HTTPRequestMethodPost,
	http.MethodPut:     semconv.HTTPRequestMethodPut,
	http.MethodTrace:   semconv.HTTPRequestMethodTrace,
	"QUERY":            semconv.HTTPRequestMethodQuery,
}

// methodAttr returns the http.request.method attribute of method.
func methodAttr(method string) attribute.KeyValue {
	if kv, ok := knownMethods[method]; ok {
		return kv
	}

	return semconv.HTTPRequestMethodOther
}

// errorType returns the error.type of res, or "" when it did not fail:
// "panic" for a recovered panic or an abort, semconv.ErrorType of the
// returned error, or the status code for a failure without an error
// value (a 5xx written directly, no route matched).
func errorType(res srvr.ResultInfo) string {
	switch {
	case res.ErrorKind == srvr.ErrorKindPanic:
		return "panic"
	case res.Err != nil:
		return semconvErrorType(res.Err)
	case res.ErrorKind == srvr.ErrorKindError || res.Status >= http.StatusInternalServerError:
		return strconv.Itoa(res.Status)
	default:
		return ""
	}
}

// semconvErrorType returns semconv.ErrorType of err, or err's Go type when
// a method of err panics (ErrorType, Unwrap), so a faulty error never
// fails the request.
func semconvErrorType(err error) (t string) {
	defer func() {
		if recover() != nil {
			t = engine.TypeName(err)
		}
	}()

	return semconv.ErrorType(err).Value.AsString()
}
