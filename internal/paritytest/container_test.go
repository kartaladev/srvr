package paritytest

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// containerService is the lifecycle shape containers such as doger, samber/do
// and fx detect by method set. It is declared here, so srvr imports no
// container to satisfy it.
type containerService interface {
	Start(context.Context) error
	Shutdown(context.Context) error
	HealthCheck(context.Context) error
}

var (
	_ containerService = (*srvr.Server)(nil)
	_ containerService = (*ginsrvr.Server)(nil)
	_ containerService = (*fibersrvr.Server)(nil)
)

// containerModules are the module path fragments of lifecycle containers
// none of srvr's modules may depend on.
var containerModules = []string{"kartaladev/doger", "samber/do", "go.uber.org/fx", "go.uber.org/dig"}

// TestContainerConformance covers server-lifecycle / Scenario:
// Container-shaped lifecycle.
func TestContainerConformance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		engine string
		build  func(t *testing.T) containerService
	}{
		{"net/http", func(t *testing.T) containerService {
			t.Helper()
			return newNetServer(t, &logBuffer{}, func(g *srvr.Group) {
				g.GET("/ok", func(w http.ResponseWriter, _ *http.Request) error { w.WriteHeader(http.StatusOK); return nil })
			})
		}},
		{"gin", func(t *testing.T) containerService {
			t.Helper()

			return newGinServer(t, &logBuffer{}, func(g *gin.RouterGroup) { g.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) }) })
		}},
		{"fiber", func(t *testing.T) containerService {
			t.Helper()

			return newFiberServer(t, &logBuffer{}, idleWorkerDuration, func(r fiber.Router) { r.Get("/ok", func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) }) })
		}},
	}

	for _, tt := range tests {
		t.Run(tt.engine, func(t *testing.T) {
			t.Parallel()

			svc := tt.build(t)
			call := func(name string, fn func(context.Context) error) error {
				t.Helper()
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()

				done := make(chan error, 1)
				go func() { done <- fn(ctx) }()

				select {
				case err := <-done:
					return err
				case <-ctx.Done():
					t.Fatalf("%s did not return within 1s", name)

					return nil
				}
			}

			if err := call("Start", svc.Start); err != nil {
				t.Fatalf("Start = %v, want nil", err)
			}
			if err := call("HealthCheck", svc.HealthCheck); err != nil {
				t.Fatalf("HealthCheck = %v, want nil", err)
			}
			if err := call("Shutdown", svc.Shutdown); err != nil {
				t.Fatalf("Shutdown = %v, want nil", err)
			}
		})
	}
}

// TestContainerDependencies covers the dependency rows of server-lifecycle
// / Scenario: Container-shaped lifecycle for the engine modules, whose
// dependency sets are not allowlisted; TestCoreDependencies already keeps
// every module but prblm out of the core.
func TestContainerDependencies(t *testing.T) {
	t.Parallel()

	for _, dir := range []string{"ginsrvr", "fibersrvr"} {
		t.Run(dir, func(t *testing.T) {
			t.Parallel()

			out, err := goCmd(t, filepath.Join(repoRoot(t), dir), nil, "list", "-deps",
				"-f", "{{with .Module}}{{.Path}}{{end}}", "./...")
			if err != nil {
				t.Fatalf("go list: %v\n%s", err, out)
			}
			for _, m := range containerModules {
				if strings.Contains(out, m) {
					t.Fatalf("module %s depends on container module %s", dir, m)
				}
			}
		})
	}
}
