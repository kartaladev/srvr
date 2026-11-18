package ginsrvr_test

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

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
