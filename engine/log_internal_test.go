package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

// TestLogHandler_TargetCache: a request logger reuses its target between
// records, yet every record carries the trace and truncation flag in force
// when it was written: a trace an observer replaced in place, a record
// with a cut value next to one without, concurrent records of two traces.
func TestLogHandler_TargetCache(t *testing.T) {
	t.Parallel()

	var buf syncBuffer

	h, err := newLogHandler(&LogSettings{Output: &buf, MaxAttrBytes: 8})
	if err != nil {
		t.Fatal(err)
	}

	req := &RequestState{trace: Trace{TraceID: "a1", SpanID: "s1"}}
	logger := slog.New(h.bind(req).WithAttrs([]slog.Attr{slog.String("step", "x")}))

	logger.Info("first")
	req.trace = Trace{TraceID: "b2", SpanID: "s2"} // an observer's trace replaced in place
	logger.Info("second")
	logger.Info("third", "long", strings.Repeat("y", 20))
	logger.Info("fourth")

	want := []struct {
		msg, trace string
		cut        bool
	}{
		{"first", "a1", false}, {"second", "b2", false}, {"third", "b2", true}, {"fourth", "b2", false},
	}

	recs := buf.records(t)
	if len(recs) != len(want) {
		t.Fatalf("%d records, want %d", len(recs), len(want))
	}
	for i, w := range want {
		_, cut := recs[i][keyAttrTruncated]
		if recs[i]["msg"] != w.msg || recs[i][keyTraceID] != w.trace || cut != w.cut || recs[i]["step"] != "x" {
			t.Fatalf("record %d = %v, want msg %s trace %s truncated %v step x", i, recs[i], w.msg, w.trace, w.cut)
		}
	}

	t.Run("concurrent traces", func(t *testing.T) {
		t.Parallel()

		var out syncBuffer

		h, err := newLogHandler(&LogSettings{Output: &out})
		if err != nil {
			t.Fatal(err)
		}

		shared := slog.New(h.WithAttrs([]slog.Attr{slog.String("step", "x")}))

		var wg sync.WaitGroup
		for _, id := range []string{"c3", "d4"} {
			ctx := context.WithValue(t.Context(), stateKey{}, &RequestState{trace: Trace{TraceID: id, SpanID: id}})
			wg.Go(func() {
				for range 200 {
					shared.InfoContext(ctx, id)
				}
			})
		}
		wg.Wait()

		for _, rec := range out.records(t) {
			if rec[keyTraceID] != rec["msg"] {
				t.Fatalf("record %v carries another goroutine's trace", rec)
			}
		}
	})
}

// syncBuffer is a bytes.Buffer safe for concurrent writes.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) records(t *testing.T) []map[string]any {
	t.Helper()

	b.mu.Lock()
	defer b.mu.Unlock()

	var recs []map[string]any
	for line := range strings.Lines(b.buf.String()) {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		recs = append(recs, rec)
	}

	return recs
}

// TestLoggerFromContext_Shared: every LoggerFromContext call for one
// request after the first returns the logger the first call built, so a
// handler that takes the logger for each record allocates nothing for it
// and reuses its target. Not parallel: AllocsPerRun panics while a
// parallel test runs.
func TestLoggerFromContext_Shared(t *testing.T) { //nolint:paralleltest // AllocsPerRun cannot run beside parallel tests.
	ctx := context.WithValue(t.Context(), stateKey{}, &RequestState{core: bufferedCore(t, func(*Settings) {}), trace: Trace{TraceID: "a1", SpanID: "s1"}})
	first := LoggerFromContext(ctx)

	if got := LoggerFromContext(ctx); got != first {
		t.Fatalf("second call = %p, want the first call's logger %p", got, first)
	}

	if allocs := testing.AllocsPerRun(100, func() { _ = LoggerFromContext(ctx) }); allocs != 0 {
		t.Fatalf("LoggerFromContext allocates %v times per call after the first, want 0", allocs)
	}
}

// TestLoggerFromContext_ConcurrentFirstCalls: concurrent first calls for
// one request agree on one logger.
func TestLoggerFromContext_ConcurrentFirstCalls(t *testing.T) {
	t.Parallel()

	ctx := context.WithValue(t.Context(), stateKey{}, &RequestState{core: bufferedCore(t, func(*Settings) {}), trace: Trace{TraceID: "b2", SpanID: "s2"}})
	loggers := make([]*slog.Logger, 8)

	var wg sync.WaitGroup
	for i := range loggers {
		wg.Go(func() { loggers[i] = LoggerFromContext(ctx) })
	}
	wg.Wait()

	for i, l := range loggers {
		if l != loggers[0] {
			t.Fatalf("goroutine %d got logger %p, goroutine 0 got %p", i, l, loggers[0])
		}
	}
}

// panickyString is a panic value whose own String panics, so fmt, which
// recovers a panicking Format and prints the panic value, panics again.
type panickyString struct{}

func (panickyString) String() string { panic("String") }

// panickyFormat is a plain value (no String, no Error) whose Format panics
// with a panickyString.
type panickyFormat struct{}

func (panickyFormat) Format(fmt.State, rune) { panic(panickyString{}) }

// TestCapAny_FormattingPanics: a plain value whose formatting panics past
// fmt's own recovery is measured as unknown and logged as the guarded
// placeholder, never crashing the record.
func TestCapAny_FormattingPanics(t *testing.T) {
	t.Parallel()

	if n, ok := formattedLen(panickyFormat{}); ok {
		t.Fatalf("formattedLen = %d, true; want false for a value whose formatting panics", n)
	}

	tr := truncator{maxBytes: 1024}
	if got := tr.capAny("v", slog.AnyValue(panickyFormat{})); got.Value.String() != panicFormatting {
		t.Fatalf("capAny = %v, want %q", got.Value, panicFormatting)
	}
}

// TestCapAny_Stringer: a fmt.Stringer is measured by its String, and one
// that fits is kept as the value itself, for the base handler to render.
func TestCapAny_Stringer(t *testing.T) {
	t.Parallel()

	addr := netip.MustParseAddr("192.0.2.1")
	tr := truncator{maxBytes: 9}

	if got := tr.capAny("a", slog.AnyValue(addr)); got.Value.Any() != addr || tr.cut {
		t.Fatalf("capAny = %v (cut %v), want the address itself", got.Value, tr.cut)
	}

	if got := tr.capAny("a", slog.AnyValue(netip.MustParseAddr("192.0.2.10"))); got.Value.String() != "192.0.2.1…[truncated]" || !tr.cut {
		t.Fatalf("capAny = %v (cut %v), want the text cut", got.Value, tr.cut)
	}
}
