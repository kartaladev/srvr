package paritytest

import (
	"context"
	"errors"
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

	for _, e := range lifecycleEngines() {
		t.Run(e.engine, func(t *testing.T) {
			t.Parallel()

			var svc containerService = e.build(t)
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

// lifecycleEngines builds one unstarted server per engine, each answering
// GET /ok.
func lifecycleEngines() []struct {
	engine string
	build  func(t *testing.T) server
} {
	return []struct {
		engine string
		build  func(t *testing.T) server
	}{
		{"net/http", func(t *testing.T) server {
			t.Helper()

			return newNetServer(t, &logBuffer{}, func(g *srvr.Group) {
				g.GET("/ok", func(w http.ResponseWriter, _ *http.Request) error { w.WriteHeader(http.StatusOK); return nil })
			})
		}},
		{"gin", func(t *testing.T) server {
			t.Helper()

			return newGinServer(t, &logBuffer{}, func(g *gin.RouterGroup) { g.GET("/ok", func(c *gin.Context) { c.Status(http.StatusOK) }) })
		}},
		{"fiber", func(t *testing.T) server {
			t.Helper()

			return newFiberServer(t, &logBuffer{}, idleWorkerDuration, func(r fiber.Router) { r.Get("/ok", func(c fiber.Ctx) error { return c.SendStatus(http.StatusOK) }) })
		}},
	}
}

// TestLifecycleParity runs the container-lifecycle rows shared by every
// engine (server-lifecycle "Start returns while serving", "Wait after
// Start", "Wait misuse", "Start twice", "Health across the lifecycle";
// engine-support "Lifecycle misuse on every engine") on all three engines,
// so they cannot drift.
func TestLifecycleParity(t *testing.T) {
	t.Parallel()

	for _, e := range lifecycleEngines() {
		t.Run(e.engine+"/Start, serve, Shutdown, Wait", func(t *testing.T) {
			t.Parallel()

			srv := e.build(t)
			if err := srv.Wait(); !errors.Is(err, srvr.ErrServerNotStarted) {
				t.Fatalf("Wait before Start = %v, want ErrServerNotStarted", err)
			}
			if err := srv.HealthCheck(t.Context()); !errors.Is(err, srvr.ErrServerNotStarted) {
				t.Fatalf("HealthCheck before Start = %v, want ErrServerNotStarted", err)
			}

			startCtx, cancelStart := context.WithCancel(t.Context())
			must(t, srv.Start(startCtx))
			cancelStart() // bounds startup only

			registerClient(t, srv.Addr())
			getOK(t, "http://"+srv.Addr())
			must(t, srv.HealthCheck(t.Context()))
			if err := srv.Start(t.Context()); !errors.Is(err, srvr.ErrServerStarted) {
				t.Fatalf("second Start = %v, want ErrServerStarted", err)
			}
			if err := srv.Run(t.Context()); !errors.Is(err, srvr.ErrServerStarted) {
				t.Fatalf("Run while running = %v, want ErrServerStarted", err)
			}

			must(t, srv.Shutdown(t.Context()))
			must(t, srv.Wait())
			if err := srv.HealthCheck(t.Context()); !errors.Is(err, srvr.ErrServerClosed) {
				t.Fatalf("HealthCheck after stop = %v, want ErrServerClosed", err)
			}
			if err := srv.Start(t.Context()); !errors.Is(err, srvr.ErrServerClosed) {
				t.Fatalf("Start after stop = %v, want ErrServerClosed", err)
			}
		})

		t.Run(e.engine+"/Run until its context ends", func(t *testing.T) {
			t.Parallel()

			srv := e.build(t)
			ctx, cancel := context.WithCancel(t.Context())
			done := make(chan error, 1)
			go func() { done <- srv.Run(ctx) }()
			<-srv.Ready()
			must(t, srv.HealthCheck(t.Context()))

			cancel()
			select {
			case err := <-done:
				must(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("Run did not return")
			}
			must(t, srv.Wait())
		})
	}
}
