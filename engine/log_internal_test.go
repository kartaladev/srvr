package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
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
