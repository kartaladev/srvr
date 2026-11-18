package paritytest

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// resultObserver keeps the ResultInfo of every request.
type resultObserver struct {
	mu      sync.Mutex
	results []srvr.ResultInfo
}

func (o *resultObserver) Begin(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
	return ctx, func(res srvr.ResultInfo) {
		o.mu.Lock()
		defer o.mu.Unlock()

		o.results = append(o.results, res)
	}
}

func (o *resultObserver) only(t *testing.T) srvr.ResultInfo {
	t.Helper()

	o.mu.Lock()
	defer o.mu.Unlock()

	if len(o.results) != 1 {
		t.Fatalf("end calls = %d, want 1", len(o.results))
	}

	return o.results[0]
}

// durationPoint returns the only http.server.request.duration data point.
func durationPoint(t *testing.T, reader sdkmetric.Reader) metricdata.HistogramDataPoint[float64] {
	t.Helper()

	var rm metricdata.ResourceMetrics
	must(t, reader.Collect(context.Background(), &rm))

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.server.request.duration" {
				continue
			}

			if m.Unit != "s" {
				t.Errorf("unit = %q, want s", m.Unit)
			}

			points := m.Data.(metricdata.Histogram[float64]).DataPoints
			if len(points) != 1 || points[0].Count != 1 {
				t.Fatalf("data points = %+v, want one with count 1", points)
			}

			return points[0]
		}
	}

	t.Fatal("no http.server.request.duration collected")

	return metricdata.HistogramDataPoint[float64]{}
}

// observedRun is what one engine reported for one request.
type observedRun struct {
	result   srvr.ResultInfo
	attrs    attribute.Set
	problems int64
}

// observe sends GET path to a server of engine e whose handler does o,
// with a recording observer, otelsrvr on a manual reader and an error hook
// counting rendered problems.
func observe(t *testing.T, e engineCase, o outcome, path string) observedRun {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	metrics, err := otelsrvr.New(otelsrvr.WithMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))))
	must(t, err)

	rec := &resultObserver{}

	var problems atomic.Int64

	hook := srvr.WithErrorHook(func(_ context.Context, _ error, p *prblm.Problem) {
		if p != nil {
			problems.Add(1)
		}
	})

	base, logs := e.start(t, o, srvr.WithObserver(rec), srvr.WithObserver(metrics), hook)
	if _, _, err := send(t, http.MethodGet, base+path, nil); err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}

	logs.access(t) // the access record follows the end functions

	return observedRun{result: rec.only(t), attrs: durationPoint(t, reader).Attributes, problems: problems.Load()}
}

// TestDurationPerEngine covers otel-metrics "Duration per engine", all rows.
func TestDurationPerEngine(t *testing.T) {
	t.Parallel()

	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			t.Parallel()

			run := observe(t, e, succeeds, "/items/42")

			if route, _ := run.attrs.Value("http.route"); route.AsString() != e.route {
				t.Errorf("http.route = %q, want %q", route.AsString(), e.route)
			}

			if status, _ := run.attrs.Value("http.response.status_code"); status.AsInt64() != http.StatusOK {
				t.Errorf("http.response.status_code = %d, want 200", status.AsInt64())
			}
		})
	}
}

// TestObserverParity asserts that every engine reports the same ResultInfo
// and data point attributes for the same request, except http.route,
// which is in each engine's syntax, and that a failed fiber request is
// rendered exactly once.
func TestObserverParity(t *testing.T) {
	t.Parallel()

	// sameSize is false where the response bodies differ for a reason
	// outside the observer: the unmatched 404 problem (notes.md "Unmatched
	// 404 problems differ between net/http and gin/fiber",
	// TestUnmatchedProblemParity).
	tests := []struct {
		name     string
		outcome  outcome
		path     string
		problems int64
		sameSize bool
	}{
		{"succeeds", succeeds, "/items/42", 0, true},
		{"returns ErrNotFound", failsNotFound, "/items/42", 1, true},
		{"returns boom", failsBoom, "/items/42", 1, true},
		{"panics", panicsBoom, "/items/42", 1, true},
		{"unmatched", succeeds, "/nope", 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runs := make([]observedRun, len(engines))
			for i, e := range engines {
				runs[i] = observe(t, e, tt.outcome, tt.path)
			}

			want := viewOf(runs[0], engines[0].route, tt.sameSize)
			for i, e := range engines {
				if got := viewOf(runs[i], e.route, tt.sameSize); got != want {
					t.Errorf("%s reported %+v, net/http %+v", e.name, got, want)
				}

				if runs[i].problems != tt.problems {
					t.Errorf("%s rendered %d problems, want %d", e.name, runs[i].problems, tt.problems)
				}
			}
		})
	}
}

// parityView is the engine-independent part of what an engine reported.
type parityView struct {
	matched                   bool
	status                    int
	kind                      srvr.ErrorKind
	failed, committed         bool
	responseSize, requestSize int64
	attrs                     attribute.Distinct
}

// viewOf returns the engine-independent view of run, with the route
// checked against the engine's syntax and left out, and the response size
// left out unless sameSize.
func viewOf(run observedRun, route string, sameSize bool) parityView {
	res := run.result

	set, _ := run.attrs.Filter(func(kv attribute.KeyValue) bool {
		return kv.Key != "http.route" || kv.Value.AsString() != route
	})

	if !sameSize {
		res.ResponseBodySize = 0
	}

	return parityView{
		matched: res.Route == route, status: res.Status, kind: res.ErrorKind,
		failed: res.Err != nil, committed: res.Committed,
		responseSize: res.ResponseBodySize, requestSize: res.RequestBodySize,
		attrs: set.Equivalent(),
	}
}

// TestObserverAborts covers request-observer "Aborts and late failures per
// engine", all rows.
func TestObserverAborts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		start              func(*testing.T, outcome, ...srvr.Option) (string, *logBuffer)
		outcome            outcome
		status             int
		aborted, committed bool
		accessSize         bool // the size is the access record's (and not 0)
	}{
		{"net/http aborts", startNetHTTP, panicsAbort, 0, true, false, false},
		{"gin aborts", startGin, panicsAbort, 0, true, false, false},
		// Documented fiber exception: fasthttp has no abort sentinel, so
		// the panic is recovered like any other and rendered as a problem.
		{"fiber aborts", startFiber, panicsAbort, http.StatusInternalServerError, false, false, true},
		{"fiber streams and then fails", startFiberStreamLate, succeeds, http.StatusOK, false, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &resultObserver{}
			base, logs := tt.start(t, tt.outcome, srvr.WithObserver(rec))
			_, _, _ = send(t, http.MethodGet, base+"/items/42", nil) // an abort is a transport error

			access := logs.access(t) // the access record follows the end functions
			res := rec.only(t)

			if res.Status != tt.status || res.Aborted != tt.aborted || res.Committed != tt.committed {
				t.Errorf("ResultInfo status %d aborted %v committed %v, want %d %v %v", res.Status, res.Aborted, res.Committed, tt.status, tt.aborted, tt.committed)
			}

			var want int64
			if tt.accessSize {
				size, _ := access[0]["http.response.body.size"].(float64)
				if want = int64(size); want == 0 {
					t.Errorf("access record size 0: %v", access[0])
				}
			}

			if res.ResponseBodySize != want {
				t.Errorf("ResponseBodySize = %d, want %d (access record %v)", res.ResponseBodySize, want, access[0])
			}
		})
	}
}

// startFiberStreamLate starts a fiber server whose /items/:id handler
// streams "streamed" with its declared length and then fails; the outcome
// is ignored.
func startFiberStreamLate(t *testing.T, _ outcome, opts ...srvr.Option) (string, *logBuffer) {
	t.Helper()

	return fiberServer(t, func(r fiber.Router) {
		r.Get("/items/:id", func(c fiber.Ctx) error {
			_ = c.SendStream(strings.NewReader("streamed"), len("streamed")) // a declared length

			return errors.New("late")
		})
	}, opts...)
}
