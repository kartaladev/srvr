package otelsrvr_test

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// An observer recording through an SDK MeterProvider with a manual
// reader, as a test would; an application uses an exporting reader.
func Example() {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	obs, err := otelsrvr.New(otelsrvr.WithMeterProvider(mp))
	if err != nil {
		panic(err)
	}

	srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithObserver(obs), srvr.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}

	_ = srv.Register(configFunc(func(g *srvr.Group) {
		g.GET("/users/{id}", func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(http.StatusOK)

			return nil
		})
	}))

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()
	<-srv.Ready()

	res, err := http.Get("http://" + srv.Addr() + "/users/42")
	if err != nil {
		panic(err)
	}
	_ = res.Body.Close()

	http.DefaultClient.CloseIdleConnections()
	stop()

	if err := <-done; err != nil {
		panic(err)
	}

	var rm metricdata.ResourceMetrics
	_ = reader.Collect(context.Background(), &rm)

	m := rm.ScopeMetrics[0].Metrics[0]
	point := m.Data.(metricdata.Histogram[float64]).DataPoints[0]
	route, _ := point.Attributes.Value("http.route")

	fmt.Println(m.Name, m.Unit, point.Count, route.AsString())
	// Output: http.server.request.duration s 1 /users/{id}
}

// Tracing with an SDK TracerProvider: the server span continues the
// incoming traceparent and supplies srvr's trace_id.
func Example_tracing() {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))

	obs, err := otelsrvr.New(otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp))
	if err != nil {
		panic(err)
	}

	srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithObserver(obs), srvr.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}

	_ = srv.Register(configFunc(func(g *srvr.Group) {
		g.GET("/users/{id}", func(w http.ResponseWriter, r *http.Request) error {
			tr, _ := srvr.TraceFromContext(r.Context())
			fmt.Println("handler trace:", tr.TraceID)
			w.WriteHeader(http.StatusOK)

			return nil
		})
	}))

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()
	<-srv.Ready()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+srv.Addr()+"/users/42", http.NoBody)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	_ = res.Body.Close()

	http.DefaultClient.CloseIdleConnections()
	stop()

	if err := <-done; err != nil {
		panic(err)
	}

	span := rec.Ended()[0]
	fmt.Println("span:", span.Name(), span.SpanContext().TraceID())
	// Output:
	// handler trace: 4bf92f3577b34da6a3ce929d0e0e4736
	// span: GET /users/{id} 4bf92f3577b34da6a3ce929d0e0e4736
}
