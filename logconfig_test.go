package srvr_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kartaladev/srvr"
)

// logBoth registers GET /log writing a DEBUG and an INFO record through
// the request logger.
func logBoth(g *srvr.Group) {
	g.GET("/log", func(_ http.ResponseWriter, r *http.Request) error {
		l := srvr.LoggerFromContext(r.Context())
		l.Debug("debug.record")
		l.Info("info.record")

		return nil
	})
}

// TestLogConfig covers structured-logging / Scenario: Level and format.
func TestLogConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		opts         []srvr.Option
		debug, info  bool
		text         bool
		rejectsField string
	}{
		{name: "none", info: true},
		{name: "Level debug", opts: []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "debug"})}, debug: true, info: true},
		{name: "Level DEBUG", opts: []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "DEBUG"})}, debug: true, info: true},
		{name: "Level warn", opts: []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "warn"})}},
		{name: "Level info-4", opts: []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "info-4"})}, debug: true, info: true},
		{name: "Format text", opts: []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Format: "text"})}, info: true, text: true},
		{name: "Format JSON", opts: []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Format: "JSON"})}, info: true},
		{name: "WithLogLevel debug", opts: []srvr.Option{srvr.WithLogLevel(slog.LevelDebug)}, debug: true, info: true},
		{name: "WithLogConfig then WithLogLevel", opts: []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "debug"}), srvr.WithLogLevel(slog.LevelWarn)}},
		{name: "WithLogLevel then WithLogConfig", opts: []srvr.Option{srvr.WithLogLevel(slog.LevelDebug), srvr.WithLogConfig(srvr.LogConfig{Format: "text"})}, info: true, text: true},
		{name: "WithLogFormat text", opts: []srvr.Option{srvr.WithLogFormat("text")}, info: true, text: true},
		// rejected
		{name: "Level verbose", opts: []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "verbose"})}, rejectsField: "Level"},
		{name: "Format xml", opts: []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Format: "xml"})}, rejectsField: "Format"},
		{name: "WithLogFormat xml", opts: []srvr.Option{srvr.WithLogFormat("xml")}, rejectsField: "Format"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out := &logBuffer{}
			srv, err := srvr.New(append([]srvr.Option{srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(out)}, tt.opts...)...)
			if tt.rejectsField != "" {
				if !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.rejectsField) {
					t.Fatalf("New = %v, want ErrConfig naming %s", err, tt.rejectsField)
				}

				return
			}
			mustNil(t, err)
			mustNil(t, srv.Register(configFunc(logBoth)))
			get(t, startServer(t, srv)+"/log")

			got := out.String()
			if strings.Contains(got, "debug.record") != tt.debug || strings.Contains(got, "info.record") != tt.info {
				t.Fatalf("output %q: debug want %v, info want %v", got, tt.debug, tt.info)
			}
			if tt.info && strings.HasPrefix(strings.TrimSpace(got), "{") == tt.text {
				t.Fatalf("output %q: text format want %v", got, tt.text)
			}
		})
	}
}

// TestLogConfigDecode covers structured-logging / Scenario: Config struct
// decoded from a file.
func TestLogConfigDecode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		document            string
		debug, text, source bool
		rejects             string
	}{
		{document: `{"level":"debug","format":"text","add_source":true}`, debug: true, text: true, source: true},
		{document: `{}`},
		{document: `{"level":"loud"}`, rejects: "loud"},
		{document: `{"level":"debug","unknown":1}`, debug: true},
	}

	for _, tt := range tests {
		t.Run(tt.document, func(t *testing.T) {
			t.Parallel()

			var c srvr.LogConfig
			mustNil(t, json.Unmarshal([]byte(tt.document), &c))

			out := &logBuffer{}
			srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(out), srvr.WithLogConfig(c))
			if tt.rejects != "" {
				if !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), "Level") || !strings.Contains(err.Error(), tt.rejects) {
					t.Fatalf("New = %v, want ErrConfig naming Level and %s", err, tt.rejects)
				}

				return
			}
			mustNil(t, err)
			mustNil(t, srv.Register(configFunc(logBoth)))
			get(t, startServer(t, srv)+"/log")

			got := out.String()
			if strings.Contains(got, "debug.record") != tt.debug || strings.HasPrefix(got, "{") == tt.text || strings.Contains(got, "source") != tt.source {
				t.Fatalf("output %q, want debug %v text %v source %v", got, tt.debug, tt.text, tt.source)
			}
		})
	}
}

// TestLogLevelVar covers structured-logging / Scenario: Runtime level change.
func TestLogLevelVar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		before, after slog.Level
		concurrent    bool
		written       bool
	}{
		{"INFO to DEBUG", slog.LevelInfo, slog.LevelDebug, false, true},
		{"DEBUG to INFO", slog.LevelDebug, slog.LevelInfo, false, false},
		{"INFO to DEBUG with 100 in-flight requests", slog.LevelInfo, slog.LevelDebug, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var lv slog.LevelVar
			lv.Set(tt.before)
			out := &logBuffer{}
			srv := newServer(t, srvr.WithLogOutput(out), srvr.WithLogLevelVar(&lv))
			mustNil(t, srv.Register(configFunc(logBoth)))
			base := startServer(t, srv)

			if tt.concurrent {
				var wg sync.WaitGroup
				for range 100 {
					wg.Go(func() { get(t, base+"/log") })
				}
				lv.Set(tt.after)
				wg.Wait()
			} else {
				lv.Set(tt.after)
			}
			get(t, base+"/log")

			if got := strings.Contains(out.String(), "debug.record"); got != tt.written {
				t.Fatalf("DEBUG record written = %v, want %v", got, tt.written)
			}
		})
	}
}

// TestAddSource covers structured-logging / Scenario: Source attribute.
func TestAddSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts []srvr.Option
		add  bool
	}{
		{"LogConfig AddSource", []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{AddSource: true})}, true},
		{"WithLogAddSource", []srvr.Option{srvr.WithLogAddSource()}, true},
		{"none", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out := &logBuffer{}
			srv := newServer(t, append([]srvr.Option{srvr.WithLogOutput(out)}, tt.opts...)...)
			mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
				g.GET("/log", func(_ http.ResponseWriter, r *http.Request) error {
					srvr.LoggerFromContext(r.Context()).Info("handler") // sourceLine

					return nil
				})
			})))
			get(t, startServer(t, srv)+"/log")

			rec := out.waitRecord(t, "handler")
			src, _ := rec["source"].(map[string]any)
			if tt.add != (src != nil) || (tt.add && !strings.HasSuffix(src["file"].(string), "logconfig_test.go")) {
				t.Fatalf("source = %v, want present %v and pointing at the handler", rec["source"], tt.add)
			}
			if _, has := out.waitRecord(t, "http.request.completed")["source"]; has {
				t.Fatal("the access record carries source")
			}
		})
	}
}

// recordingHandler is a consumer slog.Handler recording records with the
// attributes bound by WithAttrs; it can be made to fail or panic. Groups
// are not needed by these tests and are ignored.
type recordingHandler struct {
	store  *recordStore
	attrs  []slog.Attr
	fail   error
	panics bool
	// panicsIn names a method that panics: "Enabled" (for request records
	// only, so the server still starts), "WithAttrs" or "WithGroup".
	panicsIn string
}

type recordStore struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(ctx context.Context, _ slog.Level) bool {
	if _, request := srvr.TraceFromContext(ctx); request && h.panicsIn == "Enabled" {
		panic("Enabled")
	}

	return true
}

func (h *recordingHandler) WithGroup(string) slog.Handler {
	if h.panicsIn == "WithGroup" {
		panic("WithGroup")
	}

	return h
}

func (h *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if h.panicsIn == "WithAttrs" {
		panic("WithAttrs")
	}

	c := *h
	c.attrs = append(slices.Clip(h.attrs), attrs...)

	return &c
}

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	if h.panics {
		panic("handler")
	}
	r = r.Clone()
	r.AddAttrs(h.attrs...)
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	h.store.records = append(h.store.records, r)

	return h.fail
}

// newRecording returns a recording handler.
func newRecording() *recordingHandler { return &recordingHandler{store: &recordStore{}} }

// record returns the attributes of the first record with msg, if any.
func (h *recordingHandler) record(msg string) (map[string]string, bool) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()

	for _, r := range h.store.records {
		if r.Message == msg {
			attrs := map[string]string{}
			r.Attrs(func(a slog.Attr) bool { attrs[a.Key] = a.Value.String(); return true })

			return attrs, true
		}
	}

	return nil, false
}

// TestOwnHandler covers structured-logging / Scenario: Own handler.
func TestOwnHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		h       *recordingHandler
		opts    func(h *recordingHandler) []srvr.Option
		rejects string
	}{
		{name: "WithLogHandler", h: newRecording(), opts: func(h *recordingHandler) []srvr.Option { return []srvr.Option{srvr.WithLogHandler(h)} }},
		{name: "WithLogger", h: newRecording(), opts: func(h *recordingHandler) []srvr.Option { return []srvr.Option{srvr.WithLogger(slog.New(h))} }},
		{name: "WithLogHandler and Level", h: newRecording(), opts: func(h *recordingHandler) []srvr.Option {
			return []srvr.Option{srvr.WithLogHandler(h), srvr.WithLogConfig(srvr.LogConfig{Level: "debug"})}
		}, rejects: "Level"},
		{name: "WithLogHandler and Output", h: newRecording(), opts: func(h *recordingHandler) []srvr.Option {
			return []srvr.Option{srvr.WithLogHandler(h), srvr.WithLogOutput(&logBuffer{})}
		}, rejects: "Output"},
		{name: "WithLogHandler nil", opts: func(*recordingHandler) []srvr.Option { return []srvr.Option{srvr.WithLogHandler(nil)} }, rejects: "Handler"},
		{name: "WithLogger nil", opts: func(*recordingHandler) []srvr.Option { return []srvr.Option{srvr.WithLogger(nil)} }, rejects: "Logger"},
		{name: "erroring handler", h: func() *recordingHandler { h := newRecording(); h.fail = errors.New("disk full"); return h }(), opts: func(h *recordingHandler) []srvr.Option { return []srvr.Option{srvr.WithLogHandler(h)} }},
		{name: "panicking handler", h: func() *recordingHandler { h := newRecording(); h.panics = true; return h }(), opts: func(h *recordingHandler) []srvr.Option { return []srvr.Option{srvr.WithLogHandler(h)} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, err := srvr.New(append([]srvr.Option{srvr.WithAddr("127.0.0.1:0")}, tt.opts(tt.h)...)...)
			if tt.rejects != "" {
				if !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.rejects) {
					t.Fatalf("New = %v, want ErrConfig naming %s", err, tt.rejects)
				}

				return
			}
			mustNil(t, err)
			mustNil(t, srv.Register(okRoute("/ping")))

			if got := get(t, startServer(t, srv)+"/ping").status; got != http.StatusOK {
				t.Fatalf("status = %d, want 200", got)
			}
			if tt.h.panics {
				return
			}
			var attrs map[string]string
			eventually(t, func() bool { var ok bool; attrs, ok = tt.h.record("http.request.completed"); return ok }, "no access record")
			if len(attrs["trace_id"]) != 32 {
				t.Fatalf("access record %v lacks trace_id", attrs)
			}
		})
	}
}

// TestOwnHandler_FaultyMethods covers the rows of structured-logging /
// Scenario: Own handler whose consumer handler panics in Enabled, WithAttrs
// or WithGroup: the response is unaffected, the record is skipped and the
// failure is counted.
func TestOwnHandler_FaultyMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method  string
		skipped []string // records that go through the failing method
	}{
		{"Enabled", []string{"query", "http.request.completed"}},
		{"WithAttrs", []string{"query", "http.request.completed"}},
		{"WithGroup", []string{"query"}}, // only the grouped logger calls WithGroup
	}

	for _, tt := range tests {
		t.Run(tt.method+" panics", func(t *testing.T) {
			t.Parallel()

			h := newRecording()
			h.panicsIn = tt.method
			srv := newServer(t, srvr.WithLogHandler(h))
			mustNil(t, srv.Register(on("", "/log", func(w http.ResponseWriter, r *http.Request) error {
				srvr.LoggerFromContext(r.Context()).WithGroup("db").InfoContext(r.Context(), "query")
				w.WriteHeader(http.StatusOK)

				return nil
			})))
			base := startServer(t, srv)

			before := srvr.LogWriteErrors()
			if got := get(t, base+"/log").status; got != http.StatusOK {
				t.Fatalf("status = %d, want 200", got)
			}
			eventually(t, func() bool { return srvr.LogWriteErrors() > before }, "the failure was not counted")
			for _, msg := range tt.skipped {
				if _, ok := h.record(msg); ok {
					t.Fatalf("record %q was written through a failing handler", msg)
				}
			}
		})
	}
}
