package paritytest

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
)

// records returns every log record, waiting up to a second for an access
// record.
func (b *logBuffer) records(t *testing.T) []map[string]any {
	t.Helper()

	b.access(t)

	return b.parse(t)
}

// debugEngines start a server of one engine with debug buffering whose
// GET /items/1 handler logs DEBUG ctx, then fails with boom or, when fail
// is false, writes 200.
var debugEngines = []struct {
	name  string
	start func(t *testing.T, fail bool) (string, *logBuffer)
}{
	{"net/http", func(t *testing.T, fail bool) (string, *logBuffer) {
		t.Helper()

		return netServer(t, func(g *srvr.Group) {
			g.GET("/items/{id}", func(w http.ResponseWriter, r *http.Request) error {
				srvr.LoggerFromContext(r.Context()).DebugContext(r.Context(), "ctx")
				if fail {
					return errors.New("boom")
				}
				w.WriteHeader(http.StatusOK)

				return nil
			})
		}, srvr.WithDebugBuffer())
	}},
	{"gin", func(t *testing.T, fail bool) (string, *logBuffer) {
		t.Helper()

		return ginServer(t, func(g *gin.RouterGroup) {
			g.GET("/items/:id", func(c *gin.Context) {
				srvr.LoggerFromContext(c.Request.Context()).DebugContext(c.Request.Context(), "ctx")
				if fail {
					_ = c.Error(errors.New("boom"))

					return
				}
				c.Status(http.StatusOK)
			})
		}, srvr.WithDebugBuffer())
	}},
	{"fiber", func(t *testing.T, fail bool) (string, *logBuffer) {
		t.Helper()

		return fiberServer(t, func(r fiber.Router) {
			r.Get("/items/:id", func(c fiber.Ctx) error {
				// The pooled fiber.Ctx itself is the logging context.
				srvr.LoggerFromContext(c).DebugContext(c, "ctx")
				if fail {
					return errors.New("boom")
				}

				return c.SendStatus(http.StatusOK)
			})
		}, srvr.WithDebugBuffer())
	}},
}

// TestDebugBuffer_Parity covers triage-debug-buffer / Scenario: Engine
// parity.
func TestDebugBuffer_Parity(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name     string
		fail     bool
		sequence []string
	}{
		{"fails", true, []string{"ctx", "http.request.failed"}},
		{"writes 200", false, []string{"http.request.completed"}},
	}

	for _, e := range debugEngines {
		for _, tt := range rows {
			t.Run(e.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				base, logs := e.start(t, tt.fail)
				_, _, _ = send(t, http.MethodGet, base+"/items/1", nil)

				var got []string
				for _, rec := range logs.records(t) {
					if _, ok := rec["trace_id"]; !ok {
						continue
					}

					msg, _ := rec["msg"].(string)
					got = append(got, msg)

					if msg == "ctx" && rec["srvr.buffered"] != true {
						t.Errorf("ctx record lacks srvr.buffered: %v", rec)
					}

					if msg == "http.request.failed" && rec["srvr.debug_buffer.flushed"] != float64(1) {
						t.Errorf("failed record summary: %v", rec)
					}
				}

				if !slices.Equal(got, tt.sequence) {
					t.Fatalf("sequence %v, want %v", got, tt.sequence)
				}
			})
		}
	}
}
