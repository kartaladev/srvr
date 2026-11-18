package otelsetup_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/srvr/otelsetup"
	"go.opentelemetry.io/otel"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// local is a Prometheus configuration on an ephemeral loopback port.
var local = otelsetup.PrometheusConfig{Enabled: true, Addr: "127.0.0.1:0"}

// setUp builds a setup and shuts it down when the test ends.
func setUp(t *testing.T, opts ...otelsetup.Option) *otelsetup.Setup {
	t.Helper()

	s := newSetup(t, opts...)
	t.Cleanup(func() { shutdown(t, s) })

	if addr := s.MetricsAddr(); addr != "" {
		registerClient(t, addr) // its cleanup runs before Shutdown
	}

	return s
}

// newSetup builds a setup the test shuts down itself.
func newSetup(t *testing.T, opts ...otelsetup.Option) *otelsetup.Setup {
	t.Helper()

	s, err := otelsetup.New(context.Background(), opts...) //nolint:usetesting // the setup is shut down in a cleanup, which runs after t.Context() is cancelled
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return s
}

// shutdown shuts s down within 5s and fails t on an error.
func shutdown(t *testing.T, s *otelsetup.Setup) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) //nolint:usetesting // called from cleanups, which run after t.Context() is cancelled; it needs a fresh 5s deadline
	defer cancel()

	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

// fetch GETs url and returns the status and body.
func fetch(t *testing.T, url string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	res, err := clientFor(url).Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	return res.StatusCode, string(b)
}

// scrape fetches s's metrics from its own listener.
func scrape(t *testing.T, s *otelsetup.Setup) (int, string) {
	t.Helper()

	return fetch(t, "http://"+s.MetricsAddr()+"/metrics")
}

// resetGlobals resets the OpenTelemetry globals to no-op ones when the
// test ends. The originals cannot be restored: the global getters return
// delegates that refuse to be set as their own targets.
func resetGlobals(t *testing.T) {
	t.Helper()

	t.Cleanup(func() {
		otel.SetMeterProvider(metricnoop.NewMeterProvider())
		otel.SetTracerProvider(tracenoop.NewTracerProvider())
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
		otel.SetErrorHandler(otel.ErrorHandlerFunc(func(error) {}))
	})
}

// freeAddr returns a loopback address whose port was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	defer ln.Close()

	return ln.Addr().String()
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

	dt, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport is %T, want *http.Transport", http.DefaultTransport)
	}

	tr := dt.Clone()
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
			if hc, ok := c.(*http.Client); ok {
				return hc
			}
		}
	}

	return http.DefaultClient
}
