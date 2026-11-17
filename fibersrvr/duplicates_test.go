package fibersrvr_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
)

// mountAPI mounts a sub-app with GET /x at "/"; xAPI registers GET /x.
type (
	mountAPI struct{}
	xAPI     struct{}
)

func (mountAPI) Configure(r fiber.Router) {
	sub := fiber.New()
	sub.Get("/x", ok)
	r.Use("/", sub)
}

func (xAPI) Configure(r fiber.Router) { r.Get("/x", ok) }

// second answers 201, telling the second of two routes from the first.
func second(c fiber.Ctx) error { return c.SendStatus(http.StatusCreated) }

// TestFiberDuplicateNormalisation covers fiber-engine / Scenario: Fiber
// duplicate normalisation: duplicates are compared as fiber matches them.
func TestFiberDuplicateNormalisation(t *testing.T) {
	t.Parallel()

	caseSensitive := fibersrvr.WithFiberConfig(func(c *fiber.Config) { c.CaseSensitive = true })
	strict := fibersrvr.WithFiberConfig(func(c *fiber.Config) { c.StrictRouting = true })

	tests := []struct {
		name    string
		opts    []srvr.Option
		confs   []srvr.RouteConfigurer[fiber.Router]
		start   []string // substrings of the Start error; nil when Start succeeds
		reaches string   // when Start succeeds, a path that must answer 201 (the second route)
	}{
		{
			name: "case-sensitive opt-in keeps /users and /Users apart", opts: []srvr.Option{caseSensitive},
			confs: oneConf(func(r fiber.Router) {
				r.Get("/users", ok)
				r.Get("/Users", second)
			}), reaches: "/Users",
		},
		{
			name: "strict routing opt-in keeps /users and /users/ apart", opts: []srvr.Option{strict},
			confs: oneConf(func(r fiber.Router) {
				r.Get("/users", ok)
				r.Get("/users/", second)
			}), reaches: "/users/",
		},
		{name: "sub-app route at another path than a parent route", confs: oneConf(func(r fiber.Router) {
			r.Get("/users", second)
			r.Use("/api", newSub())
		}), reaches: "/users"},
		{name: "sub-app route colliding with a parent route", confs: oneConf(func(r fiber.Router) {
			sub := fiber.New()
			sub.Get("/Users/", ok)
			r.Use("/api", sub)
			r.Get("/api/users", ok)
		}), start: []string{"/api/Users/", "/api/users"}},
		{name: "sub-app at / colliding across configurers", confs: []srvr.RouteConfigurer[fiber.Router]{
			mountAPI{}, xAPI{},
		}, start: []string{"/x", "fibersrvr_test.mountAPI", "fibersrvr_test.xAPI"}},
		{name: "trailing slash", confs: oneConf(func(r fiber.Router) {
			r.Get("/users", ok)
			r.Get("/users/", ok)
		}), start: []string{"/users/"}},
		{name: "letter case", confs: oneConf(func(r fiber.Router) {
			r.Get("/users", ok)
			r.Get("/Users", ok)
		}), start: []string{"/Users"}},
		{
			name: "case-sensitive opt-in still ignores a trailing slash", opts: []srvr.Option{caseSensitive},
			confs: oneConf(func(r fiber.Router) {
				r.Get("/users", ok)
				r.Get("/users/", ok)
			}), start: []string{"/users/"},
		},
		{
			name: "strict routing opt-in still ignores letter case", opts: []srvr.Option{strict},
			confs: oneConf(func(r fiber.Router) {
				r.Get("/users", ok)
				r.Get("/Users", ok)
			}), start: []string{"/Users"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t, tt.opts...)
			for _, c := range tt.confs {
				if err := srv.Register(c); err != nil {
					t.Fatal(err)
				}
			}

			if tt.start != nil {
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
			if res := do(t, http.MethodGet, base+tt.reaches, nil, nil); res.status != http.StatusCreated {
				t.Fatalf("GET %s = %d, want the second route's 201", tt.reaches, res.status)
			}
		})
	}
}

// oneConf returns a single configurer declaring declare.
func oneConf(declare func(fiber.Router)) []srvr.RouteConfigurer[fiber.Router] {
	return []srvr.RouteConfigurer[fiber.Router]{routes(declare)}
}
