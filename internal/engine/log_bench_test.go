package engine

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// benchTrace is the trace of the request context the logging benchmarks
// write with.
var benchTrace = Trace{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "00f067aa0ba902b7"}

// benchRequestContext returns a request context carrying req (nil: a
// request with benchTrace), as a request handled by the server has.
func benchRequestContext(b *testing.B, req *RequestState) context.Context {
	b.Helper()

	if req == nil {
		req = &RequestState{trace: benchTrace}
	}

	return context.WithValue(b.Context(), stateKey{}, req)
}

// BenchmarkCorrelation compares ungrouped and grouped request records: a
// grouped logger replays its WithGroup and WithAttrs steps per record.
func BenchmarkCorrelation(b *testing.B) {
	base := &logHandler{base: slog.NewJSONHandler(io.Discard, nil)} //nolint:sloglint // the benchmark measures encoding records: slog.DiscardHandler is disabled and would skip that work
	ctx := benchRequestContext(b, nil)

	cases := []struct {
		name   string
		logger *slog.Logger
	}{
		{"ungrouped", slog.New(base)},
		{"grouped", slog.New(base).With("table", "users").WithGroup("db")},
	}

	for _, bc := range cases {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				bc.logger.InfoContext(ctx, "query", "k", "v")
			}
		})
	}
}

// BenchmarkLogHandler measures srvr's logging handler as the server builds
// it by default (JSON, default attribute cap) writing a request record to
// io.Discard, after 0, 1 and 3 With steps, with and without a slog.Any
// attribute, which the attribute cap formats to measure. Two more rows
// measure the patterns where the handler's target cache cannot hit: a
// request logger taken from LoggerFromContext for each record, and the
// root logger shared by concurrent requests, each with its own trace,
// whose records interleave.
func BenchmarkLogHandler(b *testing.B) {
	h := newLogHandler(&LogSettings{Output: io.Discard})

	ctx := benchRequestContext(b, nil)
	header := http.Header{"Accept": {"application/json"}, "User-Agent": {"bench/1.0"}}

	cases := []struct {
		name    string
		steps   int
		withAny bool
	}{
		{"0 steps", 0, false},
		{"0 steps with slog.Any", 0, true},
		{"1 step", 1, false},
		{"1 step with slog.Any", 1, true},
		{"3 steps", 3, false},
		{"3 steps with slog.Any", 3, true},
	}

	for _, bc := range cases {
		b.Run(bc.name, func(b *testing.B) {
			l := slog.New(h)
			for i := range bc.steps {
				l = l.With(slog.Int("step", i))
			}

			attrs := []slog.Attr{slog.String("table", "users")}
			if bc.withAny {
				attrs = append(attrs, slog.Any("header", header))
			}

			b.ReportAllocs()

			for b.Loop() {
				l.LogAttrs(ctx, slog.LevelInfo, "query", attrs...)
			}
		})
	}

	attr := slog.String("table", "users")

	b.Run("LoggerFromContext one record", func(b *testing.B) {
		ctx := benchRequestContext(b, &RequestState{core: &Core{log: h}, trace: benchTrace})

		b.ReportAllocs()

		for b.Loop() {
			LoggerFromContext(ctx).LogAttrs(ctx, slog.LevelInfo, "query", attr)
		}
	})

	// Each goroutine alternates between two requests of its own, so no
	// record's trace is the last one the root handler cached: every record
	// rebuilds its target, at any GOMAXPROCS and however the goroutines
	// interleave. With one request per goroutine the cache hit whenever no
	// other goroutine had logged since, and allocs/op followed the
	// scheduling: 0 at -cpu=1, 4 at -cpu=2, 4 to 7 at -cpu=4 (design D10).
	b.Run("root logger interleaved requests", func(b *testing.B) {
		l := slog.New(h)

		var requests atomic.Uint64

		request := func() context.Context {
			tr := benchTrace
			tr.SpanID = fmt.Sprintf("%016x", requests.Add(1))

			return benchRequestContext(b, &RequestState{trace: tr})
		}

		b.ReportAllocs()

		b.RunParallel(func(pb *testing.PB) {
			ctxs := [2]context.Context{request(), request()}

			for i := 0; pb.Next(); i++ {
				l.LogAttrs(ctxs[i%2], slog.LevelInfo, "query", attr)
			}
		})
	})
}

// BenchmarkRedactSecrets measures the default redactor over representative
// error and stack texts.
func BenchmarkRedactSecrets(b *testing.B) {
	frame := "github.com/example/app/internal/store.(*Users).Find(0x14000123450, {0x1045a2f40, 0x14000234560})\n" +
		"\t/home/build/app/internal/store/users.go:42 +0x1c4\n"

	cases := []struct {
		name string
		text string
	}{
		{"no secret", "store: find user 42: connection refused"},
		{"URL query secret", `Get "https://api.example.com/v1/users?api_key=abc123&page=2": dial tcp: i/o timeout`},
		{"key=value secret", "auth: login failed for user=alice password=hunter2 retry=3"},
		{"32 KiB stack", strings.Repeat(frame, 32<<10/len(frame))},
	}

	for _, bc := range cases {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				_ = RedactSecrets(bc.text)
			}
		})
	}
}

// BenchmarkDebugBuffer measures one request that logs DEBUG records below
// the INFO level and ends: five two-attribute records with buffering off
// (the records are dropped by Enabled), on with a 200 (buffered, then
// discarded) and on with a 500 (buffered, then written before the access
// record); on with a 500, five records of seven attributes, one a
// slog.Any value; and on with a 500, 150 records, past the default 100,
// so the oldest are evicted.
func BenchmarkDebugBuffer(b *testing.B) {
	// two attributes per record, below slog's inline five: Clone does not
	// allocate.
	narrow := func(ctx context.Context, logger *slog.Logger, i int) {
		logger.DebugContext(ctx, "step", "i", i, "user.id", "42")
	}
	// seven attributes, one a slog.Any value: Clone allocates and the
	// value is formatted to cut and to size it (design D8).
	header := http.Header{"Accept": {"application/json"}, "X-Request-Id": {"r-1"}}
	wide := func(ctx context.Context, logger *slog.Logger, i int) {
		logger.LogAttrs(ctx, slog.LevelDebug, "step", slog.Int("i", i), slog.String("user.id", "42"),
			slog.String("order.id", "o-7"), slog.Int("cart.items", 3), slog.String("region", "eu-west-1"),
			slog.Bool("retry", false), slog.Any("http.request.header", header))
	}

	cases := []struct {
		name    string
		enabled bool
		status  int
		records int
		log     func(context.Context, *slog.Logger, int)
	}{
		{"off", false, http.StatusOK, 5, narrow},
		{"on-success", true, http.StatusOK, 5, narrow},
		{"on-failure", true, http.StatusInternalServerError, 5, narrow},
		{"on-failure 7 attributes with slog.Any", true, http.StatusInternalServerError, 5, wide},
		// past the default 100 records: the oldest are evicted.
		{"on-failure 150 records", true, http.StatusInternalServerError, 150, narrow},
	}

	for _, bc := range cases {
		b.Run(bc.name, func(b *testing.B) {
			c := bufferedCore(b, func(s *Settings) { s.Log.DebugBuffer.Enabled = bc.enabled })
			info := AccessInfo{Method: http.MethodGet, Status: bc.status}

			b.ReportAllocs()

			for b.Loop() {
				ctx := c.BeginRequest(b.Context(), nil)
				logger := LoggerFromContext(ctx)

				for i := range bc.records {
					bc.log(ctx, logger, i)
				}

				c.WriteAccess(ctx, info)
			}
		})
	}
}
