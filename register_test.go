package srvr_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/kartaladev/srvr"
)

// countingConfigurer counts Configure calls and adds no routes.
type countingConfigurer struct {
	mu    sync.Mutex
	calls int
}

func (c *countingConfigurer) Configure(*srvr.Group) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
}

func okRoute(path string) configFunc {
	return func(g *srvr.Group) {
		g.GET(path, func(w http.ResponseWriter, _ *http.Request) error {
			w.WriteHeader(http.StatusOK)

			return nil
		})
	}
}

// TestRegister covers route-registration / Scenario: Registration outcome.
func TestRegister(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// setup prepares the server state and returns the configurer to register.
		setup func(t *testing.T, srv *srvr.Server) srvr.RouteConfigurer[*srvr.Group]
		// check asserts Register's result and what follows from it.
		check func(t *testing.T, srv *srvr.Server, err error)
	}{
		{
			name: "not started, configurer adding GET /ping",
			setup: func(*testing.T, *srvr.Server) srvr.RouteConfigurer[*srvr.Group] {
				return okRoute("/ping")
			},
			check: func(t *testing.T, srv *srvr.Server, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("Register = %v, want nil", err)
				}
				base := startServer(t, srv)
				if got := get(t, base+"/ping").status; got != http.StatusOK {
					t.Fatalf("GET /ping = %d, want 200", got)
				}
			},
		},
		{
			name: "one configurer already, second adding GET /pong",
			setup: func(t *testing.T, srv *srvr.Server) srvr.RouteConfigurer[*srvr.Group] {
				t.Helper()
				if err := srv.Register(okRoute("/ping")); err != nil {
					t.Fatal(err)
				}

				return okRoute("/pong")
			},
			check: func(t *testing.T, srv *srvr.Server, err error) {
				t.Helper()
				if err != nil {
					t.Fatalf("Register = %v, want nil", err)
				}
				base := startServer(t, srv)
				for _, p := range []string{"/ping", "/pong"} {
					if got := get(t, base+p).status; got != http.StatusOK {
						t.Fatalf("GET %s = %d, want 200", p, got)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			conf := tt.setup(t, srv)
			tt.check(t, srv, srv.Register(conf))
		})
	}

	t.Run("same configurer value again runs Configure twice", func(t *testing.T) {
		t.Parallel()

		srv := newServer(t)
		c := &countingConfigurer{}
		for range 2 {
			if err := srv.Register(c); err != nil {
				t.Fatalf("Register = %v, want nil", err)
			}
		}
		startServer(t, srv)
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.calls != 2 {
			t.Fatalf("Configure calls = %d, want 2", c.calls)
		}
	})
}

// TestRegister_ConfigureOrder covers route-registration / Scenario: Configure order.
func TestRegister_ConfigureOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		first, second string
		want          []string
	}{
		{first: "A", second: "B", want: []string{"A", "B"}},
	}

	for _, tt := range tests {
		t.Run(tt.first+tt.second, func(t *testing.T) {
			t.Parallel()

			var (
				mu    sync.Mutex
				order []string
			)
			record := func(name string) configFunc {
				return func(*srvr.Group) {
					mu.Lock()
					defer mu.Unlock()
					order = append(order, name)
				}
			}

			srv := newServer(t)
			for _, n := range []string{tt.first, tt.second} {
				if err := srv.Register(record(n)); err != nil {
					t.Fatal(err)
				}
			}
			mu.Lock()
			if len(order) != 0 {
				t.Fatalf("Configure ran during Register: %v", order)
			}
			mu.Unlock()

			startServer(t, srv)

			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(order, tt.want) {
				t.Fatalf("order = %v, want %v", order, tt.want)
			}
		})
	}
}

// TestGroupMethods checks that each method helper routes its own method.
func TestGroupMethods(t *testing.T) {
	t.Parallel()

	ok := func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(http.StatusNoContent)

		return nil
	}
	srv := newServer(t)
	err := srv.Register(configFunc(func(g *srvr.Group) {
		g.GET("/get", ok)
		g.POST("/post", ok)
		g.PUT("/put", ok)
		g.PATCH("/patch", ok)
		g.DELETE("/delete", ok)
		g.Handle(http.MethodOptions, "/options", ok)
	}))
	if err != nil {
		t.Fatal(err)
	}
	base := startServer(t, srv)

	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		t.Run(m, func(t *testing.T) {
			if got := do(t, m, base+"/"+strings.ToLower(m), nil, nil).status; got != http.StatusNoContent {
				t.Fatalf("%s = %d, want 204", m, got)
			}
		})
	}
}

// ptrConfigurer is a pointer configurer, to register as a typed nil.
type ptrConfigurer struct{}

func (*ptrConfigurer) Configure(*srvr.Group) {}

// TestRegister_Rejected covers route-registration / Scenario: Registration
// outcome, rejected rows.
func TestRegister_Rejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state string
		// prepare brings srv into state.
		prepare func(t *testing.T, srv *srvr.Server)
		conf    srvr.RouteConfigurer[*srvr.Group]
		want    error
	}{
		{"not started, nil", func(*testing.T, *srvr.Server) {}, nil, srvr.ErrRouteConfig},
		{"not started, typed nil", func(*testing.T, *srvr.Server) {}, (*ptrConfigurer)(nil), srvr.ErrRouteConfig},
		{"running", func(t *testing.T, srv *srvr.Server) { t.Helper(); startServer(t, srv) }, okRoute("/late"), srvr.ErrServerStarted},
		{"stopped", func(t *testing.T, srv *srvr.Server) {
			t.Helper()
			mustNil(t, srv.Shutdown(context.Background()))
		}, okRoute("/late"), srvr.ErrServerClosed},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			tt.prepare(t, srv)

			if err := srv.Register(tt.conf); !errors.Is(err, tt.want) {
				t.Fatalf("Register = %v, want %v", err, tt.want)
			}
		})
	}
}
