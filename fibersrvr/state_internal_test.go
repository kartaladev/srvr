package fibersrvr

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

type routes func(fiber.Router)

func (f routes) Configure(r fiber.Router) { f(r) }

// TestFiberRequestState covers engine-support / Scenario: Request state
// after the chain, fiber rows: a probe registered after srvr's stage
// captures the state, read once the request finished.
func TestFiberRequestState(t *testing.T) {
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
		{http.MethodGet, "/boom/7", "/boom/:id", true, http.StatusInternalServerError, true},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			srv, err := New(srvr.WithLogOutput(io.Discard))
			if err != nil {
				t.Fatal(err)
			}

			var state *engine.RequestState
			srv.app.Use(func(c fiber.Ctx) error {
				state, _ = engine.StateFrom(c.Context())

				return c.Next()
			})

			res, err := srv.Test(httptest.NewRequest(tt.method, tt.path, nil), routes(func(r fiber.Router) {
				r.Get("/users/:id", func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) })
				r.Get("/boom/:id", func(fiber.Ctx) error { return errors.New("boom") })
			}))
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()

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
