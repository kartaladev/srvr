package engine

import (
	"errors"
	"fmt"
	"net"
	"runtime/debug"
	"slices"
	"strconv"
	"time"

	"github.com/kartaladev/prblm"
)

// Config holds the server settings that a configuration loader can fill.
// A zero field means the default; for timeouts and MaxRequestBodyBytes a
// negative value disables the limit. Consumers use it as srvr.Config and
// pass it with srvr.WithConfig.
type Config struct {
	// Addr is the TCP address to listen on. Default ":8080".
	Addr string `json:"addr" yaml:"addr" env:"ADDR"`
	// ReadHeaderTimeout bounds reading the request headers. Default 10s.
	ReadHeaderTimeout time.Duration `json:"read_header_timeout" yaml:"read_header_timeout" env:"READ_HEADER_TIMEOUT"`
	// ReadTimeout bounds reading the whole request. Default 60s.
	ReadTimeout time.Duration `json:"read_timeout" yaml:"read_timeout" env:"READ_TIMEOUT"`
	// WriteTimeout bounds writing the response. Default 60s.
	WriteTimeout time.Duration `json:"write_timeout" yaml:"write_timeout" env:"WRITE_TIMEOUT"`
	// IdleTimeout bounds how long a keep-alive connection waits for the next
	// request. Default 120s.
	IdleTimeout time.Duration `json:"idle_timeout" yaml:"idle_timeout" env:"IDLE_TIMEOUT"`
	// MaxHeaderBytes limits the size of the request headers. Default 1 MiB.
	// It cannot be disabled.
	MaxHeaderBytes int `json:"max_header_bytes" yaml:"max_header_bytes" env:"MAX_HEADER_BYTES"`
	// MaxRequestBodyBytes limits the size of request bodies. Default 4 MiB.
	MaxRequestBodyBytes int64 `json:"max_request_body_bytes" yaml:"max_request_body_bytes" env:"MAX_REQUEST_BODY_BYTES"`
	// ShutdownTimeout bounds draining in-flight requests when Start's
	// context ends. Default 15s. It cannot be disabled.
	ShutdownTimeout time.Duration `json:"shutdown_timeout" yaml:"shutdown_timeout" env:"SHUTDOWN_TIMEOUT"`
	// ShutdownHookTimeout bounds the AfterDrain shutdown hooks. Default 5s;
	// negative leaves them unbounded.
	ShutdownHookTimeout time.Duration `json:"shutdown_hook_timeout" yaml:"shutdown_hook_timeout" env:"SHUTDOWN_HOOK_TIMEOUT"`
}

// Settings is the configuration options build, validated once by
// [ResolveConfig]. Its fields are exported only so that the option
// functions of package srvr can set them: they are for srvr's engines.
// Consumers configure a server through srvr's With... options and config
// structs and never write Settings themselves.
type Settings struct {
	Config

	Listener net.Listener
	Hooks    []Hook
	Trace    TraceConfig
	Log      LogSettings
	Access   AccessLogConfig
	Service  ServiceConfig
	Redactor func(string) string
	// ReadBuild reads the binary's build information: debug.ReadBuildInfo,
	// replaced in tests.
	ReadBuild      func() (*debug.BuildInfo, bool)
	ProblemOptions []prblm.Option
	ErrorHook      ErrorHook
	// Errs collects mistakes options detect while they are applied.
	Errs []error

	// engineOpts are the engine-specific options, each read only by the
	// engine it names (see ForEngine).
	engineOpts []engineOpt
}

// ConfigError returns an error wrapping ErrConfig that names field and
// value, whose text is read through ValueText: a value may be consumer code.
func ConfigError(field string, value any, reason string) error {
	return fmt.Errorf("%w: %s %s: %s", ErrConfig, field, ValueText(value), reason)
}

// Option configures a server of any srvr engine; consumers use it as
// srvr.Option. Options apply in argument order, so a later option wins
// over an earlier one.
type Option func(*Settings)

// engineOpt is one engine-specific option: the engine that reads it, the
// option's name for error messages and its value.
type engineOpt struct {
	engine, name string
	value        any
}

// ForEngine returns an Option that carries value for engine only, such
// as ginsrvr.WithEngine for "gin". The engine reads it with
// [Core.EngineOptions]; any other engine fails construction naming name.
func ForEngine(engine, name string, value any) Option {
	return func(s *Settings) {
		s.engineOpts = append(s.engineOpts, engineOpt{engine: engine, name: name, value: value})
	}
}

// newSettings applies opts and validates the result.
func newSettings(opts []Option) (Settings, error) {
	s := Settings{ReadBuild: debug.ReadBuildInfo}

	for i, opt := range opts {
		if opt == nil {
			s.Errs = append(s.Errs, ConfigError("Option", i, "is nil"))

			continue
		}

		opt(&s)
	}

	if errs := slices.Concat(s.Errs, s.validate()); len(errs) > 0 {
		return Settings{}, errors.Join(errs...)
	}

	return s, nil
}

// validate reports every invalid field.
func (s *Settings) validate() []error {
	var errs []error

	for _, check := range []func(*Settings) error{
		validateAddr,
		validateListener,
		validateShutdownTimeout,
		validateMaxHeaderBytes,
	} {
		if err := check(s); err != nil {
			errs = append(errs, err)
		}
	}

	return errs
}

// validateAddr checks that Addr, when set, is host:port with a numeric port
// in 0-65535.
func validateAddr(s *Settings) error {
	if s.Addr == "" {
		return nil
	}

	_, port, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return ConfigError("Addr", strconv.Quote(s.Addr), err.Error())
	}

	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return ConfigError("Addr", strconv.Quote(s.Addr), fmt.Sprintf("port %q is not a number in 0-65535", port))
	}

	return nil
}

// validateListener checks that a listener set with WithListener is not
// combined with an address.
func validateListener(s *Settings) error {
	if s.Listener != nil && s.Addr != "" {
		addr, err := listenerAddr(s.Listener)
		if err != nil {
			addr = TypeName(s.Listener)
		}

		return ConfigError("Listener", addr, fmt.Sprintf("cannot be combined with Addr %q", s.Addr))
	}

	return nil
}

func validateShutdownTimeout(s *Settings) error {
	if s.ShutdownTimeout < 0 {
		return ConfigError("ShutdownTimeout", s.ShutdownTimeout, "must not be negative")
	}

	return nil
}

func validateMaxHeaderBytes(s *Settings) error {
	if s.MaxHeaderBytes < 0 {
		return ConfigError("MaxHeaderBytes", s.MaxHeaderBytes, "must not be negative")
	}

	return nil
}
