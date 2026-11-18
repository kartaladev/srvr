package otelsetup_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsetup"
)

// TestNew covers telemetry-setup "Provider construction", the Prometheus
// rows and the rejected rows that do not involve OTLP.
func TestNew(t *testing.T) {
	t.Parallel()

	t.Run("Prometheus only", func(t *testing.T) {
		t.Parallel()

		s := setUp(t, otelsetup.WithConfig(otelsetup.Config{Prometheus: local}))
		if s.MeterProvider() == nil || s.TracerProvider() != nil {
			t.Fatalf("meter provider %v, tracer provider %v; want a meter provider only", s.MeterProvider(), s.TracerProvider())
		}

		if status, _ := scrape(t, s); status != 200 {
			t.Errorf("scrape status = %d", status)
		}
	})

	t.Run("service resource", func(t *testing.T) {
		t.Parallel()

		s := setUp(t, otelsetup.WithConfig(otelsetup.Config{
			Service:    srvr.ServiceConfig{Name: "registration-api", Version: "1.4.2", Environment: "staging"},
			Prometheus: local,
		}))

		_, body := scrape(t, s)
		for _, want := range []string{`service_name="registration-api"`, `service_version="1.4.2"`, `deployment_environment_name="staging"`} {
			if !strings.Contains(body, want) {
				t.Errorf("target_info lacks %s:\n%s", want, body)
			}
		}
	})

	rejected := []struct {
		name  string
		cfg   otelsetup.Config
		field string
	}{
		{"empty", otelsetup.Config{}, "no exporter"},
		{"bad Addr", otelsetup.Config{Prometheus: otelsetup.PrometheusConfig{Enabled: true, Addr: "nope"}}, "Addr"},
		{"relative Path", otelsetup.Config{Prometheus: otelsetup.PrometheusConfig{Enabled: true, Path: "metrics"}}, "Path"},
		{"negative FlushTimeout", otelsetup.Config{FlushTimeout: -time.Second, Prometheus: local}, "FlushTimeout"},
		// Paths http.ServeMux rejects: they must fail before a port is bound.
		{"path with an unclosed wildcard", otelsetup.Config{Prometheus: otelsetup.PrometheusConfig{Enabled: true, Addr: freeAddr(t), Path: "/metrics/{"}}, "Prometheus.Path"},
		{"path with a space", otelsetup.Config{Prometheus: otelsetup.PrometheusConfig{Enabled: true, Addr: freeAddr(t), Path: "/metrics x"}}, "Prometheus.Path"},
	}

	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				s   *otelsetup.Setup
				err error
			)

			func() {
				defer func() {
					if v := recover(); v != nil {
						err = fmt.Errorf("New panicked: %v", v)
					}
				}()

				s, err = otelsetup.New(context.Background(), otelsetup.WithConfig(tt.cfg))
			}()

			// A failed New leaves no port bound.
			if addr := tt.cfg.Prometheus.Addr; strings.HasPrefix(addr, "127.0.0.1:") {
				ln, lerr := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
				if lerr != nil {
					t.Errorf("%s is still bound after New failed: %v", addr, lerr)
				} else {
					_ = ln.Close()
				}
			}

			if !errors.Is(err, otelsetup.ErrConfig) || !strings.Contains(err.Error(), tt.field) || s != nil {
				t.Fatalf("New = %v, %v; want an error wrapping ErrConfig mentioning %q", s, err, tt.field)
			}
		})
	}
}
