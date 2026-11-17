package fibersrvr_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// TestFiberErrors covers fiber-engine / Scenario: Fiber error to problem,
// and checks srvr's hook runs once per error.
func TestFiberErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		handler     fiber.Handler
		status      int
		contentType string
		hidden      string
	}{
		{"JSON", func(c fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) }, 200, "application/json; charset=utf-8", ""},
		{"ErrNotFound", func(fiber.Ctx) error { return prblm.ErrNotFound }, 404, "application/problem+json", ""},
		{"fiber.NewError 409", func(fiber.Ctx) error { return fiber.NewError(409, "dup key alice") }, 409, "application/problem+json", "alice"},
		{"unmapped", func(fiber.Ctx) error { return errors.New("pq: auth failed") }, 500, "application/problem+json", "pq:"},
		{"fiber.ErrForbidden", func(fiber.Ctx) error { return fiber.ErrForbidden }, 403, "application/problem+json", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var hooks int

			srv, _ := newServer(t, srvr.WithErrorHook(func(context.Context, error, *prblm.Problem) { hooks++ }))
			res := serve(t, srv, routes(func(r fiber.Router) { r.Get("/items/:id", tt.handler) }), http.MethodGet, "/items/7", nil, nil)

			if res.status != tt.status || res.header.Get("Content-Type") != tt.contentType {
				t.Fatalf("response %d %q %s, want %d %q", res.status, res.header.Get("Content-Type"), res.body, tt.status, tt.contentType)
			}
			if tt.hidden != "" && strings.Contains(res.body, tt.hidden) {
				t.Fatalf("body %s contains %q", res.body, tt.hidden)
			}
			if tt.status >= 400 {
				if res.problem == nil {
					t.Fatalf("no problem in %s", res.body)
				}
				if id, _ := res.problem.Ext("trace_id"); len(fmt.Sprint(id)) != 32 {
					t.Fatalf("problem %s has no trace_id", res.body)
				}
				if hooks != 1 {
					t.Fatalf("error hook ran %d times, want 1", hooks)
				}
			}
		})
	}
}
