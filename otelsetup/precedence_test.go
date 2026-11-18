package otelsetup_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsetup"
)

// TestConfigPrecedence covers telemetry-setup "Configuration precedence",
// the service.name and flush-timeout rows. The environment rows use
// t.Setenv, so the test is not parallel.
func TestConfigPrecedence(t *testing.T) {
	t.Run("Config service.name beats OTEL_SERVICE_NAME", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "from-env")

		s := setUp(t, otelsetup.WithConfig(otelsetup.Config{Service: srvr.ServiceConfig{Name: "registration-api"}, Prometheus: local}))
		if _, body := scrape(t, s); !strings.Contains(body, `service_name="registration-api"`) {
			t.Errorf("service.name is not registration-api:\n%s", body)
		}
	})

	t.Run("OTEL_SERVICE_NAME fills an empty service.name", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "from-env")

		s := setUp(t, otelsetup.WithPrometheus(), otelsetup.WithPrometheusConfig(local))
		if _, body := scrape(t, s); !strings.Contains(body, `service_name="from-env"`) {
			t.Errorf("service.name is not from-env:\n%s", body)
		}
	})

	for _, tt := range []struct {
		name string
		opts []otelsetup.Option
		want time.Duration
	}{
		{"option after config", []otelsetup.Option{
			otelsetup.WithConfig(otelsetup.Config{FlushTimeout: 2 * time.Second, Prometheus: local}), otelsetup.WithFlushTimeout(time.Second),
		}, time.Second},
		{"config after option", []otelsetup.Option{
			otelsetup.WithFlushTimeout(time.Second), otelsetup.WithConfig(otelsetup.Config{Prometheus: local}),
		}, 5 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := otelsetup.FlushTimeout(setUp(t, tt.opts...)); got != tt.want {
				t.Errorf("flush timeout = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestConfigPrecedence_OTLP covers telemetry-setup "Configuration
// precedence", the OTLP endpoint rows, against fake collectors. It sets
// the environment, so it is not parallel.
func TestConfigPrecedence_OTLP(t *testing.T) {
	for _, explicit := range []bool{true, false} {
		t.Run(map[bool]string{true: "explicit Endpoint wins", false: "environment fills an empty Endpoint"}[explicit], func(t *testing.T) {
			fromEnv, fromConfig := newHTTPCollector(t, false), newHTTPCollector(t, false)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", fromEnv.url)

			otlp := otelsetup.OTLPConfig{Protocol: "http/protobuf", Metrics: true}
			want, other := fromEnv, fromConfig

			if explicit {
				otlp.Endpoint, want, other = fromConfig.url, fromConfig, fromEnv
			}

			s, err := otelsetup.New(context.Background(), otelsetup.WithConfig(otelsetup.Config{OTLP: otlp}))
			if err != nil {
				t.Fatal(err)
			}

			emit(t, s)

			if err := s.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}

			if len(want.received("metrics")) == 0 || len(other.received("metrics")) != 0 {
				t.Errorf("export went to the wrong collector")
			}
		})
	}
}
