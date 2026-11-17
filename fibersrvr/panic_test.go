package fibersrvr_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// TestFiberPanic covers fiber-engine / Scenario: Fiber panic, including the
// http.ErrAbortHandler parity exception. Each record must describe a
// panic, although fiberprblm hands srvr's hook its own wrapper type rather
// than *prblm.PanicError.
func TestFiberPanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		value  any
		hidden string
	}{
		{"boom", "boom", "boom"},
		{"error", errors.New("db down"), "db down"},
		{"http.ErrAbortHandler", http.ErrAbortHandler, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, logs := newServer(t)
			conf := routes(func(r fiber.Router) {
				r.Get("/p", func(fiber.Ctx) error { panic(tt.value) })
				r.Get("/ok", ok)
			})

			res := serve(t, srv, conf, http.MethodGet, "/p", nil, nil)
			if res.status != http.StatusInternalServerError || res.problem == nil || (tt.hidden != "" && strings.Contains(res.body, tt.hidden)) {
				t.Fatalf("response %d %s, want a safe 500 problem", res.status, res.body)
			}

			if next := serve(t, srv, conf, http.MethodGet, "/ok", nil, nil); next.status != http.StatusOK {
				t.Fatalf("next request %d", next.status)
			}

			recs := logs.access(t)
			if len(recs) != 2 {
				t.Fatalf("%d access records, want 2", len(recs))
			}

			r := recs[0]
			if r["msg"] != "http.request.failed" || r["level"] != "ERROR" || r["error.kind"] != "panic" || r["exception.stacktrace"] == nil {
				t.Fatalf("record %v, want a failed panic record", r)
			}
			if _, aborted := r["srvr.response.aborted"]; aborted {
				t.Fatalf("record %v has srvr.response.aborted", r)
			}
		})
	}
}
