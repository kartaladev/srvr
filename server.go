package srvr

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/kartaladev/prblm"
)

const (
	// defaultShutdownTimeout bounds the drain when Start's context ends.
	defaultShutdownTimeout = 15 * time.Second
	// defaultReadHeaderTimeout protects against slow-header (Slowloris) clients.
	defaultReadHeaderTimeout = 10 * time.Second
)

// Option configures a Server.
type Option func(*settings)

// settings is the configuration options build.
type settings struct {
	addr string
}

// WithAddr sets the TCP address the server listens on. The default is
// ":8080", the conventional container port.
func WithAddr(addr string) Option {
	return func(s *settings) { s.addr = addr }
}

// Server is an HTTP server on the net/http engine.
//
// Create it with [New], add routes with [Server.Register] and run it with
// [Server.Start], which blocks until its context ends or [Server.Shutdown]
// is called. Every error a handler returns is rendered as an RFC 9457
// problem by github.com/kartaladev/prblm; an unrecognised error becomes a
// 500 problem that carries none of the error's text.
//
// Defaults: listens on ":8080", ReadHeaderTimeout 10s (Slowloris
// protection) and a shutdown drain of 15s, which fits a 30s Kubernetes
// termination grace period.
type Server struct {
	settings settings
	// problems renders every error response.
	problems *prblm.Handler

	mu          sync.Mutex
	configurers []RouteConfigurer[*Group]
	ready       chan struct{}
	addr        string

	// stopReq carries the context of a Shutdown call to Start.
	stopReq chan context.Context
	// stopped is closed once Start has drained; drainErr is its result.
	stopped  chan struct{}
	drainErr error
}

var _ RouteRegistrar[*Group] = (*Server)(nil)

// New returns a server configured by opts. It does not open a listener.
func New(opts ...Option) (*Server, error) {
	s := settings{addr: ":8080"}
	for _, opt := range opts {
		opt(&s)
	}

	return &Server{
		settings: s,
		problems: prblm.NewHandler(),
		ready:    make(chan struct{}),
		stopReq:  make(chan context.Context, 1),
		stopped:  make(chan struct{}),
	}, nil
}

// Register adds conf to the configurers run when the server starts. Each
// registration runs Configure once, in registration order, during Start.
func (s *Server) Register(conf RouteConfigurer[*Group]) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.configurers = append(s.configurers, conf)

	return nil
}

// Ready returns a channel closed once the listener is bound and the server
// accepts connections.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Addr returns the bound address once the server is ready, for example the
// port chosen for "127.0.0.1:0", and the empty string before.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.addr
}

// Start configures routes, listens and serves until ctx is done or Shutdown
// is called, then drains in-flight requests and returns.
func (s *Server) Start(ctx context.Context) error {
	var routes []route
	g := &Group{routes: &routes}

	for _, c := range s.configurers {
		c.Configure(g)
	}

	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.Handle(rt.method+" "+rt.pattern, s.problems.Wrap(rt.handler))
	}

	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", s.settings.addr)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.addr = ln.Addr().String()
	s.mu.Unlock()
	close(s.ready)

	hs := &http.Server{Handler: mux, ReadHeaderTimeout: defaultReadHeaderTimeout}
	errc := make(chan error, 1)

	go func() { errc <- hs.Serve(ln) }()

	//nolint:contextcheck // a Shutdown call bounds the drain with its own context, not Start's.
	var drainCtx context.Context

	select {
	case <-ctx.Done():
		c, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultShutdownTimeout)
		defer cancel()

		drainCtx = c
	case c := <-s.stopReq:
		drainCtx = c
	case err := <-errc:
		return err
	}

	defer close(s.stopped)

	s.drainErr = drain(drainCtx, hs)
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
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
// waits for in-flight requests until ctx is done.
func (s *Server) Shutdown(ctx context.Context) error {
	select {
	case s.stopReq <- ctx:
	default:
	}

	select {
	case <-s.stopped:
		return s.drainErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
