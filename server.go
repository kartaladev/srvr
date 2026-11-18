package srvr

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/engine"
)

// engineName names the net/http engine in engine-specific option errors.
const engineName = "net/http"

// Server is an HTTP server on the net/http engine.
//
// Create it with [New], add routes with [Server.Register] and run it with
// [Server.Start], which blocks until its context ends or [Server.Shutdown]
// is called. Every error a handler returns is rendered as an RFC 9457
// problem by github.com/kartaladev/prblm; an unrecognised error becomes a
// 500 problem that carries none of the error's text.
//
// A Server is single-use: it starts at most once and, once stopped, stays
// stopped. Its methods are safe for concurrent use.
//
// The defaults are listed on [Config]: listen on ":8080", bounded timeouts
// and header size, request bodies limited to 4 MiB and a shutdown drain of
// 15s, which fits a 30s Kubernetes termination grace period.
type Server struct {
	core *engine.Core
	lc   *engine.Lifecycle[RouteConfigurer[*Group]]
}

var _ RouteRegistrar[*Group] = (*Server)(nil)

// New returns a server configured by opts. It does not open a listener.
// Invalid configuration returns an error wrapping [ErrConfig] that names
// every offending field and value, including an option meant for another
// engine.
func New(opts ...Option) (*Server, error) {
	core, err := engine.ResolveConfig(opts)
	if err != nil {
		return nil, err
	}

	if _, err := core.EngineOptions(engineName); err != nil {
		return nil, err
	}

	s := &Server{core: core, lc: engine.NewLifecycle[RouteConfigurer[*Group]](core)}
	core.SetDefaultLogger()

	return s, nil
}

// Register adds conf to the configurers run when the server starts. Each
// registration runs Configure once, in registration order, during Start.
// A nil or typed-nil conf returns an error wrapping [ErrRouteConfig]. Once
// Start was called it returns [ErrServerStarted], or [ErrServerClosed] when
// the server has stopped.
func (s *Server) Register(conf RouteConfigurer[*Group]) error {
	return s.lc.Register(conf)
}

// Ready returns a channel closed once the listener is bound and the server
// accepts connections. It is never closed when Start fails.
func (s *Server) Ready() <-chan struct{} { return s.lc.Ready() }

// Addr returns the bound address once the server is ready, for example the
// port chosen for "127.0.0.1:0", and the empty string before.
func (s *Server) Addr() string { return s.lc.Addr() }

// Start configures the routes, binds the listener, closes [Server.Ready]
// and serves until ctx is done or [Server.Shutdown] is called. Then it stops
// accepting connections, drains in-flight requests for at most
// ShutdownTimeout and returns nil.
//
// Start returns an error, without closing Ready, when ctx is nil (wrapping
// [ErrConfig]) or already done, or when binding fails. A second Start
// returns [ErrServerStarted], or [ErrServerClosed] once the server stopped,
// without affecting it. When serving fails on its own, for example because
// a [WithListener] listener's Accept fails, Start stops the server in order
// and returns that error. When [Server.Shutdown] is called while Start is
// still starting (configuring routes, running route checks or binding), the
// context route checks receive is cancelled and Start returns an error
// wrapping [ErrServerClosed] without binding; a check that ignores its
// context keeps Start waiting until it returns or ctx is done.
//
// Start writes a server.failed record for a failure to start or serve and
// a server.shutdown_hook.failed record for each failing hook, so a caller
// that also logs the returned error logs it twice. It writes none for a
// nil ctx, [ErrServerStarted], [ErrServerClosed] or a start Shutdown
// interrupted.
func (s *Server) Start(ctx context.Context) error {
	return s.lc.Start(ctx, func(configurers []RouteConfigurer[*Group]) (engine.Runner, engine.Routes, error) {
		mux, routes, err := s.router(configurers)
		if err != nil {
			return nil, nil, err
		}

		return engine.HTTPRunner(s.core.Config(), s.pipeline(mux)), routes, nil
	})
}

// Shutdown stops the server gracefully: it stops accepting connections and
// waits for in-flight requests until ctx is done, then closes the remaining
// connections and returns the context error.
//
// Shutdown is idempotent: on a server that was never started it closes the
// server, so a later Start returns [ErrServerClosed]; on a server still
// starting it interrupts Start (see [Server.Start]) and waits for it to
// close the server; while another call is
// stopping the server it waits for that stop and returns nil; once stopped
// it returns nil. A nil ctx returns an error wrapping [ErrConfig] and
// leaves the server running. A failing shutdown hook is logged as
// server.shutdown_hook.failed as well as returned.
func (s *Server) Shutdown(ctx context.Context) error { return s.lc.Shutdown(ctx) }

// router runs the configurers and returns the ServeMux holding their
// routes and the listing of the routes they declared.
func (s *Server) router(configurers []RouteConfigurer[*Group]) (*http.ServeMux, engine.Routes, error) {
	b := &builder{wrap: s.wrapHandler}
	g := &Group{b: b}

	for _, c := range configurers {
		b.owner = fmt.Sprintf("%T", c)
		if err := engine.Configure(c, g); err != nil {
			return nil, nil, err
		}
	}

	if len(b.errs) > 0 {
		return nil, nil, errors.Join(b.errs...)
	}

	mux := http.NewServeMux()
	if err := installRoutes(mux, b.routes); err != nil {
		return nil, nil, err
	}

	routes := make(engine.Routes, len(b.routes))
	for i, rt := range b.routes {
		routes[i] = engine.Route{Method: rt.method, Pattern: rt.pattern, Configurer: rt.owner}
	}

	return mux, routes, nil
}

// pipeline wraps the router in the per-request stages, outermost first:
// trace context, access log (which begins the observers), body limit, panic recovery, unmatched-route
// answers, router.
func (s *Server) pipeline(mux *http.ServeMux) http.Handler {
	return s.core.StartTrace(s.stages(mux))
}

// stages are the per-request stages inside the trace context stage.
func (s *Server) stages(mux *http.ServeMux) http.Handler {
	return s.core.LogAccess(engine.LimitBody(s.core.Config().MaxRequestBodyBytes, s.core.Problems().Recover(s.answerUnmatched(mux))))
}

// wrapHandler renders the errors h returns as problems, after guarding
// prblm's resolution against cyclic error chains.
func (s *Server) wrapHandler(h func(http.ResponseWriter, *http.Request) error) http.Handler {
	return s.core.Problems().Wrap(func(w http.ResponseWriter, r *http.Request) error { return engine.GuardChain(h(w, r)) })
}

// answerUnmatched returns the router stage: matched requests go to mux;
// for unmatched ones, ServeMux's plain-text 404 and 405 become problems
// (the 405 keeps ServeMux's Allow header), and any other fallback answer,
// such as a redirect to the canonical path, passes through unchanged.
//
// A matched request is dispatched through mux.ServeHTTP, never through the
// handler mux.Handler returns, because only ServeHTTP sets r.Pattern and
// the path values.
func (s *Server) answerUnmatched(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)

			return
		}

		rec := &fallbackRecorder{header: http.Header{}}
		mux.ServeHTTP(rec, r)

		switch rec.status {
		case http.StatusNotFound:
			s.core.Problems().Write(w, r, prblm.ErrNotFound)
		case http.StatusMethodNotAllowed:
			s.core.Problems().Write(w, r, prblm.New(http.StatusMethodNotAllowed, prblm.WithHeader("Allow", rec.header.Get("Allow"))))
		default:
			rec.replay(w)
		}
	})
}

// fallbackRecorder records what ServeMux's fallback handler writes.
type fallbackRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (f *fallbackRecorder) Header() http.Header { return f.header }

func (f *fallbackRecorder) WriteHeader(code int) {
	if f.status == 0 {
		f.status = code
	}
}

func (f *fallbackRecorder) Write(b []byte) (int, error) {
	f.WriteHeader(http.StatusOK)

	return f.body.Write(b)
}

// replay writes the recorded response to w.
func (f *fallbackRecorder) replay(w http.ResponseWriter) {
	maps.Copy(w.Header(), f.header)

	w.WriteHeader(cmp.Or(f.status, http.StatusOK))
	_, _ = w.Write(f.body.Bytes())
}
