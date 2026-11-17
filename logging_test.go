package srvr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// logBuffer is a concurrency-safe log output.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// records parses every line as a JSON object.
func (b *logBuffer) records(t *testing.T) []map[string]any {
	t.Helper()

	var out []map[string]any
	for line := range strings.Lines(b.String()) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line %q is not a JSON object: %v", line, err)
		}
		out = append(out, m)
	}

	return out
}

// find returns the records with msg.
func (b *logBuffer) find(t *testing.T, msg string) []map[string]any {
	t.Helper()

	var out []map[string]any
	for _, r := range b.records(t) {
		if r["msg"] == msg {
			out = append(out, r)
		}
	}

	return out
}

// waitRecord waits for one record with msg: access records are written as
// the response finishes, which can be just after the client has it.
func (b *logBuffer) waitRecord(t *testing.T, msg string) map[string]any {
	t.Helper()

	var got []map[string]any
	eventually(t, func() bool { got = b.find(t, msg); return len(got) > 0 }, "no "+msg+" record in:\n"+b.String())
	time.Sleep(10 * time.Millisecond) // a second record would arrive by now
	if got = b.find(t, msg); len(got) != 1 {
		t.Fatalf("%d %s records, want 1:\n%s", len(got), msg, b.String())
	}

	return got[0]
}

// loggedServer returns a started server logging to a buffer.
func loggedServer(t *testing.T, conf configFunc, opts ...srvr.Option) (string, *logBuffer) {
	t.Helper()

	out := &logBuffer{}
	srv := newServer(t, append([]srvr.Option{srvr.WithLogOutput(out)}, opts...)...)
	mustNil(t, srv.Register(conf))

	return startServer(t, srv), out
}

// TestDefaultOutput covers structured-logging / Scenario: Default output.
func TestDefaultOutput(t *testing.T) {
	t.Parallel()

	base, out := loggedServer(t, okRoute("/ping"))
	get(t, base+"/ping")

	rec := out.waitRecord(t, "http.request.completed")
	if rec["level"] != "INFO" {
		t.Fatalf("level = %v, want INFO", rec["level"])
	}
	if _, ok := rec["source"]; ok {
		t.Fatalf("record has source: %v", rec)
	}
}

// TestCorrelation covers structured-logging / Scenario: Correlation keys.
func TestCorrelation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		where string
		log   func(r *http.Request)
		want  bool // the record has the request's trace id
	}{
		{"LoggerFromContext", func(r *http.Request) { srvr.LoggerFromContext(r.Context()).Info("handler") }, true},
		{"LoggerFromContext with group", func(r *http.Request) { srvr.LoggerFromContext(r.Context()).WithGroup("db").Info("handler") }, true},
	}

	for _, tt := range tests {
		t.Run(tt.where, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) {
				g.GET("/log", func(_ http.ResponseWriter, r *http.Request) error { tt.log(r); return nil })
			})
			do(t, http.MethodGet, base+"/log", nil, map[string]string{"traceparent": incoming})

			rec := out.waitRecord(t, "handler")
			if got := rec["trace_id"] == incomingTrace && rec["span_id"] != nil; got != tt.want {
				t.Fatalf("record %v: correlated = %v, want %v", rec, got, tt.want)
			}
		})
	}
}

// TestCorrelation_Groups covers structured-logging / Scenario: Grouped loggers.
func TestCorrelation_Groups(t *testing.T) {
	t.Parallel()

	tests := []struct {
		logger func(l *slog.Logger) *slog.Logger
		shape  string
	}{
		{func(l *slog.Logger) *slog.Logger { return l }, `"k":"v"`},
		{func(l *slog.Logger) *slog.Logger { return l.WithGroup("db") }, `"db":{"k":"v"}`},
		{func(l *slog.Logger) *slog.Logger { return l.WithGroup("a").WithGroup("b") }, `"a":{"b":{"k":"v"}}`},
		{func(l *slog.Logger) *slog.Logger { return l.With("table", "users").WithGroup("db") }, `"table":"users","db":{"k":"v"}`},
		{func(l *slog.Logger) *slog.Logger { return l.WithGroup("db").With("table", "users") }, `"db":{"table":"users","k":"v"}`},
		{func(l *slog.Logger) *slog.Logger { return l.WithGroup("") }, `"k":"v"`},
	}

	for _, tt := range tests {
		t.Run(tt.shape, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) {
				g.GET("/log", func(_ http.ResponseWriter, r *http.Request) error {
					tt.logger(srvr.LoggerFromContext(r.Context())).Info("query", "k", "v")

					return nil
				})
			})
			do(t, http.MethodGet, base+"/log", nil, map[string]string{"traceparent": incoming})

			rec := out.waitRecord(t, "query")
			if rec["trace_id"] != incomingTrace || rec["span_id"] == nil {
				t.Fatalf("record %v lacks top-level trace_id and span_id", rec)
			}
			line := ""
			for l := range strings.Lines(out.String()) {
				if strings.Contains(l, `"msg":"query"`) {
					line = l
				}
			}
			if !strings.Contains(line, tt.shape) || strings.Count(line, "trace_id") != 1 {
				t.Fatalf("line %s does not have shape %s with one trace_id", line, tt.shape)
			}
		})
	}
}

// TestAccessRecord covers structured-logging / Scenario: Access record content.
func TestAccessRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		behaviour string
		handler   srvr.HandlerFunc
		msg       string
		level     string
		status    float64
	}{
		{"writes 200", status(200), "http.request.completed", "INFO", 200},
		{"writes 304", status(304), "http.request.completed", "INFO", 304},
		{"writes 404 and returns nil", status(404), "http.request.completed", "WARN", 404},
		{"returns ErrNotFound", func(http.ResponseWriter, *http.Request) error { return prblm.ErrNotFound }, "http.request.failed", "WARN", 404},
		{"returns boom", func(http.ResponseWriter, *http.Request) error { return errors.New("boom") }, "http.request.failed", "ERROR", 500},
		{"panics", func(http.ResponseWriter, *http.Request) error { panic("boom") }, "http.request.failed", "ERROR", 500},
	}

	for _, tt := range tests {
		t.Run(tt.behaviour, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) { g.GET("/users/{id}", tt.handler) })
			get(t, base+"/users/42")

			rec := out.waitRecord(t, tt.msg)
			if rec["level"] != tt.level || rec["http.request.method"] != "GET" || rec["http.route"] != "/users/{id}" ||
				rec["http.response.status_code"] != tt.status || rec["srvr.log.schema"] != float64(1) {
				t.Fatalf("record = %v", rec)
			}
			if _, ok := rec["duration_ms"].(float64); !ok {
				t.Fatalf("duration_ms is not a number: %v", rec)
			}
		})
	}
}

// TestAccessRecord_Unmatched covers structured-logging / Scenario: Unmatched requests.
func TestAccessRecord_Unmatched(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, path string
		status       float64
	}{
		{http.MethodGet, "/nope", 404},
		{http.MethodDelete, "/users/42", 405},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) { g.GET("/users/{id}", status(200)) })
			do(t, tt.method, base+tt.path, nil, nil)

			rec := out.waitRecord(t, "http.request.failed")
			problem, _ := rec["problem"].(map[string]any)
			if _, hasRoute := rec["http.route"]; rec["level"] != "WARN" || hasRoute || rec["http.response.status_code"] != tt.status || problem["status"] != tt.status {
				t.Fatalf("record = %v", rec)
			}
		})
	}
}

// TestAccessRecord_Deferred holds the access-record assertion deferred
// from task 5.4 (panicking consumer hook); the late-error assertions of
// task 5.3 are in TestLateError.
func TestAccessRecord_Deferred(t *testing.T) {
	t.Parallel()

	t.Run("panicking consumer hook", func(t *testing.T) {
		t.Parallel()

		base, out := loggedServer(t, func(g *srvr.Group) {
			g.GET("/x", func(http.ResponseWriter, *http.Request) error { return prblm.ErrNotFound })
		}, srvr.WithErrorHook(func(_ context.Context, _ error, _ *prblm.Problem) { panic("hook") }))
		get(t, base+"/x")

		out.waitRecord(t, "http.request.failed")
	})
}

// TestCorrelation_DefaultLogger covers the slog.Default() rows of
// structured-logging / Scenario: Correlation keys and Grouped loggers. It
// changes the process default logger, so it does not run in parallel.
//
//nolint:paralleltest // slog.SetDefault is global.
func TestCorrelation_DefaultLogger(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	base, out := loggedServer(t, func(g *srvr.Group) {
		g.GET("/log", func(_ http.ResponseWriter, r *http.Request) error {
			slog.Default().InfoContext(r.Context(), "default")
			slog.Default().WithGroup("db").InfoContext(r.Context(), "grouped", "k", "v")
			srvr.LoggerFromContext(context.Background()).Info("background") //nolint:contextcheck // the row logs without a request context.

			return nil
		})
	}, srvr.WithSetDefaultLogger())
	do(t, http.MethodGet, base+"/log", nil, map[string]string{"traceparent": incoming})

	for msg, correlated := range map[string]bool{"default": true, "grouped": true, "background": false} {
		rec := out.waitRecord(t, msg)
		if _, has := rec["trace_id"]; has != correlated || (correlated && rec["trace_id"] != incomingTrace) {
			t.Fatalf("%s record %v: correlated want %v", msg, rec, correlated)
		}
	}
	if !strings.Contains(out.String(), `"db":{"k":"v"}`) {
		t.Fatalf("grouped record lacks the group: %s", out.String())
	}
}

// TestAccessRecord_Streaming: flushing and hijacking work through srvr's
// response recorder, and both count as a started response.
func TestAccessRecord_Streaming(t *testing.T) {
	t.Parallel()

	base, out := loggedServer(t, func(g *srvr.Group) {
		g.GET("/flush", func(w http.ResponseWriter, _ *http.Request) error {
			_, _ = w.Write([]byte("a"))

			return http.NewResponseController(w).Flush()
		})
		g.GET("/hijack", func(w http.ResponseWriter, _ *http.Request) error {
			conn, buf, err := http.NewResponseController(w).Hijack()
			if err != nil {
				return err
			}
			defer conn.Close()
			_, _ = buf.WriteString("HTTP/1.1 204 No Content\r\nConnection: close\r\n\r\n")

			return buf.Flush()
		})
		g.GET("/flush-first", func(w http.ResponseWriter, _ *http.Request) error {
			return http.NewResponseController(w).Flush()
		})
		g.GET("/flusher", func(w http.ResponseWriter, _ *http.Request) error {
			w.(http.Flusher).Flush()

			return nil
		})
		g.GET("/copy", func(w http.ResponseWriter, _ *http.Request) error {
			_, err := io.Copy(w, strings.NewReader("copied"))

			return err
		})
	})

	for path, want := range map[string]int{"/flush": 200, "/hijack": 204, "/flush-first": 200, "/flusher": 200, "/copy": 200} {
		if got := get(t, base+path).status; got != want {
			t.Fatalf("GET %s = %d, want %d", path, got, want)
		}
	}
	eventually(t, func() bool { return len(out.find(t, "http.request.completed")) == 5 }, "want 5 completed records:\n"+out.String())
}

// TestUnwrap: http.ResponseController reaches the connection through srvr's
// writers, for example to set a deadline.
func TestUnwrap(t *testing.T) {
	t.Parallel()

	base, _ := loggedServer(t, func(g *srvr.Group) {
		g.GET("/deadline", func(w http.ResponseWriter, _ *http.Request) error {
			return http.NewResponseController(w).SetWriteDeadline(time.Now().Add(time.Minute))
		})
	})
	if got := get(t, base+"/deadline").status; got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
}
