package ginsrvr

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/internal/engine"
)

type routes func(*gin.RouterGroup)

func (f routes) Configure(g *gin.RouterGroup) { f(g) }

// TestGinRequestState covers engine-support / Scenario: Request state after
// the chain, gin rows: a probe just inside the outermost stage reads the
// state after the chain returns, as the access stage does.
func TestGinRequestState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, path string
		route        string
		matched      bool
		status       int
		failed       bool
	}{
		{http.MethodGet, "/users/42", "/users/:id", true, http.StatusOK, false},
		{http.MethodGet, "/nope", "", false, http.StatusNotFound, true},
		{http.MethodDelete, "/users/42", "", false, http.StatusMethodNotAllowed, true},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			srv, err := New(srvr.WithLogOutput(io.Discard))
			if err != nil {
				t.Fatal(err)
			}

			if _, err := srv.configure([]srvr.RouteConfigurer[*gin.RouterGroup]{routes(func(g *gin.RouterGroup) {
				g.GET("/users/:id", func(c *gin.Context) { c.Status(http.StatusOK) })
			})}); err != nil {
				t.Fatal(err)
			}

			var state *engine.RequestState
			probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				srv.stages().ServeHTTP(w, r)
				state, _ = engine.StateFrom(r.Context())
			})

			srv.core.StartTrace(probe).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tt.method, tt.path, nil))

			if state == nil {
				t.Fatal("no state")
			}
			if state.Route() != tt.route || state.Matched() != tt.matched || state.Status() != tt.status || state.Failed() != tt.failed {
				t.Fatalf("state route %q matched %v status %d failed %v; want %q %v %d %v",
					state.Route(), state.Matched(), state.Status(), state.Failed(), tt.route, tt.matched, tt.status, tt.failed)
			}
		})
	}
}

// TestGinEngineHook covers gin-engine / Scenario: Engine hook.
func TestGinEngineHook(t *testing.T) {
	t.Parallel()

	type outcome struct {
		code    int
		body    string
		problem bool
	}

	// serve answers one request through the server's whole pipeline.
	serve := func(t *testing.T, srv *Server, method, path string, header map[string]string) outcome {
		t.Helper()

		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "10.0.0.1:4000"
		for k, v := range header {
			req.Header.Set(k, v)
		}

		w := httptest.NewRecorder()
		srv.handler().ServeHTTP(w, req)

		return outcome{w.Code, w.Body.String(), w.Header().Get("Content-Type") == "application/problem+json"}
	}

	xff := map[string]string{"X-Forwarded-For": "203.0.113.9"}

	accepted := []struct {
		name  string
		hook  func(*gin.Engine)
		check func(t *testing.T, srv *Server)
	}{
		{"ContextWithFallback off", func(e *gin.Engine) { e.ContextWithFallback = false }, func(t *testing.T, srv *Server) {
			t.Helper()

			if !srv.engine.ContextWithFallback {
				t.Fatal("ContextWithFallback is false")
			}
		}},
		{"HandleMethodNotAllowed off", func(e *gin.Engine) { e.HandleMethodNotAllowed = false }, func(t *testing.T, srv *Server) {
			t.Helper()

			if o := serve(t, srv, http.MethodDelete, "/users", nil); o.code != http.StatusMethodNotAllowed {
				t.Fatalf("DELETE /users = %d", o.code)
			}
		}},
		{"RedirectTrailingSlash on", func(e *gin.Engine) { e.RedirectTrailingSlash = true }, func(t *testing.T, srv *Server) {
			t.Helper()

			if o := serve(t, srv, http.MethodGet, "/users/", nil); o.code != http.StatusNotFound || !o.problem {
				t.Fatalf("GET /users/ = %+v, want a 404 problem", o)
			}
		}},
		{"trusted proxies loosened", func(e *gin.Engine) { _ = e.SetTrustedProxies([]string{"10.0.0.0/8"}) }, func(t *testing.T, srv *Server) {
			t.Helper()

			if o := serve(t, srv, http.MethodGet, "/ip", xff); o.body != "203.0.113.9" {
				t.Fatalf("ClientIP = %q, want the forwarded address", o.body)
			}
		}},
		{"no hook", nil, func(t *testing.T, srv *Server) {
			t.Helper()

			if o := serve(t, srv, http.MethodGet, "/ip", xff); o.body != "10.0.0.1" {
				t.Fatalf("ClientIP = %q, want the peer address", o.body)
			}
		}},
	}

	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			opts := []srvr.Option{srvr.WithLogOutput(io.Discard)}
			if tt.hook != nil {
				opts = append(opts, WithGinEngine(tt.hook))
			}

			srv, err := New(opts...)
			if err != nil {
				t.Fatal(err)
			}

			if _, err := srv.configure([]srvr.RouteConfigurer[*gin.RouterGroup]{routes(func(g *gin.RouterGroup) {
				g.GET("/users", func(c *gin.Context) { c.Status(http.StatusOK) })
				g.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
			})}); err != nil {
				t.Fatal(err)
			}

			tt.check(t, srv)
		})
	}

	rejected := []struct {
		name string
		hook func(*gin.Engine)
		want string
	}{
		{"nil", nil, "WithGinEngine"},
		{"Use", func(e *gin.Engine) { e.Use(gin.Logger()) }, "TestGinEngineHook"},
		{"route", func(e *gin.Engine) { e.GET("/x", func(*gin.Context) {}) }, "TestGinEngineHook"},
		{"panics", func(*gin.Engine) { panic("hook boom") }, "hook boom"},
	}

	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, err := New(srvr.WithLogOutput(io.Discard), WithGinEngine(tt.hook))
			if srv != nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New = %v, %v; want an error wrapping ErrConfig containing %q", srv, err, tt.want)
			}
		})
	}
}
