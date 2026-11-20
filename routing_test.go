package srvr_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/internal/engine"
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
			t.Parallel()

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
			mustNil(t, srv.Shutdown(t.Context()))
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

// TestMount covers route-registration / Scenario: Mounted handler.
func TestMount(t *testing.T) {
	t.Parallel()

	files := http.FileServerFS(fstest.MapFS{"a.txt": {Data: []byte("a")}})
	panicky := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })

	tests := []struct {
		name    string
		handler http.Handler
		method  string
		request string
		status  int // 0: Start fails with ErrRouteConfig
	}{
		{"file server, existing file", files, http.MethodGet, "/static/a.txt", http.StatusOK},
		{"file server, missing file", files, http.MethodGet, "/static/missing.txt", http.StatusNotFound},
		{"panicking handler", panicky, http.MethodPost, "/static/x", http.StatusInternalServerError},
		{"nil", nil, "", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
				g.Group("/static").Mount("/", tt.handler)
			})))

			if tt.status == 0 {
				if err := srv.Start(t.Context()); !errors.Is(err, srvr.ErrRouteConfig) {
					t.Fatalf("Start = %v, want ErrRouteConfig", err)
				}

				return
			}

			base := startServer(t, srv)
			if got := do(t, tt.method, base+tt.request, nil, nil).status; got != tt.status {
				t.Fatalf("%s %s = %d, want %d", tt.method, tt.request, got, tt.status)
			}
		})
	}
}

// TestMount_Middleware: a mounted handler receives the group's middleware.
func TestMount_Middleware(t *testing.T) {
	t.Parallel()

	var rec recorder
	srv := newServer(t)
	mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
		g.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rec.add(r.URL.Path)
				next.ServeHTTP(w, r)
			})
		})
		g.Mount("/m/", http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			rec.add("mounted " + r.URL.Path)
		}))
	})))
	base := startServer(t, srv)

	get(t, base+"/m/x")
	if got := rec.list(); len(got) != 2 || got[0] != "/m/x" || got[1] != "mounted /m/x" {
		t.Fatalf("calls = %v", got)
	}
}

// TestMount_WildcardPrefix: a group prefix with a wildcard is stripped like
// a literal one, including escaped paths and the prefix itself.
func TestMount_WildcardPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct{ mount, request, want string }{
		{"/", "/t/5/x/y", "/x/y"},
		{"/", "/t/5/a%2Fb", "/a/b"},
		{"", "/t/5", "/"},
	}

	for _, tt := range tests {
		t.Run(tt.request, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
				g.Group("/t/{id}").Mount(tt.mount, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte(r.URL.Path))
				}))
			})))
			base := startServer(t, srv)

			if res := get(t, base+tt.request); res.status != http.StatusOK || res.body != tt.want {
				t.Fatalf("GET %s = %d %q, want 200 %q", tt.request, res.status, res.body, tt.want)
			}
		})
	}
}

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

		err := srv.Start(t.Context())
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

// TestRouteCheck_FaultyErrors covers the net/http rows of engine-support /
// Scenario: Route check outcomes for errors whose chain cannot be walked:
// Start fails with ErrRouteConfig within 1s and no panic escapes it.
func TestRouteCheck_FaultyErrors(t *testing.T) {
	t.Parallel()

	var typedNil *MyError

	tests := []struct {
		name string
		err  error
	}{
		{"Unwrap returns the error itself", selfError{}},
		{"a typed-nil *MyError", typedNil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t, srvr.WithRouteCheck(func(context.Context, engine.Routes) error { return tt.err }))
			mustNil(t, srv.Register(okRoute("/x")))

			done := make(chan error, 1)
			go func() { done <- recoverPanic(func() error { return srv.Start(t.Context()) }) }()

			if err := receiveWithin(t, done, time.Second, "Start did not return"); !errors.Is(err, srvr.ErrRouteConfig) || errors.Is(err, errPanicked) {
				t.Fatalf("Start = %s, want an error wrapping ErrRouteConfig without a panic", safeText(err))
			}
		})
	}
}
