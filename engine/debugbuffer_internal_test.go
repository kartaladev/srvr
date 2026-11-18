package engine

import (
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDebugBuffer_AllocatedOnlyWhenEnabled covers triage-debug-buffer /
// Requirement: Buffering is off by default (no per-request buffer).
func TestDebugBuffer_AllocatedOnlyWhenEnabled(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		opts []Option
		want bool
	}{
		{"default", nil, false},
		{"disabled", []Option{func(s *Settings) { s.Log.DebugBuffer = DebugBufferConfig{Enabled: false} }}, false},
		{"enabled", []Option{func(s *Settings) { s.Log.DebugBuffer.Enabled = true }}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, err := ResolveConfig(tt.opts)
			if err != nil {
				t.Fatal(err)
			}

			req := requestFrom(c.BeginRequest(t.Context(), nil))
			if got := req.buffer != nil; got != tt.want {
				t.Fatalf("buffer allocated = %v, want %v", got, tt.want)
			}
		})
	}
}

type sized struct{ Name string }

// TestAttrSize: the size estimate counts the text an attribute holds, by
// kind.
func TestAttrSize(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		attr slog.Attr
		want int
	}{
		{"string", slog.String("k", "abcd"), 1 + 4},
		{"int", slog.Int("k", 7), 1 + 8},
		{"group", slog.Group("g", slog.String("a", "xy"), slog.Bool("b", true)), 1 + (1 + 2) + (1 + 8)},
		{"any", slog.Any("k", sized{Name: "bob"}), 1 + len("{bob}")},
		{"empty group", slog.Group("g"), 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := attrSize(tt.attr); got != tt.want {
				t.Fatalf("attrSize = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestDebugBuffer_OwnRecordsFollowTheLevel: srvr's own records are never
// buffered, so one logged with a request context below the log level is
// dropped even while the request's buffer is open.
func TestDebugBuffer_OwnRecordsFollowTheLevel(t *testing.T) {
	t.Parallel()

	var buf syncBuffer

	c := bufferedCore(t, func(s *Settings) { s.Log.Output, s.Log.Level = &buf, "error" })
	ctx := c.BeginRequest(t.Context(), nil)
	c.logEvent(ctx, slog.LevelWarn, eventObserverFault)

	if got := buf.records(t); len(got) != 0 {
		t.Fatalf("own WARN record written at level ERROR: %v", got)
	}
}

// entryOf returns a record logged through c's handler whose entry size is
// exactly size bytes.
func entryOf(t *testing.T, c *Core, size int) slog.Record {
	t.Helper()

	r := slog.NewRecord(time.Now(), slog.LevelDebug, "fill", 0)
	pad := size - newEntry(t.Context(), c.log, r, false).size - len("pad")
	if pad < 0 {
		t.Fatalf("entry size %d below the overhead", size)
	}

	r.AddAttrs(slog.String("pad", strings.Repeat("p", pad)))

	return r
}

// bufferedCore returns a core with buffering on, logging to nowhere,
// after set changed its settings.
func bufferedCore(tb testing.TB, set func(*Settings)) *Core {
	tb.Helper()

	c, err := ResolveConfig([]Option{func(s *Settings) {
		s.Log.Output = io.Discard
		s.Log.DebugBuffer.Enabled = true
		set(s)
	}})
	if err != nil {
		tb.Fatal(err)
	}

	return c
}

// TestDebugBuffer_Budget covers triage-debug-buffer / Scenario:
// Process-wide budget: requests buffer at the same time, the budget is
// read while they all hold their buffers, then they fail.
func TestDebugBuffer_Budget(t *testing.T) {
	t.Parallel()

	const recordSize = 1024

	for _, tt := range []struct {
		budget, requests, perRequest int
		minTruncated, maxTruncated   int
	}{
		{16 << 20, 10, 64 << 10, 0, 0},
		{64 << 10, 4, 32 << 10, 2, 4},
	} {
		t.Run(strconv.Itoa(tt.budget), func(t *testing.T) {
			t.Parallel()

			c := bufferedCore(t, func(s *Settings) { s.Log.DebugBuffer.TotalBytes = tt.budget })
			logger := slog.New(c.log)
			rec := entryOf(t, c, recordSize)

			var (
				logged, done sync.WaitGroup
				release      = make(chan struct{})
				truncated    atomic.Int32
			)

			logged.Add(tt.requests)
			for range tt.requests {
				done.Go(func() {
					ctx := c.BeginRequest(t.Context(), nil)
					for range tt.perRequest / recordSize {
						_ = logger.Handler().Handle(ctx, rec)
					}
					logged.Done()
					<-release

					req := requestFrom(ctx)
					req.buffer.mu.Lock()
					if req.buffer.truncated {
						truncated.Add(1)
					}
					req.buffer.mu.Unlock()
					c.WriteAccess(ctx, AccessInfo{Method: "GET", Status: 500})
				})
			}

			logged.Wait()
			if used := c.log.buffering.budget.used.Load(); used > int64(tt.budget) {
				t.Errorf("buffered %d bytes, budget %d", used, tt.budget)
			}
			close(release)
			done.Wait()

			if n := int(truncated.Load()); n < tt.minTruncated || n > tt.maxTruncated {
				t.Errorf("%d requests truncated, want %d-%d", n, tt.minTruncated, tt.maxTruncated)
			}
			if used := c.log.buffering.budget.used.Load(); used != 0 {
				t.Errorf("budget holds %d bytes after every request ended", used)
			}
		})
	}
}

// TestDebugBuffer_BudgetReleased: after 1000 mixed requests (flushed,
// discarded, evicting, dropping) the budget holds nothing.
func TestDebugBuffer_BudgetReleased(t *testing.T) {
	t.Parallel()

	c := bufferedCore(t, func(s *Settings) {
		s.Log.DebugBuffer = DebugBufferConfig{Enabled: true, MaxRecords: 5, MaxBytes: 4096, TotalBytes: 64 << 10}
	})
	logger := slog.New(c.log)

	var wg sync.WaitGroup
	for i := range 1000 {
		wg.Go(func() {
			ctx := c.BeginRequest(t.Context(), nil)
			for n := range i % 9 {
				logger.DebugContext(ctx, "step", "n", n, "pad", strings.Repeat("p", i%7*300))
			}

			c.WriteAccess(ctx, AccessInfo{Method: "GET", Status: []int{200, 404, 500}[i%3]})
		})
	}
	wg.Wait()

	if used := c.log.buffering.budget.used.Load(); used != 0 {
		t.Fatalf("budget holds %d bytes after 1000 requests", used)
	}
}

// TestDebugBuffer_OutsideRequest: with buffering on, a below-level record
// logged without a request context is not enabled and goes nowhere.
func TestDebugBuffer_OutsideRequest(t *testing.T) {
	t.Parallel()

	c := bufferedCore(t, func(*Settings) {})
	if c.log.Enabled(t.Context(), slog.LevelDebug) {
		t.Fatal("DEBUG enabled outside a request")
	}

	if err := c.log.Handle(t.Context(), slog.NewRecord(time.Now(), slog.LevelDebug, "x", 0)); err != nil {
		t.Fatal(err)
	}

	if used := c.log.buffering.budget.used.Load(); used != 0 {
		t.Fatalf("a record outside a request reserved %d bytes", used)
	}
}

// TestDebugBuffer_EvictionReleases: evicting a record larger than the one
// that replaces it returns the difference to the budget.
func TestDebugBuffer_EvictionReleases(t *testing.T) {
	t.Parallel()

	c := bufferedCore(t, func(s *Settings) { s.Log.DebugBuffer.MaxRecords = 1 })
	ctx := c.BeginRequest(t.Context(), nil)
	budget := c.log.buffering.budget

	// Both fit the 1024-byte attribute cap, so buffering does not cut them.
	for _, size := range []int{1300, 900} {
		if err := c.log.Handle(ctx, entryOf(t, c, size)); err != nil {
			t.Fatal(err)
		}

		if used := budget.used.Load(); used != int64(size) {
			t.Fatalf("after a %d-byte record the budget holds %d bytes", size, used)
		}
	}

	c.WriteAccess(ctx, AccessInfo{Method: "GET", Status: 200})

	if used := budget.used.Load(); used != 0 {
		t.Fatalf("budget holds %d bytes after the request", used)
	}
}

// TestStaticLevel: the log level is known at construction only when it is
// set by a valid name or left to the default.
func TestStaticLevel(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		ls     LogSettings
		level  slog.Level
		static bool
	}{
		{"default", LogSettings{}, slog.LevelInfo, true},
		{"named", LogSettings{Level: "warn"}, slog.LevelWarn, true},
		{"invalid name", LogSettings{Level: "loud"}, 0, false},
		{"LevelVar", LogSettings{LevelVar: new(slog.LevelVar)}, 0, false},
		{"consumer handler", LogSettings{Handler: slog.DiscardHandler}, 0, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			level, static := tt.ls.staticLevel()
			if static != tt.static || (static && level != tt.level) {
				t.Fatalf("staticLevel = %v, %v; want %v, %v", level, static, tt.level, tt.static)
			}
		})
	}
}
