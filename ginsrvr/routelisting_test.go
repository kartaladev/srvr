package ginsrvr_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"github.com/kartaladev/srvr/ginsrvr"
)

// UserRoutes is the configurer the listing rows name.
type UserRoutes struct{ declare func(*gin.RouterGroup) }

func (u *UserRoutes) Configure(g *gin.RouterGroup) { u.declare(g) }

// recordRoutes returns a route check option recording its input.
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

// TestRouteListing covers engine-support / Scenario: Route listing, gin
// rows.
func TestRouteListing(t *testing.T) {
	t.Parallel()

	const owner = "*ginsrvr_test.UserRoutes"

	audit := func(c *gin.Context) { c.Next() }

	tests := []struct {
		name    string
		declare func(*gin.RouterGroup)
		want    engine.Routes
	}{
		{"GET and POST", func(g *gin.RouterGroup) {
			g.GET("/users/:id", ok)
			g.POST("/users", ok)
		}, engine.Routes{{Method: "GET", Pattern: "/users/:id", Configurer: owner}, {Method: "POST", Pattern: "/users", Configurer: owner}}},
		{"Use and GET", func(g *gin.RouterGroup) {
			g.Use(audit)
			g.GET("/x", ok)
		}, engine.Routes{{Method: "GET", Pattern: "/x", Configurer: owner}}},
		{"sub-group", func(g *gin.RouterGroup) {
			g.Group("/v1").GET("/users/:id", ok)
		}, engine.Routes{{Method: "GET", Pattern: "/v1/users/:id", Configurer: owner}}},
		{"Any", func(g *gin.RouterGroup) {
			g.Any("/x", ok)
		}, engine.Routes{{Method: "", Pattern: "/x", Configurer: owner}}},
		{"Static", func(g *gin.RouterGroup) {
			g.Static("/assets", ".")
		}, engine.Routes{{Method: "GET", Pattern: "/assets/*filepath", Configurer: owner}, {Method: "HEAD", Pattern: "/assets/*filepath", Configurer: owner}}},
	}

	t.Run("two configurers", func(t *testing.T) {
		t.Parallel()

		var got engine.Routes

		srv, _ := newServer(t, recordRoutes(&got))
		if err := srv.Register(&UserRoutes{declare: func(g *gin.RouterGroup) { g.GET("/a", ok) }}); err != nil {
			t.Fatal(err)
		}
		if err := srv.Register(routes(func(g *gin.RouterGroup) { g.GET("/b", ok) })); err != nil {
			t.Fatal(err)
		}

		start(t, srv)

		want := engine.Routes{{Method: "GET", Pattern: "/a", Configurer: owner}, {Method: "GET", Pattern: "/b", Configurer: "ginsrvr_test.routes"}}
		if !slices.Equal(sortedRoutes(got), want) {
			t.Fatalf("listing %v, want %v", got, want)
		}
	})

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
// gin rows, and the gin route-check row of Scenario: Consumer faults on
// every engine.
func TestRouteCheck(t *testing.T) {
	t.Parallel()

	declare := &UserRoutes{declare: func(g *gin.RouterGroup) { g.GET("/x", ok) }}

	t.Run("nil passes", func(t *testing.T) {
		t.Parallel()

		srv, _ := newServer(t, srvr.WithRouteCheck(func(context.Context, engine.Routes) error { return nil }))
		if err := srv.Register(declare); err != nil {
			t.Fatal(err)
		}
		start(t, srv)
	})

	t.Run("an error fails Start", func(t *testing.T) {
		t.Parallel()

		srv, _ := newServer(t, srvr.WithRouteCheck(func(context.Context, engine.Routes) error { return errors.New("collides") }))
		if err := srv.Register(declare); err != nil {
			t.Fatal(err)
		}
		if err := startErr(t, srv); !errors.Is(err, srvr.ErrRouteConfig) || !strings.Contains(err.Error(), "collides") {
			t.Fatalf("Start = %v", err)
		}
	})

	t.Run("nil check", func(t *testing.T) {
		t.Parallel()

		srv, err := ginsrvr.New(srvr.WithRouteCheck(nil))
		if srv != nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), "RouteCheck") {
			t.Fatalf("New = %v, %v", srv, err)
		}
	})

	t.Run("blocks until its context is done", func(t *testing.T) {
		t.Parallel()

		var afterDrain atomic.Int32

		srv, _ := newServer(t,
			srvr.WithRouteCheck(func(ctx context.Context, _ engine.Routes) error {
				<-ctx.Done()

				return ctx.Err()
			}),
			srvr.WithShutdownHook(srvr.AfterDrain, func(context.Context) error { afterDrain.Add(1); return nil }),
		)

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		if err := srv.Start(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Start = %v", err)
		}
		select {
		case <-srv.Ready():
			t.Fatal("Ready closed")
		default:
		}
		if n := afterDrain.Load(); n != 1 {
			t.Fatalf("AfterDrain ran %d times", n)
		}
	})

	t.Run("calls back and returns a hostile error", func(t *testing.T) {
		t.Parallel()

		var srv *ginsrvr.Server

		srv, _ = newServer(t, srvr.WithRouteCheck(func(context.Context, engine.Routes) error {
			_ = srv.Addr()

			return selfPanicError{}
		}))
		if err := srv.Register(declare); err != nil {
			t.Fatal(err)
		}

		if err := startErr(t, srv); !errors.Is(err, srvr.ErrRouteConfig) || !strings.Contains(err.Error(), "<panic in Error(): ginsrvr_test.selfPanicError>") {
			t.Fatalf("Start = %v", err)
		}
	})
}
