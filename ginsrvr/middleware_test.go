package ginsrvr_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestGinMiddleware covers gin-engine / Scenario: Consumer middleware
// behaviour.
func TestGinMiddleware(t *testing.T) {
	t.Parallel()

	errAuth := errors.New("unauthorized")

	tests := []struct {
		name   string
		mw     gin.HandlerFunc
		status int
		msg    string
	}{
		{"calls Next", func(c *gin.Context) { c.Next() }, 200, "http.request.completed"},
		{"AbortWithStatus 401", func(c *gin.Context) { c.AbortWithStatus(http.StatusUnauthorized) }, 401, "http.request.completed"},
		{"AbortWithError 401", func(c *gin.Context) { _ = c.AbortWithError(http.StatusUnauthorized, errAuth) }, 401, "http.request.failed"},
		{"panics", func(*gin.Context) { panic("mw") }, 500, "http.request.failed"},
		{"writes 200 then c.Error", func(c *gin.Context) {
			c.String(http.StatusOK, "ok")
			_ = c.Error(errors.New("late"))
			c.Abort()
		}, 200, "http.request.failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, logs := newServer(t)
			if err := srv.Register(routes(func(g *gin.RouterGroup) {
				g.Group("/", tt.mw).GET("/x", ok)
			})); err != nil {
				t.Fatal(err)
			}

			res := do(t, http.MethodGet, start(t, srv)+"/x", nil, nil)
			if res.status != tt.status {
				t.Fatalf("status %d, want %d", res.status, tt.status)
			}

			recs := logs.access(t)
			if len(recs) != 1 || recs[0]["msg"] != tt.msg {
				t.Fatalf("access records %v, want one %s", recs, tt.msg)
			}
		})
	}
}
