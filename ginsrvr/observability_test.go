package ginsrvr_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// TestGinAccessRoute covers gin-engine / Scenario: Gin route attribute.
func TestGinAccessRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, path string
		msg, level   string
		route        any // nil: no http.route
		status       float64
	}{
		{http.MethodGet, "/users/42", "http.request.completed", "INFO", "/users/:id", 200},
		{http.MethodGet, "/nope", "http.request.failed", "WARN", nil, 404},
		{http.MethodDelete, "/users/42", "http.request.failed", "WARN", nil, 405},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			srv, logs := newServer(t)
			if err := srv.Register(routes(func(g *gin.RouterGroup) { g.GET("/users/:id", ok) })); err != nil {
				t.Fatal(err)
			}

			do(t, tt.method, start(t, srv)+tt.path, nil, nil)

			recs := logs.access(t)
			if len(recs) != 1 {
				t.Fatalf("%d access records, want 1", len(recs))
			}

			r := recs[0]
			if r["msg"] != tt.msg || r["level"] != tt.level || r["http.route"] != tt.route || r["http.response.status_code"] != tt.status {
				t.Fatalf("record %v, want msg %s level %s route %v status %v", r, tt.msg, tt.level, tt.route, tt.status)
			}
		})
	}
}

// TestGinTraceAccess covers gin-engine / Scenario: Reading trace through
// gin.Context.
func TestGinTraceAccess(t *testing.T) {
	t.Parallel()

	const (
		traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		traceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	)

	tests := []struct {
		name   string
		read   func(c *gin.Context) (string, bool)
		logged bool // the handler logs through the accessor's logger
	}{
		{"TraceFromContext(c)", func(c *gin.Context) (string, bool) {
			tr, ok := srvr.TraceFromContext(c)

			return tr.TraceID, ok
		}, false},
		{"TraceFromContext(c.Request.Context())", func(c *gin.Context) (string, bool) {
			tr, ok := srvr.TraceFromContext(c.Request.Context())

			return tr.TraceID, ok
		}, false},
		{"LoggerFromContext(c)", func(c *gin.Context) (string, bool) {
			srvr.LoggerFromContext(c).InfoContext(context.Background(), "handler.note")

			return "", true
		}, true},
		{"TraceFromContext(c.Copy()) in a goroutine", func(c *gin.Context) (string, bool) {
			cp := c.Copy()
			type result struct {
				id string
				ok bool
			}
			ch := make(chan result, 1)
			go func() {
				tr, ok := srvr.TraceFromContext(cp)
				ch <- result{tr.TraceID, ok}
			}()
			r := <-ch

			return r.id, r.ok
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				gotID string
				gotOK bool
			)

			srv, logs := newServer(t)
			if err := srv.Register(routes(func(g *gin.RouterGroup) {
				g.GET("/t", func(c *gin.Context) {
					gotID, gotOK = tt.read(c)
					c.Status(http.StatusOK)
				})
			})); err != nil {
				t.Fatal(err)
			}

			do(t, http.MethodGet, start(t, srv)+"/t", nil, map[string]string{"traceparent": traceparent})

			if tt.logged {
				notes := logs.withMsg(t, "handler.note")
				if len(notes) != 1 || notes[0]["trace_id"] != traceID {
					t.Fatalf("handler records %v, want one with trace_id %s", notes, traceID)
				}

				return
			}

			if !gotOK || gotID != traceID {
				t.Fatalf("trace = %q, %v; want %s, true", gotID, gotOK, traceID)
			}
		})
	}
}

// recordingObserver records every Begin and end it sees, with the trace
// id at Begin.
type recordingObserver struct {
	mu      sync.Mutex
	begins  []begin
	results []srvr.ResultInfo
}

// begin is what the observer saw at Begin.
type begin struct{ method, traceID string }

func (o *recordingObserver) Begin(ctx context.Context, info srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
	tr, _ := srvr.TraceFromContext(ctx)

	o.mu.Lock()
	o.begins = append(o.begins, begin{info.Method, tr.TraceID})
	o.mu.Unlock()

	return ctx, func(res srvr.ResultInfo) {
		o.mu.Lock()
		defer o.mu.Unlock()

		o.results = append(o.results, res)
	}
}

func (o *recordingObserver) seen() ([]begin, []srvr.ResultInfo) {
	o.mu.Lock()
	defer o.mu.Unlock()

	return append([]begin(nil), o.begins...), append([]srvr.ResultInfo(nil), o.results...)
}

// TestObserver covers request-observer "Begin and end per engine" and
// "Observer view of the request", gin rows.
func TestObserver(t *testing.T) {
	t.Parallel()

	const (
		incoming = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		traceID  = "4bf92f3577b34da6a3ce929d0e0e4736"
	)

	tests := []struct {
		name    string
		handler gin.HandlerFunc
		path    string
		header  map[string]string
		route   string
		status  int
		kind    srvr.ErrorKind
	}{
		{"writes 200", ok, "/users/42", map[string]string{"traceparent": incoming}, "/users/:id", 200, srvr.ErrorKindNone},
		{"ErrNotFound", func(c *gin.Context) { _ = c.Error(prblm.ErrNotFound) }, "/users/42", nil, "/users/:id", 404, srvr.ErrorKindError},
		{"boom", func(c *gin.Context) { _ = c.Error(errors.New("boom")) }, "/users/42", nil, "/users/:id", 500, srvr.ErrorKindError},
		{"panics", func(*gin.Context) { panic("boom") }, "/users/42", nil, "/users/:id", 500, srvr.ErrorKindPanic},
		{"unmatched", ok, "/nope", nil, "", 404, srvr.ErrorKindError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs := &recordingObserver{}
			srv, _ := newServer(t, srvr.WithObserver(obs))
			if err := srv.Register(routes(func(g *gin.RouterGroup) { g.GET("/users/:id", tt.handler) })); err != nil {
				t.Fatal(err)
			}

			if got := do(t, http.MethodGet, start(t, srv)+tt.path, nil, tt.header).status; got != tt.status {
				t.Fatalf("client status = %d, want %d", got, tt.status)
			}

			begins, results := obs.seen()
			if len(begins) != 1 || len(results) != 1 {
				t.Fatalf("Begin calls = %d, end calls = %d, want 1 and 1", len(begins), len(results))
			}

			continued := tt.header != nil
			if b := begins[0]; b.method != http.MethodGet || len(b.traceID) != 32 || (continued && b.traceID != traceID) {
				t.Errorf("Begin saw %+v", b)
			}

			if got := results[0]; got.Route != tt.route || got.Status != tt.status || got.ErrorKind != tt.kind {
				t.Errorf("end route, status, kind = %q, %d, %q, want %q, %d, %q", got.Route, got.Status, got.ErrorKind, tt.route, tt.status, tt.kind)
			}
		})
	}
}

// TestDefaultLoggerAfterConstruction covers engine-support / Scenario:
// Default logger after construction, gin rows: a construction that fails
// leaves slog.Default() unchanged.
//
//nolint:paralleltest // slog.SetDefault is process-wide.
func TestDefaultLoggerAfterConstruction(t *testing.T) {
	tests := []struct {
		name    string
		opt     srvr.Option // nil adds nothing
		wantErr string      // "" when construction succeeds
	}{
		{"nothing else", nil, ""},
		{"a nil WithEngine hook", ginsrvr.WithEngine(nil), "WithEngine"},
		{"a WithEngine hook that panics", ginsrvr.WithEngine(func(*gin.Engine) { panic("boom") }), "boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkGoroutines(t)

			before := slog.Default()
			t.Cleanup(func() { slog.SetDefault(before) })

			var buf bytes.Buffer

			opts := []srvr.Option{srvr.WithSetDefaultLogger(), srvr.WithLogHandler(slog.NewJSONHandler(&buf, nil))}
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}

			_, err := ginsrvr.New(opts...)

			wantOK := tt.wantErr == ""

			switch {
			case wantOK && err != nil:
				t.Fatalf("New = %v", err)
			case !wantOK && (!errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("New = %v, want an ErrConfig containing %s", err, tt.wantErr)
			}

			buf.Reset()
			slog.Info("probe")

			reached := strings.Contains(buf.String(), "probe")
			if reached != wantOK {
				t.Fatalf("a record through slog.Default() reached the server's handler: %v; New error: %v", reached, err)
			}
		})
	}
}
