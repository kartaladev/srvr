package otelsrvr_test

import (
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

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
