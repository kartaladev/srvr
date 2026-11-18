package fibersrvr_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/static"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"github.com/kartaladev/srvr/fibersrvr"
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

// UserRoutes is the configurer the listing rows name.
type UserRoutes struct{ declare func(fiber.Router) }

func (u *UserRoutes) Configure(r fiber.Router) { u.declare(r) }

func recordRoutes(got *engine.Routes) srvr.Option {
	return srvr.WithRouteCheck(func(_ context.Context, r engine.Routes) error {
		*got = r

		return nil
	})
}

func sortedRoutes(r engine.Routes) engine.Routes {
	s := slices.Clone(r)
	slices.SortFunc(s, func(a, b engine.Route) int {
		return strings.Compare(a.Method+" "+a.Pattern, b.Method+" "+b.Pattern)
	})

	return s
}

// TestRouteListing covers engine-support / Scenario: Route listing, fiber
// rows.
func TestRouteListing(t *testing.T) {
	t.Parallel()

	const owner = "*fibersrvr_test.UserRoutes"

	audit := func(c fiber.Ctx) error { return c.Next() }

	tests := []struct {
		name    string
		declare func(fiber.Router)
		want    engine.Routes
	}{
		{"Get and Post, no implicit HEAD", func(r fiber.Router) {
			r.Get("/users/:id", ok)
			r.Post("/users", ok)
		}, engine.Routes{{Method: "GET", Pattern: "/users/:id", Configurer: owner}, {Method: "POST", Pattern: "/users", Configurer: owner}}},
		{"Use and Get", func(r fiber.Router) {
			r.Use(audit)
			r.Get("/x", ok)
		}, engine.Routes{{Method: "GET", Pattern: "/x", Configurer: owner}}},
		{"All", func(r fiber.Router) {
			r.All("/x", ok)
		}, engine.Routes{{Method: "", Pattern: "/x", Configurer: owner}}},
		{"path-scoped Use", func(r fiber.Router) {
			r.Use("/assets", static.New("."))
		}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got engine.Routes

			srv, _ := newServer(t, recordRoutes(&got))
			if err := srv.Register(&UserRoutes{declare: tt.declare}); err != nil {
				t.Fatal(err)
			}

			start(t, srv)

			if !slices.Equal(sortedRoutes(got), sortedRoutes(tt.want)) {
				t.Fatalf("listing %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRouteCheck covers engine-support / Scenario: Route check outcomes,
// fiber rows, and the fiber route-check row of Scenario: Hooks per engine
// and stop path.
func TestRouteCheck(t *testing.T) {
	t.Parallel()

	declare := &UserRoutes{declare: func(r fiber.Router) { r.Get("/x", ok) }}

	for name, tt := range map[string]struct {
		check func(context.Context, engine.Routes) error
		want  string
	}{
		"an error": {func(context.Context, engine.Routes) error { return errors.New("collides") }, "collides"},
		"a panic":  {func(context.Context, engine.Routes) error { panic("boom") }, "boom"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t, srvr.WithRouteCheck(tt.check))
			if err := srv.Register(declare); err != nil {
				t.Fatal(err)
			}
			if err := startErr(t, srv); !errors.Is(err, srvr.ErrRouteConfig) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Start = %v", err)
			}
		})
	}

	t.Run("ignores its context and blocks forever", func(t *testing.T) {
		t.Parallel()

		release := make(chan struct{})
		defer close(release)

		srv, _ := newServer(t, srvr.WithRouteCheck(func(context.Context, engine.Routes) error {
			<-release // abandoned by Start until the test ends

			return nil
		}))

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()

		begin := time.Now()
		if err := srv.Start(ctx); !errors.Is(err, context.DeadlineExceeded) || time.Since(begin) > time.Second {
			t.Fatalf("Start = %v after %v", err, time.Since(begin))
		}
	})

	t.Run("a failing check runs only AfterDrain hooks", func(t *testing.T) {
		t.Parallel()

		rec := &hookRecorder{}
		srv, _ := newServer(t, append(rec.options(), srvr.WithRouteCheck(func(context.Context, engine.Routes) error { return errors.New("collides") }))...)
		if err := srv.Register(declare); err != nil {
			t.Fatal(err)
		}
		_ = startErr(t, srv)

		if got := rec.recorded(); !slices.Equal(got, []string{"AfterDrain"}) {
			t.Fatalf("hooks = %v", got)
		}
	})
}

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

// asPanicError is an error whose As method panics.
type asPanicError struct{}

func (asPanicError) Error() string { return "faulty" }

func (asPanicError) As(any) bool { panic("as") }

// TestRouteCheck_FaultyErrors covers the fiber row of engine-support /
// Scenario: Route check outcomes for an error whose chain cannot be
// walked: Start fails with ErrRouteConfig within 1s and no panic escapes.
func TestRouteCheck_FaultyErrors(t *testing.T) {
	t.Parallel()

	srv, _ := newServer(t, srvr.WithRouteCheck(func(context.Context, engine.Routes) error { return asPanicError{} }))
	if err := srv.Register(&UserRoutes{declare: func(r fiber.Router) { r.Get("/x", ok) }}); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				done <- fmt.Errorf("Start panicked: %v", v)
			}
		}()
		done <- srv.Start(t.Context())
	}()

	select {
	case err := <-done:
		if !errors.Is(err, srvr.ErrRouteConfig) {
			t.Fatalf("Start = %v, want an error wrapping ErrRouteConfig without a panic", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not return within 1s")
	}
}

// TestShutdown_WhileStarting covers the fiber row of server-lifecycle /
// Scenario: Shutdown during the configure phase: a Shutdown while a route
// check waits on its context makes Start return ErrServerClosed, and
// Shutdown returns nil within 1s.
func TestShutdown_WhileStarting(t *testing.T) {
	t.Parallel()

	entered := make(chan struct{})
	srv, _ := newServer(t, srvr.WithRouteCheck(func(ctx context.Context, _ engine.Routes) error {
		close(entered)
		<-ctx.Done()

		return ctx.Err()
	}))
	if err := srv.Register(&UserRoutes{declare: func(r fiber.Router) { r.Get("/x", ok) }}); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- srv.Start(t.Context()) }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the route check did not run")
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown = %v, want nil", err)
	}

	select {
	case err := <-done:
		if !errors.Is(err, srvr.ErrServerClosed) {
			t.Fatalf("Start = %v, want an error wrapping ErrServerClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Start did not return within 1s")
	}

	if err := srv.Start(t.Context()); !errors.Is(err, srvr.ErrServerClosed) {
		t.Fatalf("second Start = %v, want an error wrapping ErrServerClosed", err)
	}
}
