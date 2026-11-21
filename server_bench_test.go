package srvr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
// as a 404 problem, a panic recovered as a 500, a known path with
// another method answered as a 405 problem, a body over
// MaxRequestBodyBytes answered 413, a 32-link error chain, a success
// from a client sending a User-Agent that WithoutAccessLogUserAgent
// leaves out of the record, a file a static mount serves from disk, and
// the handler error with an error hook installed, and success and the error rows with
// trivialRenderer (design D10).
// Each case
// runs sequentially and, as "<case> parallel", from GOMAXPROCS goroutines
// with b.RunParallel.
func BenchmarkPipeline(b *testing.B) {
	for _, bc := range pipelineCases {
		h := pipelineHandler(b, bc.static, bc.opts...)

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

// pipelineHandler returns the pipeline of a server built with opts that
// logs to io.Discard and limits request bodies to benchBodyLimit, routing
// the paths of pipelineCases, and with static, serving benchSite at
// /assets.
func pipelineHandler(b *testing.B, static bool, opts ...Option) http.Handler {
	b.Helper()

	srv, err := New(append([]Option{WithLogOutput(io.Discard), WithMaxRequestBodyBytes(benchBodyLimit)}, opts...)...)
	if err != nil {
		b.Fatal(err)
	}

	chain := errorChain(32)

	confs := []RouteConfigurer[*Group]{routes(func(g *Group) {
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
	})}
	if static {
		confs = append(confs, Static("/assets", benchSite(b)))
	}

	mux, _, err := srv.router(confs)
	if err != nil {
		b.Fatal(err)
	}

	return srv.pipeline(mux)
}

// benchSite returns a directory file system holding app.js, as a build
// directory served from disk is.
func benchSite(b *testing.B) fs.FS {
	b.Helper()

	dir := b.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		b.Fatal(err)
	}

	return os.DirFS(dir)
}

// pipelineCase is one request of BenchmarkPipeline.
type pipelineCase struct {
	name, method, path string
	body               []byte
	status             int
	// userAgent is the request's User-Agent header, none when empty.
	userAgent string
	// static adds a static mount of benchSite at /assets, which only its
	// case gets, so the others measure a server without one.
	static bool
	// opts are the case's server options besides the benchmark's own.
	opts []Option
}

// pipelineCases are the requests of BenchmarkPipeline.
var pipelineCases = []pipelineCase{
	{name: "success", method: http.MethodGet, path: "/ok", status: http.StatusOK},
	{name: "handler error", method: http.MethodGet, path: "/fail", status: http.StatusInternalServerError},
	{name: "unmatched", method: http.MethodGet, path: "/nope", status: http.StatusNotFound},
	{name: "panic", method: http.MethodGet, path: "/panic", status: http.StatusInternalServerError},
	{name: "method not allowed", method: http.MethodGet, path: "/upload", status: http.StatusMethodNotAllowed},
	{
		name: "body over limit", method: http.MethodPost, path: "/upload",
		body: make([]byte, 2*benchBodyLimit), status: http.StatusRequestEntityTooLarge,
	},
	{name: "32-link error chain", method: http.MethodGet, path: "/chain", status: http.StatusInternalServerError},
	{
		name: "user agent omitted", method: http.MethodGet, path: "/ok", status: http.StatusOK,
		userAgent: "bench-client/1.0", opts: []Option{WithoutAccessLogUserAgent()},
	},
	{name: "static", method: http.MethodGet, path: "/assets/app.js", status: http.StatusOK, static: true},
	{
		// design D8: compared with "handler error", zero added allocs/op.
		name: "handler error with error hook", method: http.MethodGet, path: "/fail", status: http.StatusInternalServerError,
		opts: []Option{WithErrorHook(func(context.Context, error, ErrorReport) {})},
	},
	// design D10: the trivial custom renderer on every error row and success.
	{
		name: "success trivial renderer", method: http.MethodGet, path: "/ok", status: http.StatusOK,
		opts: []Option{WithErrorRenderer(trivialRenderer)},
	},
	{
		name: "handler error trivial renderer", method: http.MethodGet, path: "/fail", status: http.StatusInternalServerError,
		opts: []Option{WithErrorRenderer(trivialRenderer)},
	},
	{
		name: "unmatched trivial renderer", method: http.MethodGet, path: "/nope", status: http.StatusNotFound,
		opts: []Option{WithErrorRenderer(trivialRenderer)},
	},
	{
		name: "panic trivial renderer", method: http.MethodGet, path: "/panic", status: http.StatusInternalServerError,
		opts: []Option{WithErrorRenderer(trivialRenderer)},
	},
	{
		name: "method not allowed trivial renderer", method: http.MethodGet, path: "/upload", status: http.StatusMethodNotAllowed,
		opts: []Option{WithErrorRenderer(trivialRenderer)},
	},
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

	if bc.userAgent != "" {
		req.Header.Set("User-Agent", bc.userAgent)
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

// trivialBody is the trivial renderer's fixed 20-byte JSON body.
var trivialBody = []byte(`{"code":"bad_thing"}`)

// trivialRenderer is design D10's trivial custom renderer: a fixed
// 20-byte JSON body with the status of srvr's own errors (404, 405) and
// 500 otherwise.
var trivialRenderer = ErrorRendererFunc(func(_ context.Context, _ ErrorRequest, err error) ErrorResponse {
	status := http.StatusInternalServerError

	var mna *MethodNotAllowedError

	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.As(err, &mna):
		status = http.StatusMethodNotAllowed
	}

	return ErrorResponse{
		Status: status,
		Header: http.Header{"Content-Type": {"application/json"}},
		Body:   trivialBody,
	}
})
