package otelsetup

import (
	"errors"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/internal/engine"
	"github.com/kartaladev/srvr/internal/errchain"
)

// ErrConfig is wrapped by every error New returns for invalid
// configuration, through one [ConfigError] per invalid field naming the
// field and value.
var ErrConfig = errors.New("otelsetup: invalid configuration")

// ConfigError is the error New returns for one invalid field: errors.As
// reads Field, Value and Reason, and errors.Is matches [ErrConfig] and,
// through Cause, the underlying error when there is one. When several
// fields are invalid, New joins one ConfigError per field.
type ConfigError struct {
	// Field names the setting, such as "Prometheus.Path".
	Field string
	// Value is the offending value's text; empty when the field is unset.
	Value string
	// Reason says what is wrong with it.
	Reason string
	// Cause is the underlying error, such as a parse error, whose text
	// Reason already holds; nil when there is none. When http.ServeMux
	// rejects Prometheus.Path by panicking with an error, Cause is that
	// error; a panic value that is not an error, a typed nil or an error
	// whose chain cannot be walked leaves Cause nil.
	Cause error

	// text is the message after the prefix when it is not Field, Value
	// and Reason joined by spaces.
	text string
}

func (e *ConfigError) Error() string {
	if e.text != "" {
		return ErrConfig.Error() + ": " + e.text
	}

	parts := make([]string, 0, 3)
	for _, p := range []string{e.Field, e.Value, e.Reason} {
		if p != "" {
			parts = append(parts, p)
		}
	}

	return ErrConfig.Error() + ": " + strings.Join(parts, " ")
}

// Unwrap returns [ErrConfig] and, when it is not nil, Cause, so errors.Is
// and errors.As reach both.
func (e *ConfigError) Unwrap() []error {
	if e.Cause == nil {
		return []error{ErrConfig}
	}

	return []error{ErrConfig, e.Cause}
}

// configError returns a *ConfigError whose message is field, value and
// reason joined by spaces.
func configError(field, value, reason string) error {
	return &ConfigError{Field: field, Value: value, Reason: reason}
}

// configErrorColon returns a *ConfigError whose message is "field value:
// reason".
func configErrorColon(field, value, reason string) *ConfigError {
	return &ConfigError{Field: field, Value: value, Reason: reason, text: field + " " + value + ": " + reason}
}

// causeError returns a *ConfigError caused by cause, whose message is
// "field value: cause".
func causeError(field, value string, cause error) error {
	e := configErrorColon(field, value, cause.Error())
	e.Cause = cause

	return e
}

// Default values of the configuration.
const (
	// DefaultPrometheusAddr is loopback-only, so metrics are never public
	// by default; in Kubernetes set Addr to ":9464" for the pod to be
	// scraped. 9464 is the OpenTelemetry Prometheus exporter's port.
	DefaultPrometheusAddr = "127.0.0.1:9464"
	// DefaultPrometheusPath is where the exposition format is served.
	DefaultPrometheusPath = "/metrics"
	// DefaultFlushTimeout bounds Shutdown; it runs in srvr's AfterDrain
	// budget (ShutdownHookTimeout, 5s), after the 15s drain.
	DefaultFlushTimeout = 5 * time.Second
)

// Config holds the settings a configuration loader can fill; a zero field
// means its default. Pass it with [WithConfig].
type Config struct {
	// Service names the service on the resource: service.name,
	// service.version and deployment.environment.name. Empty fields fall
	// back to the SDK's OTEL_SERVICE_NAME and OTEL_RESOURCE_ATTRIBUTES;
	// set fields always win.
	Service srvr.ServiceConfig `json:"service" yaml:"service" env:"SERVICE_"`
	// Prometheus serves metrics for scraping.
	Prometheus PrometheusConfig `json:"prometheus" yaml:"prometheus" env:"PROMETHEUS_"`
	// OTLP exports metrics and traces to a collector.
	OTLP OTLPConfig `json:"otlp" yaml:"otlp" env:"OTLP_"`
	// FlushTimeout bounds Shutdown. Default 5s; it must not be negative.
	FlushTimeout time.Duration `json:"flush_timeout" yaml:"flush_timeout" env:"FLUSH_TIMEOUT"`
	// SetGlobal installs the setup's providers and a W3C TraceContext plus
	// Baggage propagator as the OpenTelemetry globals. Default false: a
	// library-style setup changes no process-wide state.
	SetGlobal bool `json:"set_global" yaml:"set_global" env:"SET_GLOBAL"`
}

// PrometheusConfig configures Prometheus scraping.
type PrometheusConfig struct {
	// Enabled turns on a Prometheus reader on a registry of the setup's
	// own, never prometheus.DefaultRegisterer.
	Enabled bool `json:"enabled" yaml:"enabled" env:"ENABLED"`
	// Addr is the metrics listener's host:port. Default 127.0.0.1:9464
	// (loopback only). Binding a public address loosens an exposure
	// default: metrics describe the service's internals.
	//
	// The listener has a ReadHeaderTimeout of 10s, a WriteTimeout of 30s
	// and an IdleTimeout of 60s, and serves at most 4 scrapes at a time,
	// each cut off after 20s: every scrape runs a full collection, and the
	// listener may be made public. These limits are not configurable; with
	// DisableListener your own server sets the listener's timeouts.
	Addr string `json:"addr" yaml:"addr" env:"ADDR"`
	// Path is where metrics are served. Default /metrics. It must start
	// with / and be a pattern http.ServeMux accepts.
	Path string `json:"path" yaml:"path" env:"PATH"`
	// DisableListener leaves serving to the consumer, who mounts
	// [Setup.MetricsHandler] on a listener of their own. Default false: the
	// setup runs its own metrics listener on Addr.
	DisableListener bool `json:"disable_listener" yaml:"disable_listener" env:"DISABLE_LISTENER"`
}

// OTLP protocols.
const (
	// ProtocolHTTPProtobuf is OTLP over HTTP with protobuf bodies, the
	// default: it passes ordinary HTTP proxies and needs no HTTP/2.
	ProtocolHTTPProtobuf = "http/protobuf"
	// ProtocolGRPC is OTLP over gRPC.
	ProtocolGRPC = "grpc"
)

// OTLPConfig configures OTLP export. Fields left empty fall back to what
// the OpenTelemetry SDK reads from the OTEL_EXPORTER_OTLP_* environment
// (endpoint, headers, TLS, timeout), else its defaults; a set field always
// wins.
type OTLPConfig struct {
	// Endpoint is where the collector listens: a base URL
	// ("http://collector:4318" for plaintext, "https://..." for TLS; for
	// http/protobuf the signal path /v1/metrics or /v1/traces is appended,
	// as for OTEL_EXPORTER_OTLP_ENDPOINT) or a host:port, which uses TLS.
	// A URL must parse and have scheme http or https and a host; a
	// host:port needs a host and a numeric port; otherwise New fails with
	// [ErrConfig] (the SDK would silently ignore the value). Empty:
	// OTEL_EXPORTER_OTLP_ENDPOINT, else the SDK default (localhost:4318 or
	// :4317). An operator's environment can therefore redirect export when
	// this is empty.
	Endpoint string `json:"endpoint" yaml:"endpoint" env:"ENDPOINT"`
	// Protocol is "http/protobuf" (default) or "grpc".
	Protocol string `json:"protocol" yaml:"protocol" env:"PROTOCOL"`
	// Headers are sent with every export, for example an API key. New
	// copies the map.
	Headers map[string]string `json:"headers" yaml:"headers" env:"HEADERS"`
	// Metrics exports metrics through a periodic reader (interval
	// OTEL_METRIC_EXPORT_INTERVAL, default 60s).
	Metrics bool `json:"metrics" yaml:"metrics" env:"METRICS"`
	// Traces exports spans through a batch span processor and builds a
	// TracerProvider.
	Traces bool `json:"traces" yaml:"traces" env:"TRACES"`
}

// enabled reports whether OTLP export is configured at all.
func (o OTLPConfig) enabled() bool {
	return o.Endpoint != "" || o.Protocol != "" || o.Metrics || o.Traces || len(o.Headers) > 0
}

// Option configures a [Setup]. Options apply in argument order, so a later
// option wins over an earlier one.
type Option func(*settings)

// settings is what the options build: the configuration and the logger.
type settings struct {
	Config

	logger *slog.Logger
}

// WithConfig sets every field from c, a zero field meaning its default,
// replacing what earlier options set.
func WithConfig(c Config) Option {
	return func(s *settings) { s.Config = c }
}

// WithPrometheus enables Prometheus scraping with the defaults (see
// [PrometheusConfig]).
func WithPrometheus() Option {
	return func(s *settings) { s.Prometheus.Enabled = true }
}

// WithPrometheusConfig sets the Prometheus configuration.
func WithPrometheusConfig(p PrometheusConfig) Option {
	return func(s *settings) { s.Prometheus = p }
}

// WithOTLP sets the OTLP configuration.
func WithOTLP(o OTLPConfig) Option {
	return func(s *settings) { s.OTLP = o }
}

// WithService sets the service on the resource (see [Config.Service]).
func WithService(svc srvr.ServiceConfig) Option {
	return func(s *settings) { s.Service = svc }
}

// WithFlushTimeout sets [Config.FlushTimeout].
func WithFlushTimeout(d time.Duration) Option {
	return func(s *settings) { s.FlushTimeout = d }
}

// WithLogger sets the logger otel.export.failed records are written to
// when [Config.SetGlobal] installs the error handler. Default
// slog.Default(), which is srvr's logger with srvr.WithSetDefaultLogger;
// pass the logger given to srvr.WithLogger otherwise. A nil logger keeps
// the default.
func WithLogger(l *slog.Logger) Option {
	return func(s *settings) { s.logger = l }
}

// WithSetGlobal installs the setup's providers and propagator as the
// OpenTelemetry globals (see [Config.SetGlobal]).
func WithSetGlobal() Option {
	return func(s *settings) { s.SetGlobal = true }
}

// resolve fills the defaults.
func (c Config) resolve() Config {
	if c.FlushTimeout == 0 {
		c.FlushTimeout = DefaultFlushTimeout
	}

	if c.Prometheus.Addr == "" {
		c.Prometheus.Addr = DefaultPrometheusAddr
	}

	if c.Prometheus.Path == "" {
		c.Prometheus.Path = DefaultPrometheusPath
	}

	if c.OTLP.enabled() && c.OTLP.Protocol == "" {
		c.OTLP.Protocol = ProtocolHTTPProtobuf
	}

	// The exporters keep the map they are given and read it on every
	// export, so a caller changing its map after New would change, and
	// race with, the exports.
	c.OTLP.Headers = maps.Clone(c.OTLP.Headers)

	return c
}

// validate reports every invalid field of a resolved configuration.
func (c Config) validate() error {
	var errs []error

	if !c.Prometheus.Enabled && !c.OTLP.enabled() {
		errs = append(errs, &ConfigError{Field: "Exporters", Reason: "none is enabled", text: "no exporter is enabled (Prometheus.Enabled, OTLP)"})
	}

	if c.OTLP.enabled() {
		if p := c.OTLP.Protocol; p != ProtocolHTTPProtobuf && p != ProtocolGRPC {
			errs = append(errs, configError("OTLP.Protocol", strconv.Quote(p),
				"must be "+strconv.Quote(ProtocolHTTPProtobuf)+" or "+strconv.Quote(ProtocolGRPC)))
		}

		if !c.OTLP.Metrics && !c.OTLP.Traces {
			errs = append(errs, configError("OTLP", "", "enables neither Metrics nor Traces"))
		}

		if ep := c.OTLP.Endpoint; ep != "" {
			if err := checkEndpoint(ep); err != nil {
				errs = append(errs, causeError("OTLP.Endpoint", strconv.Quote(ep), err))
			}
		}
	}

	if c.FlushTimeout < 0 {
		errs = append(errs, configError("FlushTimeout", c.FlushTimeout.String(), "must not be negative"))
	}

	if c.Prometheus.Enabled {
		if err := validateAddr(c.Prometheus.Addr); err != nil {
			errs = append(errs, err)
		}

		if err := validatePath(c.Prometheus.Path); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// validateAddr checks that addr is host:port with a numeric port; the host
// may be empty (all interfaces).
func validateAddr(addr string) error {
	if _, err := splitHostPort(addr); err != nil {
		return causeError("Prometheus.Addr", strconv.Quote(addr), err)
	}

	return nil
}

// splitHostPort returns the host of addr, a host:port whose port is a
// number in 0-65535.
func splitHostPort(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}

	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return "", &portError{port: port, err: err}
	}

	return host, nil
}

// portError reports a port that is not a number in 0-65535; it unwraps to
// the *strconv.NumError, which its text leaves out.
type portError struct {
	port string
	err  error
}

func (e *portError) Error() string {
	return "port " + strconv.Quote(e.port) + " is not a number in 0-65535"
}

func (e *portError) Unwrap() error { return e.err }

// validatePath checks that path starts with / and is a pattern
// http.ServeMux accepts. ServeMux panics on a pattern it rejects, so the
// path is registered on a throwaway mux under recover, before anything is
// built or bound.
func validatePath(path string) (err error) {
	if !strings.HasPrefix(path, "/") {
		return configError("Prometheus.Path", strconv.Quote(path), "must start with /")
	}

	defer func() {
		if v := recover(); v != nil {
			e := configErrorColon("Prometheus.Path", strconv.Quote(path), errchain.ValueText(v))
			e.Cause = panicCause(v)
			err = e
		}
	}()

	http.NewServeMux().Handle(path, http.NotFoundHandler())

	return nil
}

// panicCause returns the recovered panic value v when it is an error that
// callers can safely reach with errors.Is and errors.As, and nil
// otherwise: a value that is not an error, a typed nil (it carries no
// cause), or an error whose chain cycles or whose Unwrap panics (errors.Is
// would loop or crash). An error whose Error method panics is kept:
// errors.Is never calls Error, and the text goes through errchain.ValueText.
// otelsrvr and otelsetup each keep a copy, so engine's public API does not
// grow for it.
func panicCause(v any) error {
	err, ok := v.(error)
	if !ok || errchain.IsNil(err) {
		return nil
	}

	// GuardChain returns err itself when its chain can be walked and an
	// error of another type otherwise. Types are compared, never values,
	// because err's type may not be comparable.
	if reflect.TypeOf(engine.GuardChain(err)) != reflect.TypeOf(err) {
		return nil
	}

	return err
}
