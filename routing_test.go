package srvr_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/kartaladev/srvr"
)

// TestJoin covers route-registration / Scenario: Joining prefix and path.
func TestJoin(t *testing.T) {
	t.Parallel()

	tests := []struct{ prefix, path, request string }{
		{"", "/users", "/users"},
		{"/api", "/users", "/api/users"},
		{"/api/", "/users", "/api/users"},
		{"/api", "/users/{id}", "/api/users/42"},
		{"/orgs/{org}", "/members", "/orgs/acme/members"},
		{"/api", "", "/api"},
		{"/api", "/", "/api/anything/below"},
		{"/api", "/{$}", "/api/"},
		{"/files", "/{path...}", "/files/a/b/c.txt"},
		{"", "", "/anything"}, // the root group's empty path is "/"
	}

	for _, tt := range tests {
		t.Run(tt.prefix+"+"+tt.path, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			if err := srv.Register(on(tt.prefix, tt.path, status(http.StatusTeapot))); err != nil {
				t.Fatal(err)
			}
			base := startServer(t, srv)

			if got := get(t, base+tt.request).status; got != http.StatusTeapot {
				t.Fatalf("GET %s = %d, want the route's 418", tt.request, got)
			}
		})
	}
}

// TestInvalidPaths covers route-registration / Scenario: Invalid paths.
func TestInvalidPaths(t *testing.T) {
	t.Parallel()

	tests := []struct{ prefix, path, message string }{
		{"/api", "users", "users"},
		{"api", "/users", "api"},
		{"/api", "/users/{id", "/users/{id"},
		{"/api", "/a/{x}/{x}", "/a/{x}/{x}"},
		{"/api", "/{rest...}/more", "/{rest...}/more"},
		{"/api", "example.com/users", "example.com/users"},
	}

	for _, tt := range tests {
		t.Run(tt.prefix+"+"+tt.path, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			if err := srv.Register(on(tt.prefix, tt.path, status(http.StatusOK))); err != nil {
				t.Fatal(err)
			}

			err := srv.Start(t.Context())
			if !errors.Is(err, srvr.ErrRouteConfig) || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("Start = %v, want ErrRouteConfig containing %q", err, tt.message)
			}
		})
	}
}

// UsersAPI and AdminAPI are named configurers registering one route each.
type (
	UsersAPI struct{ method, path string }
	AdminAPI struct{ method, path string }
)

func (c UsersAPI) Configure(g *srvr.Group) { g.Handle(c.method, c.path, status(http.StatusOK)) }
func (c AdminAPI) Configure(g *srvr.Group) { g.Handle(c.method, c.path, status(http.StatusOK)) }

// configurer builds the named configurer for a row.
func configurer(name, route string) srvr.RouteConfigurer[*srvr.Group] {
	method, path, _ := strings.Cut(route, " ")
	if name == "UsersAPI" {
		return UsersAPI{method, path}
	}

	return AdminAPI{method, path}
}

// TestConflicts covers route-registration / Scenario: Conflicting routes.
func TestConflicts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b, first, second string
		names               []string // empty: no conflict
	}{
		// conflict
		{"UsersAPI", "AdminAPI", "GET /users/{id}", "GET /users/{name}", []string{"GET /users/{id}", "GET /users/{name}", "UsersAPI", "AdminAPI"}},
		{"UsersAPI", "UsersAPI", "GET /users", "GET /users", []string{"GET /users"}},
		// no conflict
		{"UsersAPI", "AdminAPI", "GET /users/{id}", "GET /users/me", nil},
		{"UsersAPI", "AdminAPI", "GET /users", "POST /users", nil},
	}

	for _, tt := range tests {
		t.Run(tt.first+" vs "+tt.second, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			mustNil(t, srv.Register(configurer(tt.a, tt.first)))
			mustNil(t, srv.Register(configurer(tt.b, tt.second)))

			if tt.names == nil {
				startServer(t, srv)

				return
			}

			err := srv.Start(t.Context())
			if !errors.Is(err, srvr.ErrRouteConfig) {
				t.Fatalf("Start = %v, want ErrRouteConfig", err)
			}
			for _, n := range tt.names {
				if !strings.Contains(err.Error(), n) {
					t.Fatalf("Start = %v, want it to name %q", err, n)
				}
			}
		})
	}
}

// TestConflicts_InvalidAfterValid: an invalid pattern after valid ones is
// reported as invalid, not as a conflict with them.
func TestConflicts_InvalidAfterValid(t *testing.T) {
	t.Parallel()

	srv := newServer(t)
	mustNil(t, srv.Register(configurer("UsersAPI", "GET /users/{id}")))
	mustNil(t, srv.Register(configurer("AdminAPI", "GET /users/{id")))

	err := srv.Start(t.Context())
	if !errors.Is(err, srvr.ErrRouteConfig) || strings.Contains(err.Error(), "conflicts") || !strings.Contains(err.Error(), "AdminAPI") {
		t.Fatalf("Start = %v, want an invalid-pattern error naming AdminAPI", err)
	}
}

// TestConfigurePanics covers route-registration / Scenario: Configure panics.
func TestConfigurePanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		conf       srvr.RouteConfigurer[*srvr.Group]
		typ, value string
	}{
		{&PanicAPI{}, "*srvr_test.PanicAPI", "boom"},
		{NilDerefAPI{}, "srvr_test.NilDerefAPI", "runtime error: invalid memory address or nil pointer dereference"},
		{&HostileAPI{}, "*srvr_test.HostileAPI", "<panic in Error(): srvr_test.selfPanicError>"},
	}

	for _, tt := range tests {
		t.Run(tt.typ, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			mustNil(t, srv.Register(okRoute("/ok")))
			mustNil(t, srv.Register(tt.conf))

			err := recoverPanic(func() error { return srv.Start(t.Context()) })
			if !errors.Is(err, srvr.ErrRouteConfig) || !strings.Contains(err.Error(), tt.typ) || !strings.Contains(err.Error(), tt.value) {
				t.Fatalf("Start = %v, want ErrRouteConfig naming %s and %q", err, tt.typ, tt.value)
			}
			assertErr(t, shutdownBounded(t, srv), nil, "")
			select {
			case <-srv.Ready():
				t.Fatal("the listener was bound")
			default:
			}
		})
	}
}
