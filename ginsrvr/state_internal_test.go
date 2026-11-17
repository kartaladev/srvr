package ginsrvr

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
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

			if err := srv.configure([]srvr.RouteConfigurer[*gin.RouterGroup]{routes(func(g *gin.RouterGroup) {
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
			if state.Route != tt.route || state.Matched() != tt.matched || state.Status() != tt.status || state.Failed() != tt.failed {
				t.Fatalf("state route %q matched %v status %d failed %v; want %q %v %d %v",
					state.Route, state.Matched(), state.Status(), state.Failed(), tt.route, tt.matched, tt.status, tt.failed)
			}
		})
	}
}
