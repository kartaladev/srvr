package fibersrvr_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
)

// TestDebugBuffer_FiberReuse covers triage-debug-buffer / Scenario: Fiber
// context reuse: request B, served after A on the same app, may reuse A's
// pooled fiber.Ctx; both log with the fiber.Ctx itself as the context.
func TestDebugBuffer_FiberReuse(t *testing.T) {
	t.Parallel()

	srv, logs := newServer(t, srvr.WithDebugBuffer())
	conf := routes(func(r fiber.Router) {
		r.Get("/a", func(c fiber.Ctx) error {
			srvr.LoggerFromContext(c).DebugContext(c, "from A")

			return ok(c)
		})
		r.Get("/b", func(c fiber.Ctx) error {
			srvr.LoggerFromContext(c).DebugContext(c, "from B")

			return errors.New("boom")
		})
	})

	if got := serve(t, srv, conf, http.MethodGet, "/a", nil, nil).status; got != http.StatusOK {
		t.Fatalf("A: status %d", got)
	}

	if got := serve(t, srv, conf, http.MethodGet, "/b", nil, nil).status; got != http.StatusInternalServerError {
		t.Fatalf("B: status %d", got)
	}

	failed := logs.withMsg(t, "http.request.failed")
	if len(failed) != 1 {
		t.Fatalf("%d failed records", len(failed))
	}

	flushed := logs.where(t, "srvr.buffered", true)

	if len(flushed) != 1 || flushed[0]["msg"] != "from B" || flushed[0]["trace_id"] != failed[0]["trace_id"] {
		t.Fatalf("flushed %v, want only from B with trace_id %v", flushed, failed[0]["trace_id"])
	}
}
