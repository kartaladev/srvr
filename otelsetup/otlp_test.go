package otelsetup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/srvr/otelsetup"
)

// emit records one counter value and, when s traces, one span.
func emit(t *testing.T, s *otelsetup.Setup) {
	t.Helper()

	counter, err := s.MeterProvider().Meter("test").Int64Counter("otelsetup.test.counter")
	if err != nil {
		t.Fatal(err)
	}

	counter.Add(context.Background(), 1)

	if tp := s.TracerProvider(); tp != nil {
		_, span := tp.Tracer("test").Start(context.Background(), "otelsetup.test.span")
		span.End()
	}
}

// TestNew_OTLP covers telemetry-setup "Provider construction", the OTLP
// rows, against in-process collectors.
func TestNew_OTLP(t *testing.T) {
	t.Parallel()

	for _, protocol := range []string{"http/protobuf", "grpc"} {
		t.Run(protocol+" exports", func(t *testing.T) {
			t.Parallel()

			c := newHTTPCollector(t, false)
			if protocol == "grpc" {
				c = newGRPCCollector(t)
			}

			s, err := otelsetup.New(context.Background(), otelsetup.WithOTLP(otelsetup.OTLPConfig{
				Endpoint: c.url, Protocol: protocol, Metrics: true, Traces: true,
			}))
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			if s.TracerProvider() == nil {
				t.Fatal("no tracer provider")
			}

			emit(t, s)

			if err := s.Shutdown(context.Background()); err != nil {
				t.Fatalf("Shutdown: %v", err)
			}

			if !bytes.Contains(c.received("metrics"), []byte("otelsetup.test.counter")) {
				t.Error("the collector got no metric")
			}

			if !bytes.Contains(c.received("traces"), []byte("otelsetup.test.span")) {
				t.Error("the collector got no span")
			}
		})
	}

	t.Run("headers", func(t *testing.T) {
		t.Parallel()

		c := newHTTPCollector(t, false)

		s, err := otelsetup.New(context.Background(), otelsetup.WithOTLP(otelsetup.OTLPConfig{
			Endpoint: c.url, Metrics: true, Traces: true, Headers: map[string]string{"X-Api-Key": "k1"},
		}))
		if err != nil {
			t.Fatal(err)
		}

		emit(t, s)

		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}

		c.mu.Lock()
		defer c.mu.Unlock()

		if len(c.apiKeys) < 2 || c.apiKeys[0] != "k1" || c.apiKeys[1] != "k1" {
			t.Errorf("X-Api-Key of the exports = %v, want k1 on each", c.apiKeys)
		}
	})

	t.Run("grpc headers", func(t *testing.T) {
		t.Parallel()

		c := newGRPCCollector(t)

		s, err := otelsetup.New(context.Background(), otelsetup.WithOTLP(otelsetup.OTLPConfig{
			Endpoint: c.url, Protocol: "grpc", Metrics: true, Traces: true, Headers: map[string]string{"x-api-key": "k1"},
		}))
		if err != nil {
			t.Fatal(err)
		}

		emit(t, s)

		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	// Construction only: nothing listens at these endpoints, so a final
	// export at Shutdown may fail, which is fine here.
	for _, tt := range []struct {
		name string
		otlp otelsetup.OTLPConfig
	}{
		{"host:port endpoint", otelsetup.OTLPConfig{Endpoint: "collector:4318", Protocol: "http/protobuf", Metrics: true, Traces: true}},
		{"grpc host:port endpoint", otelsetup.OTLPConfig{Endpoint: "collector:4317", Protocol: "grpc", Metrics: true, Traces: true}},
		{"empty endpoint", otelsetup.OTLPConfig{Protocol: "http/protobuf", Traces: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, err := otelsetup.New(context.Background(), otelsetup.WithOTLP(tt.otlp))
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			defer func() { _ = s.Shutdown(ctx) }()

			if s.TracerProvider() == nil || s.MeterProvider() == nil {
				t.Fatal("want meter and tracer providers")
			}
		})
	}

	for _, tt := range []struct {
		name string
		otlp otelsetup.OTLPConfig
		want string
	}{
		{"unknown protocol", otelsetup.OTLPConfig{Endpoint: "http://collector:4318", Protocol: "thrift", Metrics: true}, "Protocol"},
		{"no signal", otelsetup.OTLPConfig{Endpoint: "http://collector:4318", Protocol: "http/protobuf"}, "Metrics"},
		// An unparsable URL must not be replaced by the SDK default, which
		// WithEndpointURL silently does.
		{"URL with a trailing space", otelsetup.OTLPConfig{Endpoint: "http://collector:4318 ", Metrics: true}, "OTLP.Endpoint"},
		{"unclosed IPv6 host", otelsetup.OTLPConfig{Endpoint: "http://[::1", Metrics: true}, "OTLP.Endpoint"},
		{"unsupported scheme", otelsetup.OTLPConfig{Endpoint: "ftp://collector:4318", Metrics: true}, "OTLP.Endpoint"},
		{"URL without a host", otelsetup.OTLPConfig{Endpoint: "http://", Metrics: true}, "OTLP.Endpoint"},
		{"host without a port", otelsetup.OTLPConfig{Endpoint: "collector", Metrics: true}, "OTLP.Endpoint"},
		{"port without a host", otelsetup.OTLPConfig{Endpoint: ":4318", Metrics: true}, "OTLP.Endpoint"},
		{"named port", otelsetup.OTLPConfig{Endpoint: "collector:otlp", Metrics: true}, "OTLP.Endpoint"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, err := otelsetup.New(context.Background(), otelsetup.WithOTLP(tt.otlp))
			if s != nil {
				t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
			}

			if !errors.Is(err, otelsetup.ErrConfig) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New = %v, want an error wrapping ErrConfig naming %s", err, tt.want)
			}
		})
	}
}

// TestShutdown_OTLP covers telemetry-setup "Flush outcomes", the OTLP rows.
func TestShutdown_OTLP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		endpoint func(t *testing.T) string
		deadline time.Duration
		check    func(err error) bool
		bound    time.Duration
	}{
		{"accepts", func(t *testing.T) string { return newHTTPCollector(t, false).url }, 10 * time.Second, func(err error) bool { return err == nil }, time.Second},
		{"unreachable", unreachable, 10 * time.Second, func(err error) bool {
			var opErr *net.OpError

			return errors.As(err, &opErr)
		}, 5*time.Second + 500*time.Millisecond},
		{"hangs", func(t *testing.T) string { return newHTTPCollector(t, true).url }, 200 * time.Millisecond, func(err error) bool {
			return errors.Is(err, context.DeadlineExceeded)
		}, 300 * time.Millisecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, err := otelsetup.New(context.Background(), otelsetup.WithOTLP(otelsetup.OTLPConfig{
				Endpoint: tt.endpoint(t), Protocol: "http/protobuf", Metrics: true, Traces: true,
			}))
			if err != nil {
				t.Fatal(err)
			}

			emit(t, s)

			ctx, cancel := context.WithTimeout(context.Background(), tt.deadline)
			defer cancel()

			start := time.Now()
			err = s.Shutdown(ctx)
			elapsed := time.Since(start)

			if !tt.check(err) || elapsed > tt.bound {
				t.Errorf("Shutdown = %v after %v, bound %v", err, elapsed, tt.bound)
			}
		})
	}
}

// logBuffer is a concurrency-safe log output.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

// records returns the JSON records with msg.
func (b *logBuffer) records(t *testing.T, msg string) []map[string]any {
	t.Helper()

	b.mu.Lock()
	defer b.mu.Unlock()

	var out []map[string]any

	for line := range strings.Lines(b.buf.String()) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}

		if m["msg"] == msg {
			out = append(out, m)
		}
	}

	return out
}

// TestExportFailure covers telemetry-setup "Collector down during
// traffic", all rows. It sets the environment and the globals, so it is
// not parallel and resets them.
func TestExportFailure(t *testing.T) {
	checkGoroutines(t)

	resetGlobals(t)

	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "20") // milliseconds

	for _, tt := range []struct {
		name      string
		setGlobal bool
		records   int
	}{
		{"SetGlobal", true, 1},
		{"no SetGlobal", false, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logs := &logBuffer{}
			opts := []otelsetup.Option{
				otelsetup.WithOTLP(otelsetup.OTLPConfig{Endpoint: unreachable(t), Metrics: true}),
				otelsetup.WithLogger(slog.New(slog.NewJSONHandler(logs, nil))),
			}

			if tt.setGlobal {
				opts = append(opts, otelsetup.WithSetGlobal())
			}

			s, err := otelsetup.New(context.Background(), opts...)
			if err != nil {
				t.Fatal(err)
			}

			base := app(t, s, nil)
			for range 100 {
				if status, _ := fetch(t, base+"/users/1"); status != 200 {
					t.Fatalf("status = %d", status)
				}
			}

			// Wait for the first record, or several export intervals when
			// none is expected; later failures must not add records.
			deadline := time.Now().Add(2 * time.Second)
			for tt.records > 0 && len(logs.records(t, "otel.export.failed")) == 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}

			time.Sleep(100 * time.Millisecond) // five more export intervals

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = s.Shutdown(ctx)

			recs := logs.records(t, "otel.export.failed")
			if len(recs) != tt.records {
				t.Fatalf("otel.export.failed records = %d, want %d", len(recs), tt.records)
			}

			if tt.records > 0 && (recs[0]["level"] != "WARN" || recs[0]["error.type"] == nil) {
				t.Errorf("record = %v", recs[0])
			}
		})
	}
}
