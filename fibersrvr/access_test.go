package fibersrvr_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// TestFiberAccessRoute covers fiber-engine / Scenario: Fiber route
// attribute, with the route behind group middleware.
func TestFiberAccessRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, path string
		msg, level   string
		route        any
		status       float64
	}{
		{http.MethodGet, "/users/42", "http.request.completed", "INFO", "/users/:id", 200},
		{http.MethodGet, "/nope", "http.request.failed", "WARN", nil, 404},
		{http.MethodDelete, "/users/42", "http.request.failed", "WARN", nil, 405},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			srv, logs := newServer(t)
			serve(t, srv, routes(func(r fiber.Router) {
				r.Group("/users", func(c fiber.Ctx) error { return c.Next() }).Get("/:id", ok)
			}), tt.method, tt.path, nil, nil)

			recs := logs.access(t)
			if len(recs) != 1 {
				t.Fatalf("%d access records, want 1", len(recs))
			}

			r := recs[0]
			if r["msg"] != tt.msg || r["level"] != tt.level || r["http.route"] != tt.route || r["http.response.status_code"] != tt.status {
				t.Fatalf("record %v, want msg %s level %s route %v status %v", r, tt.msg, tt.level, tt.route, tt.status)
			}
		})
	}
}

// TestFiberAccessLocation: a failed record names the route's handler, as
// on net/http and gin (triage-error-records / Scenario: Handler location).
func TestFiberAccessLocation(t *testing.T) {
	t.Parallel()

	srv, logs := newServer(t)
	serve(t, srv, routes(func(r fiber.Router) { r.Get("/f", failingHandler) }), http.MethodGet, "/f", nil, nil)

	recs := logs.access(t)
	if len(recs) != 1 || recs[0]["code.function.name"] != "github.com/kartaladev/srvr/fibersrvr_test.failingHandler" || recs[0]["code.line.number"] == nil {
		t.Fatalf("records %v, want the handler's location", recs)
	}
}

func failingHandler(fiber.Ctx) error { return errFailing }

var errFailing = errors.New("failing")
