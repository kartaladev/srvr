package otelsrvr_test

import (
	"strings"
	"testing"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

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
