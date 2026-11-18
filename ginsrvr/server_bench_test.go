package ginsrvr

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
)

// BenchmarkPipeline measures one request through the gin engine's full
// pipeline (trace context, access log, body limit, gin with srvr's
// request stage) with logging to io.Discard: a success, a handler error
// rendered as a 500 problem and an unmatched path answered as a 404
// problem.
func BenchmarkPipeline(b *testing.B) {
	srv, err := New(srvr.WithLogOutput(io.Discard))
	if err != nil {
		b.Fatal(err)
	}

	if _, err := srv.configure([]srvr.RouteConfigurer[*gin.RouterGroup]{routes(func(g *gin.RouterGroup) {
		g.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) })
		g.GET("/fail", func(c *gin.Context) { _ = c.Error(errors.New("boom")) })
	})}); err != nil {
		b.Fatal(err)
	}

	h := srv.handler()

	cases := []struct {
		name   string
		path   string
		status int
	}{
		{"success", "/ok", http.StatusOK},
		{"handler error", "/fail", http.StatusInternalServerError},
		{"unmatched", "/nope", http.StatusNotFound},
	}

	for _, bc := range cases {
		b.Run(bc.name, func(b *testing.B) {
			req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, bc.path, nil)

			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			if w.Code != bc.status {
				b.Fatalf("GET %s = %d, want %d", bc.path, w.Code, bc.status)
			}

			b.ReportAllocs()

			for b.Loop() {
				h.ServeHTTP(httptest.NewRecorder(), req)
			}
		})
	}
}
