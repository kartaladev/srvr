package ginsrvr_test

import (
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestGinUnmatched covers gin-engine / Scenario: Gin no route or wrong
// method, with POST /users registered before GET /users.
func TestGinUnmatched(t *testing.T) {
	t.Parallel()

	srv, _ := newServer(t)
	if err := srv.Register(routes(func(g *gin.RouterGroup) {
		g.POST("/users", ok)
		g.GET("/users", ok)
	})); err != nil {
		t.Fatal(err)
	}

	base := start(t, srv)

	// The client must see gin's answer, not follow a redirect.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	tests := []struct {
		method, path string
		status       int
		allow        string
	}{
		{http.MethodGet, "/nope", http.StatusNotFound, ""},
		{http.MethodDelete, "/users", http.StatusMethodNotAllowed, "GET, POST"},
		{http.MethodPatch, "/nope", http.StatusNotFound, ""},
		{http.MethodGet, "/users/", http.StatusNotFound, ""},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(t.Context(), tt.method, base+tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}

			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()

			if res.StatusCode != tt.status || res.Header.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("response %d %q, want %d problem", res.StatusCode, res.Header.Get("Content-Type"), tt.status)
			}
			if got := res.Header.Get("Allow"); got != tt.allow {
				t.Fatalf("Allow = %q, want %q", got, tt.allow)
			}
			if loc := res.Header.Get("Location"); loc != "" {
				t.Fatalf("Location = %q, want none", loc)
			}
		})
	}
}
