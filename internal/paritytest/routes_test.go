package paritytest

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

// TestParity_RouteListing: the same declared routes are listed alike on
// every engine: the same methods, one entry each, each pattern in the
// engine's syntax, attributed to the configurer.
func TestParity_RouteListing(t *testing.T) {
	t.Parallel()

	starts := map[string]func(*testing.T, srvr.Option){
		"net/http": func(t *testing.T, o srvr.Option) {
			ok := func(w http.ResponseWriter, _ *http.Request) error { w.WriteHeader(http.StatusOK); return nil }
			netServer(t, func(g *srvr.Group) { g.GET("/users/{id}", ok); g.POST("/users", ok) }, o)
		},
		"gin": func(t *testing.T, o srvr.Option) {
			ginServer(t, func(g *gin.RouterGroup) {
				g.GET("/users/:id", func(*gin.Context) {})
				g.POST("/users", func(*gin.Context) {})
			}, o)
		},
		"fiber": func(t *testing.T, o srvr.Option) {
			fiberServer(t, func(r fiber.Router) {
				r.Get("/users/:id", func(fiber.Ctx) error { return nil })
				r.Post("/users", func(fiber.Ctx) error { return nil })
			}, o)
		},
	}

	for name, start := range starts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var got engine.Routes

			start(t, srvr.WithRouteCheck(func(_ context.Context, r engine.Routes) error {
				got = r

				return nil
			}))

			methods := make([]string, 0, len(got))
			for _, r := range got {
				methods = append(methods, r.Method)
				if !strings.HasPrefix(r.Configurer, "paritytest.") || !strings.HasPrefix(r.Pattern, "/users") {
					t.Errorf("entry %v", r)
				}
			}

			slices.Sort(methods)
			if !slices.Equal(methods, []string{"GET", "POST"}) {
				t.Fatalf("listing %v", got)
			}
		})
	}
}
