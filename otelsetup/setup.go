package otelsetup

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	promexporter "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// Setup holds the SDK providers and exporters New built. Its methods are
// safe for concurrent use.
type Setup struct {
	cfg Config

	meterProvider  *sdkmetric.MeterProvider
	tracerProvider *sdktrace.TracerProvider

	metricsHandler http.Handler
	metricsServer  *http.Server
	// metricsServed is closed when the metrics server's Serve returned.
	metricsServed chan struct{}
	metricsLn     net.Listener
	metricsAddr   string

	shutdownOnce sync.Once
}

// New builds the providers and exporters opts configure and, with
// Prometheus listening, binds the metrics listener. Invalid configuration
// (a nil option is named by its position) returns an error wrapping
// [ErrConfig]; a failure after providers were
// built shuts them down, and nothing global is changed unless the whole
// setup succeeded.
func New(ctx context.Context, opts ...Option) (*Setup, error) {
	var (
		st      settings
		nilOpts []error
	)
	for i, opt := range opts {
		if opt == nil {
			nilOpts = append(nilOpts, fmt.Errorf("%w: Option %d is nil", ErrConfig, i))

			continue
		}

		opt(&st)
	}

	cfg := st.resolve()
	if err := errors.Join(append(nilOpts, cfg.validate())...); err != nil {
		return nil, err
	}

	res, err := newResource(ctx, cfg.Service)
	if err != nil {
		return nil, err
	}

	s := &Setup{cfg: cfg}

	if err := s.build(ctx, res); err != nil {
		_ = s.Shutdown(context.WithoutCancel(ctx)) // release what was built, even when ctx has ended

		return nil, err
	}

	// Process-wide state changes only once everything else succeeded.
	if cfg.SetGlobal {
		s.setGlobal(cmp.Or(st.logger, slog.Default()))
	}

	return s, nil
}

// setGlobal installs the providers, a W3C TraceContext plus Baggage
// propagator and an error handler logging export failures to logger as
// the OpenTelemetry globals.
func (s *Setup) setGlobal(logger *slog.Logger) {
	otel.SetErrorHandler(newExportErrorHandler(logger))
	otel.SetMeterProvider(s.meterProvider)

	if s.tracerProvider != nil {
		otel.SetTracerProvider(s.tracerProvider)
	}

	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
}

// build creates the providers and starts the metrics listener.
func (s *Setup) build(ctx context.Context, res *resource.Resource) error {
	mopts := []sdkmetric.Option{sdkmetric.WithResource(res)}

	if s.cfg.Prometheus.Enabled {
		registry := prometheus.NewRegistry()

		exporter, err := promexporter.New(promexporter.WithRegisterer(registry))
		if err != nil {
			return fmt.Errorf("otelsetup: Prometheus exporter: %w", err)
		}

		mopts = append(mopts, sdkmetric.WithReader(exporter))
		// Overlapping scrapes each run a full collection: cap them.
		s.metricsHandler = promhttp.HandlerFor(registry, promhttp.HandlerOpts{MaxRequestsInFlight: 4, Timeout: 20 * time.Second})
	}

	otlp := s.cfg.OTLP
	if otlp.Metrics {
		exp, err := metricExporter(ctx, otlp)
		if err != nil {
			return err
		}

		mopts = append(mopts, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp)))
	}

	s.meterProvider = sdkmetric.NewMeterProvider(mopts...)

	if otlp.Traces {
		exp, err := spanExporter(ctx, otlp)
		if err != nil {
			return err
		}

		s.tracerProvider = sdktrace.NewTracerProvider(sdktrace.WithResource(res), sdktrace.WithBatcher(exp))
	}

	if s.cfg.Prometheus.Enabled && !s.cfg.Prometheus.DisableListener {
		return s.listen(ctx)
	}

	return nil
}

// listen binds the metrics listener and serves the handler on it.
func (s *Setup) listen(ctx context.Context) error {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.cfg.Prometheus.Addr)
	if err != nil {
		return fmt.Errorf("otelsetup: metrics listener: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle(s.cfg.Prometheus.Path, s.metricsHandler)

	s.metricsLn, s.metricsAddr = ln, ln.Addr().String()
	s.metricsServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	s.metricsServed = make(chan struct{})
	go func() {
		defer close(s.metricsServed)
		_ = s.metricsServer.Serve(ln)
	}()

	return nil
}

// newResource returns the SDK's default resource with the OTEL_SERVICE_NAME
// and OTEL_RESOURCE_ATTRIBUTES environment and then the set service fields
// merged over it, so explicit configuration wins.
func newResource(ctx context.Context, svc srvr.ServiceConfig) (*resource.Resource, error) {
	var attrs []attribute.KeyValue

	if svc.Name != "" {
		attrs = append(attrs, semconv.ServiceName(svc.Name))
	}

	if svc.Version != "" {
		attrs = append(attrs, semconv.ServiceVersion(svc.Version))
	}

	if svc.Environment != "" {
		attrs = append(attrs, semconv.DeploymentEnvironmentNameKey.String(svc.Environment))
	}

	// resource.Default reads the environment once per process (it is
	// cached), so the environment is read again here and wins over it; the
	// set fields are applied last. A key removed from the environment after
	// the first read can still come from the cache, which matters only to
	// tests that change the environment.
	env, err := resource.New(ctx, resource.WithFromEnv(), resource.WithAttributes(attrs...))
	if err != nil {
		return nil, fmt.Errorf("otelsetup: resource: %w", err)
	}

	res, err := resource.Merge(resource.Default(), env)
	if err != nil {
		return nil, fmt.Errorf("otelsetup: resource: %w", err)
	}

	return res, nil
}

// MeterProvider returns the setup's meter provider, for
// otelsrvr.WithMeterProvider.
func (s *Setup) MeterProvider() *sdkmetric.MeterProvider { return s.meterProvider }

// TracerProvider returns the setup's tracer provider, or nil when no
// trace export is configured.
func (s *Setup) TracerProvider() *sdktrace.TracerProvider { return s.tracerProvider }

// MetricsHandler returns the Prometheus exposition handler, or nil without
// Prometheus. Mount it yourself with PrometheusConfig.DisableListener. It
// serves at most 4 scrapes at a time and cuts each off after 20s, because
// every scrape runs a full collection.
func (s *Setup) MetricsHandler() http.Handler { return s.metricsHandler }

// MetricsAddr returns the bound address of the metrics listener, such as
// the port chosen for "127.0.0.1:0", or "" when the setup does not listen.
func (s *Setup) MetricsAddr() string { return s.metricsAddr }

// Shutdown flushes and shuts down the providers and stops the metrics
// listener, within FlushTimeout or ctx, whichever ends first, and returns
// their joined errors. It is idempotent: a later or concurrent call waits
// for the first and returns nil.
func (s *Setup) Shutdown(ctx context.Context) error {
	var err error

	// Once.Do makes concurrent callers wait for the first; only the first
	// sets err.
	s.shutdownOnce.Do(func() { err = s.shutdown(ctx) })

	return err
}

// shutdown stops the metrics listener and shuts the providers down
// concurrently, so a slow flush of one does not starve the others, within
// FlushTimeout or ctx.
func (s *Setup) shutdown(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.FlushTimeout)
	defer cancel()

	var steps []func() error

	if s.meterProvider != nil {
		steps = append(steps, func() error { return s.meterProvider.Shutdown(ctx) })
	}

	if s.tracerProvider != nil {
		steps = append(steps, func() error { return s.tracerProvider.Shutdown(ctx) })
	}

	if s.metricsServer != nil {
		steps = append(steps, func() error { return s.stopListener(ctx) })
	}

	errs := make([]error, len(steps))

	var wg sync.WaitGroup
	for i, step := range steps {
		wg.Go(func() { errs[i] = step() })
	}

	wg.Wait()

	return errors.Join(errs...)
}

// stopListener stops the metrics server and closes its listener. When ctx
// ends while a scrape is still served, it closes the remaining
// connections; either way it returns only after Serve has.
func (s *Setup) stopListener(ctx context.Context) error {
	var errs []error

	if err := s.metricsServer.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		errs = append(errs, err)
		_ = s.metricsServer.Close() // the deadline passed: drop what is still open
	}

	// Serve may not have started yet, and http.Server only closes the
	// listeners Serve already tracks: close it here, so the port is free
	// when Shutdown returns.
	if err := s.metricsLn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		errs = append(errs, err)
	}

	<-s.metricsServed

	return errors.Join(errs...)
}

// ShutdownHook returns a srvr option registering [Setup.Shutdown] as a
// srvr.AfterDrain shutdown hook: it runs once after the drain on every
// stop path of the server (Start's context ending, Shutdown, Shutdown of a
// server that never started, and every Start failure after the server
// left its initial state), within srvr's ShutdownHookTimeout (default 5s)
// and FlushTimeout. A hook registered before it that blocks past that
// budget leaves it a spent context, so register it first when other
// AfterDrain hooks may be slow. Calling Shutdown again yourself is safe.
func (s *Setup) ShutdownHook() srvr.Option {
	return srvr.WithShutdownHook(srvr.AfterDrain, s.Shutdown)
}

// exportErrorInterval is how often one kind of export error is logged.
const exportErrorInterval = time.Minute

// exportErrorHandler logs OpenTelemetry errors (failed exports, mostly) as
// otel.export.failed at WARN, at most once per exportErrorInterval per
// error kind (the error's Go type), so a collector outage cannot flood the
// log. The logger is called without holding the lock, and a panic from it
// is recovered and the record dropped: the SDK calls Handle from its
// export goroutines, where an escaping panic would crash the process.
type exportErrorHandler struct {
	logger *slog.Logger
	now    func() time.Time

	mu   sync.Mutex
	last map[string]time.Time
}

func newExportErrorHandler(logger *slog.Logger) *exportErrorHandler {
	return &exportErrorHandler{logger: logger, now: time.Now, last: map[string]time.Time{}}
}

// Handle implements otel.ErrorHandler.
func (h *exportErrorHandler) Handle(err error) {
	if err == nil {
		return
	}

	kind := engine.TypeName(err)
	if !h.due(kind) {
		return
	}

	defer func() { _ = recover() }() // a faulty consumer slog.Handler

	// The error text is read through ValueText (an Error method that
	// panics gives a placeholder) and masked by srvr's default redactor:
	// exporter errors quote collector URLs, whose query may hold a token.
	h.logger.LogAttrs(context.Background(), slog.LevelWarn, "otel.export.failed",
		slog.Int("srvr.log.schema", engine.LogSchemaVersion), // adding this event kept the version
		slog.String("error.type", kind),
		slog.String("exception.message", engine.RedactSecrets(engine.ValueText(err))),
	)
}

// due reports whether kind may be logged now, and records it.
func (h *exportErrorHandler) due(kind string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := h.now()
	if last, ok := h.last[kind]; ok && now.Sub(last) < exportErrorInterval {
		return false
	}

	h.last[kind] = now

	return true
}
