package ginsrvr_test

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/prblm"
)

// TestGinErrors covers gin-engine / Scenario: Gin error to problem.
func TestGinErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		handler     gin.HandlerFunc
		status      int
		contentType string
		hidden      string
	}{
		{"JSON success", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }, 200, "application/json; charset=utf-8", ""},
		{"c.Error(ErrNotFound)", func(c *gin.Context) { _ = c.Error(prblm.ErrNotFound) }, 404, "application/problem+json", ""},
		{"AbortWithError 409", func(c *gin.Context) { _ = c.AbortWithError(http.StatusConflict, errors.New("dup key alice")) }, 409, "application/problem+json", "alice"},
		{"unmapped error", func(c *gin.Context) { _ = c.Error(errors.New("pq: auth failed")) }, 500, "application/problem+json", "pq:"},
		{"AbortWithStatus 403", func(c *gin.Context) { c.AbortWithStatus(http.StatusForbidden) }, 403, "application/problem+json", ""},
		{"two errors, the last wins", func(c *gin.Context) {
			_ = c.Error(errors.New("err1"))
			_ = c.Error(prblm.ErrConflict)
		}, 409, "application/problem+json", ""},
		{"c.Error(nil) panics in gin", func(c *gin.Context) { _ = c.Error(nil) }, 500, "application/problem+json", "err is nil"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t)
			if err := srv.Register(routes(func(g *gin.RouterGroup) { g.GET("/items/:id", tt.handler) })); err != nil {
				t.Fatal(err)
			}

			res := do(t, http.MethodGet, start(t, srv)+"/items/7", nil, nil)

			if res.status != tt.status || res.header.Get("Content-Type") != tt.contentType {
				t.Fatalf("response %d %q %s, want %d %q", res.status, res.header.Get("Content-Type"), res.body, tt.status, tt.contentType)
			}
			if tt.hidden != "" && strings.Contains(res.body, tt.hidden) {
				t.Fatalf("body %s contains %q", res.body, tt.hidden)
			}
			if tt.status >= 400 {
				if res.problem == nil {
					t.Fatalf("no problem in %s", res.body)
				}
				if id, _ := res.problem.Ext("trace_id"); len(fmt.Sprint(id)) != 32 {
					t.Fatalf("problem %s has no trace_id", res.body)
				}
			}
		})
	}
}
