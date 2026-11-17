package srvr_test

import (
	"bytes"
	"context"
	"io"
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
