package paritytest

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

	points := durationPoints(t, reader)
	if len(points) != 1 || points[0].Count != 1 {
		t.Fatalf("data points = %+v, want one with count 1", points)
	}

	return points[0]
}

// durationPoints returns the http.server.request.duration data points,
// failing when the histogram is missing or not in seconds.
func durationPoints(t *testing.T, reader sdkmetric.Reader) []metricdata.HistogramDataPoint[float64] {
	t.Helper()

	var rm metricdata.ResourceMetrics
	must(t, reader.Collect(t.Context(), &rm))

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != "http.server.request.duration" {
				continue
			}

			if m.Unit != "s" {
				t.Errorf("unit = %q, want s", m.Unit)
			}

			hist, ok := m.Data.(metricdata.Histogram[float64])
			if !ok {
				t.Fatalf("%s data is %T, want metricdata.Histogram[float64]", m.Name, m.Data)
			}

			return hist.DataPoints
		}
	}

	t.Fatal("no http.server.request.duration collected")

	return nil
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

// TestSpanShape covers otel-tracing "Span shape per engine", the gin and
// fiber rows, on every engine (the harness route is /items/{id} in each
// engine's syntax), and that the span continues an incoming traceparent
// and supplies the access record's ids.
func TestSpanShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		outcome   outcome
		path      string
		route     bool
		status    codes.Code
		exception string // exception.type of the exception event, "" for none
	}{
		{"writes 200", succeeds, "/items/42", true, codes.Unset, ""},
		{"returns boom", failsBoom, "/items/42", true, codes.Error, ""},
		{"unmatched", succeeds, "/nope", false, codes.Unset, ""},
		{"panics", panicsBoom, "/items/42", true, codes.Error, "string"},
	}

	for _, e := range engines {
		for _, tt := range tests {
			t.Run(e.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				rec := tracetest.NewSpanRecorder()
				obs, err := otelsrvr.New(otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))))
				must(t, err)

				base, logs := e.start(t, tt.outcome, srvr.WithObserver(obs))
				header := map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}
				if _, _, err := send(t, http.MethodGet, base+tt.path, header); err != nil {
					t.Fatal(err)
				}

				access := logs.access(t)

				want := "GET"
				if tt.route {
					want += " " + e.route
				}

				spans := rec.Ended()
				if len(spans) != 1 || spans[0].Name() != want || spans[0].Status().Code != tt.status {
					for _, s := range spans {
						t.Logf("span %q status %v", s.Name(), s.Status().Code)
					}

					t.Fatalf("want one span %q with status %v", want, tt.status)
				}

				if got := exceptionType(spans[0]); got != tt.exception {
					t.Errorf("exception.type = %q, want %q (events %v)", got, tt.exception, spans[0].Events())
				}

				// The span continues the incoming trace, and the access
				// record carries the span's ids, on every engine.
				sc := spans[0].SpanContext()
				if sc.TraceID().String() != "4bf92f3577b34da6a3ce929d0e0e4736" || spans[0].Parent().SpanID().String() != "00f067aa0ba902b7" {
					t.Errorf("span trace %s parent %s, want the incoming ones", sc.TraceID(), spans[0].Parent().SpanID())
				}

				if access[0]["trace_id"] != sc.TraceID().String() || access[0]["span_id"] != sc.SpanID().String() {
					t.Errorf("access record %v/%v, span %s/%s", access[0]["trace_id"], access[0]["span_id"], sc.TraceID(), sc.SpanID())
				}
			})
		}
	}
}

// exceptionType returns the exception.type of span's exception event, or
// "" without one.
func exceptionType(span sdktrace.ReadOnlySpan) string {
	for _, ev := range span.Events() {
		if ev.Name != "exception" {
			continue
		}

		for _, kv := range ev.Attributes {
			if kv.Key == "exception.type" {
				return kv.Value.AsString()
			}
		}
	}

	return ""
}

// TestParity_Log covers engine-parity / Scenario: Log parity: msg, level
// and http.route per engine, and the flattened key set equal to net/http's
// for the same outcome.
func TestParity_Log(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outcome outcome
		method  string
		path    string
		msg     string
		level   string
		routed  bool
		skip    string // an engine the row does not cover
	}{
		{"succeeds", succeeds, http.MethodGet, "/items/7", "http.request.completed", "INFO", true, ""},
		{"fails with boom", failsBoom, http.MethodGet, "/items/7", "http.request.failed", "ERROR", true, ""},
		{"fails with ErrNotFound", failsNotFound, http.MethodGet, "/items/7", "http.request.failed", "WARN", true, ""},
		{"GET /nope", succeeds, http.MethodGet, "/nope", "http.request.failed", "WARN", false, ""},
		{"DELETE /items/7", succeeds, http.MethodDelete, "/items/7", "http.request.failed", "WARN", false, ""},
		// fiber recovers http.ErrAbortHandler as an ordinary panic: a
		// documented exception, pinned in TestParity_Exceptions.
		{"panics with http.ErrAbortHandler", panicsAbort, http.MethodGet, "/items/7", "http.request.failed", "WARN", true, "fiber"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var reference []string

			for _, e := range engines {
				if e.name == tt.skip {
					continue
				}

				base, logs := e.start(t, tt.outcome)
				_, _, _ = send(t, tt.method, base+tt.path, map[string]string{"User-Agent": "parity"})

				recs := logs.access(t)
				if len(recs) != 1 {
					t.Fatalf("%s: %d access records, want 1", e.name, len(recs))
				}

				r := recs[0]
				wantRoute := any(nil)
				if tt.routed {
					wantRoute = e.route
				}

				if r["msg"] != tt.msg || r["level"] != tt.level || r["http.route"] != wantRoute {
					t.Errorf("%s: msg %v level %v route %v, want %s %s %v", e.name, r["msg"], r["level"], r["http.route"], tt.msg, tt.level, wantRoute)
				}

				keys := flatKeys(r)
				if reference == nil {
					reference = keys

					continue
				}

				if !slices.Equal(keys, reference) {
					t.Errorf("%s: keys %v\nnet/http: %v", e.name, keys, reference)
				}
			}
		})
	}
}

// flatKeys returns the sorted keys of r, groups flattened with ".".
func flatKeys(r map[string]any) []string {
	var keys []string

	for k, v := range r {
		if g, ok := v.(map[string]any); ok {
			for _, sub := range flatKeys(g) {
				keys = append(keys, k+"."+sub)
			}

			continue
		}

		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}

// TestZeroObserver covers otel-metrics / Scenario: Zero observer: an
// otelsrvr.Observer not built by New answers requests unchanged and
// writes no srvr.observer.fault record.
func TestZeroObserver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		engine  engineCase
		outcome outcome
		status  int
	}{
		{engines[0], succeeds, http.StatusOK},
		{engines[1], failsBoom, http.StatusInternalServerError},
		{engines[2], failsNotFound, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.engine.name, func(t *testing.T) {
			t.Parallel()

			base, logs := tt.engine.start(t, tt.outcome, srvr.WithObserver(&otelsrvr.Observer{}))
			res, _, err := send(t, http.MethodGet, base+"/items/42", nil)
			must(t, err)
			if res.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", res.StatusCode, tt.status)
			}

			if len(logs.access(t)) != 1 {
				t.Fatal("want one access record")
			}
			logs.mu.Lock()
			text := logs.buf.String()
			logs.mu.Unlock()
			if n := strings.Count(text, "srvr.observer.fault\""); n != 0 {
				t.Fatalf("%d srvr.observer.fault records, want 0:\n%s", n, text)
			}
		})
	}
}
