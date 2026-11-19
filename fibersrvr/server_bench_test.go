package fibersrvr_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
)

// errorChain returns an error chain of n links: n-1 wrappers around a
// root.
func errorChain(n int) error {
	err := errors.New("root")
	for i := 1; i < n; i++ {
		err = fmt.Errorf("layer %d: %w", i, err)
	}

	return err
}

// BenchmarkPipeline measures one request through the fiber engine's full
// pipeline with app.Test (no listener) and logging to io.Discard: a
// success, a handler error rendered as a 500 problem, an unmatched path
// answered as a 404 problem, a panic recovered as a 500, a 32-link error
// chain, a success on a skip path, whose access record is suppressed, an
// unmatched path whose record WithUnmatchedRecordLimit suppresses, and a
// success from a client sending a User-Agent that
// WithoutAccessLogUserAgent leaves out of the record. Each case runs
// sequentially and, as "<case> parallel",
// from GOMAXPROCS goroutines with b.RunParallel. app.Test's in-memory
// connection is part of every measurement.
//
// A body over MaxRequestBodyBytes has no row: fasthttp rejects it before
// fiber runs, and app.Test returns fasthttp's error instead of the 413,
// so measuring it would need a listener, timing TCP instead of srvr.
func BenchmarkPipeline(b *testing.B) {
	chain := errorChain(32)

	conf := routes(func(r fiber.Router) {
		r.Get("/ok", ok)
		r.Get("/health", ok)
		r.Get("/fail", func(fiber.Ctx) error { return errors.New("boom") })
		r.Get("/panic", func(fiber.Ctx) error { panic("boom") })
		r.Get("/chain", func(fiber.Ctx) error { return chain })
	})

	cases := []struct {
		name, path string
		status     int
		// userAgent is the request's User-Agent header; empty sends
		// net/http's default.
		userAgent string
		// opts are the case's server options besides the benchmark's own.
		opts []srvr.Option
	}{
		{name: "success", path: "/ok", status: http.StatusOK},
		{name: "handler error", path: "/fail", status: http.StatusInternalServerError},
		{name: "unmatched", path: "/nope", status: http.StatusNotFound},
		{name: "panic", path: "/panic", status: http.StatusInternalServerError},
		{name: "32-link error chain", path: "/chain", status: http.StatusInternalServerError},
		{name: "suppressed access record", path: "/health", status: http.StatusOK},
		{
			// the first request, the status check below, takes the window's
			// only record: every measured one is suppressed.
			name: "suppressed unmatched record", path: "/nope", status: http.StatusNotFound,
			opts: []srvr.Option{srvr.WithUnmatchedRecordLimit(1, time.Hour)},
		},
		{
			name: "user agent omitted", path: "/ok", status: http.StatusOK,
			userAgent: "bench-client/1.0", opts: []srvr.Option{srvr.WithoutAccessLogUserAgent()},
		},
	}

	for _, bc := range cases {
		srv := pipelineServer(b, bc.opts...)

		// do sends one request; it reports failures with Error, as
		// RunParallel's goroutines must not call FailNow.
		do := func(b *testing.B) int {
			b.Helper()

			req, err := http.NewRequestWithContext(b.Context(), http.MethodGet, bc.path, nil)
			if err != nil {
				b.Error(err)

				return 0
			}

			if bc.userAgent != "" {
				req.Header.Set("User-Agent", bc.userAgent)
			}

			res, err := srv.Test(req, conf)
			if err != nil {
				b.Error(err)

				return 0
			}
			defer res.Body.Close()

			if _, err := io.Copy(io.Discard, res.Body); err != nil {
				b.Error(err)
			}

			return res.StatusCode
		}

		b.Run(bc.name, func(b *testing.B) {
			if got := do(b); got != bc.status {
				b.Fatalf("GET %s = %d, want %d", bc.path, got, bc.status)
			}

			b.ReportAllocs()

			for b.Loop() {
				do(b)
			}
		})

		b.Run(bc.name+" parallel", func(b *testing.B) {
			b.ReportAllocs()

			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					do(b)
				}
			})
		})
	}
}

// pipelineServer returns a server built with opts that logs to io.Discard
// and suppresses the access records of /health.
func pipelineServer(b *testing.B, opts ...srvr.Option) *fibersrvr.Server {
	b.Helper()

	srv, err := fibersrvr.New(append([]srvr.Option{srvr.WithLogOutput(io.Discard), srvr.WithAccessLogSkipPaths("/health")}, opts...)...)
	if err != nil {
		b.Fatal(err)
	}

	return srv
}
