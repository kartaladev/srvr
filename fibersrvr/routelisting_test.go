package fibersrvr_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/static"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

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

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
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
