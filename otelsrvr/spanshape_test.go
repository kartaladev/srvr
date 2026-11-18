package otelsrvr_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

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
