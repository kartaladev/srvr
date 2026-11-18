package srvr_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// failingWriter fails every write with err, or writes short when err is nil.
type failingWriter struct{ err error }

func (w failingWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}

	return len(p) / 2, nil
}

// TestBrokenOutput covers structured-logging / Scenario: Broken output. It
// reads the process-wide counter, so it does not run in parallel with
// other tests that break their output.
//
//nolint:paralleltest // srvr.LogWriteErrors is process-wide.
func TestBrokenOutput(t *testing.T) {
	tests := []struct {
		fault string
		out   interface{ Write([]byte) (int, error) }
		count uint64
	}{
		{"EPIPE on every write", failingWriter{err: syscall.EPIPE}, 1},
		{"short write", failingWriter{}, 1},
		{"works", &logBuffer{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.fault, func(t *testing.T) {
			srv := newServer(t, srvr.WithLogOutput(tt.out))
			mustNil(t, srv.Register(okRoute("/ping")))
			base := startServer(t, srv)

			before := srvr.LogWriteErrors()
			if got := get(t, base+"/ping").status; got != http.StatusOK {
				t.Fatalf("status = %d, want 200", got)
			}
			eventually(t, func() bool { return srvr.LogWriteErrors()-before >= tt.count }, "write errors not counted")
			if got := srvr.LogWriteErrors() - before; got != tt.count {
				t.Fatalf("LogWriteErrors increased by %d, want %d", got, tt.count)
			}
		})
	}
}

// TestAccessLogConfig covers structured-logging / Scenario: Access log configuration.
func TestAccessLogConfig(t *testing.T) {
	t.Parallel()

	skip := srvr.AccessLogConfig{SkipPaths: []string{"/healthz"}}
	tests := []struct {
		name    string
		opts    []srvr.Option
		path    string
		status  int
		written bool
	}{
		{"none", nil, "/healthz", 200, true},
		{"skip path", []srvr.Option{srvr.WithAccessLogConfig(skip)}, "/healthz", 200, false},
		{"skip path returning 500", []srvr.Option{srvr.WithAccessLogConfig(skip)}, "/healthz", 500, true},
		{"skip path exact match", []srvr.Option{srvr.WithAccessLogConfig(skip)}, "/healthz/deep", 200, true},
		{"disabled", []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{Disabled: true})}, "/ping", 200, false},
		{"disabled returning 500", []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{Disabled: true})}, "/ping", 500, true},
		{"WithAccessLogSkipPaths", []srvr.Option{srvr.WithAccessLogSkipPaths("/healthz")}, "/healthz", 200, false},
		{"WithAccessLogDisabled", []srvr.Option{srvr.WithAccessLogDisabled()}, "/ping", 200, false},
		{"WithAccessLogDisabled then empty config", []srvr.Option{srvr.WithAccessLogDisabled(), srvr.WithAccessLogConfig(srvr.AccessLogConfig{})}, "/ping", 200, true},
		{"WithAccessLogSkipPaths then empty config", []srvr.Option{srvr.WithAccessLogSkipPaths("/healthz"), srvr.WithAccessLogConfig(srvr.AccessLogConfig{})}, "/healthz", 200, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) {
				for _, p := range []string{"/healthz", "/healthz/deep", "/ping", "/barrier"} {
					g.GET(p, func(w http.ResponseWriter, r *http.Request) error {
						if r.URL.Query().Get("fail") != "" {
							return errors.New("boom")
						}
						w.WriteHeader(http.StatusOK)

						return nil
					})
				}
			}, tt.opts...)

			url := base + tt.path
			if tt.status == 500 {
				url += "?fail=1"
			}
			get(t, url)
			// The next request on the same keep-alive connection is read only
			// after the previous one, access record included, has finished.
			get(t, base+"/barrier?fail=1")
			eventually(t, func() bool {
				for _, rec := range out.records(t) {
					if rec["http.route"] == "/barrier" {
						return true
					}
				}

				return false
			}, "no barrier record")

			written := false
			for _, rec := range out.records(t) {
				if rec["http.route"] == tt.path {
					written = true
				}
			}
			if written != tt.written {
				t.Fatalf("record written = %v, want %v:\n%s", written, tt.written, out.String())
			}
		})
	}
}

// TestRedaction covers structured-logging / Scenario: Redaction at every level.
func TestRedaction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		opts         []srvr.Option
		forbidden    string
		allowedKey   string
		allowedValue string // empty: only the key must be present
	}{
		{"INFO", nil, "s3cr3t", "http.route", "/users/{id}"},
		{"DEBUG authorization", []srvr.Option{srvr.WithLogLevel(-4)}, "abc.def", "http.route", ""},
		{"DEBUG cookie", []srvr.Option{srvr.WithLogLevel(-4)}, "sid=xyz", "http.route", ""},
		{"DEBUG path", []srvr.Option{srvr.WithLogLevel(-4)}, "alice@example.com", "http.route", ""},
		{"IncludePath", []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{IncludePath: true})}, "s3cr3t", "url.path", "/users/alice@example.com"},
		{"IncludeQuery", []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{IncludeQuery: true})}, "abc.def", "url.query", "token=s3cr3t"},
		{"IncludeClientAddress", []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{IncludeClientAddress: true})}, "sid=xyz", "client.address", "127.0.0.1"},
		{"WithAccessLogIncludePath", []srvr.Option{srvr.WithAccessLogIncludePath()}, "s3cr3t", "url.path", "/users/alice@example.com"},
		{"WithAccessLogIncludeQuery", []srvr.Option{srvr.WithAccessLogIncludeQuery()}, "abc.def", "url.query", "token=s3cr3t"},
		{"WithAccessLogIncludeClientAddress", []srvr.Option{srvr.WithAccessLogIncludeClientAddress()}, "sid=xyz", "client.address", "127.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) {
				g.GET("/users/{id}", func(http.ResponseWriter, *http.Request) error { return errors.New("boom") })
			}, tt.opts...)
			do(t, http.MethodGet, base+"/users/alice@example.com?token=s3cr3t", nil, map[string]string{
				"Authorization": "Bearer abc.def",
				"Cookie":        "sid=xyz",
			})

			rec := out.waitRecord(t, "http.request.failed")
			if strings.Contains(out.String(), tt.forbidden) {
				t.Fatalf("log contains %q:\n%s", tt.forbidden, out.String())
			}
			if v, ok := rec[tt.allowedKey]; !ok || (tt.allowedValue != "" && v != tt.allowedValue) {
				t.Fatalf("%s = %v, want %q", tt.allowedKey, v, tt.allowedValue)
			}
		})
	}
}

// TestAccessLogSuppressed covers structured-logging / Scenario: Failures
// while the access log is suppressed: failed records are always written.
func TestAccessLogSuppressed(t *testing.T) {
	t.Parallel()

	routes := func(g *srvr.Group) {
		g.GET("/healthz", returning(prblm.ErrNotFound))
		g.GET("/ping", returning(prblm.ErrNotFound))
		g.GET("/late/panic", late(http.StatusOK, func() error { panic("late") }))
		g.GET("/late/error", late(http.StatusOK, func() error { return errors.New("late") }))
	}

	tests := []struct {
		name  string
		opts  []srvr.Option
		path  string
		level string
	}{
		{"skip path returning not found", []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{SkipPaths: []string{"/healthz"}})}, "/healthz", "WARN"},
		{"disabled returning not found", []srvr.Option{srvr.WithAccessLogDisabled()}, "/ping", "WARN"},
		{"disabled with no route", []srvr.Option{srvr.WithAccessLogDisabled()}, "/nope", "WARN"},
		{"disabled with a late panic", []srvr.Option{srvr.WithAccessLogDisabled()}, "/late/panic", "ERROR"},
		{"disabled with a late error", []srvr.Option{srvr.WithAccessLogDisabled()}, "/late/error", "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, routes, tt.opts...)
			get(t, base+tt.path)

			if rec := out.waitRecord(t, "http.request.failed"); rec["level"] != tt.level {
				t.Fatalf("record = %v, want level %s", rec, tt.level)
			}
		})
	}
}

const cut = "…[truncated]"

func xs(n int) string { return strings.Repeat("x", n) }

// TestHostileValues covers structured-logging / Scenario: Hostile user agent.
func TestHostileValues(t *testing.T) {
	t.Parallel()

	tests := []struct{ agent, logged string }{
		{"curl/8.0", "curl/8.0"},
		{"a\nlevel=ERROR msg=forged", "a\nlevel=ERROR msg=forged"},
		{xs(1024), xs(1024)},
		{xs(1025), xs(1024) + cut},
		{xs(1023) + "é", xs(1023) + cut},
		{xs(5000), xs(1024) + cut},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.agent[:min(len(tt.agent), 20)], func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, okRoute("/ping"))
			req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/ping", nil)
			req.Header["User-Agent"] = []string{tt.agent}
			if tt.agent == "a\nlevel=ERROR msg=forged" {
				// net/http refuses a newline in a header value, so the hostile
				// value reaches srvr only through a raw request; the JSON
				// encoding is checked on the record instead.
				req.Header["User-Agent"] = []string{"curl/8.0"}
			}
			res, err := clientFor(req.URL.String()).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()

			rec := out.waitRecord(t, "http.request.completed")
			_ = out.records(t) // every line is one JSON object
			want := tt.logged
			if tt.agent == "a\nlevel=ERROR msg=forged" {
				want = "curl/8.0"
			}
			if got, _ := rec["user_agent.original"].(string); got != want {
				t.Fatalf("user_agent.original = %q (%d bytes), want %d bytes", got, len(got), len(want))
			}
		})
	}
}

// TestHostileValues_Newline: a control character in a logged value is
// escaped, so it cannot start a forged line.
func TestHostileValues_Newline(t *testing.T) {
	t.Parallel()

	base, out := loggedServer(t, func(g *srvr.Group) {
		g.GET("/log", func(_ http.ResponseWriter, r *http.Request) error {
			srvr.LoggerFromContext(r.Context()).Info("handler", "v", "a\nlevel=ERROR msg=forged")

			return nil
		})
	})
	get(t, base+"/log")

	rec := out.waitRecord(t, "handler")
	if rec["v"] != "a\nlevel=ERROR msg=forged" || !strings.Contains(out.String(), `a\nlevel=ERROR`) {
		t.Fatalf("value not escaped:\n%s", out.String())
	}
}

type valuer string

func (v valuer) LogValue() slog.Value { return slog.StringValue(string(v)) }

// TestTruncation covers structured-logging / Scenario: Truncating any string attribute.
func TestTruncation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opts   []srvr.Option
		log    func(l *slog.Logger)
		path   []string // where the value is in the record
		logged any
		flag   bool
	}{
		{"1024 bytes", nil, func(l *slog.Logger) { l.Info("m", "note", xs(1024)) }, []string{"note"}, xs(1024), false},
		{"1025 bytes", nil, func(l *slog.Logger) { l.Info("m", "note", xs(1025)) }, []string{"note"}, xs(1024) + cut, true},
		{"multi-byte boundary", nil, func(l *slog.Logger) { l.Info("m", "note", xs(1023)+"é") }, []string{"note"}, xs(1023) + cut, true},
		{"in a group", nil, func(l *slog.Logger) { l.WithGroup("db").Info("m", "note", xs(2000)) }, []string{"db", "note"}, xs(1024) + cut, true},
		{"With attribute", nil, func(l *slog.Logger) { l.With("note", xs(2000)).Info("m") }, []string{"note"}, xs(1024) + cut, true},
		{"integer", nil, func(l *slog.Logger) { l.Info("m", "n", 5) }, []string{"n"}, float64(5), false},
		{"short error", nil, func(l *slog.Logger) { l.Info("m", "err", errors.New("boom")) }, []string{"err"}, "boom", false},
		{"long error", nil, func(l *slog.Logger) { l.Info("m", "err", errors.New(xs(2000))) }, []string{"err"}, xs(1024) + cut, true},
		{"LogValuer", nil, func(l *slog.Logger) { l.Info("m", "v", valuer(xs(2000))) }, []string{"v"}, xs(1024) + cut, true},
		{"message", nil, func(l *slog.Logger) { l.Info(xs(2000)) }, []string{"msg"}, xs(1024) + cut, true},
		{"consumer exception.message", nil, func(l *slog.Logger) { l.Info("m", "exception.message", xs(2000)) }, []string{"exception.message"}, xs(1024) + cut, true},
		{"WithLogMaxAttrBytes 16", []srvr.Option{srvr.WithLogMaxAttrBytes(16)}, func(l *slog.Logger) { l.Info("m", "note", xs(20)) }, []string{"note"}, xs(16) + cut, true},
		{"MaxAttrBytes 16", []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{MaxAttrBytes: 16})}, func(l *slog.Logger) { l.Info("m", "note", xs(20)) }, []string{"note"}, xs(16) + cut, true},
		{"MaxAttrBytes 0", []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{MaxAttrBytes: 0})}, func(l *slog.Logger) { l.Info("m", "note", xs(1025)) }, []string{"note"}, xs(1024) + cut, true},
		{"MaxAttrBytes -1", []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{MaxAttrBytes: -1})}, func(l *slog.Logger) { l.Info("m", "note", xs(5000)) }, []string{"note"}, xs(5000), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) {
				g.GET("/log", func(_ http.ResponseWriter, r *http.Request) error {
					tt.log(srvr.LoggerFromContext(r.Context()))

					return nil
				})
			}, tt.opts...)
			get(t, base+"/log")

			var rec map[string]any
			eventually(t, func() bool {
				for _, r := range out.records(t) {
					if r["msg"] != "http.request.completed" {
						rec = r
					}
				}

				return rec != nil
			}, "no handler record")

			var v any = rec
			for _, k := range tt.path {
				v = v.(map[string]any)[k]
			}
			if v != tt.logged {
				t.Fatalf("logged %v, want %v", short(v), short(tt.logged))
			}
			if _, has := rec["srvr.attr.truncated"]; has != tt.flag || (has && rec["srvr.attr.truncated"] != true) {
				t.Fatalf("srvr.attr.truncated = %v, want %v", rec["srvr.attr.truncated"], tt.flag)
			}
		})
	}
}

// TestTruncation_OwnHandler: a consumer handler gets truncated values too.
func TestTruncation_OwnHandler(t *testing.T) {
	t.Parallel()

	h := newRecording()
	srv := newServer(t, srvr.WithLogHandler(h))
	mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
		g.GET("/log", func(_ http.ResponseWriter, r *http.Request) error {
			srvr.LoggerFromContext(r.Context()).Info("handler", "note", xs(1025))

			return nil
		})
	})))
	get(t, startServer(t, srv)+"/log")

	var attrs map[string]string
	eventually(t, func() bool { var ok bool; attrs, ok = h.record("handler"); return ok }, "no record")
	if attrs["note"] != xs(1024)+cut || attrs["srvr.attr.truncated"] != "true" {
		t.Fatalf("note %d bytes, flag %q", len(attrs["note"]), attrs["srvr.attr.truncated"])
	}
}

// short shortens a long value for a failure message.
func short(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 60 {
		return string(b[:60]) + "..."
	}

	return string(b)
}

// derefError dereferences its receiver, so a typed nil panics in Error.
type derefError struct{ msg string }

func (e *derefError) Error() string { return e.msg }

// TestHostileLoggedErrors covers structured-logging / Scenario: Hostile
// logged errors.
func TestHostileLoggedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		with   bool // bound with l.With instead of passed with the record
		logged string
	}{
		{"typed nil in the record", (*derefError)(nil), false, "<nil *srvr_test.derefError>"},
		{"panicking Error in the record", badError{}, false, "<panic in Error(): srvr_test.badError>"},
		{"typed nil through With", (*derefError)(nil), true, "<nil *srvr_test.derefError>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) {
				g.GET("/log", func(w http.ResponseWriter, r *http.Request) error {
					l := srvr.LoggerFromContext(r.Context())
					if tt.with {
						l.With(slog.Any("err", tt.err)).Info("logged")
					} else {
						l.Info("logged", slog.Any("err", tt.err))
					}
					w.WriteHeader(http.StatusOK)

					return nil
				})
			})

			if got := get(t, base+"/log").status; got != http.StatusOK {
				t.Fatalf("status = %d, want 200 (logging must never fail the request)", got)
			}
			if got := out.waitRecord(t, "logged")["err"]; got != tt.logged {
				t.Fatalf("err = %v, want %q", got, tt.logged)
			}
		})
	}
}

// TestDisabledLimitKinds covers structured-logging / Scenario: Text reaches
// a consumer handler as strings when the limit is disabled.
func TestDisabledLimitKinds(t *testing.T) {
	t.Parallel()

	logs := func(err error) srvr.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) error {
			srvr.LoggerFromContext(r.Context()).Info("logged", slog.Any("err", err))
			w.WriteHeader(http.StatusOK)

			return nil
		}
	}

	tests := []struct {
		name       string
		hook       func(context.Context) error // AfterDrain hook, run by Shutdown before Start
		handler    srvr.HandlerFunc            // handler of GET /x
		event, key string
		want       func(string) bool
	}{
		{
			name:  "hook error",
			hook:  func(context.Context) error { return errors.New("flush failed") },
			event: "server.shutdown_hook.failed", key: "exception.message",
			want: func(s string) bool { return s == "flush failed" },
		},
		{
			name:    "returned error",
			handler: returning(errors.New("boom")),
			event:   "http.request.failed", key: "exception.message",
			want: func(s string) bool { return s == "boom" },
		},
		{
			name:    "panic stack",
			handler: func(http.ResponseWriter, *http.Request) error { panic("boom") },
			event:   "http.request.failed", key: "exception.stacktrace",
			want: func(s string) bool { return strings.Contains(s, "goroutine ") },
		},
		{
			name:    "typed-nil logged error",
			handler: logs((*derefError)(nil)),
			event:   "logged", key: "err",
			want: func(s string) bool { return s == "<nil *srvr_test.derefError>" },
		},
		{
			name:    "logged error whose Error panics with a value whose Error panics",
			handler: logs(selfPanicError{}),
			event:   "logged", key: "err",
			want: func(s string) bool { return s == "<panic in Error(): srvr_test.selfPanicError>" },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newRecording()
			opts := []srvr.Option{srvr.WithLogHandler(h), srvr.WithLogMaxAttrBytes(-1)}

			if tt.hook != nil {
				srv := newServer(t, append(opts, srvr.WithShutdownHook(srvr.AfterDrain, tt.hook))...)
				_ = srv.Shutdown(context.Background())
			} else {
				srv := newServer(t, opts...)
				mustNil(t, srv.Register(on("", "/x", tt.handler)))
				get(t, startServer(t, srv)+"/x")
			}

			var v slog.Value
			eventually(t, func() bool {
				var ok bool
				v, ok = h.value(tt.event, tt.key)

				return ok
			}, "no "+tt.event+" record with "+tt.key)

			if v.Kind() != slog.KindString || !tt.want(v.String()) {
				t.Fatalf("%s = %s %s, want a matching string", tt.key, v.Kind(), short(safeText(v.Any())))
			}
		})
	}
}
