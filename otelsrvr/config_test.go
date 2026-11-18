package otelsrvr_test

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/kartaladev/srvr/otelsrvr"
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
