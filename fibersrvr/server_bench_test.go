package fibersrvr_test

import (
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
)

// BenchmarkPipeline measures one request through the fiber engine's full
// pipeline with app.Test (no listener) and logging to io.Discard: a
// success, a handler error rendered as a 500 problem and an unmatched
// path answered as a 404 problem. app.Test's in-memory connection is part
// of every measurement.
func BenchmarkPipeline(b *testing.B) {
	srv, err := fibersrvr.New(srvr.WithLogOutput(io.Discard))
	if err != nil {
		b.Fatal(err)
	}

	conf := routes(func(r fiber.Router) {
		r.Get("/ok", ok)
		r.Get("/fail", func(fiber.Ctx) error { return errors.New("boom") })
	})

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
			do := func() int {
				req, err := http.NewRequestWithContext(b.Context(), http.MethodGet, bc.path, nil)
				if err != nil {
					b.Fatal(err)
				}

				res, err := srv.Test(req, conf)
				if err != nil {
					b.Fatal(err)
				}
				defer res.Body.Close()

				if _, err := io.Copy(io.Discard, res.Body); err != nil {
					b.Fatal(err)
				}

				return res.StatusCode
			}

			if got := do(); got != bc.status {
				b.Fatalf("GET %s = %d, want %d", bc.path, got, bc.status)
			}

			b.ReportAllocs()

			for b.Loop() {
				do()
			}
		})
	}
}
