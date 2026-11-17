package ginsrvr_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
)

// boomAPI is a configurer that panics.
type boomAPI struct{}

func (*boomAPI) Configure(*gin.RouterGroup) { panic("boom") }

// TestGinRegister covers gin-engine / Scenario: Gin registration.
func TestGinRegister(t *testing.T) {
	t.Parallel()

	audit := func(c *gin.Context) { c.Next() }

	tests := []struct {
		name     string
		conf     srvr.RouteConfigurer[*gin.RouterGroup]
		register error    // matched with errors.Is; nil: Register succeeds
		start    []string // empty: Start becomes ready; else Start fails with ErrRouteConfig containing these
		probe    string   // a path answered 200 once ready
	}{
		{name: "group middleware and POST", conf: routes(func(g *gin.RouterGroup) {
			g.Use(audit).POST("/registrations", ok)
		})},
		{name: "sub-group", conf: routes(func(g *gin.RouterGroup) {
			g.Group("/v1").GET("/users/:id", ok)
		}), probe: "/v1/users/7"},
		{name: "nil", conf: nil, register: srvr.ErrRouteConfig},
		{name: "typed nil", conf: (*boomAPI)(nil), register: srvr.ErrRouteConfig},
		{name: "panicking configurer", conf: &boomAPI{}, start: []string{"boom", "*ginsrvr_test.boomAPI"}},
		{name: "conflicting wildcards", conf: routes(func(g *gin.RouterGroup) {
			g.GET("/users/:id", ok)
			g.GET("/users/:name", ok)
		}), start: []string{"/users/:name"}},
		{name: "nil handler", conf: routes(func(g *gin.RouterGroup) {
			g.GET("/x", nil)
		}), start: []string{"/x", "ginsrvr_test.routes"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t)

			err := srv.Register(tt.conf)
			if tt.register != nil {
				if !errors.Is(err, tt.register) {
					t.Fatalf("Register = %v, want %v", err, tt.register)
				}

				return
			}

			if err != nil {
				t.Fatalf("Register = %v", err)
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
