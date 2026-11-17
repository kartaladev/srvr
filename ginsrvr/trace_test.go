package ginsrvr_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
)

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
