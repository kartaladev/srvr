package engine

import (
	"cmp"
	"net/http"
	"time"
)

// Defaults, each a deliberate choice; see Config for the reasons.
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
