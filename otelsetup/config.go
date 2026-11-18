package otelsetup

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/kartaladev/srvr"
)

// ErrConfig is wrapped by every error New returns for invalid
// configuration; the message names the offending field and value.
var ErrConfig = errors.New("otelsetup: invalid configuration")

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
	Addr string `json:"addr" yaml:"addr" env:"ADDR"`
	// Path is where metrics are served. Default /metrics.
	Path string `json:"path" yaml:"path" env:"PATH"`
	// Listen runs the metrics listener. Default (nil) true; false leaves
	// serving to the consumer, who mounts [Setup.MetricsHandler].
	Listen *bool `json:"listen" yaml:"listen" env:"LISTEN"`
}

// listen reports whether the setup runs its own listener.
func (p PrometheusConfig) listen() bool { return p.Listen == nil || *p.Listen }

// Option configures a [Setup]. Options apply in argument order, so a later
// option wins over an earlier one.
type Option func(*Config)

// WithConfig sets every field from c, a zero field meaning its default,
// replacing what earlier options set.
func WithConfig(c Config) Option {
	return func(cfg *Config) { *cfg = c }
}

// WithPrometheus enables Prometheus scraping with the defaults (see
// [PrometheusConfig]).
func WithPrometheus() Option {
	return func(cfg *Config) { cfg.Prometheus.Enabled = true }
}

// WithPrometheusConfig sets the Prometheus configuration.
func WithPrometheusConfig(p PrometheusConfig) Option {
	return func(cfg *Config) { cfg.Prometheus = p }
}

// WithService sets the service on the resource (see [Config.Service]).
func WithService(s srvr.ServiceConfig) Option {
	return func(cfg *Config) { cfg.Service = s }
}

// WithFlushTimeout sets [Config.FlushTimeout].
func WithFlushTimeout(d time.Duration) Option {
	return func(cfg *Config) { cfg.FlushTimeout = d }
}

// WithSetGlobal installs the setup's providers and propagator as the
// OpenTelemetry globals (see [Config.SetGlobal]).
func WithSetGlobal() Option {
	return func(cfg *Config) { cfg.SetGlobal = true }
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

	return c
}

// validate reports every invalid field of a resolved configuration.
func (c Config) validate() error {
	var errs []error

	if !c.Prometheus.Enabled {
		errs = append(errs, fmt.Errorf("%w: no exporter is enabled (Prometheus.Enabled)", ErrConfig))
	}

	if c.FlushTimeout < 0 {
		errs = append(errs, fmt.Errorf("%w: FlushTimeout %v must not be negative", ErrConfig, c.FlushTimeout))
	}

	if c.Prometheus.Enabled {
		if err := validateAddr(c.Prometheus.Addr); err != nil {
			errs = append(errs, err)
		}

		if !strings.HasPrefix(c.Prometheus.Path, "/") {
			errs = append(errs, fmt.Errorf("%w: Prometheus.Path %q must start with /", ErrConfig, c.Prometheus.Path))
		}
	}

	return errors.Join(errs...)
}

// validateAddr checks that addr is host:port with a numeric port.
func validateAddr(addr string) error {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("%w: Prometheus.Addr %q: %w", ErrConfig, addr, err)
	}

	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return fmt.Errorf("%w: Prometheus.Addr %q: port %q is not a number in 0-65535", ErrConfig, addr, port)
	}

	return nil
}
