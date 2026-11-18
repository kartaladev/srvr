package engine

import (
	"log/slog"
	"testing"
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

	c, err := ResolveConfig([]Option{func(s *Settings) {
		s.Log.Output = &buf
		s.Log.Level = "error"
		s.Log.DebugBuffer.Enabled = true
	}})
	if err != nil {
		t.Fatal(err)
	}

	ctx := c.BeginRequest(t.Context(), nil)
	c.logEvent(ctx, slog.LevelWarn, eventObserverFault)

	if got := buf.records(t); len(got) != 0 {
		t.Fatalf("own WARN record written at level ERROR: %v", got)
	}
}
