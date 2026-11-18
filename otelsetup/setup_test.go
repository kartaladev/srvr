package otelsetup_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsetup"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	collmetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	colltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
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

				s, err = otelsetup.New(t.Context(), otelsetup.WithConfig(tt.cfg))
			}()

			// A failed New leaves no port bound.
			if addr := tt.cfg.Prometheus.Addr; strings.HasPrefix(addr, "127.0.0.1:") {
				ln, lerr := (&net.ListenConfig{}).Listen(t.Context(), "tcp", addr)
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

// fakeCollector receives OTLP exports over http/protobuf or gRPC and keeps
// the raw payloads per signal. hang makes it block until the test ends.
type fakeCollector struct {
	mu       sync.Mutex
	payloads map[string][][]byte // "metrics" or "traces"
	hang     chan struct{}
	url      string
	apiKeys  []string // X-Api-Key of every http export
}

func (c *fakeCollector) add(signal string, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.payloads[signal] = append(c.payloads[signal], b)
}

// received returns the payloads of signal joined.
func (c *fakeCollector) received(signal string) []byte {
	c.mu.Lock()
	defer c.mu.Unlock()

	return bytes.Join(c.payloads[signal], nil)
}

func (c *fakeCollector) wait(ctx context.Context) {
	if c.hang != nil {
		select {
		case <-c.hang:
		case <-ctx.Done():
		}
	}
}

// newHTTPCollector starts an http/protobuf collector; its url is the
// endpoint URL ("http://127.0.0.1:port").
func newHTTPCollector(t *testing.T, hang bool) *fakeCollector {
	t.Helper()

	c := &fakeCollector{payloads: map[string][][]byte{}}
	if hang {
		c.hang = make(chan struct{})
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.wait(r.Context())

		c.mu.Lock()
		c.apiKeys = append(c.apiKeys, r.Header.Get("X-Api-Key"))
		c.mu.Unlock()

		switch r.URL.Path {
		case "/v1/metrics":
			c.add("metrics", b)
		case "/v1/traces":
			c.add("traces", b)
		}

		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))

	t.Cleanup(func() {
		if c.hang != nil {
			close(c.hang)
		}

		srv.Close()
	})

	c.url = srv.URL

	return c
}

type grpcMetrics struct {
	collmetric.UnimplementedMetricsServiceServer

	c *fakeCollector
}

func (g grpcMetrics) Export(_ context.Context, req *collmetric.ExportMetricsServiceRequest) (*collmetric.ExportMetricsServiceResponse, error) {
	b, _ := proto.Marshal(req)
	g.c.add("metrics", b)

	return &collmetric.ExportMetricsServiceResponse{}, nil
}

type grpcTraces struct {
	colltrace.UnimplementedTraceServiceServer

	c *fakeCollector
}

func (g grpcTraces) Export(_ context.Context, req *colltrace.ExportTraceServiceRequest) (*colltrace.ExportTraceServiceResponse, error) {
	b, _ := proto.Marshal(req)
	g.c.add("traces", b)

	return &colltrace.ExportTraceServiceResponse{}, nil
}

// newGRPCCollector starts a gRPC collector; its url is
// "http://127.0.0.1:port" (plaintext).
func newGRPCCollector(t *testing.T) *fakeCollector {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	c := &fakeCollector{payloads: map[string][][]byte{}, url: "http://" + ln.Addr().String()}
	srv := grpc.NewServer()
	collmetric.RegisterMetricsServiceServer(srv, grpcMetrics{c: c})
	colltrace.RegisterTraceServiceServer(srv, grpcTraces{c: c})

	go func() { _ = srv.Serve(ln) }()

	t.Cleanup(srv.Stop)

	return c
}

// unreachable returns an endpoint URL nothing listens on.
func unreachable(t *testing.T) string {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := ln.Addr().String()
	_ = ln.Close()

	return "http://" + addr
}

// TestShutdownOrdering covers telemetry-setup "Ordering with the server",
// all rows: the in-flight request's duration reaches the final export
// because the setup flushes after the drain.
func TestShutdownOrdering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		consumer bool // the consumer calls setup.Shutdown after the server stopped
		viaCtx   bool // the Start context is cancelled instead of calling Shutdown
	}{
		{"Start context cancelled", false, true},
		{"Shutdown", false, false},
		{"Shutdown then the consumer's setup.Shutdown", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			collector := newHTTPCollector(t, false)

			s, err := otelsetup.New(t.Context(), otelsetup.WithOTLP(otelsetup.OTLPConfig{Endpoint: collector.url, Metrics: true}))
			if err != nil {
				t.Fatal(err)
			}

			obs, err := otelsrvr.New(otelsrvr.WithMeterProvider(s.MeterProvider()))
			if err != nil {
				t.Fatal(err)
			}

			started, release := make(chan struct{}), make(chan struct{})

			// draining is closed by a BeforeDrain hook: the listener is
			// closed and the drain is about to wait for the request.
			draining := make(chan struct{})
			beforeDrain := srvr.WithShutdownHook(srvr.BeforeDrain, func(context.Context) error {
				close(draining)

				return nil
			})

			srv := hooked(t, s, []srvr.Option{srvr.WithObserver(obs), beforeDrain}, configFunc(func(g *srvr.Group) {
				g.GET("/slow", func(w http.ResponseWriter, _ *http.Request) error {
					close(started)
					<-release
					w.WriteHeader(http.StatusOK)

					return nil
				})
			}))

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			done := make(chan error, 1)

			go func() { done <- srv.Start(ctx) }()
			<-srv.Ready()

			answered := make(chan struct{})

			go func() {
				defer close(answered)

				req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+srv.Addr()+"/slow", http.NoBody)
				if res, err := (&http.Client{Transport: &http.Transport{}}).Do(req); err == nil {
					_ = res.Body.Close()
				}
			}()

			<-started

			stopped := make(chan error, 1)
			if tt.viaCtx {
				cancel()
			} else {
				go func() { stopped <- srv.Shutdown(t.Context()) }()
			}

			drained := false
			select { // the stop is under way, the request still in flight
			case <-draining:
				drained = true
			case <-time.After(5 * time.Second):
			}
			close(release) // also on failure, so the drain does not wait on the handler

			if !drained {
				t.Fatal("the stop did not reach the drain")
			}
			<-answered

			if err := <-done; err != nil {
				t.Fatalf("Start = %v", err)
			}

			if !tt.viaCtx {
				if err := <-stopped; err != nil {
					t.Fatalf("Shutdown = %v", err)
				}
			}

			got := collector.received("metrics")
			if !bytes.Contains(got, []byte("http.server.request.duration")) || !bytes.Contains(got, []byte("/slow")) {
				t.Error("the final export lacks the in-flight request's duration")
			}

			if tt.consumer {
				if err := s.Shutdown(t.Context()); err != nil {
					t.Errorf("the consumer's Shutdown = %v, want nil", err)
				}
			}
		})
	}
}

// TestConfigPrecedence covers telemetry-setup "Configuration precedence",
// the service.name and flush-timeout rows. The environment rows use
// t.Setenv, so the test is not parallel.
//
//nolint:paralleltest // uses t.Setenv and runs a per-test goleak check
func TestConfigPrecedence(t *testing.T) {
	checkGoroutines(t)

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
	checkGoroutines(t)

	for _, explicit := range []bool{true, false} {
		t.Run(map[bool]string{true: "explicit Endpoint wins", false: "environment fills an empty Endpoint"}[explicit], func(t *testing.T) {
			fromEnv, fromConfig := newHTTPCollector(t, false), newHTTPCollector(t, false)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", fromEnv.url)

			otlp := otelsetup.OTLPConfig{Protocol: "http/protobuf", Metrics: true}
			want, other := fromEnv, fromConfig

			if explicit {
				otlp.Endpoint, want, other = fromConfig.url, fromConfig, fromEnv
			}

			s, err := otelsetup.New(t.Context(), otelsetup.WithConfig(otelsetup.Config{OTLP: otlp}))
			if err != nil {
				t.Fatal(err)
			}

			emit(t, s)

			if err := s.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}

			if len(want.received("metrics")) == 0 || len(other.received("metrics")) != 0 {
				t.Errorf("export went to the wrong collector")
			}
		})
	}
}

// TestSetGlobal covers telemetry-setup "Globals", all rows, plus a failed
// New that leaves the globals unchanged. It replaces the globals, so it is
// not parallel and resets them.
//
//nolint:paralleltest // sets the OpenTelemetry globals and runs a per-test goleak check
func TestSetGlobal(t *testing.T) {
	checkGoroutines(t)

	resetGlobals(t)

	injects := func() bool {
		sc := trace.NewSpanContext(trace.SpanContextConfig{
			TraceID: trace.TraceID{1}, SpanID: trace.SpanID{1}, TraceFlags: trace.FlagsSampled,
		})
		carrier := propagation.MapCarrier{}
		otel.GetTextMapPropagator().Inject(trace.ContextWithSpanContext(t.Context(), sc), carrier)

		return carrier.Get("traceparent") != ""
	}

	t.Run("defaults", func(t *testing.T) {
		before := otel.GetMeterProvider()
		setUp(t, otelsetup.WithPrometheusConfig(local))

		if otel.GetMeterProvider() != before || injects() {
			t.Error("globals changed without SetGlobal")
		}
	})

	t.Run("failed New", func(t *testing.T) {
		before := otel.GetMeterProvider()

		_, err := otelsetup.New(t.Context(), otelsetup.WithSetGlobal(), otelsetup.WithPrometheusConfig(otelsetup.PrometheusConfig{Enabled: true, Addr: "127.0.0.1:1"}))
		if err == nil {
			t.Skip("port 1 was bindable")
		}

		if otel.GetMeterProvider() != before || injects() {
			t.Error("a failed New changed the globals")
		}
	})

	t.Run("SetGlobal", func(t *testing.T) {
		s := setUp(t, otelsetup.WithSetGlobal(), otelsetup.WithPrometheusConfig(local))

		if otel.GetMeterProvider() != s.MeterProvider() || !injects() {
			t.Error("SetGlobal did not install the meter provider and a TraceContext propagator")
		}
	})
}

// TestNew_NilOption covers telemetry-setup / Scenario: Nil option. The
// Prometheus rows use a free loopback address instead of WithPrometheus's
// fixed default port, so tests never bind a fixed port.
func TestNew_NilOption(t *testing.T) {
	t.Parallel()

	prometheus := func(addr string) otelsetup.Option {
		return otelsetup.WithPrometheusConfig(otelsetup.PrometheusConfig{Enabled: true, Addr: addr})
	}

	tests := []struct {
		name  string
		opts  func(addr string) []otelsetup.Option
		want  string // text the error names; "": no error
		bound bool
	}{
		{"Prometheus, nil", func(addr string) []otelsetup.Option { return []otelsetup.Option{prometheus(addr), nil} }, "Option 1", false},
		{"nil", func(string) []otelsetup.Option { return []otelsetup.Option{nil} }, "Option 0", false},
		{"Prometheus", func(addr string) []otelsetup.Option { return []otelsetup.Option{prometheus(addr)} }, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			addr := freeAddr(t)
			s, err := otelsetup.New(context.Background(), tt.opts(addr)...) //nolint:usetesting // the setup is shut down in a cleanup, which runs after t.Context() is cancelled
			if s != nil {
				t.Cleanup(func() { shutdown(t, s) })
			}

			if tt.want == "" {
				if err != nil || s == nil {
					t.Fatalf("New = %v, %v; want a setup and nil", s, err)
				}
			} else if !errors.Is(err, otelsetup.ErrConfig) || !strings.Contains(err.Error(), tt.want) || s != nil {
				t.Fatalf("New = %v, %v; want an error wrapping ErrConfig naming %q", s, err, tt.want)
			}

			if bound := listening(addr); bound != tt.bound {
				t.Fatalf("metrics address bound = %v, want %v", bound, tt.bound)
			}
		})
	}
}
