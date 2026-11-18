package srvr

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// BenchmarkPipeline measures one request through the net/http engine's
// full pipeline (trace context, access log, body limit, recovery,
// unmatched-route answers, router) with logging to io.Discard: a success,
// a handler error rendered as a 500 problem and an unmatched path
// answered as a 404 problem.
func BenchmarkPipeline(b *testing.B) {
	srv, err := New(WithLogOutput(io.Discard))
	if err != nil {
		b.Fatal(err)
	}

	mux, _, err := srv.router([]RouteConfigurer[*Group]{routes(func(g *Group) {
		g.GET("/ok", func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(http.StatusOK)

			return nil
		})
		g.GET("/fail", func(http.ResponseWriter, *http.Request) error { return errors.New("boom") })
	})})
	if err != nil {
		b.Fatal(err)
	}

	h := srv.pipeline(mux)

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
