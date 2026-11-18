package srvr_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
		// Flush threshold, rejected rows, and the accepted bounds.
		{name: "FlushStatus 99", buffer: srvr.WithDebugBuffer(srvr.FlushAtStatus(99)), wantErr: true, field: "FlushStatus"},
		{name: "FlushStatus 600", buffer: srvr.WithDebugBuffer(srvr.FlushAtStatus(600)), wantErr: true, field: "FlushStatus"},
		{name: "FlushStatus 100", buffer: srvr.WithDebugBuffer(srvr.FlushAtStatus(100))},
		{name: "FlushStatus 599", buffer: srvr.WithDebugBuffer(srvr.FlushAtStatus(599))},
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

// jsonLogConfig returns the LogConfig decoded from document, a constant
// of the test, panicking on a mistake in it.
func jsonLogConfig(document string) srvr.LogConfig {
	var c srvr.LogConfig
	if err := json.Unmarshal([]byte(document), &c); err != nil {
		panic(err)
	}

	return c
}

// TestDebugBuffer_Config covers triage-debug-buffer / Scenario:
// Configuration sources.
func TestDebugBuffer_Config(t *testing.T) {
	t.Parallel()

	enabled400 := srvr.DebugBufferConfig{Enabled: true, FlushStatus: 400}

	tests := []struct {
		name    string
		opts    []srvr.Option
		written bool
	}{
		{"WithDebugBuffer()", []srvr.Option{srvr.WithDebugBuffer()}, false},
		{"FlushAtStatus(400)", []srvr.Option{srvr.WithDebugBuffer(srvr.FlushAtStatus(400))}, true},
		{"JSON enabled, 400", []srvr.Option{srvr.WithLogConfig(jsonLogConfig(`{"debug_buffer":{"enabled":true,"flush_status":400}}`))}, true},
		{"JSON enabled", []srvr.Option{srvr.WithLogConfig(jsonLogConfig(`{"debug_buffer":{"enabled":true}}`))}, false},
		{"JSON 400, not enabled", []srvr.Option{srvr.WithLogConfig(jsonLogConfig(`{"debug_buffer":{"flush_status":400}}`))}, false},
		{"config enabled, 400", []srvr.Option{srvr.WithDebugBufferConfig(enabled400)}, true},
		{"config 400, not enabled", []srvr.Option{srvr.WithDebugBufferConfig(srvr.DebugBufferConfig{FlushStatus: 400})}, false},
		{"config 400, then option 500", []srvr.Option{srvr.WithDebugBufferConfig(enabled400), srvr.WithDebugBuffer(srvr.FlushAtStatus(500))}, false},
		{"option 500, then config 400", []srvr.Option{srvr.WithDebugBuffer(srvr.FlushAtStatus(500)), srvr.WithDebugBufferConfig(enabled400)}, true},
		{"BufferLevel INFO, 400", []srvr.Option{srvr.WithDebugBuffer(srvr.BufferLevel(slog.LevelInfo), srvr.FlushAtStatus(400))}, false},
		{"option 400, then WithLogConfig", []srvr.Option{srvr.WithDebugBuffer(srvr.FlushAtStatus(400)), srvr.WithLogConfig(srvr.LogConfig{Format: "json"})}, false},
		{"WithLogConfig, then option 400", []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Format: "json"}), srvr.WithDebugBuffer(srvr.FlushAtStatus(400))}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			trail := requestRecords(t, http.MethodGet, "/x", logDebug(returning(prblm.ErrNotFound), "ctx"), tt.opts...)
			if got := len(where(trail, "msg", "ctx")) == 1; got != tt.written {
				t.Fatalf("ctx written = %v, want %v: %v", got, tt.written, messages(trail))
			}
		})
	}
}

// TestDebugBuffer_ThresholdConfig covers triage-debug-buffer / Scenario:
// Flush threshold, lowered rows; the rejected rows are in
// TestDebugBuffer_InvalidLimits.
func TestDebugBuffer_ThresholdConfig(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		status  int
		written bool
	}{
		{399, false},
		{400, true},
		{404, true},
	} {
		t.Run(strconv.Itoa(tt.status), func(t *testing.T) {
			t.Parallel()

			trail := requestRecords(t, http.MethodGet, "/x", logDebug(status(tt.status), "ctx"), srvr.WithDebugBuffer(srvr.FlushAtStatus(400)))
			if got := len(where(trail, "msg", "ctx")) == 1; got != tt.written {
				t.Fatalf("ctx written = %v, want %v: %v", got, tt.written, messages(trail))
			}
		})
	}
}

// TestDebugBuffer_Suppressed covers triage-debug-buffer / Scenario:
// Suppressed access record. The handler writes the status itself: only
// http.request.completed records can be suppressed.
func TestDebugBuffer_Suppressed(t *testing.T) {
	t.Parallel()

	skip := srvr.WithAccessLogConfig(srvr.AccessLogConfig{SkipPaths: []string{"/healthz"}})
	flush400 := srvr.WithDebugBuffer(srvr.FlushAtStatus(400))

	for _, tt := range []struct {
		name    string
		opts    []srvr.Option
		status  int
		written bool
		record  bool // an http.request.completed record is written
	}{
		{"skip path, 400", []srvr.Option{flush400, skip}, http.StatusNotFound, false, false},
		{"disabled, 400", []srvr.Option{flush400, srvr.WithAccessLogConfig(srvr.AccessLogConfig{Disabled: true})}, http.StatusNotFound, false, false},
		{"level ERROR, 400", []srvr.Option{flush400, srvr.WithLogLevel(slog.LevelError)}, http.StatusNotFound, false, false},
		{"none, 400", []srvr.Option{flush400}, http.StatusNotFound, true, true},
		{"skip path, 500", []srvr.Option{srvr.WithDebugBuffer(), skip}, http.StatusInternalServerError, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) {
				g.GET("/healthz", logDebug(status(tt.status), "ctx"))
				g.GET("/barrier", returning(errors.New("boom")))
			}, tt.opts...)
			if got := get(t, base+"/healthz").status; got != tt.status {
				t.Fatalf("status %d, want %d", got, tt.status)
			}

			// The next request on the same keep-alive connection is read
			// only after the previous one, its records included, finished.
			get(t, base+"/barrier")
			eventually(t, func() bool { return len(out.where(t, "http.route", "/barrier")) == 1 }, "no barrier record")

			records := out.records(t)
			if got := len(where(records, "msg", "ctx")) == 1; got != tt.written {
				t.Errorf("ctx written = %v, want %v: %v", got, tt.written, messages(records))
			}

			var want []string
			if tt.record {
				want = []string{"http.request.completed"}
			}

			if got := messages(where(records, "http.route", "/healthz")); !slices.Equal(got, want) {
				t.Errorf("/healthz access records %v, want %v", got, want)
			}
		})
	}
}

// TestDebugBuffer_LevelVar covers triage-debug-buffer / Scenario: Runtime
// level change during a request.
func TestDebugBuffer_LevelVar(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		newLevel      slog.Level
		afterBuffered bool
	}{
		{slog.LevelDebug, false},
		{slog.LevelWarn, true},
	} {
		t.Run(tt.newLevel.String(), func(t *testing.T) {
			t.Parallel()

			lv := new(slog.LevelVar) // INFO
			before, proceed := make(chan struct{}), make(chan struct{})

			h := func(_ http.ResponseWriter, r *http.Request) error {
				logger := srvr.LoggerFromContext(r.Context())
				logger.DebugContext(r.Context(), "before")
				close(before)
				<-proceed
				logger.DebugContext(r.Context(), "after")

				return errors.New("boom")
			}

			base, out := loggedServer(t, func(g *srvr.Group) { g.GET("/x", h) }, srvr.WithLogLevelVar(lv), srvr.WithDebugBuffer())

			done := make(chan response, 1)
			go func() { done <- get(t, base+"/x") }()

			receive(t, before, "the handler did not log before")
			lv.Set(tt.newLevel)
			close(proceed)
			receive(t, done, "no response")

			trail := requestTrail(t, out)
			for _, c := range []struct {
				msg      string
				buffered bool
			}{{"before", true}, {"after", tt.afterBuffered}} {
				got := where(trail, "msg", c.msg)
				if len(got) != 1 {
					t.Fatalf("%s written %d times: %v", c.msg, len(got), messages(trail))
				}

				if _, marked := got[0]["srvr.buffered"]; marked != c.buffered {
					t.Errorf("%s: srvr.buffered present = %v, want %v", c.msg, marked, c.buffered)
				}
			}
		})
	}
}

// tenantKey is the context key of the tenant a consumer handler logs.
type tenantKey struct{}

// hookHandler is a consumer handler around a JSON handler: handle replaces
// its Handle, given the wrapped handler to pass the record on to, and
// enabled, when set, its Enabled.
type hookHandler struct {
	slog.Handler

	handle  func(ctx context.Context, r slog.Record, next slog.Handler) error
	enabled func(slog.Level) bool
}

func (h hookHandler) Enabled(ctx context.Context, l slog.Level) bool {
	if h.enabled != nil {
		return h.enabled(l)
	}

	return h.Handler.Enabled(ctx, l)
}

func (h hookHandler) Handle(ctx context.Context, r slog.Record) error {
	return h.handle(ctx, r, h.Handler)
}

func (h hookHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	h.Handler = h.Handler.WithAttrs(attrs)

	return h
}

func (h hookHandler) WithGroup(name string) slog.Handler {
	h.Handler = h.Handler.WithGroup(name)

	return h
}

// consumerServer starts a server logging through the consumer handler
// handler(out) with debug buffering on and opts, serving GET /x with h,
// and returns its base URL and out.
func consumerServer(t *testing.T, handler func(io.Writer) slog.Handler, h srvr.HandlerFunc, opts ...srvr.Option) (string, *logBuffer) {
	t.Helper()

	out := &logBuffer{}
	srv := newServer(t, append([]srvr.Option{srvr.WithLogHandler(handler(out)), srvr.WithDebugBuffer()}, opts...)...)
	mustNil(t, srv.Register(on("", "/x", h)))

	return startServer(t, srv), out
}

// addTenant is a hookHandler handle that adds the tenant of the record's
// context.
func addTenant(ctx context.Context, r slog.Record, next slog.Handler) error {
	if tenant, ok := ctx.Value(tenantKey{}).(string); ok {
		r = r.Clone()
		r.AddAttrs(slog.String("tenant", tenant))
	}

	return next.Handle(ctx, r)
}

// flushedRecord returns the one record "a" in trail, failing unless it is
// marked srvr.buffered at the top level and carries the request's trace.
func flushedRecord(t *testing.T, trail []map[string]any) map[string]any {
	t.Helper()

	got := where(trail, "msg", "a")
	if len(got) != 1 {
		t.Fatalf("a written %d times: %v", len(got), messages(trail))
	}

	rec := got[0]
	if rec["srvr.buffered"] != true || rec["trace_id"] != incomingTrace {
		t.Fatalf("record %v lacks a top-level srvr.buffered or the request's trace_id", rec)
	}

	return rec
}

// TestDebugBuffer_Derived covers triage-debug-buffer / Scenario: Derived
// loggers; the slog.Default row is TestDebugBuffer_DerivedDefault. The
// server writes through a consumer handler that adds the tenant of the
// logging context, which changes nothing without one.
func TestDebugBuffer_Derived(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		log   func(r *http.Request)
		check func(rec map[string]any) bool
	}{
		{"LoggerFromContext", func(r *http.Request) {
			srvr.LoggerFromContext(r.Context()).Debug("a")
		}, func(map[string]any) bool { return true }},
		{"With", func(r *http.Request) {
			srvr.LoggerFromContext(r.Context()).With("repo", "users").Debug("a")
		}, func(rec map[string]any) bool { return rec["repo"] == "users" }},
		{"WithGroup then With", func(r *http.Request) {
			srvr.LoggerFromContext(r.Context()).WithGroup("db").With("table", "users").Debug("a")
		}, func(rec map[string]any) bool {
			db, _ := rec["db"].(map[string]any)

			return db["table"] == "users" && db["trace_id"] == nil && db["srvr.buffered"] == nil
		}},
		{"derived context", func(r *http.Request) {
			srvr.LoggerFromContext(r.Context()).DebugContext(context.WithValue(r.Context(), tenantKey{}, "acme"), "a")
		}, func(rec map[string]any) bool { return rec["tenant"] == "acme" }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tenants := func(w io.Writer) slog.Handler {
				return hookHandler{Handler: slog.NewJSONHandler(w, nil), handle: addTenant}
			}
			base, out := consumerServer(t, tenants, func(_ http.ResponseWriter, r *http.Request) error { tt.log(r); return errors.New("boom") })
			do(t, http.MethodGet, base+"/x", nil, map[string]string{"traceparent": incoming})

			if rec := flushedRecord(t, requestTrail(t, out)); !tt.check(rec) {
				t.Fatalf("record %v", rec)
			}
		})
	}
}

// TestDebugBuffer_DerivedDefault covers the slog.Default row of
// triage-debug-buffer / Scenario: Derived loggers. It changes the process
// default logger, so it does not run in parallel.
//
//nolint:paralleltest // slog.SetDefault is process-wide.
func TestDebugBuffer_DerivedDefault(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	base, out := loggedServer(t, func(g *srvr.Group) {
		g.GET("/x", func(_ http.ResponseWriter, r *http.Request) error {
			slog.Default().DebugContext(r.Context(), "a")

			return errors.New("boom")
		})
	}, srvr.WithSetDefaultLogger(), srvr.WithDebugBuffer())
	do(t, http.MethodGet, base+"/x", nil, map[string]string{"traceparent": incoming})

	flushedRecord(t, requestTrail(t, out))
}

// TestDebugBuffer_Concurrent covers triage-debug-buffer / Scenario:
// Concurrent logging in one request.
func TestDebugBuffer_Concurrent(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ goroutines, each int }{{8, 10}, {50, 10}} {
		t.Run(strconv.Itoa(tt.goroutines), func(t *testing.T) {
			t.Parallel()

			h := func(_ http.ResponseWriter, r *http.Request) error {
				var wg sync.WaitGroup
				for g := range tt.goroutines {
					wg.Go(func() {
						for i := range tt.each {
							srvr.LoggerFromContext(r.Context()).DebugContext(r.Context(), "concurrent", "g", g, "i", i)
						}
					})
				}
				wg.Wait()

				return errors.New("boom")
			}

			rec := failedOf(t, requestRecords(t, http.MethodGet, "/x", h, srvr.WithDebugBuffer()))
			flushed, _ := rec["srvr.debug_buffer.flushed"].(float64)
			dropped, _ := rec["srvr.debug_buffer.dropped"].(float64)
			if total := int(flushed + dropped); total != tt.goroutines*tt.each {
				t.Fatalf("flushed %v + dropped %v = %d, want %d", flushed, dropped, total, tt.goroutines*tt.each)
			}
		})
	}
}

// TestDebugBuffer_Late covers triage-debug-buffer / Scenario: Late
// records: a goroutine of the handler logs with the request context once
// the request's access record is written.
func TestDebugBuffer_Late(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name    string
		level   slog.Level
		outcome srvr.HandlerFunc
		times   int // how often late is written
	}{
		{"DEBUG after an error", slog.LevelDebug, returning(errors.New("boom")), 0},
		{"DEBUG after 200", slog.LevelDebug, status(http.StatusOK), 0},
		{"INFO after an error", slog.LevelInfo, returning(errors.New("boom")), 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			release, logged := make(chan struct{}), make(chan struct{})
			h := func(w http.ResponseWriter, r *http.Request) error {
				ctx := r.Context()
				go func() {
					defer close(logged)
					<-release
					srvr.LoggerFromContext(ctx).Log(ctx, tt.level, "late")
				}()

				return tt.outcome(w, r)
			}

			base, out := loggedServer(t, func(g *srvr.Group) { g.GET("/x", h) }, srvr.WithDebugBuffer())
			get(t, base+"/x")
			requestTrail(t, out) // the access record is written: the request ended
			close(release)
			receive(t, logged, "the late goroutine did not log")

			late := where(out.records(t), "msg", "late")
			if len(late) != tt.times {
				t.Fatalf("late written %d times, want %d", len(late), tt.times)
			}

			if len(late) == 1 && late[0]["srvr.buffered"] != nil {
				t.Fatalf("late record carries srvr.buffered: %v", late[0])
			}
		})
	}
}

// TestDebugBuffer_Faults covers triage-debug-buffer / Scenario: Faulty
// handler during flush: a consumer handler fails on the record "step two".
// srvr.LogWriteErrors is process-wide, so the test does not run in
// parallel with the package's other tests.
//
//nolint:paralleltest // srvr.LogWriteErrors is process-wide.
func TestDebugBuffer_Faults(t *testing.T) {
	for _, tt := range []struct {
		fault   string
		written []string
		errors  uint64
	}{
		{"error", []string{"step one", "step three", "http.request.failed"}, 1},
		{"panic", []string{"step one", "step three", "http.request.failed"}, 1},
		{"", []string{"step one", "step two", "step three", "http.request.failed"}, 0},
	} {
		t.Run("fault "+tt.fault, func(t *testing.T) {
			faulty := func(w io.Writer) slog.Handler {
				return hookHandler{Handler: slog.NewJSONHandler(w, nil), handle: func(ctx context.Context, r slog.Record, next slog.Handler) error {
					switch {
					case r.Message != "step two":
					case tt.fault == "error":
						return errors.New("disk full")
					case tt.fault == "panic":
						panic("handler")
					}

					return next.Handle(ctx, r)
				}}
			}
			base, out := consumerServer(t, faulty, logDebug(returning(errors.New("boom")), "step one", "step two", "step three"))

			before := srvr.LogWriteErrors()
			if got := get(t, base+"/x").status; got != http.StatusInternalServerError {
				t.Fatalf("status %d, want 500", got)
			}

			if got := messages(requestTrail(t, out)); !slices.Equal(got, tt.written) {
				t.Fatalf("written %v, want %v", got, tt.written)
			}

			if got := srvr.LogWriteErrors() - before; got != tt.errors {
				t.Fatalf("LogWriteErrors increased by %d, want %d", got, tt.errors)
			}
		})
	}
}

// TestDebugBuffer_Redaction covers triage-debug-buffer / Scenario:
// Redaction and truncation apply to flushed records. The server writes
// through a consumer JSON handler, with ReplaceAttr when the row sets one.
// A record split across lines would fail to parse in requestTrail.
func TestDebugBuffer_Redaction(t *testing.T) {
	t.Parallel()

	maskSecret := func(_ []string, a slog.Attr) slog.Attr {
		if a.Key == "secret" {
			a.Value = slog.StringValue("***")
		}

		return a
	}

	tests := []struct {
		name    string
		msg     string
		attr    slog.Attr
		opts    []srvr.Option
		replace func([]string, slog.Attr) slog.Attr
		key     string // the key checked: the attribute's, or "msg"
		want    string // a value ending in cut sets srvr.attr.truncated
	}{
		{name: "long note", msg: "a", attr: slog.String("note", xs(5000)), key: "note", want: xs(1024) + cut},
		{name: "long note, MaxAttrBytes 2048", msg: "a", attr: slog.String("note", xs(5000)), opts: []srvr.Option{srvr.WithLogMaxAttrBytes(2048)}, key: "note", want: xs(2048) + cut},
		{name: "long error", msg: "a", attr: slog.Any("err", errors.New(xs(5000))), key: "err", want: xs(1024) + cut},
		{name: "long message", msg: xs(5000), attr: slog.Int("n", 1), key: "msg", want: xs(1024) + cut},
		{name: "newline", msg: "a", attr: slog.String("note", "a\nlevel=ERROR"), key: "note", want: "a\nlevel=ERROR"},
		{name: "consumer ReplaceAttr", msg: "a", attr: slog.String("secret", "hunter2"), replace: maskSecret, key: "secret", want: "***"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := func(w io.Writer) slog.Handler {
				return slog.NewJSONHandler(w, &slog.HandlerOptions{ReplaceAttr: tt.replace})
			}
			base, out := consumerServer(t, handler, func(_ http.ResponseWriter, r *http.Request) error {
				srvr.LoggerFromContext(r.Context()).LogAttrs(r.Context(), slog.LevelDebug, tt.msg, tt.attr)

				return errors.New("boom")
			}, tt.opts...)
			get(t, base+"/x")

			flushed := where(requestTrail(t, out), "srvr.buffered", true)
			if len(flushed) != 1 {
				t.Fatalf("%d flushed records", len(flushed))
			}

			rec := flushed[0]
			if rec[tt.key] != tt.want {
				t.Errorf("%s = %.40q… (%d bytes), want %d bytes", tt.key, rec[tt.key], len(fmt.Sprint(rec[tt.key])), len(tt.want))
			}

			if got, want := rec["srvr.attr.truncated"] == true, strings.HasSuffix(tt.want, cut); got != want {
				t.Errorf("srvr.attr.truncated = %v, want %v", got, want)
			}
		})
	}
}

// TestDebugBuffer_ConsumerFilter covers triage-debug-buffer / Scenario:
// Consumer handler with its own level filter: Enabled is false below INFO,
// and Handle counts the ctx records it receives and, for drop, drops those
// below INFO itself.
func TestDebugBuffer_ConsumerFilter(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		drop   bool
		output int // ctx records in the output
	}{
		{"writes every record", false, 1},
		{"drops below INFO in Handle", true, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var received atomic.Int32
			filter := func(w io.Writer) slog.Handler {
				return hookHandler{
					Handler: slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}),
					enabled: func(l slog.Level) bool { return l >= slog.LevelInfo },
					handle: func(ctx context.Context, r slog.Record, next slog.Handler) error {
						if r.Message == "ctx" {
							received.Add(1)
						}

						if tt.drop && r.Level < slog.LevelInfo {
							return nil
						}

						return next.Handle(ctx, r)
					},
				}
			}
			base, out := consumerServer(t, filter, logDebug(returning(errors.New("boom")), "ctx"))
			get(t, base+"/x")

			trail := requestTrail(t, out)
			if got := received.Load(); got != 1 {
				t.Fatalf("h received ctx %d times, want once", got)
			}

			ctx := where(trail, "msg", "ctx")
			if len(ctx) != tt.output {
				t.Fatalf("ctx in output %d times, want %d", len(ctx), tt.output)
			}

			if len(ctx) == 1 && ctx[0]["srvr.buffered"] != true {
				t.Fatalf("ctx record lacks srvr.buffered: %v", ctx[0])
			}
		})
	}
}
