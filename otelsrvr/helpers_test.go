package otelsrvr_test

import (
	"context"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// configFunc adapts a function to srvr.RouteConfigurer.
type configFunc func(g *srvr.Group)

func (f configFunc) Configure(g *srvr.Group) { f(g) }

// serve starts a net/http srvr server with opts and conf on a loopback
// port, stops it when the test ends and returns its base URL.
func serve(t *testing.T, conf configFunc, opts ...srvr.Option) string {
	t.Helper()

	opts = append([]srvr.Option{srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard)}, opts...)

	srv, err := srvr.New(opts...)
	if err != nil {
		t.Fatalf("srvr.New: %v", err)
	}

	if err := srv.Register(conf); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()

	t.Cleanup(func() {
		cancel()

		if err := <-done; err != nil {
			t.Errorf("Start = %v", err)
		}
	})

	select {
	case <-srv.Ready():
	case err := <-done:
		t.Fatalf("Start returned before readiness: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not become ready")
	}

	registerClient(t, srv.Addr()) // its cleanup runs before the one above

	return "http://" + srv.Addr()
}

// send sends a request and returns the status after reading the body.
func send(t *testing.T, method, url, body string) int {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	if body == "" {
		req.Body = http.NoBody
	}

	res, err := clientFor(url).Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()

	_, _ = io.Copy(io.Discard, res.Body)

	return res.StatusCode
}

// usersRoute registers GET /users/{id} writing 200.
var usersRoute = configFunc(func(g *srvr.Group) {
	g.GET("/users/{id}", func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(http.StatusOK)

		return nil
	})
})

// collect returns everything reader holds.
func collect(t *testing.T, reader sdkmetric.Reader) metricdata.ResourceMetrics {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}

	return rm
}

// find returns the metric named name, and false when it is missing.
func find(rm metricdata.ResourceMetrics, name string) (metricdata.Metrics, instrumentation.Scope, bool) {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return m, sm.Scope, true
			}
		}
	}

	return metricdata.Metrics{}, instrumentation.Scope{}, false
}

// histogram returns the data points of the float64 histogram name.
func histogram(t *testing.T, rm metricdata.ResourceMetrics, name string) []metricdata.HistogramDataPoint[float64] {
	t.Helper()

	m, _, ok := find(rm, name)
	if !ok {
		t.Fatalf("no %s collected", name)
	}

	h, ok := m.Data.(metricdata.Histogram[float64])
	if !ok {
		t.Fatalf("%s is %T, want a float64 histogram", name, m.Data)
	}

	return h.DataPoints
}

// attr returns the value of key in set as a string, and false when absent.
func attr(set attribute.Set, key string) (string, bool) {
	v, ok := set.Value(attribute.Key(key))
	if !ok {
		return "", false
	}

	return v.String(), true
}

// attributeKeys returns the sorted attribute keys of m's data points.
func attributeKeys(t *testing.T, m metricdata.Metrics) []string {
	t.Helper()

	keys := map[string]bool{}
	add := func(set attribute.Set) {
		for _, kv := range set.ToSlice() {
			keys[string(kv.Key)] = true
		}
	}

	switch d := m.Data.(type) {
	case metricdata.Histogram[float64]:
		for _, p := range d.DataPoints {
			add(p.Attributes)
		}
	case metricdata.Histogram[int64]:
		for _, p := range d.DataPoints {
			add(p.Attributes)
		}
	case metricdata.Sum[int64]:
		for _, p := range d.DataPoints {
			add(p.Attributes)
		}
	default:
		t.Fatalf("%s is %T", m.Name, m.Data)
	}

	return slices.Sorted(maps.Keys(keys))
}
