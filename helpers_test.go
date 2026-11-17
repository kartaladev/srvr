package srvr_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
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
		// Idle client connections, including ones the transport dialed
		// speculatively and never used, would hold the drain for up to 5s.
		http.DefaultClient.CloseIdleConnections()
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

	res, err := http.DefaultClient.Do(req)
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

	select {
	case v := <-ch:
		return v
	case <-time.After(waitLimit):
		t.Fatalf("after %s: %s", waitLimit, msg)

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
