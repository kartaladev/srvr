package fibersrvr_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
)

// boomAPI is a configurer that panics.
type boomAPI struct{}

func (*boomAPI) Configure(fiber.Router) { panic("boom") }

// aAPI and otherAPI each register GET /a.
type (
	aAPI     struct{}
	otherAPI struct{}
)

func (aAPI) Configure(r fiber.Router)     { r.Get("/a", ok) }
func (otherAPI) Configure(r fiber.Router) { r.Get("/a", ok) }

// TestFiberRegister covers fiber-engine / Scenario: Fiber registration.
func TestFiberRegister(t *testing.T) {
	t.Parallel()

	audit := func(c fiber.Ctx) error { return c.Next() }

	tests := []struct {
		name     string
		confs    []srvr.RouteConfigurer[fiber.Router]
		register error
		start    []string
		probe    string
	}{
		{name: "Use and Post", confs: []srvr.RouteConfigurer[fiber.Router]{routes(func(r fiber.Router) {
			r.Use(audit).Post("/registrations", ok)
		})}},
		{name: "Use twice and Get at the root", confs: []srvr.RouteConfigurer[fiber.Router]{routes(func(r fiber.Router) {
			r.Use(audit)
			r.Use(audit)
			r.Get("/", ok)
		})}, probe: "/"},
		{name: "group", confs: []srvr.RouteConfigurer[fiber.Router]{routes(func(r fiber.Router) {
			r.Group("/v1").Get("/users/:id", ok)
		})}, probe: "/v1/users/7"},
		{name: "nil", confs: []srvr.RouteConfigurer[fiber.Router]{nil}, register: srvr.ErrRouteConfig},
		{name: "typed nil", confs: []srvr.RouteConfigurer[fiber.Router]{(*boomAPI)(nil)}, register: srvr.ErrRouteConfig},
		{name: "panics", confs: []srvr.RouteConfigurer[fiber.Router]{&boomAPI{}}, start: []string{"boom", "*fibersrvr_test.boomAPI"}},
		{name: "nil handler", confs: []srvr.RouteConfigurer[fiber.Router]{routes(func(r fiber.Router) {
			r.Get("/x", nil)
		})}, start: []string{"fibersrvr_test.routes"}},
		{name: "conflicting parameter names", confs: []srvr.RouteConfigurer[fiber.Router]{routes(func(r fiber.Router) {
			r.Get("/users/:id", ok)
			r.Get("/users/:name", ok)
		})}, start: []string{"/users/:name"}},
		{name: "twice", confs: []srvr.RouteConfigurer[fiber.Router]{routes(func(r fiber.Router) {
			r.Get("/users/:id", ok)
			r.Get("/users/:id", ok)
		})}, start: []string{"/users/:id"}},
		{name: "across configurers", confs: []srvr.RouteConfigurer[fiber.Router]{aAPI{}, otherAPI{}}, start: []string{"/a", "fibersrvr_test.aAPI", "fibersrvr_test.otherAPI"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t)

			for _, c := range tt.confs {
				if err := srv.Register(c); tt.register != nil {
					if !errors.Is(err, tt.register) {
						t.Fatalf("Register = %v, want %v", err, tt.register)
					}

					return
				} else if err != nil {
					t.Fatalf("Register = %v", err)
				}
			}

			if len(tt.start) > 0 {
				err := startErr(t, srv)
				if !errors.Is(err, srvr.ErrRouteConfig) {
					t.Fatalf("Start = %v, want ErrRouteConfig", err)
				}
				for _, s := range tt.start {
					if !strings.Contains(err.Error(), s) {
						t.Fatalf("Start = %v, want it to contain %q", err, s)
					}
				}

				return
			}

			base := start(t, srv)
			if tt.probe != "" {
				if res := do(t, http.MethodGet, base+tt.probe, nil, nil); res.status != http.StatusOK {
					t.Fatalf("GET %s = %d", tt.probe, res.status)
				}
			}
		})
	}
}
