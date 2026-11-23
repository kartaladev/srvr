package otelsetup_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsetup"
	"github.com/kartaladev/srvr/otelsrvr"
)

// Prometheus metrics for a srvr server, flushed when the server stops.
func Example() {
	ctx := context.Background()

	setup, err := otelsetup.New(ctx,
		// Addr ":9464" in Kubernetes; an ephemeral loopback port here.
		otelsetup.WithPrometheusConfig(otelsetup.PrometheusConfig{Enabled: true, Addr: "127.0.0.1:0"}),
		otelsetup.WithService(srvr.ServiceConfig{Name: "registration-api", Version: "1.4.2"}),
	)
	if err != nil {
		panic(err)
	}

	obs, err := otelsrvr.New(otelsrvr.WithMeterProvider(setup.MeterProvider()))
	if err != nil {
		panic(err)
	}

	srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithObserver(obs), setup.ShutdownHook(), srvr.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}

	err = srv.Register(configFunc(func(g *srvr.Group) {
		g.GET("/users/{id}", func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(http.StatusOK)

			return nil
		})
	}))
	if err != nil {
		panic(err)
	}

	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)

	go func() { done <- srv.Run(runCtx) }()
	<-srv.Ready()

	get := func(url string) string {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		defer res.Body.Close()

		b, _ := io.ReadAll(res.Body)

		return string(b)
	}

	get("http://" + srv.Addr() + "/users/42")

	for line := range strings.Lines(get("http://" + setup.MetricsAddr() + "/metrics")) {
		if strings.HasPrefix(line, "http_server_request_duration_seconds_count") {
			fmt.Println(strings.Contains(line, `http_route="/users/{id}"`), strings.HasSuffix(line, " 1\n"))
		}
	}

	http.DefaultClient.CloseIdleConnections()
	stop() // the shutdown hook flushes and closes the metrics listener

	if err := <-done; err != nil {
		panic(err)
	}
	// Output: true true
}

// OTLP export of metrics and traces, with the setup's providers installed
// as the OpenTelemetry globals. Not run: it needs a collector.
func Example_otlp() {
	ctx := context.Background()

	setup, err := otelsetup.New(ctx,
		otelsetup.WithOTLP(otelsetup.OTLPConfig{
			// Empty: OTEL_EXPORTER_OTLP_ENDPOINT, else localhost:4318.
			Endpoint: "http://otel-collector:4318",
			Metrics:  true,
			Traces:   true,
		}),
		otelsetup.WithService(srvr.ServiceConfig{Name: "registration-api"}),
		otelsetup.WithSetGlobal(),
	)
	if err != nil {
		panic(err)
	}

	obs, err := otelsrvr.New(otelsrvr.WithTracing()) // the globals, set above
	if err != nil {
		panic(err)
	}

	srv, err := srvr.New(srvr.WithObserver(obs), setup.ShutdownHook())
	if err != nil {
		panic(err)
	}

	_ = srv
}

// Prometheus metrics mounted on the application's own server instead of
// the setup's listener. Not run: it only shows the wiring.
func Example_disableListener() {
	ctx := context.Background()

	setup, err := otelsetup.New(ctx, otelsetup.WithPrometheusConfig(otelsetup.PrometheusConfig{
		Enabled:         true,
		DisableListener: true, // no listener of the setup's own
	}))
	if err != nil {
		panic(err)
	}

	srv, err := srvr.New(setup.ShutdownHook())
	if err != nil {
		panic(err)
	}

	err = srv.Register(configFunc(func(g *srvr.Group) {
		g.Group("/ops").Mount("/metrics", setup.MetricsHandler())
	}))
	if err != nil {
		panic(err)
	}
}
