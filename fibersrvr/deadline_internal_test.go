package fibersrvr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/internal/engine"
)

// lockedBuffer is a log output safe for concurrent writes.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

// failedRecord returns the http.request.failed record written to b.
func (b *lockedBuffer) failedRecord(t *testing.T) map[string]any {
	t.Helper()

	b.mu.Lock()
	defer b.mu.Unlock()

	sc := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
	for sc.Scan() {
		var rec map[string]any
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("record %q: %v", sc.Bytes(), err)
		}
		if rec["msg"] == "http.request.failed" {
			return rec
		}
	}

	t.Fatalf("no http.request.failed record in %s", b.buf.String())

	return nil
}

// TestErrorRenderer_Deadline covers error-rendering / Scenario: Faulty
// renderer, fiber rows "returns after the deadline" and "returns before
// the deadline while the client already disconnected", and Scenario:
// Renderer and lifecycle, fiber row (R blocks past the render deadline,
// then returns: the safe 500 is sent once it returns, with no goroutine
// started for rendering, which goleak checks), through the server's whole
// fiber pipeline with app.Test and the render deadline shortened through
// engine.SetRenderDeadline. fiber's request context never cancels when
// the client disconnects, so the disconnected row cancels the server's
// base context, as a forced drain does: the request context is then
// cancelled the way a disconnect cancels it on net/http.
func TestErrorRenderer_Deadline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		disconnect  bool
		wantStatus  int
		wantFailure any // nil when no srvr.renderer.failure key is logged
	}{
		{name: "returns after the deadline", wantStatus: 500, wantFailure: "deadline"},
		{name: "returns before the deadline while the client already disconnected", disconnect: true, wantStatus: 499},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			renderer := srvr.ErrorRendererFunc(func(ctx context.Context, _ srvr.ErrorRequest, err error) srvr.ErrorResponse {
				if !tt.disconnect {
					<-ctx.Done()
				}

				status := 429
				if errors.Is(err, srvr.ErrClientClosed) {
					status = 499
				}

				return srvr.ErrorResponse{Status: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: []byte(`{"code":"quota"}`)}
			})

			logs := &lockedBuffer{}

			srv, err := New(srvr.WithErrorRenderer(renderer), srvr.WithLogOutput(logs))
			if err != nil {
				t.Fatal(err)
			}

			engine.SetRenderDeadline(srv.core, 20*time.Millisecond)

			if tt.disconnect {
				srv.cancelBase()
			}

			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "/items/7", nil)
			if err != nil {
				t.Fatal(err)
			}

			res, err := srv.Test(req, routes(func(r fiber.Router) {
				r.Get("/items/:id", func(c fiber.Ctx) error {
					if tt.disconnect {
						return c.Context().Err()
					}

					return errors.New("db down")
				})
			}))
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()

			body, _ := io.ReadAll(res.Body)
			if res.StatusCode != tt.wantStatus {
				t.Fatalf("status %d, want %d (body %s)", res.StatusCode, tt.wantStatus, body)
			}

			if tt.wantFailure != nil {
				if res.Header.Get("Content-Type") != "application/problem+json" || bytes.Contains(body, []byte("quota")) ||
					!bytes.Contains(body, []byte(`"title":"Internal Server Error"`)) {
					t.Fatalf("response %v %s, want the safe 500", res.Header, body)
				}
			} else if string(body) != `{"code":"quota"}` {
				t.Fatalf("body %s, want the renderer's", body)
			}

			if got := logs.failedRecord(t)["srvr.renderer.failure"]; got != tt.wantFailure {
				t.Fatalf("srvr.renderer.failure %v, want %v", got, tt.wantFailure)
			}
		})
	}
}
