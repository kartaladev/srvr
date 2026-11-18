package otelsrvr_test

import (
	"errors"
	"math"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/semconv/v1.43.0/httpconv"
)

var semconvBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10}

// bounds returns the bucket boundaries of the duration histogram recorded
// for one request through an observer built with opts.
func bounds(t *testing.T, opts ...otelsrvr.Option) []float64 {
	t.Helper()

	base, reader := metered(t, usersRoute, opts...)
	sendRaw(t, "GET", base+"/users/1")

	return histogram(t, collect(t, reader), durationName)[0].Bounds
}

// TestBuckets covers otel-metrics "Bucket boundaries", all rows.
func TestBuckets(t *testing.T) {
	t.Parallel()

	valid := []struct {
		name string
		opts []otelsrvr.Option
		want []float64
	}{
		{"default", nil, semconvBuckets},
		{"option", []otelsrvr.Option{otelsrvr.WithDurationBuckets(0.1, 1, 10)}, []float64{0.1, 1, 10}},
		{"config", []otelsrvr.Option{otelsrvr.WithConfig(otelsrvr.Config{DurationBuckets: []float64{0.1, 1, 10}})}, []float64{0.1, 1, 10}},
	}

	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := bounds(t, tt.opts...); !slices.Equal(got, tt.want) {
				t.Errorf("bounds = %v, want %v", got, tt.want)
			}
		})
	}

	invalid := map[string]otelsrvr.Option{
		"decreasing":        otelsrvr.WithDurationBuckets(1, 0.1),
		"empty":             otelsrvr.WithDurationBuckets(),
		"negative":          otelsrvr.WithDurationBuckets(-1, 1),
		"equal":             otelsrvr.WithDurationBuckets(1, 1),
		"NaN":               otelsrvr.WithDurationBuckets(0.1, math.NaN()),
		"+Inf":              otelsrvr.WithDurationBuckets(0.1, math.Inf(1)),
		"config decreasing": otelsrvr.WithConfig(otelsrvr.Config{DurationBuckets: []float64{1, 0.1}}),
	}

	for name, opt := range invalid {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := otelsrvr.New(opt)
			if !errors.Is(err, otelsrvr.ErrConfig) || !strings.Contains(err.Error(), "DurationBuckets") {
				t.Fatalf("New = %v, want an error wrapping ErrConfig naming DurationBuckets", err)
			}
		})
	}
}

// TestConfigSources covers otel-metrics "Configuration sources", all rows.
func TestConfigSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    []otelsrvr.Option
		buckets []float64
		tracing bool
	}{
		{"none", nil, semconvBuckets, false},
		{"option after config", []otelsrvr.Option{
			otelsrvr.WithConfig(otelsrvr.Config{DurationBuckets: []float64{0.1, 1}}), otelsrvr.WithDurationBuckets(5, 10),
		}, []float64{5, 10}, false},
		{"config after option", []otelsrvr.Option{
			otelsrvr.WithDurationBuckets(5, 10), otelsrvr.WithConfig(otelsrvr.Config{}),
		}, semconvBuckets, false},
		{"config tracing", []otelsrvr.Option{otelsrvr.WithConfig(otelsrvr.Config{Tracing: true})}, semconvBuckets, true},
		{"config resets tracing", []otelsrvr.Option{
			otelsrvr.WithTracing(), otelsrvr.WithConfig(otelsrvr.Config{BodySizes: true}),
		}, semconvBuckets, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs, err := otelsrvr.New(tt.opts...)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			cfg := otelsrvr.EffectiveConfig(obs)
			if !slices.Equal(cfg.DurationBuckets, tt.buckets) || cfg.Tracing != tt.tracing {
				t.Errorf("effective buckets %v, tracing %v; want %v, %v", cfg.DurationBuckets, cfg.Tracing, tt.buckets, tt.tracing)
			}
		})
	}
}

// echoRoute registers POST /items answering a 12-byte body.
var echoRoute = configFunc(func(g *srvr.Group) {
	g.POST("/items", func(w http.ResponseWriter, _ *http.Request) error {
		_, _ = w.Write([]byte("twelve bytes"))

		return nil
	})
})

// names returns the sorted names of the collected metrics.
func names(rm metricdata.ResourceMetrics) []string {
	var out []string

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out = append(out, m.Name)
		}
	}

	slices.Sort(out)

	return out
}

// activeRequests returns the sum of http.server.active_requests.
func activeRequests(t *testing.T, rm metricdata.ResourceMetrics) int64 {
	t.Helper()

	m, _, ok := find(rm, "http.server.active_requests")
	if !ok {
		t.Fatal("no http.server.active_requests")
	}

	var sum int64
	for _, p := range m.Data.(metricdata.Sum[int64]).DataPoints {
		sum += p.Value
	}

	return sum
}

// sizeSum returns the sum of the int64 histogram name.
func sizeSum(t *testing.T, rm metricdata.ResourceMetrics, name string) int64 {
	t.Helper()

	m, _, ok := find(rm, name)
	if !ok {
		t.Fatalf("no %s", name)
	}

	var sum int64
	for _, p := range m.Data.(metricdata.Histogram[int64]).DataPoints {
		sum += p.Sum
	}

	return sum
}

// TestOptional covers otel-metrics "Optional instruments", all rows.
func TestOptional(t *testing.T) {
	t.Parallel()

	const (
		duration = "http.server.request.duration"
		active   = "http.server.active_requests"
		reqSize  = "http.server.request.body.size"
		resSize  = "http.server.response.body.size"
	)

	tests := []struct {
		name string
		opts []otelsrvr.Option
		want []string
	}{
		{"none", nil, []string{duration}},
		{"active requests", []otelsrvr.Option{otelsrvr.WithActiveRequests()}, []string{active, duration}},
		{"body sizes", []otelsrvr.Option{otelsrvr.WithBodySizes()}, []string{reqSize, duration, resSize}},
		{"config", []otelsrvr.Option{otelsrvr.WithConfig(otelsrvr.Config{ActiveRequests: true, BodySizes: true})}, []string{active, reqSize, duration, resSize}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, reader := metered(t, echoRoute, tt.opts...)
			if got := send(t, http.MethodPost, base+"/items", "hello"); got != http.StatusOK {
				t.Fatalf("status = %d", got)
			}

			rm := collect(t, reader)
			if got := names(rm); !slices.Equal(got, tt.want) {
				t.Fatalf("instruments = %v, want %v", got, tt.want)
			}

			if slices.Contains(tt.want, active) {
				if got := activeRequests(t, rm); got != 0 {
					t.Errorf("active requests after completion = %d, want 0", got)
				}
			}

			if slices.Contains(tt.want, reqSize) {
				if got := sizeSum(t, rm, reqSize); got != 5 {
					t.Errorf("request body size sum = %d, want 5", got)
				}

				if got := sizeSum(t, rm, resSize); got != 12 {
					t.Errorf("response body size sum = %d, want 12", got)
				}
			}
		})
	}
}

// TestActiveRequests covers otel-metrics "Active requests under
// concurrency", all rows.
func TestActiveRequests(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		panics bool
	}{{"writes", false}, {"panics", true}} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			const n = 50

			release := make(chan struct{})
			started := make(chan struct{}, n)

			base, reader := metered(t, configFunc(func(g *srvr.Group) {
				g.GET("/hold", func(w http.ResponseWriter, _ *http.Request) error {
					started <- struct{}{}
					<-release

					if tt.panics {
						panic("boom")
					}

					w.WriteHeader(http.StatusOK)

					return nil
				})
			}), otelsrvr.WithActiveRequests())

			var wg sync.WaitGroup
			for range n {
				wg.Go(func() { sendRaw(t, http.MethodGet, base+"/hold") })
			}

			for range n {
				select {
				case <-started:
				case <-time.After(5 * time.Second):
					t.Fatal("requests did not start")
				}
			}

			if got := activeRequests(t, collect(t, reader)); got != n {
				t.Errorf("active requests during = %d, want %d", got, n)
			}

			close(release)
			wg.Wait()

			if got := activeRequests(t, collect(t, reader)); got != 0 {
				t.Errorf("active requests after = %d, want 0", got)
			}
		})
	}
}

// TestNoSDKDependency guards the module's dependency set: the non-test
// packages import the OpenTelemetry API only, never the SDK or an
// exporter. go.opentelemetry.io/auto/sdk is imported by the API's own
// global package and is not the SDK.
func TestNoSDKDependency(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}

	for pkg := range strings.Lines(string(out)) {
		pkg = strings.TrimSpace(pkg)
		if strings.HasPrefix(pkg, "go.opentelemetry.io/otel/sdk") || strings.HasPrefix(pkg, "go.opentelemetry.io/otel/exporters") {
			t.Errorf("otelsrvr imports %s", pkg)
		}
	}
}

// TestMetricSchemaGolden pins the names, units and attribute keys of every
// instrument against semantic conventions v1.43 (httpconv), so a semconv
// upgrade that renames one fails here.
func TestMetricSchemaGolden(t *testing.T) {
	t.Parallel()

	failed := []string{"error.type", "http.request.method", "http.response.status_code", "http.route", "url.scheme"}
	want := map[string]struct {
		unit string
		keys []string
	}{
		httpconv.ServerRequestDuration{}.Name():  {httpconv.ServerRequestDuration{}.Unit(), failed},
		httpconv.ServerActiveRequests{}.Name():   {httpconv.ServerActiveRequests{}.Unit(), []string{"http.request.method", "url.scheme"}},
		httpconv.ServerRequestBodySize{}.Name():  {httpconv.ServerRequestBodySize{}.Unit(), failed},
		httpconv.ServerResponseBodySize{}.Name(): {httpconv.ServerResponseBodySize{}.Unit(), failed},
	}

	base, reader := metered(t, configFunc(func(g *srvr.Group) {
		g.POST("/users", func(http.ResponseWriter, *http.Request) error { return errors.New("boom") })
	}), otelsrvr.WithActiveRequests(), otelsrvr.WithBodySizes())
	sendRaw(t, http.MethodPost, base+"/users")

	rm := collect(t, reader)
	if got := names(rm); len(got) != len(want) {
		t.Fatalf("instruments = %v", got)
	}

	for name, w := range want {
		m, scope, ok := find(rm, name)
		if !ok {
			t.Errorf("no %s", name)

			continue
		}

		if m.Unit != w.unit || scope.Name != otelsrvr.ScopeName {
			t.Errorf("%s unit %q scope %q, want %q, %q", name, m.Unit, scope.Name, w.unit, otelsrvr.ScopeName)
		}

		if got := attributeKeys(t, m); !slices.Equal(got, w.keys) {
			t.Errorf("%s attribute keys = %v, want %v", name, got, w.keys)
		}
	}
}

// TestNew_NilOption covers otel-metrics / Scenario: Nil option.
func TestNew_NilOption(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []otelsrvr.Option
		want []string // texts the error names; nil: no error
	}{
		{"nil", []otelsrvr.Option{nil}, []string{"Option 0"}},
		{"WithTracing, nil", []otelsrvr.Option{otelsrvr.WithTracing(), nil}, []string{"Option 1"}},
		{"nil, empty WithDurationBuckets", []otelsrvr.Option{nil, otelsrvr.WithDurationBuckets()}, []string{"Option 0", "DurationBuckets"}},
		{"none", nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			o, err := otelsrvr.New(tt.opts...)

			if tt.want == nil {
				if err != nil || o == nil {
					t.Fatalf("New = %v, %v; want an observer and nil", o, err)
				}

				return
			}
			if !errors.Is(err, otelsrvr.ErrConfig) {
				t.Fatalf("New = %v, want an error wrapping ErrConfig", err)
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("New = %v, want it to name %q", err, w)
				}
			}
		})
	}
}
