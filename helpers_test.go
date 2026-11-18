package srvr_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// configFunc adapts a function to srvr.RouteConfigurer for tests.
type configFunc func(g *srvr.Group)

func (f configFunc) Configure(g *srvr.Group) { f(g) }

// newServer constructs a server on an ephemeral loopback port and fails the
// test on a construction error.
func newServer(t *testing.T, opts ...srvr.Option) *srvr.Server {
	t.Helper()

	srv, err := srvr.New(append([]srvr.Option{srvr.WithAddr("127.0.0.1:0")}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return srv
}

// waitReady waits until srv is ready, failing the test when Start
// returns first or readiness takes too long.
func waitReady(t *testing.T, srv *srvr.Server, done <-chan error) {
	t.Helper()

	select {
	case <-srv.Ready():
	case err := <-done:
		t.Fatalf("Start returned before readiness: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not become ready")
	}
}

// startServer starts srv in the background, waits until it is ready, stops
// it when the test ends and returns its base URL.
func startServer(t *testing.T, srv *srvr.Server) string {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Start = %v at cleanup", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Start did not return at cleanup")
		}
	})

	waitReady(t, srv, done)

	registerClient(t, srv.Addr()) // its cleanup runs before the one above

	return "http://" + srv.Addr()
}

// response is the part of an HTTP response a test asserts.
type response struct {
	status int
	header http.Header
	body   string
	// problem is the parsed problem document, or nil.
	problem *prblm.Problem
}

// do sends a request and reads the whole response.
func do(t *testing.T, method, url string, body io.Reader, header map[string]string) response {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	for k, v := range header {
		req.Header.Set(k, v)
	}

	res, err := clientFor(url).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	out := response{status: res.StatusCode, header: res.Header, body: string(b)}

	res.Body = io.NopCloser(bytes.NewReader(b))
	if p, err := prblm.Parse(res); err == nil {
		out.problem = p
	}

	return out
}

func get(t *testing.T, url string) response {
	t.Helper()

	return do(t, http.MethodGet, url, nil, nil)
}

// waitRefused waits until a new connection to addr is refused.
func waitRefused(t *testing.T, addr string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := (&net.Dialer{Timeout: 100 * time.Millisecond}).DialContext(context.Background(), "tcp", addr)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(5 * time.Millisecond)
	}

	t.Fatalf("connections to %s were still accepted", addr)
}

// eventually waits up to a second for cond.
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()

	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if cond() {
			return
		}
	}

	t.Fatal(msg)
}

// workRoute registers GET /work whose handler signals started and then
// works for d (or until release is closed) before writing 200.
func workRoute(d time.Duration, started chan<- struct{}, release <-chan struct{}) configFunc {
	return func(g *srvr.Group) {
		g.GET("/work", func(w http.ResponseWriter, _ *http.Request) error {
			started <- struct{}{}
			select {
			case <-time.After(d):
			case <-release:
			}
			w.WriteHeader(http.StatusOK)

			return nil
		})
	}
}

// startBackground starts srv with a background context and returns the
// channel Start's result arrives on.
func startBackground(t *testing.T, srv *srvr.Server) <-chan error {
	t.Helper()

	return startWith(context.Background(), t, srv)
}

// startWith starts srv with ctx, waits until it is ready and returns the
// channel Start's result arrives on.
func startWith(ctx context.Context, t *testing.T, srv *srvr.Server) <-chan error {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- recoverPanic(func() error { return srv.Start(ctx) }) }()
	waitReady(t, srv, done)

	return done
}

func mustNil(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// listen opens a loopback listener on an ephemeral port, closed when the
// test ends.
func listen(t *testing.T) net.Listener {
	t.Helper()

	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	return ln
}

// on returns a configurer registering GET path under a group with prefix
// (the root group when prefix is empty).
func on(prefix, path string, h srvr.HandlerFunc) configFunc {
	return func(g *srvr.Group) {
		if prefix != "" {
			g = g.Group(prefix)
		}
		g.GET(path, h)
	}
}

func status(code int) srvr.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(code)

		return nil
	}
}

// okRoute registers GET path answering 200.
func okRoute(path string) configFunc { return on("", path, status(http.StatusOK)) }

// PanicAPI panics with "boom" in Configure; NilDerefAPI dereferences a nil
// pointer.
type (
	PanicAPI    struct{}
	NilDerefAPI struct{ db *struct{ name string } }
)

func (*PanicAPI) Configure(*srvr.Group) { panic("boom") }

func (c NilDerefAPI) Configure(g *srvr.Group) { g.GET("/"+c.db.name, status(http.StatusOK)) }

// late returns a handler that writes status and the body "partial", then
// fails with fail: an error or a panic after the response started.
func late(status int, fail func() error) srvr.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(status)
		_, _ = w.Write([]byte("partial"))

		return fail()
	}
}

// waitLimit bounds every wait in the tests, so a regression fails with a
// message instead of hanging until the test binary's timeout.
const waitLimit = 10 * time.Second

// receive returns the next value from ch, failing with msg after waitLimit.
func receive[T any](t *testing.T, ch <-chan T, msg string) T {
	t.Helper()

	return receiveWithin(t, ch, waitLimit, msg)
}

// receiveWithin returns the next value from ch, failing with msg after d.
func receiveWithin[T any](t *testing.T, ch <-chan T, d time.Duration, msg string) T {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(d):
		t.Fatalf("after %s: %s", d, msg)

		var zero T

		return zero
	}
}

// errPanicked marks an error recoverPanic made from a panic.
var errPanicked = errors.New("panicked")

// recoverPanic returns fn's result, or an error wrapping errPanicked when
// fn panics.
func recoverPanic(fn func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("%w: %s", errPanicked, safeText(v))
		}
	}()

	return fn()
}

// shutdownBounded calls srv.Shutdown, a panic returned as an error
// wrapping errPanicked, and fails the test when it does not return within
// waitLimit.
func shutdownBounded(t *testing.T, srv *srvr.Server) error {
	t.Helper()

	return bounded(t, "Shutdown did not return", func() error {
		return recoverPanic(func() error { return srv.Shutdown(context.Background()) })
	})
}

// startBounded calls srv.Start with ctx, a panic returned as an error
// wrapping errPanicked, and fails the test when it does not return within
// waitLimit.
func startBounded(ctx context.Context, t *testing.T, srv *srvr.Server) error {
	t.Helper()

	return bounded(t, "Start did not return", func() error {
		return recoverPanic(func() error { return srv.Start(ctx) })
	})
}

// bounded returns fn's result, failing the test when fn does not return
// within waitLimit.
func bounded[T any](t *testing.T, msg string, fn func() T) T {
	t.Helper()

	result := make(chan T, 1)
	go func() { result <- fn() }()

	return receive(t, result, msg)
}

// newRecovered is srvr.New with a panic returned as an error wrapping
// errPanicked.
func newRecovered(opts ...srvr.Option) (srv *srvr.Server, err error) {
	err = recoverPanic(func() (err error) {
		srv, err = srvr.New(opts...)

		return err
	})

	return srv, err
}

// clients holds the HTTP client of every server the helpers started, by
// address. Each server has its own, so a test's cleanup closes only its
// own idle connections: closing those of http.DefaultClient, shared by
// parallel tests, broke another test's request in flight ("http:
// CloseIdleConnections called").
var clients sync.Map // string -> *http.Client

// registerClient gives the server at addr a client of its own; when the
// test ends, before the server stops (cleanups run last-in first-out), it
// closes that client's idle connections, which would otherwise hold the
// drain for up to 5s.
func registerClient(t *testing.T, addr string) {
	t.Helper()

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConnsPerHost = 16 // parallel requests reuse connections instead of exhausting ports
	c := &http.Client{Transport: tr}
	clients.Store(addr, c)

	t.Cleanup(func() {
		c.CloseIdleConnections()
		clients.CompareAndDelete(addr, c)
	})
}

// clientFor returns the client of the server rawURL points at, or
// http.DefaultClient for a server the helpers did not start.
func clientFor(rawURL string) *http.Client {
	if u, err := url.Parse(rawURL); err == nil {
		if c, ok := clients.Load(u.Host); ok {
			return c.(*http.Client)
		}
	}

	return http.DefaultClient
}

// selfPanicError is an error whose Error method panics with itself, so fmt's
// own guard, which formats the panic value, panics again.
type selfPanicError struct{}

func (selfPanicError) Error() string { panic(selfPanicError{}) }

// safeText formats v for a test message, guarded against a value whose
// formatting panics at any depth.
func safeText(v any) (text string) {
	defer func() {
		if recover() != nil {
			text = fmt.Sprintf("<unprintable %T>", v)
		}
	}()

	return fmt.Sprint(v)
}

// addrListener is a listener whose Addr method is replaced by addr.
type addrListener struct {
	net.Listener

	addr func() net.Addr
}

func (l addrListener) Addr() net.Addr { return l.addr() }

// hostileAddr is a net.Addr whose String panics with selfPanicError.
type hostileAddr struct{}

func (hostileAddr) Network() string { return "tcp" }
func (hostileAddr) String() string  { panic(selfPanicError{}) }

// HostileAPI panics in Configure with a value whose Error panics with
// itself.
type HostileAPI struct{}

func (*HostileAPI) Configure(*srvr.Group) { panic(selfPanicError{}) }

// hostileContext is a context that is done after a delay and whose Err
// then returns err.
type hostileContext struct {
	context.Context

	done chan struct{}
	err  error
}

func newHostileContext(t *testing.T, after time.Duration, err error) hostileContext {
	t.Helper()

	c := hostileContext{Context: context.Background(), done: make(chan struct{}), err: err}
	timer := time.AfterFunc(after, func() { close(c.done) })
	t.Cleanup(func() { timer.Stop() })

	return c
}

func (c hostileContext) Done() <-chan struct{} { return c.done }

func (c hostileContext) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// TestHarness_CleanupKeepsOtherClients pins the test harness against the
// one-off TestPanicRecovery failure ("http: CloseIdleConnections called"):
// a started server's cleanup must not close connections a parallel test
// uses for its own server.
func TestHarness_CleanupKeepsOtherClients(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, base, otherBase string)
	}{
		{"an idle connection survives another server's cleanup", idleSurvives},
		{"requests in flight survive another server's cleanups", inFlightSurvive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			mustNil(t, srv.Register(okRoute("/ok")))
			base := startServer(t, srv)

			tt.run(t, base, startServer(t, newServer(t)))
		})
	}
}

// getOK sends GET base/ok with base's client and reports whether it
// reused an idle connection.
func getOK(base string) (reused bool, err error) {
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}

	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), http.MethodGet, base+"/ok", http.NoBody)
	if err != nil {
		return false, err
	}

	res, err := clientFor(base).Do(req)
	if err != nil {
		return false, err
	}

	_, _ = io.Copy(io.Discard, res.Body)

	return reused, res.Body.Close()
}

// idleSurvives: the connection left idle by one request is reused by the
// next, although another server's cleanup ran in between.
func idleSurvives(t *testing.T, base, otherBase string) {
	t.Helper()

	if _, err := getOK(base); err != nil {
		t.Fatal(err)
	}

	clientFor(otherBase).CloseIdleConnections() // what its cleanup does

	reused, err := getOK(base)
	if err != nil {
		t.Fatal(err)
	}

	if !reused {
		t.Error("another server's cleanup closed this server's idle connection")
	}
}

// inFlightSurvive: requests sent while another server's cleanup runs in a
// loop never fail (the scratch reproduction; probabilistic before the fix,
// where about 1 run in 10 failed).
func inFlightSurvive(t *testing.T, base, otherBase string) {
	t.Helper()

	if testing.Short() {
		t.Skip("8000 requests; idleSurvives is the deterministic guard")
	}

	var stop atomic.Bool

	done := make(chan struct{})

	go func() {
		defer close(done)

		for !stop.Load() {
			clientFor(otherBase).CloseIdleConnections()
			runtime.Gosched() // leave the parallel tests their share
		}
	}()

	defer func() { stop.Store(true); <-done }()

	const workers, requests = 8, 1000

	errs := make(chan error, workers)

	for range workers {
		go func() {
			for range requests {
				if _, err := getOK(base); err != nil {
					errs <- err

					return
				}
			}
			errs <- nil
		}()
	}

	for range workers {
		if err := <-errs; err != nil {
			t.Fatalf("GET /ok: %v", err)
		}
	}
}
