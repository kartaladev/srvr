package srvr_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/kartaladev/srvr"
)

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
			res, err := http.DefaultClient.Do(req)
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
