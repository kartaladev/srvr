package otelsetup_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsetup"
	"github.com/kartaladev/srvr/otelsrvr"
	"github.com/prometheus/client_golang/prometheus"
)

// configFunc adapts a function to srvr.RouteConfigurer.
type configFunc func(g *srvr.Group)

func (f configFunc) Configure(g *srvr.Group) { f(g) }

// app starts a net/http srvr server recording through s, serving GET
// /users/{id} and what conf adds, and returns its base URL.
func app(t *testing.T, s *otelsetup.Setup, conf configFunc) string {
	t.Helper()

	obs, err := otelsrvr.New(otelsrvr.WithMeterProvider(s.MeterProvider()))
	if err != nil {
		t.Fatal(err)
	}

	srv := hooked(t, s, []srvr.Option{srvr.WithObserver(obs)}, configFunc(func(g *srvr.Group) {
		g.GET("/users/{id}", func(w http.ResponseWriter, _ *http.Request) error { w.WriteHeader(http.StatusOK); return nil })

		if conf != nil {
			conf(g)
		}
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()

	t.Cleanup(func() {
		http.DefaultClient.CloseIdleConnections()
		cancel()

		if err := <-done; err != nil {
			t.Errorf("Start = %v", err)
		}
	})

	select {
	case <-srv.Ready():
	case err := <-done:
		t.Fatalf("Start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("not ready")
	}

	return "http://" + srv.Addr()
}

// TestScrape covers telemetry-setup "Scraping", all rows except the
// default-address one (TestScrapeDefaultAddr).
func TestScrape(t *testing.T) {
	t.Parallel()

	noListen := false

	tests := []struct {
		name     string
		prom     otelsetup.PrometheusConfig
		mount    bool
		path     string
		onApp    bool
		status   int
		contains string
	}{
		{"ephemeral port", local, false, "/metrics", false, 200, "http_server_request_duration_seconds_bucket"},
		{"custom path", otelsetup.PrometheusConfig{Enabled: true, Addr: "127.0.0.1:0", Path: "/internal/metrics"}, false, "/internal/metrics", false, 200, "http_server_request_duration_seconds_count"},
		{"not on the application listener", local, false, "/metrics", true, 404, `"status":404`},
		{"mounted by the consumer", otelsetup.PrometheusConfig{Enabled: true, Listen: &noListen}, true, "/ops/metrics", true, 200, "http_server_request_duration_seconds_count"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := setUp(t, otelsetup.WithPrometheusConfig(tt.prom))

			var mount configFunc
			if tt.mount {
				mount = func(g *srvr.Group) { g.Group("/ops").Mount("/metrics", s.MetricsHandler()) }
			}

			base := app(t, s, mount)
			if status, _ := fetch(t, base+"/users/42"); status != 200 {
				t.Fatalf("request status = %d", status)
			}

			target := "http://" + s.MetricsAddr() + tt.path
			if tt.onApp {
				target = base + tt.path
			}

			status, body := fetch(t, target)
			if status != tt.status || !strings.Contains(body, tt.contains) {
				t.Errorf("scrape %s: status %d, body lacks %q:\n%s", target, status, tt.contains, body)
			}

			if tt.mount && s.MetricsAddr() != "" {
				t.Errorf("MetricsAddr = %q with Listen false", s.MetricsAddr())
			}
		})
	}
}

// TestScrapeDefaultAddr covers the default-address Scraping row: it binds
// 127.0.0.1:9464, so it is not parallel.
func TestScrapeDefaultAddr(t *testing.T) {
	s := setUp(t, otelsetup.WithPrometheus())
	base := app(t, s, nil)
	fetch(t, base+"/users/42")

	_, body := fetch(t, "http://127.0.0.1:9464/metrics")
	if !strings.Contains(body, "http_server_request_duration_seconds_count{") || !strings.Contains(body, `http_route="/users/{id}"`) {
		t.Errorf("default scrape lacks the route's duration:\n%s", body)
	}
}

// TestRegistryIsolation covers telemetry-setup "Registry isolation", all
// rows.
func TestRegistryIsolation(t *testing.T) {
	t.Parallel()

	t.Run("two setups", func(t *testing.T) {
		t.Parallel()

		a, b := setUp(t, otelsetup.WithPrometheusConfig(local)), setUp(t, otelsetup.WithPrometheusConfig(local))
		fetch(t, app(t, a, nil)+"/users/1")

		_, fromA := scrape(t, a)
		_, fromB := scrape(t, b)

		if !strings.Contains(fromA, "http_server_request_duration") || strings.Contains(fromB, "http_server_request_duration") {
			t.Errorf("setup b serves setup a's metrics:\n%s", fromB)
		}
	})

	t.Run("default registry unchanged", func(t *testing.T) {
		t.Parallel()

		before, err := prometheus.DefaultGatherer.Gather()
		if err != nil {
			t.Fatal(err)
		}

		s := setUp(t, otelsetup.WithPrometheusConfig(local))
		fetch(t, app(t, s, nil)+"/users/1")

		after, err := prometheus.DefaultGatherer.Gather()
		if err != nil {
			t.Fatal(err)
		}

		if len(after) != len(before) {
			t.Errorf("default registry families %d -> %d", len(before), len(after))
		}
	})

	t.Run("address in use", func(t *testing.T) {
		t.Parallel()

		taken, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer taken.Close()

		s, err := otelsetup.New(context.Background(), otelsetup.WithPrometheusConfig(otelsetup.PrometheusConfig{Enabled: true, Addr: taken.Addr().String()}))

		var opErr *net.OpError
		if !errors.As(err, &opErr) || s != nil {
			t.Fatalf("New = %v, %v; want an error matching *net.OpError", s, err)
		}
	})
}
