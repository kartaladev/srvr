package otelsrvr_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

// recorder returns a tracer provider recording ended spans synchronously.
func recorder() (*sdktrace.TracerProvider, *tracetest.SpanRecorder) {
	rec := tracetest.NewSpanRecorder()

	return sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)), rec
}

// signalRecorder is a span recorder that also signals each ended span on
// ended, dropping the signal when one is already pending.
type signalRecorder struct {
	*tracetest.SpanRecorder

	ended chan struct{}
}

func (r signalRecorder) OnEnd(s sdktrace.ReadOnlySpan) {
	r.SpanRecorder.OnEnd(s)

	select {
	case r.ended <- struct{}{}:
	default:
	}
}

// signalling returns a tracer provider recording into a signalRecorder.
func signalling() (*sdktrace.TracerProvider, signalRecorder) {
	rec := signalRecorder{SpanRecorder: tracetest.NewSpanRecorder(), ended: make(chan struct{}, 1)}

	return sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec)), rec
}

// traced starts a net/http server with otelsrvr built from opts and conf,
// and returns its base URL.
func traced(t *testing.T, conf configFunc, srvOpts []srvr.Option, opts ...otelsrvr.Option) string {
	t.Helper()

	obs, err := otelsrvr.New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return serve(t, conf, append(srvOpts, srvr.WithObserver(obs))...)
}

// TestTracingSwitch covers otel-tracing "Tracing switch", all rows. The
// global-provider row replaces the global TracerProvider, so the test is
// not parallel and restores it.
//
//nolint:paralleltest // sets the global TracerProvider and runs a per-test goleak check
func TestTracingSwitch(t *testing.T) {
	checkGoroutines(t)

	saved := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(saved) })

	tests := []struct {
		name   string
		global bool
		opts   func(tp *sdktrace.TracerProvider) []otelsrvr.Option
		spans  int
	}{
		{"off", false, func(tp *sdktrace.TracerProvider) []otelsrvr.Option {
			return []otelsrvr.Option{otelsrvr.WithTracerProvider(tp)}
		}, 0},
		{"WithTracing", false, func(tp *sdktrace.TracerProvider) []otelsrvr.Option {
			return []otelsrvr.Option{otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp)}
		}, 1},
		{"Config.Tracing with the global provider", true, func(*sdktrace.TracerProvider) []otelsrvr.Option {
			return []otelsrvr.Option{otelsrvr.WithConfig(otelsrvr.Config{Tracing: true})}
		}, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tp, rec := recorder()
			if tt.global {
				otel.SetTracerProvider(tp)
			}

			base := traced(t, configFunc(func(g *srvr.Group) {
				g.GET("/ping", func(w http.ResponseWriter, _ *http.Request) error { w.WriteHeader(http.StatusOK); return nil })
			}), nil, tt.opts(tp)...)
			send(t, http.MethodGet, base+"/ping", "")

			if got := len(rec.Ended()); got != tt.spans {
				t.Errorf("spans = %d, want %d", got, tt.spans)
			}
		})
	}

	for name, tp := range map[string]trace.TracerProvider{"nil": nil, "typed-nil": (*sdktrace.TracerProvider)(nil)} {
		t.Run(name+" provider", func(t *testing.T) {
			if _, err := otelsrvr.New(otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp)); !errors.Is(err, otelsrvr.ErrConfig) {
				t.Fatalf("New = %v, want an error wrapping ErrConfig", err)
			}
		})
	}
}

// panickingTracerProvider panics in Tracer.
type panickingTracerProvider struct{ tracenoop.TracerProvider }

func (panickingTracerProvider) Tracer(string, ...trace.TracerOption) trace.Tracer {
	panic("Tracer panicked")
}

// TestTracerProviderPanics checks that a provider panicking in Tracer
// fails New (design D11).
func TestTracerProviderPanics(t *testing.T) {
	t.Parallel()

	if _, err := otelsrvr.New(otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(panickingTracerProvider{})); !errors.Is(err, otelsrvr.ErrConfig) {
		t.Fatalf("New = %v, want an error wrapping ErrConfig", err)
	}
}

// usersBehaviour registers GET /users/{id} running h.
func usersBehaviour(h srvr.HandlerFunc) configFunc {
	return func(g *srvr.Group) { g.GET("/users/{id}", h) }
}

// onlySpan returns the only ended span.
func onlySpan(t *testing.T, rec *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()

	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(spans))
	}

	return spans[0]
}

// spanAttr returns the value of key on span, and false when absent.
func spanAttr(span sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, kv := range span.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.String(), true
		}
	}

	return "", false
}

// TestSpanShape covers otel-tracing "Span shape per engine", net/http rows.
func TestSpanShape(t *testing.T) {
	t.Parallel()

	ok := func(w http.ResponseWriter, _ *http.Request) error { w.WriteHeader(http.StatusOK); return nil }

	tests := []struct {
		name      string
		method    string
		path      string
		handler   srvr.HandlerFunc
		spanName  string
		status    codes.Code
		exception bool
		noStatus  bool
	}{
		{"200", "GET", "/users/42", ok, "GET /users/{id}", codes.Unset, false, false},
		{"ErrNotFound", "GET", "/users/42", func(http.ResponseWriter, *http.Request) error { return prblm.ErrNotFound }, "GET /users/{id}", codes.Unset, false, false},
		{"boom", "GET", "/users/42", func(http.ResponseWriter, *http.Request) error { return errors.New("boom") }, "GET /users/{id}", codes.Error, false, false},
		{"panic", "GET", "/users/42", func(http.ResponseWriter, *http.Request) error { panic("boom") }, "GET /users/{id}", codes.Error, true, false},
		{"ErrAbortHandler", "GET", "/users/42", func(http.ResponseWriter, *http.Request) error { panic(http.ErrAbortHandler) }, "GET /users/{id}", codes.Error, true, true},
		{"unmatched", "GET", "/nope", ok, "GET", codes.Unset, false, false},
		{"unknown method", "XYZZY", "/users/42", ok, "HTTP", codes.Unset, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tp, rec := recorder()
			base := traced(t, usersBehaviour(tt.handler), nil, otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp))
			sendRaw(t, tt.method, base+tt.path)

			span := onlySpan(t, rec)
			if span.Name() != tt.spanName || span.Status().Code != tt.status || span.SpanKind() != trace.SpanKindServer {
				t.Errorf("span %q status %v kind %v, want %q %v SERVER", span.Name(), span.Status().Code, span.SpanKind(), tt.spanName, tt.status)
			}

			if got := len(span.Events()) > 0 && span.Events()[0].Name == "exception"; got != tt.exception {
				t.Errorf("exception event = %v, want %v (events %v)", got, tt.exception, span.Events())
			}

			if _, ok := spanAttr(span, "http.response.status_code"); ok == tt.noStatus {
				t.Errorf("http.response.status_code present = %v, want %v", ok, !tt.noStatus)
			}

			if span.Status().Description != "" {
				t.Errorf("status description %q leaks error text", span.Status().Description)
			}

			for _, e := range span.Events() {
				for _, kv := range e.Attributes {
					if kv.Key == "exception.message" {
						t.Errorf("exception.message %q leaks error text", kv.Value.String())
					}
				}
			}
		})
	}
}

// TestSpanPathAttributes covers otel-tracing "Span path attributes", all
// rows.
func TestSpanPathAttributes(t *testing.T) {
	t.Parallel()

	long := "/" + strings.Repeat("a", 4096)

	tests := []struct {
		name     string
		include  bool
		method   string
		path     string
		wantPath string // empty: absent
		original string // empty: absent
	}{
		{"off", false, "GET", "/users/42", "", ""},
		{"on", true, "GET", "/users/42", "/users/42", ""},
		{"off unknown method", false, "XYZZY", "/users/42", "", ""},
		{"on unknown method", true, "XYZZY", "/users/42", "/users/42", "XYZZY"},
		{"on oversized path", true, "GET", long, long[:1024] + "…[truncated]", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var srvOpts []srvr.Option
			if tt.include {
				srvOpts = append(srvOpts, srvr.WithAccessLogIncludePath())
			}

			tp, rec := recorder()
			base := traced(t, usersRoute, srvOpts, otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp))
			sendRaw(t, tt.method, base+tt.path)

			span := onlySpan(t, rec)

			if got, ok := spanAttr(span, "url.path"); got != tt.wantPath || ok != (tt.wantPath != "") {
				t.Errorf("url.path = %q (present %v), want %q", got, ok, tt.wantPath)
			}

			if got, ok := spanAttr(span, "http.request.method_original"); got != tt.original || ok != (tt.original != "") {
				t.Errorf("http.request.method_original = %q (present %v), want %q", got, ok, tt.original)
			}
		})
	}
}

// TestSpanPathNotOnMetrics checks that url.path never reaches a metric.
func TestSpanPathNotOnMetrics(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	tp, _ := recorder()
	base := traced(t, usersRoute, []srvr.Option{srvr.WithAccessLogIncludePath()},
		otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp), otelsrvr.WithMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))))
	sendRaw(t, "XYZZY", base+"/users/42")

	for _, p := range histogram(t, collect(t, reader), durationName) {
		for _, key := range []string{"url.path", "http.request.method_original"} {
			if _, ok := attr(p.Attributes, key); ok {
				t.Errorf("metric carries %s", key)
			}
		}
	}
}

// TestSpanEnding covers otel-tracing "Span ending", all rows.
func TestSpanEnding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		handler  srvr.HandlerFunc
		cancel   bool // the client gives up after the handler started
		shutdown bool // Shutdown with a short deadline while it runs
	}{
		{"writes 200", func(w http.ResponseWriter, _ *http.Request) error { w.WriteHeader(http.StatusOK); return nil }, false, false},
		{"panics", func(http.ResponseWriter, *http.Request) error { panic("boom") }, false, false},
		{"panics with ErrAbortHandler", func(http.ResponseWriter, *http.Request) error { panic(http.ErrAbortHandler) }, false, false},
		{"blocks until the client disconnects", blockUntilDone, true, false},
		{"blocks until Shutdown forces the connection closed", blockUntilDone, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tp, rec := signalling()

			obs, err := otelsrvr.New(otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp))
			if err != nil {
				t.Fatal(err)
			}

			srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard), srvr.WithObserver(obs), srvr.WithShutdownTimeout(50*time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}

			started := make(chan struct{}, 1)
			if err := srv.Register(configFunc(func(g *srvr.Group) {
				g.GET("/x", func(w http.ResponseWriter, r *http.Request) error {
					started <- struct{}{}

					return tt.handler(w, r)
				})
			})); err != nil {
				t.Fatal(err)
			}

			done := make(chan error, 1)
			go func() { done <- srv.Start(t.Context()) }()
			<-srv.Ready()

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv.Addr()+"/x", http.NoBody)
			client := &http.Client{Transport: &http.Transport{}}
			sent := make(chan struct{})

			go func() {
				defer close(sent)

				if res, err := client.Do(req); err == nil {
					_ = res.Body.Close()
				}
			}()

			<-started

			switch {
			case tt.cancel:
				cancel()
			case tt.shutdown:
				_ = srv.Shutdown(t.Context())
			}

			<-sent

			select {
			case <-rec.ended:
			case <-time.After(5 * time.Second):
				t.Error("no span ended")
			}

			_ = srv.Shutdown(t.Context())

			if err := <-done; err != nil {
				t.Logf("Start = %v", err)
			}

			if got := len(rec.Ended()); got != 1 {
				t.Errorf("ended spans = %d, want 1", got)
			}
		})
	}
}

// blockUntilDone blocks until the request context ends.
func blockUntilDone(_ http.ResponseWriter, r *http.Request) error {
	<-r.Context().Done()

	return r.Context().Err()
}

const (
	incomingParent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	incomingTrace  = "4bf92f3577b34da6a3ce929d0e0e4736"
	incomingSpan   = "00f067aa0ba902b7"
)

// extractPanics is a propagator whose Extract panics.
type extractPanics struct{ propagation.TraceContext }

func (extractPanics) Extract(context.Context, propagation.TextMapCarrier) context.Context {
	panic("Extract panicked")
}

// fieldsPanics is a propagator whose Fields panics.
type fieldsPanics struct{ propagation.TraceContext }

func (fieldsPanics) Fields() []string { panic("Fields panicked") }

// TestPropagation covers otel-tracing "Parent extraction", all rows, plus
// faulty propagators (design D11). Rows using the global propagator
// replace it, so the test is not parallel and restores it.
//
//nolint:paralleltest // sets the global TextMapPropagator and runs a per-test goleak check
func TestPropagation(t *testing.T) {
	checkGoroutines(t)

	saved := otel.GetTextMapPropagator()
	t.Cleanup(func() { otel.SetTextMapPropagator(saved) })

	tests := []struct {
		name        string
		propagator  propagation.TextMapPropagator // nil: the global one
		global      propagation.TextMapPropagator
		srvOpts     []srvr.Option
		traceparent string
		continued   bool
	}{
		{"W3C valid", propagation.TraceContext{}, nil, nil, incomingParent, true},
		{"W3C garbage", propagation.TraceContext{}, nil, nil, "garbage", false},
		{"W3C none", propagation.TraceContext{}, nil, nil, "", false},
		{"no-op global falls back to W3C", nil, propagation.NewCompositeTextMapPropagator(), nil, incomingParent, true},
		{"Baggage-only global", nil, propagation.Baggage{}, nil, incomingParent, false},
		{"ignored by option", propagation.TraceContext{}, nil, []srvr.Option{srvr.WithIgnoreIncomingTraceContext()}, incomingParent, false},
		{"ignored by config", propagation.TraceContext{}, nil, []srvr.Option{srvr.WithTraceConfig(srvr.TraceConfig{IgnoreIncoming: true})}, incomingParent, false},
		{"Extract panics", extractPanics{}, nil, nil, incomingParent, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.global != nil {
				otel.SetTextMapPropagator(tt.global)
			}

			tp, rec := recorder()

			opts := []otelsrvr.Option{otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp)}
			if tt.propagator != nil {
				opts = append(opts, otelsrvr.WithPropagator(tt.propagator))
			}

			base := traced(t, usersRoute, tt.srvOpts, opts...)

			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/users/1", http.NoBody)
			if tt.traceparent != "" {
				req.Header.Set("traceparent", tt.traceparent)
			}

			res, err := clientFor(req.URL.String()).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()

			if res.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", res.StatusCode)
			}

			span := onlySpan(t, rec)
			sc, parent := span.SpanContext(), span.Parent()

			if tt.continued {
				if sc.TraceID().String() != incomingTrace || parent.SpanID().String() != incomingSpan || !parent.IsRemote() {
					t.Errorf("trace %s parent %s remote %v, want %s, %s remote", sc.TraceID(), parent.SpanID(), parent.IsRemote(), incomingTrace, incomingSpan)
				}

				return
			}

			if sc.TraceID().String() == incomingTrace || parent.IsValid() {
				t.Errorf("trace %s parent %v, want a new root", sc.TraceID(), parent.SpanID())
			}
		})
	}

	for name, p := range map[string]propagation.TextMapPropagator{
		"nil":           nil,
		"typed-nil":     (*propagation.TraceContext)(nil),
		"Fields panics": fieldsPanics{},
	} {
		t.Run(name+" propagator", func(t *testing.T) {
			if _, err := otelsrvr.New(otelsrvr.WithTracing(), otelsrvr.WithPropagator(p)); !errors.Is(err, otelsrvr.ErrConfig) {
				t.Fatalf("New = %v, want an error wrapping ErrConfig", err)
			}
		})
	}
}
