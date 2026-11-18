package otelsetup_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	collmetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	colltrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

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

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
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

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := ln.Addr().String()
	_ = ln.Close()

	return "http://" + addr
}
