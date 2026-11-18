package otelsrvr

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"slices"
	"strconv"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/semconv/v1.43.0/httpconv"
	"go.opentelemetry.io/otel/trace"
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

	meterProvider  metric.MeterProvider
	tracerProvider trace.TracerProvider
	// propagator is the one WithPropagator set, or nil for the global one.
	propagator propagation.TextMapPropagator
	// propagatorErr is why WithPropagator's propagator is unusable.
	propagatorErr error
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

	if engine.IsNil(s.tracerProvider) {
		errs = append(errs, fmt.Errorf("%w: TracerProvider %T is nil", ErrConfig, s.tracerProvider))
	}

	if s.propagatorErr != nil {
		errs = append(errs, s.propagatorErr)
	}

	if err := validateBuckets(s.DurationBuckets); err != nil {
		errs = append(errs, err)
	}

	return errors.Join(errs...)
}

// validatePropagator rejects a nil or typed-nil propagator and one whose
// Fields panics.
func validatePropagator(p propagation.TextMapPropagator) (err error) {
	if engine.IsNil(p) {
		return fmt.Errorf("%w: Propagator %T is nil", ErrConfig, p)
	}

	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%w: Propagator %T panicked: %s", ErrConfig, p, engine.ValueText(v))
		}
	}()

	_ = p.Fields()

	return nil
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

// WithTracerProvider starts spans through tp instead of the global
// TracerProvider (the default, a no-op until the application installs
// one). A nil or typed-nil tp fails [New].
func WithTracerProvider(tp trace.TracerProvider) Option {
	return func(s *settings) { s.tracerProvider = tp }
}

// WithPropagator extracts the remote parent of server spans with p instead
// of the global propagator. Without it otelsrvr reads the global one per
// request and, when that declares no fields (the no-op default), uses W3C
// TraceContext, so enabling tracing never drops an incoming traceparent
// that srvr honours. A nil or typed-nil p, or one whose Fields panics,
// fails [New].
func WithPropagator(p propagation.TextMapPropagator) Option {
	return func(s *settings) { s.propagator, s.propagatorErr = p, validatePropagator(p) }
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
	// tracer starts server spans; nil when tracing is off.
	tracer trace.Tracer
	// propagator is WithPropagator's, or nil to read the global one per
	// request.
	propagator propagation.TextMapPropagator
}

var (
	_ srvr.Observer    = (*Observer)(nil)
	_ srvr.TraceSource = (*Observer)(nil)
)

// TraceFromContext implements srvr.TraceSource: with tracing on, srvr's
// trace_id and span_id in logs, problems and srvr.TraceFromContext are the
// server span's, including a non-recording (unsampled) one. With tracing
// off it reports false and srvr keeps its own trace context.
func (o *Observer) TraceFromContext(ctx context.Context) (srvr.Trace, bool) {
	if o.tracer == nil {
		return srvr.Trace{}, false
	}

	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return srvr.Trace{}, false
	}

	return srvr.Trace{TraceID: sc.TraceID().String(), SpanID: sc.SpanID().String(), Sampled: sc.IsSampled()}, true
}

// New returns an observer configured by opts. Invalid configuration, or a
// meter that fails to create an instrument, returns an error wrapping
// [ErrConfig].
func New(opts ...Option) (*Observer, error) {
	s := settings{meterProvider: otel.GetMeterProvider(), tracerProvider: otel.GetTracerProvider()}
	for _, opt := range opts {
		opt(&s)
	}

	if err := s.validate(); err != nil {
		return nil, err
	}

	if s.DurationBuckets == nil {
		s.DurationBuckets = slices.Clone(semconvBuckets)
	}

	o, err := newObserver(s.meterProvider, s.Config)
	if err != nil {
		return nil, err
	}

	if s.Tracing {
		if o.tracer, err = tracer(s.tracerProvider); err != nil {
			return nil, err
		}

		o.propagator = s.propagator
	}

	return o, nil
}

// tracer returns tp's tracer for otelsrvr; a provider that panics fails
// it.
func tracer(tp trace.TracerProvider) (t trace.Tracer, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%w: TracerProvider %T panicked: %s", ErrConfig, tp, engine.ValueText(v))
		}
	}()

	return tp.Tracer(ScopeName), nil
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

	var span trace.Span
	if o.tracer != nil {
		if !info.IgnoreIncomingTrace {
			ctx = o.extract(ctx, info.Header)
		}

		ctx, span = o.tracer.Start(ctx, "HTTP", trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(pathAttributes(method, info)...))
	}

	return ctx, func(res srvr.ResultInfo) {
		kvs := attributes(method, scheme, res)

		if span != nil {
			endSpan(span, method, res, kvs)
		}

		if o.active != nil {
			o.active.Add(ctx, -1, active)
		}

		attrs := metric.WithAttributes(kvs...)
		o.duration.Record(ctx, res.Duration.Seconds(), attrs)

		if o.requestSize != nil {
			o.requestSize.Record(ctx, res.RequestBodySize, attrs)
			o.responseSize.Record(ctx, res.ResponseBodySize, attrs)
		}
	}
}

// extract returns ctx with the remote parent the propagator finds in
// header; a propagator that panics leaves ctx unchanged, so the span is a
// new root.
func (o *Observer) extract(ctx context.Context, header srvr.HeaderReader) (out context.Context) {
	defer func() {
		if recover() != nil {
			out = ctx
		}
	}()

	p := o.propagator
	if p == nil {
		// The global propagator is read per request, so a later
		// otel.SetTextMapPropagator applies; the no-op default falls back
		// to W3C TraceContext.
		if p = otel.GetTextMapPropagator(); len(p.Fields()) == 0 {
			p = propagation.TraceContext{}
		}
	}

	return p.Extract(ctx, headerCarrier{header})
}

// headerCarrier adapts srvr's header reader to a read-only carrier.
type headerCarrier struct{ srvr.HeaderReader }

// Set does nothing: the request headers are read-only.
func (headerCarrier) Set(string, string) {}

// endSpan names span after the route, sets its attributes and status and
// ends it. The status is Error, without a description, for a 5xx and a
// recovered panic (semantic conventions leave 4xx Unset on server spans);
// a panic adds an exception event with the panic value's Go type and no
// message, so no error text leaves srvr unredacted.
func endSpan(span trace.Span, method attribute.KeyValue, res srvr.ResultInfo, kvs []attribute.KeyValue) {
	if !span.IsRecording() {
		span.End()

		return
	}

	span.SetName(spanName(method, res.Route))
	span.SetAttributes(kvs...)

	panicked := res.ErrorKind == srvr.ErrorKindPanic
	if panicked {
		span.AddEvent(semconv.ExceptionEventName, trace.WithAttributes(semconv.ExceptionType(panicType(res))))
	}

	if panicked || res.Status >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, "")
	}

	span.End()
}

// pathAttributes returns url.path and, for a method semantic conventions do
// not name, http.request.method_original: span-only attributes, present
// only when srvr passes the path (core AccessLogConfig.IncludePath).
func pathAttributes(method attribute.KeyValue, info srvr.RequestInfo) []attribute.KeyValue {
	if info.Path == "" {
		return nil
	}

	attrs := []attribute.KeyValue{semconv.URLPath(info.Path)}
	if method == semconv.HTTPRequestMethodOther {
		attrs = append(attrs, semconv.HTTPRequestMethodOriginal(info.Method))
	}

	return attrs
}

// spanName returns "{method} {route}", "{method}" when no route matched,
// and "HTTP" for a method semantic conventions do not name.
func spanName(method attribute.KeyValue, route string) string {
	m := method.Value.AsString()
	switch {
	case method == semconv.HTTPRequestMethodOther:
		return "HTTP"
	case route == "":
		return m
	default:
		return m + " " + route
	}
}

// panicType returns the Go type of the value a recovered panic carried,
// or "panic" when srvr does not have it (an aborted request).
func panicType(res srvr.ResultInfo) string {
	if pe, ok := errors.AsType[*prblm.PanicError](res.Err); ok && pe != nil {
		return engine.TypeName(pe.Value)
	}

	return "panic"
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

// methodAttr returns the http.request.method attribute of method: the
// method when semantic conventions name it, _OTHER for any other method,
// including a different case. It shares engine.MethodLabel with srvr's
// error fingerprints, so the two never disagree on the set.
func methodAttr(method string) attribute.KeyValue {
	return semconv.HTTPRequestMethodKey.String(engine.MethodLabel(method))
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
