package fibersrvr_test

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"github.com/kartaladev/srvr/fibersrvr"
)

// upload is a route reading the whole body.
var upload = routes(func(r fiber.Router) {
	r.Post("/upload", ok)
	r.Get("/fail", func(fiber.Ctx) error { return errors.New("boom") })
})

// TestFiberConfigHook covers fiber-engine / Scenario: Fiber config hook.
func TestFiberConfigHook(t *testing.T) {
	t.Parallel()

	ten := srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: 10})

	bodies := []struct {
		name   string
		opts   []srvr.Option
		size   int
		status int
	}{
		{"default at limit", nil, 4194304, 200},
		{"default past limit", nil, 4194305, 413},
		{"10 at limit", []srvr.Option{ten}, 10, 200},
		{"10 past limit", []srvr.Option{ten}, 11, 413},
		{"disabled", []srvr.Option{srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: -1})}, 8388608, 200},
	}

	for _, tt := range bodies {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t, tt.opts...)
			if err := srv.Register(upload); err != nil {
				t.Fatal(err)
			}

			start(t, srv)
			res := rawRequest(t, srv.Addr(), http.MethodPost, "/upload", "", make([]byte, tt.size))
			if res.status != tt.status || (tt.status == 413 && res.problem == nil) {
				t.Fatalf("response %d %s, want %d", res.status, res.body, tt.status)
			}
		})
	}

	t.Run("ErrorHandler set to nil", func(t *testing.T) {
		t.Parallel()

		srv, _ := newServer(t, fibersrvr.WithFiberConfig(func(c *fiber.Config) { c.ErrorHandler = nil }))
		if res := serve(t, srv, upload, http.MethodGet, "/fail", nil, nil); res.status != 500 || res.problem == nil {
			t.Fatalf("response %d %s, want a problem", res.status, res.body)
		}
	})

	t.Run("Immutable", func(t *testing.T) {
		t.Parallel()

		srv, _ := newServer(t, fibersrvr.WithFiberConfig(func(c *fiber.Config) { c.Immutable = true }))
		if !srv.App().Config().Immutable {
			t.Fatal("Immutable is false")
		}
	})

	for name, tt := range map[string]struct {
		hook func(*fiber.Config)
		want string
	}{
		"nil hook":       {nil, "WithFiberConfig"},
		"panicking hook": {func(*fiber.Config) { panic("boom") }, "boom"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv, err := fibersrvr.New(srvr.WithLogOutput(io.Discard), fibersrvr.WithFiberConfig(tt.hook))
			if srv != nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New = %v, %v; want an error wrapping ErrConfig containing %q", srv, err, tt.want)
			}
		})
	}
}

// TestFiberConfig covers engine-support / Scenario: Same validation on
// every engine and Scenario: Engine-specific option placement, fiber rows.
func TestFiberConfig(t *testing.T) {
	t.Parallel()

	var hookRan bool

	accepted := []struct {
		name string
		opts []srvr.Option
		warn bool // only WARN and above are written
	}{
		{"none", nil, false},
		{"MaxRequestBodyBytes 10", []srvr.Option{srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: 10})}, false},
		{"level then config", []srvr.Option{srvr.WithLogLevel(slog.LevelDebug), srvr.WithLogConfig(srvr.LogConfig{Level: "warn"})}, true},
		{"level and WithFiberConfig in one list", []srvr.Option{srvr.WithLogLevel(slog.LevelDebug), fibersrvr.WithFiberConfig(func(*fiber.Config) { hookRan = true })}, false},
	}

	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			srv, logs := newServer(t, tt.opts...)
			serve(t, srv, routes(func(r fiber.Router) { r.Get("/x", ok) }), http.MethodGet, "/x", nil, nil)

			if got := len(logs.access(t)) == 0; got != tt.warn {
				t.Fatalf("INFO access record suppressed %v, want %v", got, tt.warn)
			}
		})
	}

	if !hookRan {
		t.Fatal("WithFiberConfig hook did not run")
	}

	rejected := []struct {
		name string
		opts []srvr.Option
		want string
	}{
		{"invalid level", []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "verbose"})}, "Level"},
		{"nil listener", []srvr.Option{srvr.WithListener(nil)}, "Listener"},
		{"an option of another engine", []srvr.Option{engine.Only("gin", "ginsrvr.WithGinEngine", func() {})}, "ginsrvr.WithGinEngine"},
	}

	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := fibersrvr.New(tt.opts...)
			if srv != nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New = %v, %v; want an error wrapping ErrConfig naming %q", srv, err, tt.want)
			}
		})
	}
}
