package fibersrvr_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr/engine"
)

// newSub returns a sub-app with GET /users.
func newSub() *fiber.App {
	sub := fiber.New()
	sub.Get("/users", ok)

	return sub
}

// TestSubAppRouteListing covers engine-support / Scenario: Sub-app route
// listing.
func TestSubAppRouteListing(t *testing.T) {
	t.Parallel()

	const owner = "*fibersrvr_test.UserRoutes"

	audit := func(c fiber.Ctx) error { return c.Next() }

	tests := []struct {
		name    string
		declare func(fiber.Router)
		want    engine.Routes
		probe   string // a listed path that must answer 200
	}{
		{"mounted at /api", func(r fiber.Router) {
			r.Use("/api", newSub())
		}, engine.Routes{{Method: "GET", Pattern: "/api/users", Configurer: owner}}, "/api/users"},
		{"route added after mounting", func(r fiber.Router) {
			sub := newSub()
			r.Use("/api", sub)
			sub.Post("/users", ok)
		}, engine.Routes{{Method: "GET", Pattern: "/api/users", Configurer: owner}, {Method: "POST", Pattern: "/api/users", Configurer: owner}}, ""},
		{"nested mounts", func(r fiber.Router) {
			inner, outer := fiber.New(), fiber.New()
			inner.Get("/x", ok)
			outer.Use("/v2", inner)
			r.Use("/api", outer)
		}, engine.Routes{{Method: "GET", Pattern: "/api/v2/x", Configurer: owner}}, "/api/v2/x"},
		{"sub-app middleware excluded", func(r fiber.Router) {
			sub := fiber.New()
			sub.Use(audit)
			sub.Get("/users", ok)
			r.Use("/api", sub)
		}, engine.Routes{{Method: "GET", Pattern: "/api/users", Configurer: owner}}, ""},
		{"mounted at /", func(r fiber.Router) {
			r.Use("/", newSub())
		}, engine.Routes{{Method: "GET", Pattern: "/users", Configurer: owner}}, "/users"},
		{"mounted through a group", func(r fiber.Router) {
			r.Group("/v1").Use("/api", newSub())
		}, engine.Routes{{Method: "GET", Pattern: "/v1/api/users", Configurer: owner}}, "/v1/api/users"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got engine.Routes

			srv, _ := newServer(t, recordRoutes(&got))
			if err := srv.Register(&UserRoutes{declare: tt.declare}); err != nil {
				t.Fatal(err)
			}

			base := start(t, srv)

			if !slices.Equal(sortedRoutes(got), sortedRoutes(tt.want)) {
				t.Fatalf("listing %v, want %v", got, tt.want)
			}

			if tt.probe != "" {
				if res := do(t, http.MethodGet, base+tt.probe, nil, nil); res.status != http.StatusOK {
					t.Fatalf("GET %s = %d, want 200", tt.probe, res.status)
				}
			}
		})
	}
}
