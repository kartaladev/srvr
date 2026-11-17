package srvr

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/kartaladev/prblm"
)

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
	settings settings
	// cfg is the server configuration with the defaults applied.
	cfg Config
	// problems renders every error response.
	problems *prblm.Handler
	// log is the handler srvr writes its records through and request
	// loggers are bound from.
	log *logHandler
	// serviceAttrs are added to lifecycle and failed records.
	serviceAttrs []slog.Attr
	// ready is closed once the listener is bound.
	ready chan struct{}

	mu          sync.Mutex
	state       state
	configurers []RouteConfigurer[*Group]
	addr        string
	// run is the serving part of a running server, nil before.
	run *run
	// stopped is closed when the server reaches stateClosed. hookErr and
	// drainErr are the results of its stop sequence: Start returns hookErr,
	// the Shutdown call that stopped the server returns both.
	stopped  chan struct{}
	hookErr  error
	drainErr error
}

var _ RouteRegistrar[*Group] = (*Server)(nil)

// New returns a server configured by opts. It does not open a listener.
// Invalid configuration returns an error wrapping [ErrConfig] that names
// every offending field and value.
func New(opts ...Option) (*Server, error) {
	s, err := newSettings(opts)
	if err != nil {
		return nil, err
	}

	srv := &Server{
		settings: s,
		cfg:      s.resolve(),
		ready:    make(chan struct{}),
		stopped:  make(chan struct{}),
	}

	if srv.log, err = newLogHandler(&srv.settings.log); err != nil {
		return nil, err
	}

	srv.serviceAttrs = s.service.attrs(s.readBuild)

	if s.log.setDefault {
		slog.SetDefault(slog.New(srv.log))
	}

	srv.problems, err = newProblems(s.problemOptions, prblm.WithEnrichers(traceIDEnricher), prblm.OnError(srv.onError))
	if err != nil {
		return nil, err
	}

	return srv, nil
}

// Register adds conf to the configurers run when the server starts. Each
// registration runs Configure once, in registration order, during Start.
// A nil or typed-nil conf returns an error wrapping [ErrRouteConfig]. Once
// Start was called it returns [ErrServerStarted], or [ErrServerClosed] when
// the server has stopped.
func (s *Server) Register(conf RouteConfigurer[*Group]) error {
	if isNil(conf) {
		return fmt.Errorf("%w: configurer %T is nil", ErrRouteConfig, conf)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.state.startable(); err != nil {
		return err
	}

	s.configurers = append(s.configurers, conf)

	return nil
}

// Ready returns a channel closed once the listener is bound and the server
// accepts connections. It is never closed when Start fails.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Addr returns the bound address once the server is ready, for example the
// port chosen for "127.0.0.1:0", and the empty string before.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.addr
}

// Start configures the routes, binds the listener, closes [Server.Ready]
// and serves until ctx is done or [Server.Shutdown] is called. Then it stops
// accepting connections, drains in-flight requests for at most
// ShutdownTimeout and returns nil.
//
// Start returns an error, without closing Ready, when ctx is nil (wrapping
// [ErrConfig]) or already done, or when binding fails. A second Start
// returns [ErrServerStarted], or [ErrServerClosed] once the server stopped,
// without affecting it.
func (s *Server) Start(ctx context.Context) error {
	if ctx == nil {
		return configError("Start context", "<nil>", "is nil")
	}

	configurers, err := s.begin()
	if err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return s.fail(ctx, err)
	}

	handler, err := s.configure(configurers)
	if err != nil {
		return s.fail(ctx, err)
	}

	ln, err := s.listen(ctx)
	if err != nil {
		return s.fail(ctx, err)
	}

	addr, err := listenerAddr(ln)
	if err != nil {
		return s.fail(ctx, configError("Listener", typeName(ln), err.Error()))
	}

	r := s.serve(ctx, ln, addr, handler)
	if r == nil {
		// Shutdown was called while the server was starting.
		return s.hookErr
	}

	var served error

	select {
	case <-ctx.Done():
	case <-r.stopping:
	case served = <-r.served:
		select {
		case <-r.stopping:
			// Serve returned because the stop sequence closed the listener.
			served = nil
		default:
			// Serving failed on its own; the server still stops in order.
		}
	}

	drainCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.ShutdownTimeout)
	defer cancel()

	s.stop(drainCtx)
	<-s.stopped

	return errors.Join(served, s.hookErr)
}

// begin moves a new server to starting and returns the configurers to run.
func (s *Server) begin() ([]RouteConfigurer[*Group], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.state.startable(); err != nil {
		return nil, err
	}

	s.state = stateStarting

	return s.configurers, nil
}

// fail ends a Start that failed before serving: nothing was served, so
// only the AfterDrain hooks run, and err is returned with their failures.
func (s *Server) fail(ctx context.Context, err error) error {
	s.logEvent(ctx, slog.LevelError, eventFailed, s.exceptionMessage(errorText(err)))
	s.closeUnserved(ctx, s.settings.listener)

	return errors.Join(err, s.hookErr)
}

// closeUnserved closes a server that served nothing: it closes ln, the
// listener the server owns (nil when there is none), and runs only the
// AfterDrain hooks.
func (s *Server) closeUnserved(ctx context.Context, ln net.Listener) {
	if ln != nil {
		_ = ln.Close()
	}

	s.finish(s.runAfterDrain(ctx), nil)
}

// runAfterDrain runs the AfterDrain hooks within their own budget.
func (s *Server) runAfterDrain(parent context.Context) error {
	ctx, cancel := s.afterDrainContext(parent)
	defer cancel()

	return s.runHooks(ctx, AfterDrain)
}

// finish closes the server with the results of its stop sequence.
func (s *Server) finish(hookErr, drainErr error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.state = stateClosed
	s.hookErr, s.drainErr = hookErr, drainErr
	close(s.stopped)
}

// configure runs the configurers and returns the handler serving their
// routes.
func (s *Server) configure(configurers []RouteConfigurer[*Group]) (http.Handler, error) {
	b := &builder{wrap: s.wrapHandler}
	g := &Group{b: b}

	for _, c := range configurers {
		b.owner = fmt.Sprintf("%T", c)
		if err := safeConfigure(c, g); err != nil {
			return nil, err
		}
	}

	if len(b.errs) > 0 {
		return nil, errors.Join(b.errs...)
	}

	mux := http.NewServeMux()
	if err := installRoutes(mux, b.routes); err != nil {
		return nil, err
	}

	return s.pipeline(mux), nil
}

// pipeline wraps the router in the per-request stages, outermost first:
// trace context, access log, body limit, panic recovery, unmatched-route
// answers, router.
func (s *Server) pipeline(mux *http.ServeMux) http.Handler {
	return s.startTrace(s.logAccess(limitBody(s.cfg.MaxRequestBodyBytes, s.problems.Recover(s.answerUnmatched(mux)))))
}

// listen returns the configured listener or binds the configured address.
func (s *Server) listen(ctx context.Context) (net.Listener, error) {
	if s.settings.listener != nil {
		return s.settings.listener, nil
	}

	return new(net.ListenConfig).Listen(ctx, "tcp", s.cfg.Addr)
}

// listenerAddr returns the text of ln's address. A nil or typed-nil
// address, or a panic in Addr or String, is an error: ln is consumer code,
// and its address is read without trusting it.
func listenerAddr(ln net.Listener) (addr string, err error) {
	defer func() {
		if v := recover(); v != nil {
			addr, err = "", errors.New("reading its address panicked: "+valueText(v))
		}
	}()

	switch a := ln.Addr(); {
	case a == nil:
		return "", errors.New("its address is nil")
	case isNil(a):
		return "", errors.New("its address is a nil " + typeName(a))
	default:
		return a.String(), nil
	}
}

// run is the serving part of a running server.
type run struct {
	hs *http.Server
	ln net.Listener
	// served receives the result of http.Server.Serve.
	served chan error
	// stopping is closed when the stop sequence begins.
	stopping chan struct{}
}

// serve moves the server to running and serves on ln, whose address is
// addr. It returns nil, after closing ln and the server, when Shutdown was
// called while starting. It calls no consumer code under s.mu.
func (s *Server) serve(ctx context.Context, ln net.Listener, addr string, handler http.Handler) *run {
	ln = &onceCloseListener{Listener: ln}

	s.mu.Lock()

	if s.state == stateStopping {
		// Shutdown was called while the server was starting.
		s.mu.Unlock()
		s.closeUnserved(ctx, ln)

		return nil
	}

	r := &run{
		hs:       s.cfg.httpServer(handler),
		ln:       ln,
		served:   make(chan error, 1),
		stopping: make(chan struct{}),
	}
	s.run = r
	s.state = stateRunning
	s.addr = addr
	s.mu.Unlock()

	s.logEvent(ctx, slog.LevelInfo, eventStarted, slog.String(keyServerAddress, s.addr))
	close(s.ready)

	go func() { r.served <- r.hs.Serve(ln) }()

	return r
}

// stop runs the stop sequence of a running server, unless it has already
// begun; a caller that finds it begun can wait on s.stopped.
func (s *Server) stop(drainCtx context.Context) {
	s.mu.Lock()
	if s.state != stateRunning {
		s.mu.Unlock()

		return
	}

	s.state = stateStopping
	r := s.run
	s.mu.Unlock()

	s.stopRun(drainCtx, r)
}

// stopRun is the stop sequence of a running server: stop accepting
// connections, BeforeDrain hooks, drain, AfterDrain hooks.
func (s *Server) stopRun(drainCtx context.Context, r *run) {
	close(r.stopping)
	_ = r.ln.Close()
	s.logEvent(drainCtx, slog.LevelInfo, eventStopping)

	begin := time.Now()
	beforeErr := s.runHooks(drainCtx, BeforeDrain)
	drainErr := drain(drainCtx, r.hs)
	s.logStopped(drainCtx, time.Since(begin), drainErr)
	afterErr := s.runAfterDrain(drainCtx)

	s.finish(errors.Join(beforeErr, afterErr), drainErr)
}

// drain stops accepting connections and waits for in-flight requests until
// ctx is done, then closes the remaining connections.
func drain(ctx context.Context, hs *http.Server) error {
	err := hs.Shutdown(ctx)
	if err != nil {
		_ = hs.Close()
	}

	return err
}

// Shutdown stops the server gracefully: it stops accepting connections and
// waits for in-flight requests until ctx is done, then closes the remaining
// connections and returns the context error.
//
// Shutdown is idempotent: on a server that was never started it closes the
// server, so a later Start returns [ErrServerClosed]; while another call is
// stopping the server it waits for that stop and returns nil; once stopped
// it returns nil. A nil ctx returns an error wrapping [ErrConfig] and
// leaves the server running.
func (s *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return configError("Shutdown context", "<nil>", "is nil")
	}

	s.mu.Lock()
	st := s.state

	switch st {
	case stateNew:
		// Stopping, so a concurrent Start or Register is refused while the
		// AfterDrain hooks run.
		s.state = stateStopping
		s.mu.Unlock()
		s.closeUnserved(ctx, s.settings.listener)

		return s.hookErr
	case stateStarting:
		// Start sees the stopping state before it serves and closes the
		// server; this call reports the result.
		s.state = stateStopping
		s.mu.Unlock()

		return s.wait(ctx, true)
	case stateRunning:
		// Claimed under the lock that read the state, so no other stop can
		// begin in between.
		s.state = stateStopping
		r := s.run
		s.mu.Unlock()

		drainCtx, cancel := context.WithTimeout(ctx, s.cfg.ShutdownTimeout)
		defer cancel()

		s.stopRun(drainCtx, r)

		return s.result()
	default:
		s.mu.Unlock()

		return s.wait(ctx, false)
	}
}

// wait waits until the server is closed or ctx is done. It returns the
// stop result when first, nil otherwise.
func (s *Server) wait(ctx context.Context, first bool) error {
	select {
	case <-s.stopped:
		if first {
			return s.result()
		}

		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// onceCloseListener makes Close idempotent, so that srvr can close the
// listener before draining and http.Server.Shutdown can close it again
// without reporting an error. The Once runs the consumer's Close under its
// own lock, which guards no srvr state: a second caller waits only for the
// same Close it would have called itself.
type onceCloseListener struct {
	net.Listener

	once sync.Once
	err  error
}

func (l *onceCloseListener) Close() error {
	l.once.Do(func() { l.err = l.Listener.Close() })

	return l.err
}

// result is what the Shutdown call that stopped the server returns: the
// hook failures and the drain error. Read it only after s.stopped closed.
func (s *Server) result() error { return errors.Join(s.hookErr, s.drainErr) }

// logStopped writes server.stopped: INFO, or WARN with srvr.drain.forced
// when the drain deadline passed and connections were closed.
func (s *Server) logStopped(ctx context.Context, d time.Duration, drainErr error) {
	attrs := []slog.Attr{slog.Float64(keyDrainDuration, millis(d))}
	level := slog.LevelInfo

	if drainErr != nil {
		level = slog.LevelWarn
		attrs = append(attrs, slog.Bool(keyDrainForced, true))
	}

	s.logEvent(ctx, level, eventStopped, attrs...)
}

// wrapHandler renders the errors h returns as problems, after guarding
// prblm's resolution against cyclic error chains.
func (s *Server) wrapHandler(h func(http.ResponseWriter, *http.Request) error) http.Handler {
	return s.problems.Wrap(func(w http.ResponseWriter, r *http.Request) error { return guardChain(h(w, r)) })
}
