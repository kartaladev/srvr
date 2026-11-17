package srvr

import (
	"net"

	"github.com/kartaladev/srvr/engine"
)

// WithConfig sets every field of [Config], a zero field meaning its
// default. It replaces what earlier options set for those fields.
func WithConfig(c Config) Option {
	return func(s *engine.Settings) { s.Config = c }
}

// WithAddr sets the TCP address the server listens on. The default is
// ":8080", the conventional container port.
func WithAddr(addr string) Option {
	return func(s *engine.Settings) { s.Addr = addr }
}

// WithMaxHeaderBytes limits the size of the request headers (default
// 1 MiB). n must be positive: unlike a zero Config field, an explicit zero
// is a mistake.
func WithMaxHeaderBytes(n int) Option {
	return func(s *engine.Settings) {
		if n == 0 {
			s.Errs = append(s.Errs, engine.ConfigError("MaxHeaderBytes", n, "must be positive"))
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
// wrapping ErrConfig naming Listener. The server calls ln's Close once and
// ignores its error or panic on every path, including
// http.Server.Shutdown's own close: the stop completes, its result is the
// one a successful Close gives, and the drain is never reported as forced
// because of it. Accepted connections are
// used as ln returns them.
func WithListener(ln net.Listener) Option {
	return func(s *engine.Settings) {
		if engine.IsNil(ln) {
			s.Errs = append(s.Errs, engine.ConfigError("Listener", engine.TypeName(ln), "is nil"))

			return
		}

		s.Listener = ln
	}
}
