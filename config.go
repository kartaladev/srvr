package srvr

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

// ErrConfig is wrapped by every error New returns for invalid
// configuration. The message names the offending field and value.
var ErrConfig = errors.New("srvr: invalid configuration")

// Config holds the server settings that a configuration loader can fill.
// A zero field means the default; for timeouts and MaxRequestBodyBytes a
// negative value disables the limit. Pass it with [WithConfig].
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

// settings is the configuration the options build, validated once by New.
type settings struct {
	Config

	listener net.Listener
	hooks    []hook
	trace    TraceConfig
	log      logSettings
	access   AccessLogConfig
	service  ServiceConfig
	redactor func(string) string
	// readBuild reads the binary's build information: debug.ReadBuildInfo,
	// replaced in tests (export_test.go).
	readBuild      func() (*debug.BuildInfo, bool)
	problemOptions []prblm.Option
	errorHook      ErrorHook
	// errs collects mistakes options detect while they are applied.
	errs []error
}

// configError returns an error wrapping ErrConfig that names field and
// value, whose text is read through valueText: a value may be consumer code.
func configError(field string, value any, reason string) error {
	return fmt.Errorf("%w: %s %s: %s", ErrConfig, field, valueText(value), reason)
}

// Option configures a Server. Options apply in argument order, so a later
// option wins over an earlier one.
type Option func(*settings)

// WithConfig sets every field of [Config], a zero field meaning its
// default. It replaces what earlier options set for those fields.
func WithConfig(c Config) Option {
	return func(s *settings) { s.Config = c }
}

// WithAddr sets the TCP address the server listens on. The default is
// ":8080", the conventional container port.
func WithAddr(addr string) Option {
	return func(s *settings) { s.Addr = addr }
}

// WithMaxHeaderBytes limits the size of the request headers (default
// 1 MiB). n must be positive: unlike a zero Config field, an explicit zero
// is a mistake.
func WithMaxHeaderBytes(n int) Option {
	return func(s *settings) {
		if n == 0 {
			s.errs = append(s.errs, configError("MaxHeaderBytes", n, "must be positive"))
		}

		s.MaxHeaderBytes = n
	}
}

// WithListener makes the server serve on ln instead of listening on Addr,
// for example a listener from socket activation or a test. It cannot be
// combined with an address.
//
// The server owns ln once New returns it, as http.Server.Serve does; if New
// fails, the caller keeps ln. The server closes ln whenever it closes,
// whether it served or not: after serving, when Start fails, and when
// Shutdown is called before Start. Start(nil) and a Start returning
// ErrServerStarted do not close ln; a Start returning ErrServerClosed does
// not either (ln was closed when the server closed).
//
// A nil or typed-nil ln fails New. Start reads ln.Addr() before serving: a
// nil or typed-nil address, or a panic in Addr or in the address's String,
// fails Start with an error wrapping ErrConfig naming Listener, like any
// other Start failure, instead of serving under an invented address.
//
// The server calls ln's Accept and Close under recover. A panic in Accept,
// which net/http calls on a goroutine of its own where it would end the
// process, stops serving: Start writes server.failed, stops the server in
// order (BeforeDrain hooks, drain, AfterDrain hooks) and returns an error
// wrapping ErrConfig naming Listener. A panic in Close is treated like an
// error from Close, which the server ignores: the stop completes and its
// result is the one a successful Close gives. Accepted connections are
// used as ln returns them.
func WithListener(ln net.Listener) Option {
	return func(s *settings) {
		if isNil(ln) {
			s.errs = append(s.errs, configError("Listener", typeName(ln), "is nil"))

			return
		}

		s.listener = ln
	}
}

// newSettings applies opts and validates the result.
func newSettings(opts []Option) (settings, error) {
	s := settings{readBuild: debug.ReadBuildInfo}

	for i, opt := range opts {
		if opt == nil {
			s.errs = append(s.errs, configError("Option", i, "is nil"))

			continue
		}

		opt(&s)
	}

	if errs := slices.Concat(s.errs, s.validate()); len(errs) > 0 {
		return settings{}, errors.Join(errs...)
	}

	return s, nil
}

// validate reports every invalid field.
func (s *settings) validate() []error {
	var errs []error

	for _, check := range []func(*settings) error{
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
func validateAddr(s *settings) error {
	if s.Addr == "" {
		return nil
	}

	_, port, err := net.SplitHostPort(s.Addr)
	if err != nil {
		return configError("Addr", strconv.Quote(s.Addr), err.Error())
	}

	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return configError("Addr", strconv.Quote(s.Addr), fmt.Sprintf("port %q is not a number in 0-65535", port))
	}

	return nil
}

// validateListener checks that a listener set with WithListener is not
// combined with an address.
func validateListener(s *settings) error {
	if s.listener != nil && s.Addr != "" {
		addr, err := listenerAddr(s.listener)
		if err != nil {
			addr = typeName(s.listener)
		}

		return configError("Listener", addr, fmt.Sprintf("cannot be combined with Addr %q", s.Addr))
	}

	return nil
}

func validateShutdownTimeout(s *settings) error {
	if s.ShutdownTimeout < 0 {
		return configError("ShutdownTimeout", s.ShutdownTimeout, "must not be negative")
	}

	return nil
}

func validateMaxHeaderBytes(s *settings) error {
	if s.MaxHeaderBytes < 0 {
		return configError("MaxHeaderBytes", s.MaxHeaderBytes, "must not be negative")
	}

	return nil
}
