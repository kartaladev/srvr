package ginsrvr_test

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
)

// TestGinBodyLimit covers gin-engine / Scenario: Gin body limit.
func TestGinBodyLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opts   []srvr.Option
		size   int
		status int
	}{
		{"default at limit", nil, 4194304, 200},
		{"default past limit", nil, 4194305, 413},
		{"10 at limit", []srvr.Option{srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: 10})}, 10, 200},
		{"10 past limit", []srvr.Option{srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: 10})}, 11, 413},
		{"disabled", []srvr.Option{srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: -1})}, 8388608, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t, tt.opts...)
			if err := srv.Register(routes(func(g *gin.RouterGroup) {
				g.POST("/upload", func(c *gin.Context) {
					if _, err := io.ReadAll(c.Request.Body); err != nil {
						_ = c.Error(err)

						return
					}

					c.Status(http.StatusOK)
				})
			})); err != nil {
				t.Fatal(err)
			}

			res := do(t, http.MethodPost, start(t, srv)+"/upload", bytes.NewReader(make([]byte, tt.size)), nil)
			if res.status != tt.status {
				t.Fatalf("status %d, want %d", res.status, tt.status)
			}
		})
	}
}
