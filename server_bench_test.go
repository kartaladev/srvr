package srvr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// benchBodyLimit is the MaxRequestBodyBytes of the pipeline benchmarks:
// small, so the over-limit row reads little.
const benchBodyLimit = 1 << 10

// errorChain returns an error chain of n links: n-1 wrappers around a root.
func errorChain(n int) error {
	err := errors.New("root")
	for i := 1; i < n; i++ {
		err = fmt.Errorf("layer %d: %w", i, err)
	}

	return err
}

// BenchmarkPipeline measures one request through the net/http engine's
// full pipeline (trace context, access log, body limit, recovery,
// unmatched-route answers, router) with logging to io.Discard: a success,
// a handler error rendered as a 500 problem, an unmatched path answered
// as a 404 problem, a panic recovered as a 500, a body over
// MaxRequestBodyBytes answered 413 and a 32-link error chain. Each case
// runs sequentially and, as "<case> parallel", from GOMAXPROCS goroutines
// with b.RunParallel.
func BenchmarkPipeline(b *testing.B) {
	srv, err := New(WithLogOutput(io.Discard), WithMaxRequestBodyBytes(benchBodyLimit))
	if err != nil {
		b.Fatal(err)
	}

	chain := errorChain(32)

	mux, _, err := srv.router([]RouteConfigurer[*Group]{routes(func(g *Group) {
		g.GET("/ok", func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(http.StatusOK)

			return nil
		})
		g.GET("/fail", func(http.ResponseWriter, *http.Request) error { return errors.New("boom") })
		g.GET("/panic", func(http.ResponseWriter, *http.Request) error { panic("boom") })
		g.POST("/upload", func(_ http.ResponseWriter, r *http.Request) error {
			_, err := io.Copy(io.Discard, r.Body)

			return err
		})
		g.GET("/chain", func(http.ResponseWriter, *http.Request) error { return chain })
	})})
	if err != nil {
		b.Fatal(err)
	}

	h := srv.pipeline(mux)

	for _, bc := range pipelineCases {
		b.Run(bc.name, func(b *testing.B) {
			req := bc.request(b)
			if w := serve(h, req); w.Code != bc.status {
				b.Fatalf("%s %s = %d, want %d", bc.method, bc.path, w.Code, bc.status)
			}

			b.ReportAllocs()

			for b.Loop() {
				serve(h, req)
			}
		})

		b.Run(bc.name+" parallel", func(b *testing.B) {
			b.ReportAllocs()

			b.RunParallel(func(pb *testing.PB) {
				req := bc.request(b) // one request, and body, per goroutine
				for pb.Next() {
					serve(h, req)
				}
			})
		})
	}
}

// pipelineCase is one request of BenchmarkPipeline.
type pipelineCase struct {
	name, method, path string
	body               []byte
	status             int
}

// pipelineCases are the requests of BenchmarkPipeline.
var pipelineCases = []pipelineCase{
	{name: "success", method: http.MethodGet, path: "/ok", status: http.StatusOK},
	{name: "handler error", method: http.MethodGet, path: "/fail", status: http.StatusInternalServerError},
	{name: "unmatched", method: http.MethodGet, path: "/nope", status: http.StatusNotFound},
	{name: "panic", method: http.MethodGet, path: "/panic", status: http.StatusInternalServerError},
	{
		name: "body over limit", method: http.MethodPost, path: "/upload",
		body: make([]byte, 2*benchBodyLimit), status: http.StatusRequestEntityTooLarge,
	},
	{name: "32-link error chain", method: http.MethodGet, path: "/chain", status: http.StatusInternalServerError},
}

// request returns the case's request. serve rewinds its body, so it is
// reused across iterations; it is not safe for concurrent use.
func (bc pipelineCase) request(b *testing.B) *http.Request {
	b.Helper()

	req := httptest.NewRequestWithContext(b.Context(), bc.method, bc.path, nil)
	if bc.body != nil {
		req.Body = rewindBody{bytes.NewReader(bc.body)}
		req.ContentLength = int64(len(bc.body))
	}

	return req
}

// rewindBody is a request body serve rewinds before every request.
type rewindBody struct{ *bytes.Reader }

func (rewindBody) Close() error { return nil }

// serve rewinds req's body, serves req with h and returns the recorder.
func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	if body, ok := req.Body.(rewindBody); ok {
		_, _ = body.Seek(0, io.SeekStart)
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	return w
}
