package otelsrvr_test

import (
	"errors"
	"fmt"
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

	data, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("http.server.active_requests data is %T, want metricdata.Sum[int64]", m.Data)
	}

	var sum int64
	for _, p := range data.DataPoints {
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

	data, ok := m.Data.(metricdata.Histogram[int64])
	if !ok {
		t.Fatalf("%s data is %T, want metricdata.Histogram[int64]", name, m.Data)
	}

	var sum int64
	for _, p := range data.DataPoints {
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
	t.Parallel()

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

// TestConfigError covers otel-metrics / Scenario: Typed configuration
// errors.
func TestConfigError(t *testing.T) {
	t.Parallel()

	errMeter := errors.New("meter refused")
	failing := func(instrument string) otelsrvr.Option {
		return otelsrvr.WithMeterProvider(meterProvider{meter: faultyMeter{fail: instrument, err: errMeter}})
	}

	errPanic := errors.New("provider panicked")
	errorPanics := &textPanicsError{}
	meterPanics := func(v any) otelsrvr.Option { return otelsrvr.WithMeterProvider(meterProvider{panicWith: v}) }
	tracerPanics := func(v any) []otelsrvr.Option {
		return []otelsrvr.Option{otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(panickingTracerProvider{v: v})}
	}
	propagatorPanics := func(v any) otelsrvr.Option { return otelsrvr.WithPropagator(fieldsPanics{v: v}) }

	tests := []struct {
		name   string
		opts   []otelsrvr.Option
		fields []string
		// cause reports whether the returned error reaches the expected
		// cause; nil: every *ConfigError has a nil Cause.
		cause func(error) bool
	}{
		{"nil", []otelsrvr.Option{nil}, []string{"Option"}, nil},
		{"WithMeterProvider(nil)", []otelsrvr.Option{otelsrvr.WithMeterProvider(nil)}, []string{"MeterProvider"}, nil},
		{"WithTracerProvider(nil)", []otelsrvr.Option{otelsrvr.WithTracerProvider(nil)}, []string{"TracerProvider"}, nil},
		{"WithPropagator(nil)", []otelsrvr.Option{otelsrvr.WithPropagator(nil)}, []string{"Propagator"}, nil},
		{"WithDurationBuckets()", []otelsrvr.Option{otelsrvr.WithDurationBuckets()}, []string{"DurationBuckets"}, nil},
		{"WithDurationBuckets(1, 0.5)", []otelsrvr.Option{otelsrvr.WithDurationBuckets(1, 0.5)}, []string{"DurationBuckets"}, nil},
		{
			"WithMeterProvider(nil) and WithTracerProvider(nil)",
			[]otelsrvr.Option{otelsrvr.WithMeterProvider(nil), otelsrvr.WithTracerProvider(nil)},
			[]string{"MeterProvider", "TracerProvider"},
			nil,
		},
		{
			"meter failing to create http.server.request.duration",
			[]otelsrvr.Option{failing("http.server.request.duration")},
			[]string{"MeterProvider"},
			isCause(errMeter),
		},
		{
			"meter failing to create http.server.response.body.size, WithBodySizes()",
			[]otelsrvr.Option{failing("http.server.response.body.size"), otelsrvr.WithBodySizes()},
			[]string{"MeterProvider"},
			isCause(errMeter),
		},
		{"Meter panics with error E", []otelsrvr.Option{meterPanics(errPanic)}, []string{"MeterProvider"}, isCause(errPanic)},
		{"Tracer panics with error E", tracerPanics(errPanic), []string{"TracerProvider"}, isCause(errPanic)},
		{"Fields panics with error E", []otelsrvr.Option{propagatorPanics(errPanic)}, []string{"Propagator"}, isCause(errPanic)},
		{"Meter panics with \"boom\"", []otelsrvr.Option{meterPanics("boom")}, []string{"MeterProvider"}, nil},
		{"Meter panics with a typed-nil *MyError", []otelsrvr.Option{meterPanics((*myError)(nil))}, []string{"MeterProvider"}, nil},
		{"Tracer panics with an error unwrapping to itself", tracerPanics(&selfUnwrapError{}), []string{"TracerProvider"}, nil},
		{"Fields panics with an error whose Error panics", []otelsrvr.Option{propagatorPanics(errorPanics)}, []string{"Propagator"}, isCause(errorPanics)},
		{
			"Meter panics with an uncomparable sliceError",
			[]otelsrvr.Option{meterPanics(sliceError{"a"})},
			[]string{"MeterProvider"},
			func(err error) bool { _, ok := errors.AsType[sliceError](err); return ok },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := newWithin(t, time.Second, tt.opts...)
			if !errors.Is(err, otelsrvr.ErrConfig) {
				t.Fatalf("New = %v, want an error wrapping ErrConfig", err)
			}

			got := configFields(err)
			for _, field := range tt.fields {
				if !slices.Contains(got, field) {
					t.Errorf("New error has *ConfigError fields %q, want %q among them; error: %v", got, field, err)
				}
			}

			if tt.cause == nil {
				if causes := configCauses(err); len(causes) > 0 {
					t.Errorf("New error has *ConfigError causes %v, want none; error: %v", causes, err)
				}
			} else if !tt.cause(err) {
				t.Errorf("New error does not reach the expected cause; error: %v", err)
			}
		})
	}
}

// newWithin returns the error of otelsrvr.New(opts...), failing the test
// when New panics or does not return within d.
func newWithin(t *testing.T, d time.Duration, opts ...otelsrvr.Option) error {
	t.Helper()

	done := make(chan error, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				done <- fmt.Errorf("New panicked: %v", v)
			}
		}()

		_, err := otelsrvr.New(opts...)
		done <- err
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(d):
		t.Fatalf("New did not return within %v", d)

		return nil
	}
}

// isCause returns a check that errors.Is reaches target.
func isCause(target error) func(error) bool {
	return func(err error) bool { return errors.Is(err, target) }
}

// myError is an error type whose typed nil a provider panics with.
type myError struct{}

func (*myError) Error() string { return "my error" }

// selfUnwrapError unwraps to itself, so its chain never ends.
type selfUnwrapError struct{}

func (e *selfUnwrapError) Error() string { return "self" }
func (e *selfUnwrapError) Unwrap() error { return e }

// textPanicsError panics in Error.
type textPanicsError struct{}

func (*textPanicsError) Error() string { panic("Error panicked") }

// sliceError is an error of an uncomparable type.
type sliceError []string

func (e sliceError) Error() string { return strings.Join(e, ",") }

// configCauses returns the non-nil Cause of every *otelsrvr.ConfigError a
// joined error holds, in order.
func configCauses(err error) []error {
	var causes []error
	for _, ce := range configErrors(err) {
		if ce.Cause != nil {
			causes = append(causes, ce.Cause)
		}
	}

	return causes
}

// configFields returns the Field of every *otelsrvr.ConfigError a joined
// error holds, in order.
func configFields(err error) []string {
	ces := configErrors(err)
	fields := make([]string, 0, len(ces))
	for _, ce := range ces {
		fields = append(fields, ce.Field)
	}

	return fields
}

// configErrors returns every *otelsrvr.ConfigError a joined error holds, in
// order.
func configErrors(err error) []*otelsrvr.ConfigError {
	// A *ConfigError also has Unwrap() []error (ErrConfig and its cause), so
	// it is matched before the join it may sit in.
	if ce, ok := err.(*otelsrvr.ConfigError); ok { //nolint:errorlint // only this error, not one it wraps
		return []*otelsrvr.ConfigError{ce}
	}

	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var all []*otelsrvr.ConfigError
		for _, e := range joined.Unwrap() {
			all = append(all, configErrors(e)...)
		}

		return all
	}

	if ce, ok := errors.AsType[*otelsrvr.ConfigError](err); ok {
		return []*otelsrvr.ConfigError{ce}
	}

	return nil
}
