package engine

import (
	"cmp"
	"errors"
	"fmt"
	"net"
	"net/http"
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
	// Addr is the TCP address to listen on. Default ":8080", the common
	// container port, on every interface so the pod or host can route to
	// it.
	Addr string `json:"addr" yaml:"addr" env:"ADDR"`
	// ReadHeaderTimeout bounds reading the request headers. Default 10s:
	// long enough for slow clients, short enough that a slowloris client
	// cannot hold connections open.
	ReadHeaderTimeout time.Duration `json:"read_header_timeout" yaml:"read_header_timeout" env:"READ_HEADER_TIMEOUT"`
	// ReadTimeout bounds reading the whole request. Default 60s, enough to
	// upload the default 4 MiB body over a slow link while bounding stalled
	// uploads.
	ReadTimeout time.Duration `json:"read_timeout" yaml:"read_timeout" env:"READ_TIMEOUT"`
	// WriteTimeout bounds writing the response. Default 60s, so a client
	// that stops reading cannot hold a handler forever, while ordinary
	// responses and modest downloads finish.
	WriteTimeout time.Duration `json:"write_timeout" yaml:"write_timeout" env:"WRITE_TIMEOUT"`
	// IdleTimeout bounds how long a keep-alive connection waits for the next
	// request. Default 120s, above the 60s idle timeout of common load
	// balancers, so they close idle connections first and never send a
	// request on one the server is closing.
	IdleTimeout time.Duration `json:"idle_timeout" yaml:"idle_timeout" env:"IDLE_TIMEOUT"`
	// MaxHeaderBytes limits the size of the request headers. Default 1 MiB,
	// net/http's own default, which fits large cookies and tokens. It cannot
	// be disabled: unbounded headers would let one request exhaust memory.
	MaxHeaderBytes int `json:"max_header_bytes" yaml:"max_header_bytes" env:"MAX_HEADER_BYTES"`
	// MaxRequestBodyBytes limits the size of request bodies. Default 4 MiB:
	// ample for JSON APIs, small enough that a flood of large bodies cannot
	// exhaust memory. Raise it for uploads; negative disables it, which
	// loosens a safety default.
	MaxRequestBodyBytes int64 `json:"max_request_body_bytes" yaml:"max_request_body_bytes" env:"MAX_REQUEST_BODY_BYTES"`
	// ShutdownTimeout bounds draining in-flight requests when Start's
	// context ends. Default 15s, inside Kubernetes' 30s termination grace
	// period with room for the shutdown hooks. It cannot be disabled, so a
	// stuck request never blocks a stop forever.
	ShutdownTimeout time.Duration `json:"shutdown_timeout" yaml:"shutdown_timeout" env:"SHUTDOWN_TIMEOUT"`
	// ShutdownHookTimeout bounds the AfterDrain shutdown hooks. Default 5s,
	// enough to flush telemetry or close a pool, and with ShutdownTimeout
	// still inside a 30s grace period; negative leaves them unbounded, which
	// loosens a safety default.
	ShutdownHookTimeout time.Duration `json:"shutdown_hook_timeout" yaml:"shutdown_hook_timeout" env:"SHUTDOWN_HOOK_TIMEOUT"`
}

// Settings is the configuration options build, validated once by
// [ResolveConfig]. Its fields are exported only so that the option
// functions of package srvr can set them: they are for srvr's engines.
// Consumers configure a server through srvr's With... options and config
// structs and never write Settings themselves.
type Settings struct {
	Config

	// Listener is the consumer's listener (WithListener), used instead of
	// binding Addr.
	Listener net.Listener
	// Hooks are the shutdown hooks in registration order.
	Hooks []Hook
	// Trace is the trace context configuration.
	Trace TraceConfig
	// Log is the logging configuration.
	Log LogSettings
	// Access is the access log configuration; SkipPaths is the server's
	// own copy.
	Access AccessLogConfig
	// Service names the service in lifecycle and failed records.
	Service ServiceConfig
	// Redactor is applied to error text; RedactSecrets unless an option
	// set another.
	Redactor func(string) string
	// ReadBuild reads the binary's build information: debug.ReadBuildInfo,
	// replaced in tests.
	ReadBuild func() (*debug.BuildInfo, bool)
	// Now is the clock of the unmatched-record limit, its only reader:
	// time.Now, whose monotonic reading ignores clock steps, replaced in
	// tests.
	Now func() time.Time
	// ProblemOptions are the consumer's prblm options, applied to every
	// engine's problem renderer before srvr's own.
	ProblemOptions []prblm.Option
	// ErrorHook is the consumer's hook for answered errors (WithErrorHook).
	ErrorHook ErrorHook
	// RouteChecks run in order after the configure phase.
	RouteChecks []RouteCheck
	// Observers are the request observers in registration order.
	Observers []Observer
	// Errs collects mistakes options detect while they are applied.
	Errs []error

	// engineOpts are the engine-specific options, each read only by the
	// engine it names (see Only).
	engineOpts []engineOpt
}

// ConfigError is the error construction returns for one invalid field:
// errors.As reads Field, Value and Reason, and errors.Is matches
// [ErrConfig].
type ConfigError struct {
	// Field names the setting, such as "Level" or "Listener".
	Field string
	// Value is the offending value's text.
	Value string
	// Reason says what is wrong with it.
	Reason string
}

func (e *ConfigError) Error() string {
	return ErrConfig.Error() + ": " + e.Field + " " + e.Value + ": " + e.Reason
}

// Unwrap returns [ErrConfig].
func (e *ConfigError) Unwrap() error { return ErrConfig }

// NewConfigError returns a *ConfigError naming field and value, whose text
// is read through ValueText: a value may be consumer code.
func NewConfigError(field string, value any, reason string) error {
	return &ConfigError{Field: field, Value: ValueText(value), Reason: reason}
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

// Only returns an Option that carries value for engine only, such
// as ginsrvr.WithGinEngine for "gin". The engine reads it with
// [Core.EngineOptions]; any other engine fails construction naming name.
func Only(engineName, name string, value any) Option {
	return func(s *Settings) {
		s.engineOpts = append(s.engineOpts, engineOpt{engine: engineName, name: name, value: value})
	}
}

// newSettings applies opts and validates the result.
func newSettings(opts []Option) (Settings, error) {
	s := Settings{ReadBuild: debug.ReadBuildInfo, Now: time.Now}

	for i, opt := range opts {
		if opt == nil {
			s.Errs = append(s.Errs, NewConfigError("Option", i, "is nil"))

			continue
		}

		opt(&s)
	}

	s.resolveDefaults()

	if errs := slices.Concat(s.Errs, s.validate()); len(errs) > 0 {
		return Settings{}, errors.Join(errs...)
	}

	return s, nil
}

// resolveDefaults fills the defaults chosen after every option applied
// and copies what the caller still owns, so no option has to remember to:
// the error redactor is RedactSecrets unless an option set one, and the
// access log's SkipPaths is a copy, so changing the caller's slice after
// New never changes which records are written.
func (s *Settings) resolveDefaults() {
	if s.Redactor == nil {
		s.Redactor = RedactSecrets
	}

	s.Access.SkipPaths = slices.Clone(s.Access.SkipPaths)
}

// validate reports every invalid field.
func (s *Settings) validate() []error {
	var errs []error

	for _, check := range []func(*Settings) error{
		validateAddr,
		validateListener,
		validateShutdownTimeout,
		validateMaxHeaderBytes,
		validateObservers,
		validateLog,
		validateDebugBuffer,
		validateUnmatchedLimit,
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
		return NewConfigError("Addr", strconv.Quote(s.Addr), err.Error())
	}

	if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return NewConfigError("Addr", strconv.Quote(s.Addr), fmt.Sprintf("port %q is not a number in 0-65535", port))
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

		return NewConfigError("Listener", addr, fmt.Sprintf("cannot be combined with Addr %q", s.Addr))
	}

	return nil
}

func validateShutdownTimeout(s *Settings) error {
	if s.ShutdownTimeout < 0 {
		return NewConfigError("ShutdownTimeout", s.ShutdownTimeout, "must not be negative")
	}

	return nil
}

func validateMaxHeaderBytes(s *Settings) error {
	if s.MaxHeaderBytes < 0 {
		return NewConfigError("MaxHeaderBytes", s.MaxHeaderBytes, "must not be negative")
	}

	return nil
}

// validateObservers rejects a nil or typed-nil observer, which would panic
// on every request.
func validateObservers(s *Settings) error {
	var errs []error

	for i, o := range s.Observers {
		if IsNil(o) {
			errs = append(errs, NewConfigError("Observer", fmt.Sprintf("%d (%T)", i, o), "is nil"))
		}
	}

	return errors.Join(errs...)
}

// Defaults, each a deliberate choice; the godoc of the matching Config
// field states the full reason.
const (
	// defaultAddr is the conventional container port.
	defaultAddr = ":8080"
	// defaultReadHeaderTimeout protects against slow-header (Slowloris) clients.
	defaultReadHeaderTimeout = 10 * time.Second
	// defaultReadTimeout and defaultWriteTimeout bound resource use while
	// staying long enough for typical APIs.
	defaultReadTimeout  = 60 * time.Second
	defaultWriteTimeout = 60 * time.Second
	// defaultIdleTimeout keeps connections for reuse without leaking them.
	defaultIdleTimeout = 120 * time.Second
	// defaultMaxHeaderBytes is the net/http default, made explicit.
	defaultMaxHeaderBytes = 1 << 20
	// defaultMaxRequestBodyBytes refuses accidental or hostile large bodies.
	defaultMaxRequestBodyBytes = 4 << 20
	// defaultShutdownTimeout fits a 30s Kubernetes termination grace period.
	defaultShutdownTimeout = 15 * time.Second
)

// resolve returns c with the defaults applied: a zero field becomes its
// default and a negative one stays negative, which disables the limit.
// net/http also reads a negative timeout as "none", while it reads a zero
// ReadHeaderTimeout or IdleTimeout as "use ReadTimeout", so a disabled
// timeout must never become zero.
func (c Config) resolve() Config {
	return Config{
		Addr:                cmp.Or(c.Addr, defaultAddr),
		ReadHeaderTimeout:   cmp.Or(c.ReadHeaderTimeout, defaultReadHeaderTimeout),
		ReadTimeout:         cmp.Or(c.ReadTimeout, defaultReadTimeout),
		WriteTimeout:        cmp.Or(c.WriteTimeout, defaultWriteTimeout),
		IdleTimeout:         cmp.Or(c.IdleTimeout, defaultIdleTimeout),
		MaxHeaderBytes:      cmp.Or(c.MaxHeaderBytes, defaultMaxHeaderBytes),
		MaxRequestBodyBytes: cmp.Or(c.MaxRequestBodyBytes, defaultMaxRequestBodyBytes),
		ShutdownTimeout:     cmp.Or(c.ShutdownTimeout, defaultShutdownTimeout),
		ShutdownHookTimeout: cmp.Or(c.ShutdownHookTimeout, defaultShutdownHookTimeout),
	}
}

// httpServer returns the http.Server serving h with the limits of c, which
// resolve has completed.
func (c Config) httpServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: c.ReadHeaderTimeout,
		ReadTimeout:       c.ReadTimeout,
		WriteTimeout:      c.WriteTimeout,
		IdleTimeout:       c.IdleTimeout,
		MaxHeaderBytes:    c.MaxHeaderBytes,
	}
}

// LimitBody returns next with request bodies limited to n bytes, or next
// itself when n is negative (disabled). Reading past the limit returns an
// *http.MaxBytesError, which prblm renders as a 413 problem.
func LimitBody(n int64, next http.Handler) http.Handler {
	if n < 0 {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && r.Body != http.NoBody {
			r.Body = http.MaxBytesReader(w, r.Body, n)
		}
		next.ServeHTTP(w, r)
	})
}
