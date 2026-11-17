package fibersrvr_test

import (
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// TestFiberUnmatched covers fiber-engine / Scenario: Fiber no route or
// wrong method.
func TestFiberUnmatched(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, path string
		status       int
		allow        string
	}{
		{http.MethodGet, "/nope", http.StatusNotFound, ""},
		{http.MethodDelete, "/users", http.StatusMethodNotAllowed, "GET, HEAD, POST"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t)
			res := serve(t, srv, routes(func(r fiber.Router) {
				r.Get("/users", ok)
				r.Post("/users", ok)
			}), tt.method, tt.path, nil, nil)

			if res.status != tt.status || res.header.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("response %d %q, want %d problem", res.status, res.header.Get("Content-Type"), tt.status)
			}
			if got := res.header.Get("Allow"); got != tt.allow {
				t.Fatalf("Allow = %q, want %q", got, tt.allow)
			}
		})
	}
}
