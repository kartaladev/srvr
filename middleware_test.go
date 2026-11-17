package srvr_test

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kartaladev/srvr"
)

// TestMiddlewareOrder covers route-registration / Scenario: Middleware order and scope.
func TestMiddlewareOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup   string
		conf    func(g *srvr.Group, mw func(string) srvr.Middleware)
		request string
		want    []string
	}{
		{"Use(A, B) then GET /x", func(g *srvr.Group, mw func(string) srvr.Middleware) {
			g.Use(mw("A"), mw("B"))
			g.GET("/x", status(200))
		}, "/x", []string{"A", "B"}},
		{"GET /x then Use(A)", func(g *srvr.Group, mw func(string) srvr.Middleware) {
			g.GET("/x", status(200))
			g.Use(mw("A"))
		}, "/x", nil},
		{"Use(A), sub-group /v1 with Use(B), GET /v1/x", func(g *srvr.Group, mw func(string) srvr.Middleware) {
			g.Use(mw("A"))
			v1 := g.Group("/v1")
			v1.Use(mw("B"))
			v1.GET("/x", status(200))
		}, "/v1/x", []string{"A", "B"}},
		{"sub-group /v1, then parent Use(A), then GET /v1/x", func(g *srvr.Group, mw func(string) srvr.Middleware) {
			v1 := g.Group("/v1")
			g.Use(mw("A"))
			v1.GET("/x", status(200))
		}, "/v1/x", nil},
		{"Group(\"/v1\", A) then GET /v1/x", func(g *srvr.Group, mw func(string) srvr.Middleware) {
			g.Group("/v1", mw("A")).GET("/x", status(200))
		}, "/v1/x", []string{"A"}},
	}

	for _, tt := range tests {
		t.Run(tt.setup, func(t *testing.T) {
			t.Parallel()

			var rec recorder
			mw := func(name string) srvr.Middleware {
				return func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						rec.add(name)
						next.ServeHTTP(w, r)
					})
				}
			}

			srv := newServer(t)
			mustNil(t, srv.Register(configFunc(func(g *srvr.Group) { tt.conf(g, mw) })))
			base := startServer(t, srv)

			if got := get(t, base+tt.request).status; got != http.StatusOK {
				t.Fatalf("status = %d", got)
			}
			if got := rec.list(); !slices.Equal(got, tt.want) {
				t.Fatalf("middleware run = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestFaultyMiddleware covers route-registration / Scenario: Faulty middleware.
func TestFaultyMiddleware(t *testing.T) {
	t.Parallel()

	tests := []struct {
		fault  string
		mw     srvr.Middleware
		status int // 0: Start fails with ErrRouteConfig
	}{
		{"is nil", nil, 0},
		{"returns a nil handler", func(http.Handler) http.Handler { return nil }, 0},
		{"panics while handling", func(http.Handler) http.Handler {
			return http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })
		}, http.StatusInternalServerError},
		{"never calls next and writes 403", func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
		}, http.StatusForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.fault, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
				g.Use(tt.mw)
				g.GET("/x", status(http.StatusOK))
			})))

			if tt.status == 0 {
				if err := srv.Start(t.Context()); !errors.Is(err, srvr.ErrRouteConfig) {
					t.Fatalf("Start = %v, want ErrRouteConfig", err)
				}

				return
			}

			base := startServer(t, srv)
			res := get(t, base+"/x")
			if res.status != tt.status {
				t.Fatalf("status = %d, want %d", res.status, tt.status)
			}
			if strings.Contains(res.body, "boom") {
				t.Fatalf("body %q leaks the panic value", res.body)
			}
		})
	}
}

// TestNilHandler: a nil handler fails Start instead of panicking.
func TestNilHandler(t *testing.T) {
	t.Parallel()

	srv := newServer(t)
	mustNil(t, srv.Register(configFunc(func(g *srvr.Group) { g.GET("/x", nil) })))

	if err := srv.Start(t.Context()); !errors.Is(err, srvr.ErrRouteConfig) || !strings.Contains(err.Error(), "GET /x") {
		t.Fatalf("Start = %v, want ErrRouteConfig naming GET /x", err)
	}
}
