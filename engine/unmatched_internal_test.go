package engine

import (
	"log/slog"
	"testing"
	"time"

	"github.com/kartaladev/prblm"
)

// TestUnmatchedLimit_DiscardsDebugBuffer: a suppressed unmatched record
// discards the request's debug buffer, as a suppressed access record does
// (design D7), and returns its bytes to the budget.
func TestUnmatchedLimit_DiscardsDebugBuffer(t *testing.T) {
	t.Parallel()

	out := &syncBuffer{}
	c := bufferedCore(t, func(s *Settings) {
		s.Log.Output = out
		s.Log.DebugBuffer.FlushStatus = 404
		s.Access.UnmatchedLimit = UnmatchedLimitConfig{Burst: 1, Interval: time.Minute}
	})

	for range 3 {
		ctx := c.BeginRequest(t.Context(), nil)
		slog.New(c.log).DebugContext(ctx, "step")
		recordFailure(ctx, prblm.ErrNotFound, prblm.New(404))
		c.WriteAccess(ctx, AccessInfo{Method: "GET", Status: 404})
	}

	flushed := 0
	for _, rec := range out.records(t) {
		if rec[keyBuffered] == true {
			flushed++
		}
	}

	if flushed != 1 {
		t.Fatalf("%d flushed records, want 1 (the admitted request's)", flushed)
	}

	if used := c.log.buffering.budget.used.Load(); used != 0 {
		t.Fatalf("budget holds %d bytes after suppressed requests", used)
	}
}

// TestUnmatchedLimiter covers the limiter's branches directly: admitted
// within the burst, counted over it, a new window reporting the count, and
// report on stop.
func TestUnmatchedLimiter(t *testing.T) {
	t.Parallel()

	now := time.Unix(0, 0)
	l := newUnmatchedLimiter(UnmatchedLimitConfig{Burst: 2, Interval: time.Second}, func() time.Time { return now })

	type step struct {
		advance  time.Duration
		admitted bool
		reported int64
	}

	for i, s := range []step{
		{0, true, 0},                       // starts the first window
		{0, true, 0},                       // within the burst
		{0, false, 0},                      // over it, counted
		{999 * time.Millisecond, false, 0}, // one before the window ends
		{time.Millisecond, true, 2},        // exactly at its end: a new window reports 2
		{0, true, 0},
	} {
		now = now.Add(s.advance)
		if ok, n := l.admit(); ok != s.admitted || n != s.reported {
			t.Fatalf("step %d: admit = %t, %d; want %t, %d", i, ok, n, s.admitted, s.reported)
		}
	}

	l.admit() // suppressed

	if n := l.report(); n != 1 {
		t.Fatalf("report = %d, want 1", n)
	}

	if n := l.report(); n != 0 {
		t.Fatalf("second report = %d, want 0", n)
	}

	var none *unmatchedLimiter
	if n := none.report(); n != 0 || newUnmatchedLimiter(UnmatchedLimitConfig{}, time.Now) != nil {
		t.Fatal("no limit must be a nil limiter reporting 0")
	}
}
