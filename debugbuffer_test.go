package srvr_test

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// accessEvents are the messages of access records.
var accessEvents = []string{"http.request.completed", "http.request.failed"}

// requestRecords serves method path with h on a server logging to a buffer
// with opts, sends one request and returns the records written with the
// request's trace (lifecycle records left out), in output order, once the
// access record is written.
func requestRecords(t *testing.T, method, path string, h srvr.HandlerFunc, opts ...srvr.Option) []map[string]any {
	t.Helper()

	base, out := loggedServer(t, func(g *srvr.Group) { g.Handle(method, path, h) }, opts...)
	do(t, method, base+path, nil, nil)

	return requestTrail(t, out)
}

// requestTrail waits for the access record in out and returns the records
// that carry a trace_id, in output order.
func requestTrail(t *testing.T, out *logBuffer) []map[string]any {
	t.Helper()

	eventually(t, func() bool {
		return slices.ContainsFunc(out.records(t), func(r map[string]any) bool {
			msg, _ := r["msg"].(string)

			return slices.Contains(accessEvents, msg)
		})
	}, "no access record in:\n"+out.String())

	var trail []map[string]any
	for _, r := range out.records(t) {
		if _, ok := r["trace_id"]; ok {
			trail = append(trail, r)
		}
	}

	return trail
}

// messages returns the msg of every record.
func messages(records []map[string]any) []string {
	out := make([]string, len(records))
	for i, r := range records {
		out[i], _ = r["msg"].(string)
	}

	return out
}

// logDebug returns a handler that logs DEBUG msgs with the request
// context, then ends with then.
func logDebug(then srvr.HandlerFunc, msgs ...string) srvr.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		for _, msg := range msgs {
			srvr.LoggerFromContext(r.Context()).DebugContext(r.Context(), msg)
		}

		return then(w, r)
	}
}

// TestDebugBuffer_Off covers triage-debug-buffer / Scenario: Default drops
// below-level records.
func TestDebugBuffer_Off(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    []srvr.Option
		outcome srvr.HandlerFunc
	}{
		{"none, error", nil, returning(errors.New("boom"))},
		{"none, 200", nil, status(http.StatusOK)},
		{"disabled config, panic", []srvr.Option{srvr.WithDebugBufferConfig(srvr.DebugBufferConfig{Enabled: false})}, panicking},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			trail := requestRecords(t, http.MethodGet, "/users/42", logDebug(tt.outcome, "loading user"), tt.opts...)
			if slices.Contains(messages(trail), "loading user") {
				t.Fatalf("records %v contain loading user", messages(trail))
			}
		})
	}
}

// logInfo returns a handler that logs INFO msg with the request context,
// then ends with then.
func logInfo(msg string, then srvr.HandlerFunc) srvr.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) error {
		srvr.LoggerFromContext(r.Context()).InfoContext(r.Context(), msg)

		return then(w, r)
	}
}

// TestDebugBuffer_Flush covers triage-debug-buffer / Scenario: Flush on
// failure.
func TestDebugBuffer_Flush(t *testing.T) {
	t.Parallel()

	failed := []string{"step one", "step two", "http.request.failed"}

	tests := []struct {
		name     string
		outcome  srvr.HandlerFunc
		sequence []string
	}{
		{"error", returning(errors.New("db down")), failed},
		{"checkpoint then error", logInfo("checkpoint", returning(errors.New("db down"))), slices.Concat([]string{"checkpoint"}, failed)},
		{"panic", panicking, failed},
		{"503", returning(prblm.WithStatus(errors.New("x"), http.StatusServiceUnavailable)), failed},
		{"late error", late(http.StatusOK, func() error { return errors.New("late") }), failed},
		{"direct 500", status(http.StatusInternalServerError), []string{"step one", "step two", "http.request.completed"}},
		{"200", status(http.StatusOK), []string{"http.request.completed"}},
		{"not found", returning(prblm.ErrNotFound), []string{"http.request.failed"}},
		{"201 then conflict", late(http.StatusCreated, func() error { return prblm.ErrConflict }), []string{"http.request.failed"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			trail := requestRecords(t, http.MethodPost, "/registrations", logDebug(tt.outcome, "step one", "step two"), srvr.WithDebugBuffer())
			if got := messages(trail); !slices.Equal(got, tt.sequence) {
				t.Fatalf("sequence %s, want %s", strings.Join(got, ", "), strings.Join(tt.sequence, ", "))
			}
		})
	}
}

// recordTime returns the time of rec.
func recordTime(t *testing.T, rec map[string]any) time.Time {
	t.Helper()

	s, _ := rec["time"].(string)
	at, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("record %v: time: %v", rec, err)
	}

	return at
}

// TestDebugBuffer_Marking covers triage-debug-buffer / Scenario: Marking of
// flushed records. The clock is the real one: the handler notes the time
// around its DEBUG call and fails 20ms later, so a flushed record stamped
// at flush time falls outside the window.
func TestDebugBuffer_Marking(t *testing.T) {
	t.Parallel()

	var loggedFrom, loggedTo time.Time

	h := func(_ http.ResponseWriter, r *http.Request) error {
		loggedFrom = time.Now()
		srvr.LoggerFromContext(r.Context()).DebugContext(r.Context(), "step one", "user.id", 42)
		loggedTo = time.Now()
		time.Sleep(20 * time.Millisecond)

		return errors.New("boom")
	}

	base, out := loggedServer(t, func(g *srvr.Group) { g.GET("/x", h) }, srvr.WithDebugBuffer())
	do(t, http.MethodGet, base+"/x", nil, map[string]string{"traceparent": incoming})

	trail := requestTrail(t, out)
	if got := messages(trail); !slices.Equal(got, []string{"step one", "http.request.failed"}) {
		t.Fatalf("sequence %v", got)
	}

	step, failed := trail[0], trail[1]
	if at := recordTime(t, step); at.Before(loggedFrom.Truncate(time.Microsecond)) || at.After(loggedTo) {
		t.Errorf("step one time %v outside the logging window %v..%v", at, loggedFrom, loggedTo)
	}

	if step["level"] != "DEBUG" || step["srvr.buffered"] != true || step["user.id"] != float64(42) || step["trace_id"] != incomingTrace {
		t.Errorf("step one record %v", step)
	}

	if at := recordTime(t, failed); at.Sub(loggedTo) < 20*time.Millisecond {
		t.Errorf("failed record time %v not after the handler's wait (logged by %v)", at, loggedTo)
	}

	if _, ok := failed["srvr.buffered"]; ok {
		t.Errorf("failed record carries srvr.buffered: %v", failed)
	}
}

// TestDebugBuffer_Threshold covers triage-debug-buffer / Scenario: Flush
// threshold, default rows.
func TestDebugBuffer_Threshold(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		status  int
		written bool
	}{
		{http.StatusOK, false},
		{499, false},
		{http.StatusInternalServerError, true},
		{599, true},
	} {
		t.Run(http.StatusText(tt.status)+strconv.Itoa(tt.status), func(t *testing.T) {
			t.Parallel()

			trail := requestRecords(t, http.MethodGet, "/x", logDebug(status(tt.status), "ctx"), srvr.WithDebugBuffer())
			if got := len(where(trail, "msg", "ctx")) == 1; got != tt.written {
				t.Fatalf("ctx written = %v, want %v: %v", got, tt.written, messages(trail))
			}
		})
	}
}

// TestDebugBuffer_LevelRouting covers triage-debug-buffer / Scenario: Level
// routing.
func TestDebugBuffer_LevelRouting(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		logLevel, recordLevel slog.Level
		times                 int
		buffered              bool
	}{
		{slog.LevelInfo, slog.LevelDebug, 1, true},
		{slog.LevelInfo, slog.LevelInfo, 1, false},
		{slog.LevelInfo, slog.LevelError, 1, false},
		{slog.LevelInfo, slog.LevelDebug - 4, 0, false},
		{slog.LevelWarn, slog.LevelInfo, 1, true},
		{slog.LevelDebug, slog.LevelDebug, 1, false},
	} {
		t.Run(tt.logLevel.String()+"/"+tt.recordLevel.String(), func(t *testing.T) {
			t.Parallel()

			h := func(_ http.ResponseWriter, r *http.Request) error {
				srvr.LoggerFromContext(r.Context()).Log(r.Context(), tt.recordLevel, "record")

				return errors.New("boom")
			}

			trail := requestRecords(t, http.MethodGet, "/x", h, srvr.WithDebugBuffer(), srvr.WithLogLevel(tt.logLevel))
			got := where(trail, "msg", "record")
			if len(got) != tt.times {
				t.Fatalf("record written %d times, want %d: %v", len(got), tt.times, messages(trail))
			}

			if tt.times == 1 {
				if _, marked := got[0]["srvr.buffered"]; marked != tt.buffered {
					t.Fatalf("record %v: srvr.buffered present = %v, want %v", got[0], marked, tt.buffered)
				}
			}
		})
	}
}

// logNumbered returns a handler that logs n DEBUG records of about 100
// bytes, numbered from 1 in the attribute n, then returns boom.
func logNumbered(n int) srvr.HandlerFunc {
	pad := strings.Repeat("p", 80)

	return func(_ http.ResponseWriter, r *http.Request) error {
		logger := srvr.LoggerFromContext(r.Context())
		for i := range n {
			logger.DebugContext(r.Context(), "numbered", "n", i+1, "pad", pad)
		}

		return errors.New("boom")
	}
}

// numbers returns the n attribute of every numbered record.
func numbers(records []map[string]any) []int {
	numbered := where(records, "msg", "numbered")
	out := make([]int, 0, len(numbered))
	for _, r := range numbered {
		n, _ := r["n"].(float64)
		out = append(out, int(n))
	}

	return out
}

// span returns the numbers from..to.
func span(from, to int) []int {
	out := make([]int, 0, max(to-from+1, 0))
	for i := from; i <= to; i++ {
		out = append(out, i)
	}

	return out
}

// TestDebugBuffer_DefaultLimits covers triage-debug-buffer / Scenario:
// Per-request limits, default-limit rows.
func TestDebugBuffer_DefaultLimits(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		logged         int
		flushed        []int
		count, dropped int
	}{
		{0, nil, 0, 0},
		{1, span(1, 1), 1, 0},
		{100, span(1, 100), 100, 0},
		{101, span(2, 101), 100, 1},
	} {
		t.Run(strconv.Itoa(tt.logged), func(t *testing.T) {
			t.Parallel()

			trail := requestRecords(t, http.MethodGet, "/x", logNumbered(tt.logged), srvr.WithDebugBuffer())
			if got := numbers(trail); !slices.Equal(got, tt.flushed) {
				t.Fatalf("flushed %v, want %v", got, tt.flushed)
			}

			checkSummary(t, trail, tt.count, tt.dropped)
		})
	}
}

// logLarge returns a handler that logs one DEBUG numbered record whose
// attr holds 5000 bytes of x, then returns boom.
func logLarge(attr func(string) slog.Attr) srvr.HandlerFunc {
	return func(_ http.ResponseWriter, r *http.Request) error {
		srvr.LoggerFromContext(r.Context()).LogAttrs(r.Context(), slog.LevelDebug, "numbered", slog.Int("n", 1), attr(strings.Repeat("x", 5000)))

		return errors.New("boom")
	}
}

// TestDebugBuffer_Limits covers triage-debug-buffer / Scenario: Per-request
// limits, over-limit rows.
func TestDebugBuffer_Limits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    []srvr.DebugBufferOption
		handler srvr.HandlerFunc
		// flushed checks the numbers flushed.
		flushed func([]int) bool
		// logged is the number of records the handler logs.
		logged int
	}{
		{"MaxRecords 10", []srvr.DebugBufferOption{srvr.MaxRecords(10)}, logNumbered(25), func(got []int) bool { return slices.Equal(got, span(16, 25)) }, 25},
		{"MaxBytes 1024", []srvr.DebugBufferOption{srvr.MaxBytes(1024)}, logNumbered(50), func(got []int) bool {
			return len(got) >= 1 && len(got) <= 10 && slices.Equal(got, span(51-len(got), 50))
		}, 50},
		{"MaxBytes 50", []srvr.DebugBufferOption{srvr.MaxBytes(50)}, logNumbered(1), func(got []int) bool { return len(got) == 0 }, 1},
		{"MaxBytes 2048, long attribute", []srvr.DebugBufferOption{srvr.MaxBytes(2048)}, logLarge(func(s string) slog.Attr { return slog.String("note", s) }), func(got []int) bool { return slices.Equal(got, []int{1}) }, 1},
		{"MaxBytes 2048, long error", []srvr.DebugBufferOption{srvr.MaxBytes(2048)}, logLarge(func(s string) slog.Attr { return slog.Any("err", errors.New(s)) }), func(got []int) bool { return slices.Equal(got, []int{1}) }, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			trail := requestRecords(t, http.MethodGet, "/x", tt.handler, srvr.WithDebugBuffer(tt.opts...))
			got := numbers(trail)
			if !tt.flushed(got) {
				t.Fatalf("flushed %v", got)
			}

			checkSummary(t, trail, len(got), tt.logged-len(got))
		})
	}
}

// failedOf returns the one failed record in trail.
func failedOf(t *testing.T, trail []map[string]any) map[string]any {
	t.Helper()

	failed := where(trail, "msg", "http.request.failed")
	if len(failed) != 1 {
		t.Fatalf("%d failed records in %v", len(failed), messages(trail))
	}

	return failed[0]
}

// checkSummary checks the debug buffer summary of the failed record in
// trail.
func checkSummary(t *testing.T, trail []map[string]any, flushed, dropped int) {
	t.Helper()

	rec := failedOf(t, trail)
	if rec["srvr.debug_buffer.flushed"] != float64(flushed) || rec["srvr.debug_buffer.dropped"] != float64(dropped) {
		t.Fatalf("summary flushed %v dropped %v, want %d and %d", rec["srvr.debug_buffer.flushed"], rec["srvr.debug_buffer.dropped"], flushed, dropped)
	}
}

// TestDebugBuffer_Summary covers triage-debug-buffer / Scenario: Summary
// attributes.
func TestDebugBuffer_Summary(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		opts    []srvr.Option
		logged  int
		summary map[string]any // nil: no srvr.debug_buffer.* keys
	}{
		{"enabled, 3", []srvr.Option{srvr.WithDebugBuffer()}, 3, map[string]any{"flushed": float64(3), "dropped": float64(0), "truncated": false}},
		{"enabled, 0", []srvr.Option{srvr.WithDebugBuffer()}, 0, map[string]any{"flushed": float64(0), "dropped": float64(0), "truncated": false}},
		{"disabled, 3", nil, 3, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := failedOf(t, requestRecords(t, http.MethodGet, "/x", logNumbered(tt.logged), tt.opts...))
			for _, name := range []string{"flushed", "dropped", "truncated"} {
				got, ok := rec["srvr.debug_buffer."+name]
				if want, wanted := tt.summary[name]; ok != wanted || got != want {
					t.Errorf("srvr.debug_buffer.%s = %v (present %v), want %v (present %v)", name, got, ok, want, wanted)
				}
			}
		})
	}
}

// TestDebugBuffer_InvalidLimits covers triage-debug-buffer / Scenario:
// Invalid limits.
func TestDebugBuffer_InvalidLimits(t *testing.T) {
	t.Parallel()

	buffer := func(c srvr.DebugBufferConfig) srvr.Option {
		c.Enabled = true

		return srvr.WithDebugBufferConfig(c)
	}

	tests := []struct {
		name   string
		buffer srvr.Option
		// handler is a consumer's log handler; nil logs to io.Discard.
		handler slog.Handler
		wantErr bool
		// field is named by the error; empty for an error naming no
		// particular field.
		field string
	}{
		{name: "MaxRecords 0", buffer: buffer(srvr.DebugBufferConfig{MaxRecords: 0})},
		{name: "MaxRecords -1", buffer: buffer(srvr.DebugBufferConfig{MaxRecords: -1}), wantErr: true, field: "MaxRecords"},
		{name: "MaxBytes -1", buffer: buffer(srvr.DebugBufferConfig{MaxBytes: -1}), wantErr: true, field: "MaxBytes"},
		{name: "TotalBytes below MaxBytes", buffer: buffer(srvr.DebugBufferConfig{TotalBytes: 1024, MaxBytes: 65536}), wantErr: true, field: "TotalBytes"},
		{name: "MaxBytes 0", buffer: buffer(srvr.DebugBufferConfig{MaxBytes: 0})},
		{name: "TotalBytes 0", buffer: buffer(srvr.DebugBufferConfig{TotalBytes: 0})},
		{name: "TotalBytes equal to MaxBytes", buffer: buffer(srvr.DebugBufferConfig{TotalBytes: 65536, MaxBytes: 65536})},
		{name: "Level loud", buffer: buffer(srvr.DebugBufferConfig{Level: "loud"}), wantErr: true, field: "Level"},
		{name: "Level warn above INFO", buffer: buffer(srvr.DebugBufferConfig{Level: "warn"}), wantErr: true, field: "Level"},
		{name: "Level info at INFO", buffer: buffer(srvr.DebugBufferConfig{Level: "info"})},
		{name: "Level warn with a handler", buffer: buffer(srvr.DebugBufferConfig{Level: "warn"}), handler: slog.DiscardHandler},
		{name: "nil option", buffer: srvr.WithDebugBuffer(nil), wantErr: true},
		{name: "MaxRecords option -1", buffer: srvr.WithDebugBuffer(srvr.MaxRecords(-1)), wantErr: true, field: "MaxRecords"},
		{name: "BufferLevel option WARN above INFO", buffer: srvr.WithDebugBuffer(srvr.BufferLevel(slog.LevelWarn)), wantErr: true, field: "Level"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logTo := srvr.WithLogOutput(io.Discard)
			if tt.handler != nil {
				logTo = srvr.WithLogHandler(tt.handler)
			}

			_, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), logTo, tt.buffer)

			var ce *srvr.ConfigError
			switch {
			case !tt.wantErr && err != nil:
				t.Fatalf("New = %v, want a server", err)
			case tt.wantErr && (!errors.Is(err, srvr.ErrConfig) || !errors.As(err, &ce) || !strings.Contains(err.Error(), tt.field)):
				t.Fatalf("New = %v, want ErrConfig naming %q", err, tt.field)
			}
		})
	}
}
