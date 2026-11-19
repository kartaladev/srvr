package srvr_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
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
			file, _ := src["file"].(string) // a missing or non-string file fails the suffix check below
			if tt.add != (src != nil) || (tt.add && !strings.HasSuffix(file, "logconfig_test.go")) {
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

// value returns the value of attribute key, kind kept, in the first record
// with msg, if any.
func (h *recordingHandler) value(msg, key string) (slog.Value, bool) {
	h.store.mu.Lock()
	defer h.store.mu.Unlock()

	for _, r := range h.store.records {
		if r.Message != msg {
			continue
		}

		var v slog.Value
		found := false
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == key {
				v, found = a.Value, true
			}

			return !found
		})

		return v, found
	}

	return slog.Value{}, false
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

			before := srvr.LogRecordsLost()
			if got := get(t, base+"/log").status; got != http.StatusOK {
				t.Fatalf("status = %d, want 200", got)
			}
			eventually(t, func() bool { return srvr.LogRecordsLost() > before }, "the failure was not counted")
			for _, msg := range tt.skipped {
				if _, ok := h.record(msg); ok {
					t.Fatalf("record %q was written through a failing handler", msg)
				}
			}
		})
	}
}

// goldenSchemaVersion is the srvr.log.schema the golden keys belong to.
// Renaming or removing a key, or changing its meaning or type, must bump
// it (and LOGGING.md); adding a key keeps it.
const goldenSchemaVersion = 1

// goldenKeys pins the keys of every event srvr writes, groups flattened
// with ".", for a request with a User-Agent. Optional keys a scenario
// adds (url.path, url.query, client.address, srvr.attr.truncated, ...)
// are covered by their own tests.
var goldenKeys = map[string][]string{
	"http.request.completed": {
		"duration_ms", "http.request.method", "http.response.body.size", "http.response.status_code", "http.route",
		"level", "msg", "span_id", "srvr.log.schema", "time", "trace_id", "user_agent.original",
	},
	"http.request.failed": {
		"code.file.path", "code.function.name", "code.line.number", "duration_ms", "error.chain", "error.fingerprint",
		"error.type", "exception.message", "http.request.method", "http.response.body.size", "http.response.status_code",
		"http.route", "level", "msg", "problem.status", "problem.title", "problem.type", "service.name", "service.version",
		"span_id", "srvr.log.schema", "time", "trace_id", "user_agent.original",
	},
}

// goldenPanicKeys and goldenAbortedKeys pin the failed record of a
// recovered panic and of a request aborted with http.ErrAbortHandler.
var (
	goldenPanicKeys = []string{
		"code.file.path", "code.function.name", "code.line.number", "duration_ms", "error.fingerprint", "error.kind",
		"error.type", "exception.message", "exception.stacktrace", "exception.type", "http.request.method",
		"http.response.body.size", "http.response.status_code", "http.route", "level", "msg", "problem.status",
		"problem.title", "problem.type", "service.name", "service.version", "span_id", "srvr.log.schema",
		"srvr.stacktrace.truncated", "time", "trace_id", "user_agent.original",
	}
	goldenAbortedKeys = []string{
		"duration_ms", "error.kind", "error.type", "http.request.method", "http.response.body.size", "http.route", "level",
		"msg", "service.name", "service.version", "span_id", "srvr.log.schema", "srvr.response.aborted", "time",
		"trace_id", "user_agent.original",
	}
)

// TestLogSchemaGolden covers structured-logging / Scenario: Schema version
// attribute and pins the keys of every event.
func TestLogSchemaGolden(t *testing.T) {
	t.Parallel()

	base, out := loggedServer(t, func(g *srvr.Group) {
		g.GET("/ok", func(_ http.ResponseWriter, r *http.Request) error {
			srvr.LoggerFromContext(r.Context()).Info("handler.own")

			return nil
		})
		g.GET("/fail", func(http.ResponseWriter, *http.Request) error { return errors.New("boom") })
		g.GET("/panic", func(http.ResponseWriter, *http.Request) error { panic("boom") })
		g.GET("/abort", func(http.ResponseWriter, *http.Request) error { panic(http.ErrAbortHandler) })
	}, srvr.WithServiceVersion("1.4.2"))
	get(t, base+"/ok")
	get(t, base+"/fail")

	for event, want := range goldenKeys {
		t.Run(event, func(t *testing.T) {
			t.Parallel()

			var rec map[string]any
			eventually(t, func() bool {
				for _, r := range out.find(t, event) {
					if r["http.route"] == "/ok" || r["http.route"] == "/fail" {
						rec = r
					}
				}

				return rec != nil
			}, "no "+event+" record")
			if got := flatKeys(rec, ""); !slices.Equal(got, want) {
				t.Errorf("%s keys = %v\nwant %v (bump srvr.log.schema on a rename or removal)", event, got, want)
			}
			if rec["srvr.log.schema"] != float64(goldenSchemaVersion) {
				t.Errorf("%s srvr.log.schema = %v, want %d", event, rec["srvr.log.schema"], goldenSchemaVersion)
			}
		})
	}

	failed := []struct {
		name, path string
		want       []string
	}{
		{"panic", "/panic", goldenPanicKeys},
		{"abort", "/abort", goldenAbortedKeys},
	}

	for _, tt := range failed {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+tt.path, nil)
			if res, err := clientFor(req.URL.String()).Do(req); err == nil {
				_, _ = io.Copy(io.Discard, res.Body)
				res.Body.Close()
			}

			var rec map[string]any
			eventually(t, func() bool {
				for _, r := range out.find(t, "http.request.failed") {
					if r["http.route"] == tt.path {
						rec = r
					}
				}

				return rec != nil
			}, "no failed record for "+tt.path)
			if got := flatKeys(rec, ""); !slices.Equal(got, tt.want) {
				t.Errorf("%s failed keys = %v\nwant %v (bump srvr.log.schema on a rename or removal)", tt.path, got, tt.want)
			}
		})
	}

	if _, has := out.waitRecord(t, "handler.own")["srvr.log.schema"]; has {
		t.Error("a handler's own record carries srvr.log.schema")
	}
}

// flatKeys returns the sorted keys of rec, nested groups joined with ".".
func flatKeys(rec map[string]any, prefix string) []string {
	var keys []string
	for _, k := range slices.Sorted(maps.Keys(rec)) {
		if sub, ok := rec[k].(map[string]any); ok {
			keys = append(keys, flatKeys(sub, prefix+k+".")...)

			continue
		}
		keys = append(keys, prefix+k)
	}
	slices.SortFunc(keys, strings.Compare)

	return keys
}

// goldenLifecycleKeys pins the keys of srvr's lifecycle events, for a
// server with a service name and version (vcs.revision and
// deployment.environment.name are added when known).
var goldenLifecycleKeys = map[string][]string{
	"server.started":              {"level", "msg", "server.address", "service.name", "service.version", "srvr.log.schema", "time"},
	"server.stopping":             {"level", "msg", "service.name", "service.version", "srvr.log.schema", "time"},
	"server.stopped":              {"level", "msg", "service.name", "service.version", "srvr.drain.duration_ms", "srvr.log.schema", "time"},
	"server.shutdown_hook.failed": {"code.function.name", "exception.message", "level", "msg", "service.name", "service.version", "srvr.log.schema", "srvr.shutdown_hook.index", "srvr.shutdown_hook.phase", "time"},
	"server.failed":               {"exception.message", "level", "msg", "service.name", "service.version", "srvr.log.schema", "time"},
}

// TestLogSchemaGolden_Lifecycle pins the keys of the lifecycle events.
func TestLogSchemaGolden_Lifecycle(t *testing.T) {
	t.Parallel()

	out := &logBuffer{}
	opts := []srvr.Option{
		srvr.WithLogOutput(out),
		srvr.WithServiceConfig(srvr.ServiceConfig{Name: "registration-api", Version: "1.4.2"}),
		srvr.WithShutdownHook(srvr.AfterDrain, func(context.Context) error { return errors.New("flush failed") }),
	}
	stopByContext(t, opts)

	taken := listen(t)
	srv, err := srvr.New(append(opts, srvr.WithAddr(taken.Addr().String()))...)
	mustNil(t, err)
	_ = srv.Start(t.Context())

	for event, want := range goldenLifecycleKeys {
		t.Run(event, func(t *testing.T) {
			t.Parallel()

			recs := out.find(t, event)
			if len(recs) == 0 {
				t.Fatalf("no %s record:\n%s", event, out.String())
			}
			got := slices.DeleteFunc(flatKeys(recs[0], ""), func(k string) bool { return k == "vcs.revision" })
			if !slices.Equal(got, want) {
				t.Errorf("%s keys = %v\nwant %v (bump srvr.log.schema on a rename or removal)", event, got, want)
			}
			if recs[0]["srvr.log.schema"] != float64(goldenSchemaVersion) {
				t.Errorf("%s srvr.log.schema = %v", event, recs[0]["srvr.log.schema"])
			}
		})
	}
}

// goldenObserverFaultKeys pins srvr.observer.fault, an additive event
// (srvr.log.schema unchanged).
var goldenObserverFaultKeys = []string{
	"exception.message", "level", "msg", "service.name", "service.version", "span_id", "srvr.log.schema",
	"srvr.observer.fault.kind", "srvr.observer.type", "time", "trace_id",
}

// TestLogSchemaGolden_ObserverFault pins the keys of srvr.observer.fault.
func TestLogSchemaGolden_ObserverFault(t *testing.T) {
	t.Parallel()

	obs := observerFunc(func(context.Context, srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) { panic("boom") })
	base, out := loggedServer(t, func(g *srvr.Group) {
		g.GET("/ok", func(http.ResponseWriter, *http.Request) error { return nil })
	}, srvr.WithServiceVersion("1.4.2"), srvr.WithObserver(obs))
	get(t, base+"/ok")

	rec := out.waitRecord(t, "srvr.observer.fault")
	if got := flatKeys(rec, ""); !slices.Equal(got, goldenObserverFaultKeys) {
		t.Errorf("srvr.observer.fault keys = %v\nwant %v", got, goldenObserverFaultKeys)
	}

	if rec["srvr.log.schema"] != float64(goldenSchemaVersion) {
		t.Errorf("srvr.log.schema = %v", rec["srvr.log.schema"])
	}
}

// goldenDebugBufferKeys pins the additive debug buffer keys
// (srvr.log.schema unchanged): a flushed record of a handler that logs a
// plain DEBUG message, and the summary on the failed record.
var (
	goldenFlushedKeys       = []string{"level", "msg", "span_id", "srvr.buffered", "time", "trace_id"}
	goldenBufferSummaryKeys = []string{"srvr.debug_buffer.dropped", "srvr.debug_buffer.flushed", "srvr.debug_buffer.truncated"}
)

// TestLogSchemaGolden_DebugBuffer pins the keys a failed request writes
// with debug buffering on.
func TestLogSchemaGolden_DebugBuffer(t *testing.T) {
	t.Parallel()

	base, out := loggedServer(t, func(g *srvr.Group) {
		g.GET("/fail", func(_ http.ResponseWriter, r *http.Request) error {
			srvr.LoggerFromContext(r.Context()).DebugContext(r.Context(), "step")

			return errors.New("boom")
		})
	}, srvr.WithServiceVersion("1.4.2"), srvr.WithDebugBuffer())
	do(t, http.MethodGet, base+"/fail", nil, map[string]string{"User-Agent": "golden"})

	failed := out.waitRecord(t, "http.request.failed")
	want := slices.Sorted(slices.Values(slices.Concat(goldenKeys["http.request.failed"], goldenBufferSummaryKeys)))
	if got := flatKeys(failed, ""); !slices.Equal(got, want) {
		t.Errorf("failed keys = %v\nwant %v", got, want)
	}

	if got := flatKeys(out.waitRecord(t, "step"), ""); !slices.Equal(got, goldenFlushedKeys) {
		t.Errorf("flushed keys = %v\nwant %v", got, goldenFlushedKeys)
	}

	if failed["srvr.log.schema"] != float64(goldenSchemaVersion) {
		t.Errorf("srvr.log.schema = %v", failed["srvr.log.schema"])
	}
}

// goldenUnmatchedKeys pins the record of an unmatched request that
// reports suppressed records; srvr.unmatched.suppressed is additive
// (srvr.log.schema unchanged), also on server.stopped.
var goldenUnmatchedKeys = []string{
	"duration_ms", "error.chain", "error.fingerprint", "error.type", "exception.message", "http.request.method",
	"http.response.body.size", "http.response.status_code", "level", "msg", "problem.status", "problem.title",
	"problem.type", "service.name", "service.version", "span_id", "srvr.log.schema", "srvr.unmatched.suppressed",
	"time", "trace_id", "user_agent.original",
}

// TestLogSchemaGolden_UnmatchedLimit pins the keys a server with an
// unmatched-record limit writes: the record reporting suppressed records
// and server.stopped reporting the rest.
func TestLogSchemaGolden_UnmatchedLimit(t *testing.T) {
	t.Parallel()

	var (
		mu  sync.Mutex
		now = time.Unix(0, 0)
	)
	clock := func(s *engine.Settings) {
		s.Now = func() time.Time {
			mu.Lock()
			defer mu.Unlock()

			return now
		}
	}

	out := &logBuffer{}
	srv := newServer(t, srvr.WithLogOutput(out), srvr.WithServiceVersion("1.4.2"),
		srvr.WithUnmatchedRecordLimit(1, time.Minute), clock)
	base := startServer(t, srv)

	for _, path := range []string{"/nope-1", "/nope-2"} {
		get(t, base+path)
	}

	mu.Lock()
	now = now.Add(time.Minute)
	mu.Unlock()

	for _, path := range []string{"/nope-3", "/nope-4"} {
		get(t, base+path)
	}

	mustNil(t, srv.Shutdown(t.Context()))

	failed := out.find(t, "http.request.failed")
	if len(failed) != 2 {
		t.Fatalf("%d failed records, want 2:\n%s", len(failed), out.String())
	}

	if got := flatKeys(failed[1], ""); !slices.Equal(got, goldenUnmatchedKeys) {
		t.Errorf("unmatched keys = %v\nwant %v", got, goldenUnmatchedKeys)
	}

	stopped := out.find(t, "server.stopped")
	if len(stopped) != 1 || stopped[0]["srvr.unmatched.suppressed"] != float64(1) || stopped[0]["srvr.log.schema"] != float64(goldenSchemaVersion) {
		t.Errorf("server.stopped = %v, want srvr.unmatched.suppressed 1 and schema %d", stopped, goldenSchemaVersion)
	}
}

// TestLogSchemaDocumented checks that LOGGING.md lists every pinned key.
func TestLogSchemaDocumented(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile("LOGGING.md")
	mustNil(t, err)

	keys := slices.Concat(goldenPanicKeys, goldenAbortedKeys, goldenObserverFaultKeys, []string{"srvr.observer.fault"},
		goldenFlushedKeys, goldenBufferSummaryKeys, goldenUnmatchedKeys)
	for event, k := range goldenKeys {
		keys = append(keys, event)
		keys = append(keys, k...)
	}
	for event, k := range goldenLifecycleKeys {
		keys = append(keys, event)
		keys = append(keys, k...)
	}

	for _, k := range keys {
		name := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(k, "problem."), "problem"), ".")
		if !strings.Contains(string(doc), "`"+k+"`") && !strings.Contains(string(doc), "`"+name+"`") {
			t.Errorf("LOGGING.md does not list %q", k)
		}
	}
}

// TestLogSchemaDocumented_DebugBufferSample checks that the sample flushed
// sequence in LOGGING.md has the pinned keys: every flushed record the
// golden flushed keys, the failed record exactly the golden failed and
// summary keys, written last.
func TestLogSchemaDocumented_DebugBufferSample(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile("LOGGING.md")
	mustNil(t, err)

	_, sample, found := strings.Cut(string(doc), "**Sample flushed sequence**")
	if !found {
		t.Fatal("LOGGING.md has no sample flushed sequence")
	}
	_, sample, _ = strings.Cut(sample, "```json\n")
	sample, _, _ = strings.Cut(sample, "```")

	var records []map[string]any
	for line := range strings.Lines(sample) {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("sample line %q: %v", line, err)
		}
		records = append(records, rec)
	}
	if len(records) < 2 {
		t.Fatalf("sample has %d records, want flushed records and the failed record", len(records))
	}

	failed := records[len(records)-1]
	want := slices.Sorted(slices.Values(slices.Concat(goldenKeys["http.request.failed"], goldenBufferSummaryKeys)))
	if got := flatKeys(failed, ""); failed["msg"] != "http.request.failed" || !slices.Equal(got, want) {
		t.Errorf("sample failed record keys = %v\nwant %v", got, want)
	}

	flushed := 0
	for _, rec := range records[:len(records)-1] {
		if rec["srvr.buffered"] != true {
			continue
		}
		flushed++
		for _, k := range goldenFlushedKeys {
			if _, ok := rec[k]; !ok {
				t.Errorf("sample flushed record %v lacks %q", rec, k)
			}
		}
	}
	if failed["srvr.debug_buffer.flushed"] != float64(flushed) {
		t.Errorf("sample srvr.debug_buffer.flushed = %v, want %d", failed["srvr.debug_buffer.flushed"], flushed)
	}
}
