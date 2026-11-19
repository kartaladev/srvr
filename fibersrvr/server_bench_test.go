package fibersrvr_test

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

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
// chain and a success on a skip path, whose access record is suppressed. Each case runs sequentially and, as "<case> parallel",
// from GOMAXPROCS goroutines with b.RunParallel. app.Test's in-memory
// connection is part of every measurement.
//
// A body over MaxRequestBodyBytes has no row: fasthttp rejects it before
// fiber runs, and app.Test returns fasthttp's error instead of the 413,
// so measuring it would need a listener, timing TCP instead of srvr.
func BenchmarkPipeline(b *testing.B) {
	srv, err := fibersrvr.New(srvr.WithLogOutput(io.Discard), srvr.WithAccessLogSkipPaths("/health"))
	if err != nil {
		b.Fatal(err)
	}

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
	}{
		{"success", "/ok", http.StatusOK},
		{"handler error", "/fail", http.StatusInternalServerError},
		{"unmatched", "/nope", http.StatusNotFound},
		{"panic", "/panic", http.StatusInternalServerError},
		{"32-link error chain", "/chain", http.StatusInternalServerError},
		{"suppressed access record", "/health", http.StatusOK},
	}

	for _, bc := range cases {
		// do sends one request; it reports failures with Error, as
		// RunParallel's goroutines must not call FailNow.
		do := func(b *testing.B) int {
			b.Helper()

			req, err := http.NewRequestWithContext(b.Context(), http.MethodGet, bc.path, nil)
			if err != nil {
				b.Error(err)

				return 0
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
