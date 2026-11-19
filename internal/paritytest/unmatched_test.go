package paritytest

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"github.com/kartaladev/srvr/fibersrvr"
	"github.com/kartaladev/srvr/ginsrvr"
	"github.com/kartaladev/srvr/otelsrvr"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// fakeClock is the unmatched-record limit's clock in tests: it stands
// still until advanced.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// option makes a server read c instead of time.Now.
func (c *fakeClock) option() srvr.Option {
	return func(s *engine.Settings) { s.Now = c.Now }
}

// limitEngines build, without starting, a server of one engine serving
// GET /items/{id} with 200, logging to logs.
var limitEngines = []struct {
	name  string
	build func(t *testing.T, logs *logBuffer, opts ...srvr.Option) server
}{
	{"net/http", func(t *testing.T, logs *logBuffer, opts ...srvr.Option) server {
		t.Helper()

		return newNetServer(t, logs, func(g *srvr.Group) {
			g.GET("/items/{id}", func(w http.ResponseWriter, _ *http.Request) error {
				w.WriteHeader(http.StatusOK)

				return nil
			})
		}, opts...)
	}},
	{"gin", func(t *testing.T, logs *logBuffer, opts ...srvr.Option) server {
		t.Helper()

		return newGinServer(t, logs, func(g *gin.RouterGroup) {
			g.GET("/items/:id", func(c *gin.Context) { c.Status(http.StatusOK) })
		}, opts...)
	}},
	{"fiber", func(t *testing.T, logs *logBuffer, opts ...srvr.Option) server {
		t.Helper()

		return newFiberServer(t, logs, idleWorkerDuration, func(r fiber.Router) {
			r.Get("/items/:id", func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
		}, opts...)
	}},
}

// sendN sends n requests method base+path(i) and checks their status,
// stopping at the first failure. It reports with t.Errorf, so it may run
// on a goroutine of its own.
func sendN(t *testing.T, method, base string, n, status int, path func(int) string) {
	t.Helper()

	for i := range n {
		res, _, err := send(t, method, base+path(i), nil)
		if err != nil {
			t.Errorf("%s %s: %v", method, path(i), err)

			return
		}
		if res.StatusCode != status {
			t.Errorf("%s %s = %d, want %d", method, path(i), res.StatusCode, status)

			return
		}
	}
}

func nope(i int) string { return fmt.Sprintf("/nope-%d", i) }

// failedRecords returns the http.request.failed records, waiting up to a
// second for want of them.
func (b *logBuffer) failedRecords(t *testing.T, want int) []map[string]any {
	t.Helper()

	return b.recordsWithin(t, time.Second, want, func(msg string) bool { return msg == "http.request.failed" })
}

// durationCount returns how many requests http.server.request.duration
// counted.
func durationCount(t *testing.T, reader sdkmetric.Reader) uint64 {
	t.Helper()

	var n uint64
	for _, p := range durationPoints(t, reader) {
		n += p.Count
	}

	return n
}

const keySuppressed = "srvr.unmatched.suppressed"

// TestUnmatchedLimit covers structured-logging / Scenario: Limiting
// unmatched failed records, Examples: within and over the limit, on every
// engine.
func TestUnmatchedLimit(t *testing.T) {
	t.Parallel()

	limit := srvr.WithUnmatchedRecordLimit
	tests := []struct {
		name string
		opts []srvr.Option
		// n requests GET /nope-<n>, or DELETE /items/42 with deleteRoute,
		// from clients concurrent clients (1 when 0) each; with later, one
		// more GET /nope-x two seconds later.
		n, clients  int
		deleteRoute bool
		later       bool
		shutdown    bool
		want        int
		// last is srvr.unmatched.suppressed on the last record, 0 when no
		// record carries it; stopped is the one on server.stopped.
		last, stopped float64
		// observed checks http.server.request.duration's count.
		observed uint64
	}{
		{name: "no limit", n: 100, want: 100},
		{name: "explicit zero limit", opts: []srvr.Option{limit(0, 0)}, n: 3, want: 3},
		{name: "exactly the burst", opts: []srvr.Option{limit(10, time.Second)}, n: 10, want: 10},
		{name: "one over the burst", opts: []srvr.Option{limit(10, time.Second)}, n: 11, want: 10},
		{name: "next window reports the count", opts: []srvr.Option{limit(10, time.Second)}, n: 15, later: true, want: 11, last: 5},
		{name: "method not allowed", opts: []srvr.Option{limit(10, time.Second)}, n: 15, deleteRoute: true, want: 10},
		{name: "50 concurrent clients", opts: []srvr.Option{limit(10, time.Second)}, n: 20, clients: 50, later: true, want: 11, last: 990},
		{name: "shutdown reports the count", opts: []srvr.Option{limit(1, time.Minute)}, n: 3, shutdown: true, want: 1, stopped: 2},
		{name: "shutdown with nothing suppressed", opts: []srvr.Option{limit(1, time.Minute)}, n: 1, shutdown: true, want: 1},
		{
			name: "AccessLogConfig",
			opts: []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{
				UnmatchedLimit: srvr.UnmatchedLimitConfig{Burst: 1, Interval: time.Minute},
			})},
			n: 3, want: 1,
		},
		{name: "observed by otelsrvr", opts: []srvr.Option{limit(1, time.Minute)}, n: 3, want: 1, observed: 3},
	}

	for _, e := range limitEngines {
		for _, tt := range tests {
			t.Run(e.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				clk := newFakeClock()
				opts := append([]srvr.Option{clk.option()}, tt.opts...)

				var reader *sdkmetric.ManualReader
				if tt.observed > 0 {
					reader = sdkmetric.NewManualReader()
					metrics, err := otelsrvr.New(otelsrvr.WithMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))))
					must(t, err)
					opts = append(opts, srvr.WithObserver(metrics))
				}

				logs := &logBuffer{}
				base, stop := start(t, e.build(t, logs, opts...))

				method, status := http.MethodGet, http.StatusNotFound
				if tt.deleteRoute {
					method, status = http.MethodDelete, http.StatusMethodNotAllowed
				}

				var wg sync.WaitGroup
				for c := range max(tt.clients, 1) {
					wg.Go(func() {
						sendN(t, method, base, tt.n, status, func(i int) string {
							if tt.deleteRoute {
								return "/items/42"
							}

							return nope(c*tt.n + i)
						})
					})
				}
				wg.Wait()

				if tt.later {
					clk.advance(2 * time.Second)
					sendN(t, http.MethodGet, base, 1, http.StatusNotFound, func(int) string { return "/nope-x" })
				}

				if tt.shutdown {
					stop()
				}

				recs := logs.failedRecords(t, tt.want)
				if len(recs) != tt.want {
					t.Fatalf("%d http.request.failed records, want %d", len(recs), tt.want)
				}

				for i, rec := range recs {
					if rec["http.response.status_code"] != float64(status) {
						t.Errorf("record %d status = %v, want %d", i, rec["http.response.status_code"], status)
					}

					got, has := rec[keySuppressed]
					if i == len(recs)-1 && tt.last > 0 {
						if got != tt.last {
							t.Errorf("last record %s = %v, want %v", keySuppressed, got, tt.last)
						}
					} else if has {
						t.Errorf("record %d has %s = %v, want none", i, keySuppressed, got)
					}
				}

				if tt.shutdown {
					checkStopped(t, logs, tt.stopped)
				}

				if reader != nil {
					if n := durationCount(t, reader); n != tt.observed {
						t.Errorf("http.server.request.duration counted %d requests, want %d", n, tt.observed)
					}
				}
			})
		}
	}
}

// checkStopped checks srvr.unmatched.suppressed on server.stopped: want,
// or none when want is 0.
func checkStopped(t *testing.T, logs *logBuffer, want float64) {
	t.Helper()

	for _, rec := range logs.parse(t) {
		if rec["msg"] != "server.stopped" {
			continue
		}

		got, has := rec[keySuppressed]
		if want == 0 && has || want > 0 && got != want {
			t.Errorf("server.stopped %s = %v, want %v", keySuppressed, got, want)
		}

		return
	}

	t.Error("no server.stopped record")
}

// TestUnmatchedLimit_Config covers structured-logging / Scenario: Limiting
// unmatched failed records, Examples: rejected configuration, on every
// engine.
func TestUnmatchedLimit_Config(t *testing.T) {
	t.Parallel()

	builders := []struct {
		name string
		new  func(opts ...srvr.Option) error
	}{
		{"net/http", func(opts ...srvr.Option) error { _, err := srvr.New(opts...); return err }},
		{"gin", func(opts ...srvr.Option) error { _, err := ginsrvr.New(opts...); return err }},
		{"fiber", func(opts ...srvr.Option) error { _, err := fibersrvr.New(opts...); return err }},
	}

	tests := []struct {
		name     string
		burst    int
		interval time.Duration
		field    string // "" when construction succeeds
	}{
		{"negative burst", -1, time.Second, "UnmatchedLimit.Burst"},
		{"zero interval", 10, 0, "UnmatchedLimit.Interval"},
		{"negative interval", 10, -time.Second, "UnmatchedLimit.Interval"},
		{"no limit", 0, 0, ""},
	}

	for _, b := range builders {
		for _, tt := range tests {
			t.Run(b.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				err := b.new(srvr.WithUnmatchedRecordLimit(tt.burst, tt.interval))
				if tt.field == "" {
					must(t, err)

					return
				}

				var ce *srvr.ConfigError
				if !errors.Is(err, srvr.ErrConfig) || !errors.As(err, &ce) || ce.Field != tt.field {
					t.Fatalf("New = %v, want a *ConfigError naming %s", err, tt.field)
				}
			})
		}
	}
}

// TestFailedRecords_UnmatchedLimit covers structured-logging / Scenario:
// Failures while the access log is suppressed, the rows with an unmatched
// limit, on every engine: handler errors and panics are never limited.
func TestFailedRecords_UnmatchedLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outcome outcome
		opts    []srvr.Option
		level   string
	}{
		{
			"returned error with the access log disabled", failsBoom,
			[]srvr.Option{srvr.WithAccessLogDisabled(), srvr.WithUnmatchedRecordLimit(1, time.Minute)},
			"ERROR",
		},
		{"panic", panicsBoom, []srvr.Option{srvr.WithUnmatchedRecordLimit(1, time.Minute)}, "ERROR"},
	}

	for _, e := range engines {
		for _, tt := range tests {
			t.Run(e.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				base, logs := e.start(t, tt.outcome, tt.opts...)
				sendN(t, http.MethodGet, base, 10, http.StatusInternalServerError, func(int) string { return "/items/1" })

				recs := logs.failedRecords(t, 10)
				if len(recs) != 10 {
					t.Fatalf("%d http.request.failed records, want 10", len(recs))
				}

				for i, rec := range recs {
					if rec["level"] != tt.level {
						t.Errorf("record %d level = %v, want %s", i, rec["level"], tt.level)
					}
				}
			})
		}
	}
}

// TestAccessLog_OmitUserAgent covers structured-logging / Scenario:
// Omitting the user agent, on every engine.
func TestAccessLog_OmitUserAgent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    []srvr.Option
		outcome outcome
		msg     string
		agent   string // "" when user_agent.original must be absent
	}{
		{"default", nil, succeeds, "http.request.completed", "probe/1.0"},
		{"WithoutAccessLogUserAgent", []srvr.Option{srvr.WithoutAccessLogUserAgent()}, succeeds, "http.request.completed", ""},
		{"WithoutAccessLogUserAgent on a failure", []srvr.Option{srvr.WithoutAccessLogUserAgent()}, failsBoom, "http.request.failed", ""},
		{"OmitUserAgent", []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{OmitUserAgent: true})}, succeeds, "http.request.completed", ""},
		{
			"replaced by WithAccessLogConfig",
			[]srvr.Option{srvr.WithoutAccessLogUserAgent(), srvr.WithAccessLogConfig(srvr.AccessLogConfig{})},
			succeeds, "http.request.completed", "probe/1.0",
		},
	}

	for _, e := range engines {
		for _, tt := range tests {
			t.Run(e.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				base, logs := e.start(t, tt.outcome, tt.opts...)
				if _, _, err := send(t, http.MethodGet, base+"/items/1", map[string]string{"User-Agent": "probe/1.0"}); err != nil {
					t.Fatal(err)
				}

				rec := one(t, logs)
				if rec["msg"] != tt.msg {
					t.Fatalf("msg = %v, want %s", rec["msg"], tt.msg)
				}

				got, has := rec["user_agent.original"]
				if tt.agent == "" && has || tt.agent != "" && got != tt.agent {
					t.Fatalf("user_agent.original = %v (present %t), want %q", got, has, tt.agent)
				}
			})
		}
	}
}
