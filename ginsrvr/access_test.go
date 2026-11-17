package ginsrvr_test

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
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
