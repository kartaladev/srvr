package srvr_test

import (
	"errors"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/kartaladev/srvr"
)

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
