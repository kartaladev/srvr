package srvr

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kartaladev/srvr/engine"
)

// TestRequestState covers engine-support / Scenario: Request state after
// the chain, net/http rows: a probe just inside the outermost stage reads
// the state after the chain returns, as the access stage does.
func TestRequestState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		request string
		route   string
		matched bool
		status  int
		failed  bool
	}{
		{"/users/42", "/users/{id}", true, http.StatusOK, false},
		{"/nope", "", false, http.StatusNotFound, true},
	}

	for _, tt := range tests {
		t.Run(tt.request, func(t *testing.T) {
			t.Parallel()

			srv, err := New(WithLogOutput(io.Discard))
			if err != nil {
				t.Fatal(err)
			}

			mux, _, err := srv.router([]RouteConfigurer[*Group]{routes(func(g *Group) {
				g.GET("/users/{id}", func(http.ResponseWriter, *http.Request) error { return nil })
			})})
			if err != nil {
				t.Fatal(err)
			}

			var state *engine.RequestState
			probe := func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					next.ServeHTTP(w, r)
					state, _ = engine.StateFrom(r.Context())
				})
			}

			srv.core.StartTrace(probe(srv.stages(mux))).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, tt.request, nil))

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

// routes adapts a function to RouteConfigurer for internal tests.
type routes func(*Group)

func (f routes) Configure(g *Group) { f(g) }
