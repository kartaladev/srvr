package srvr_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

// UserRoutes is the configurer the listing rows name.
type UserRoutes struct{ declare func(*srvr.Group) }

func (u *UserRoutes) Configure(g *srvr.Group) { u.declare(g) }

// recordRoutes returns a route check option recording its input.
func recordRoutes(got *engine.Routes) srvr.Option {
	return srvr.WithRouteCheck(func(_ context.Context, r engine.Routes) error {
		*got = r

		return nil
	})
}

// sortedRoutes returns r sorted by method and pattern.
func sortedRoutes(r engine.Routes) engine.Routes {
	s := slices.Clone(r)
	slices.SortFunc(s, func(a, b engine.Route) int {
		if a.Method != b.Method {
			if a.Method < b.Method {
				return -1
			}

			return 1
		}
		if a.Pattern < b.Pattern {
			return -1
		}
		if a.Pattern > b.Pattern {
			return 1
		}

		return 0
	})

	return s
}

// TestRouteListing covers engine-support / Scenario: Route listing,
// net/http rows.
func TestRouteListing(t *testing.T) {
	t.Parallel()

	const owner = "*srvr_test.UserRoutes"

	tests := []struct {
		name    string
		declare func(*srvr.Group)
		want    engine.Routes
	}{
		{"GET and POST", func(g *srvr.Group) {
			g.GET("/users/{id}", status(http.StatusOK))
			g.POST("/users", status(http.StatusOK))
		}, engine.Routes{{Method: "GET", Pattern: "/users/{id}", Configurer: owner}, {Method: "POST", Pattern: "/users", Configurer: owner}}},
		{"Mount in a group", func(g *srvr.Group) {
			g.Group("/static").Mount("/", http.NotFoundHandler())
		}, engine.Routes{{Method: "", Pattern: "/static/", Configurer: owner}}},
		{"method-less pattern", func(g *srvr.Group) {
			g.Handle("", "/health", status(http.StatusOK))
		}, engine.Routes{{Method: "", Pattern: "/health", Configurer: owner}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got engine.Routes

			srv := newServer(t, recordRoutes(&got))
			mustNil(t, srv.Register(&UserRoutes{declare: tt.declare}))
			startServer(t, srv)

			if !slices.Equal(sortedRoutes(got), sortedRoutes(tt.want)) {
				t.Fatalf("listing %v, want %v", got, tt.want)
			}
		})
	}
}

// TestRouteCheck covers engine-support / Scenario: Route check outcomes,
// net/http rows.
func TestRouteCheck(t *testing.T) {
	t.Parallel()

	declare := &UserRoutes{declare: func(g *srvr.Group) { g.GET("/users/{id}", status(http.StatusOK)) }}

	t.Run("an error fails Start", func(t *testing.T) {
		t.Parallel()

		srv := newServer(t, srvr.WithRouteCheck(func(context.Context, engine.Routes) error { return errors.New("collides") }))
		mustNil(t, srv.Register(declare))

		err := srv.Start(context.Background())
		if !errors.Is(err, srvr.ErrRouteConfig) || !strings.Contains(err.Error(), "collides") {
			t.Fatalf("Start = %v", err)
		}
		select {
		case <-srv.Ready():
			t.Fatal("Ready closed")
		default:
		}
	})

	t.Run("each check gets its own copy", func(t *testing.T) {
		t.Parallel()

		var got engine.Routes

		srv := newServer(t,
			srvr.WithRouteCheck(func(_ context.Context, r engine.Routes) error {
				clear(r)

				return nil
			}),
			recordRoutes(&got),
		)
		mustNil(t, srv.Register(declare))
		startServer(t, srv)

		if len(got) != 1 || got[0].Pattern != "/users/{id}" {
			t.Fatalf("second check got %v", got)
		}
	})
}
