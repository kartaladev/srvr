package ginsrvr

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
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
// renderer, gin rows "returns after the deadline" and "returns before the
// deadline while the client already disconnected", through the server's
// whole gin pipeline with the render deadline shortened through
// engine.SetRenderDeadline (owner decision 2026-11-21): the bytes sent and
// the failed record.
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

			if _, err := srv.configure([]srvr.RouteConfigurer[*gin.RouterGroup]{routes(func(g *gin.RouterGroup) {
				g.GET("/items/:id", func(c *gin.Context) {
					if tt.disconnect {
						_ = c.Error(c.Request.Context().Err())

						return
					}
					_ = c.Error(errors.New("db down"))
				})
			})}); err != nil {
				t.Fatal(err)
			}

			ctx := t.Context()
			if tt.disconnect {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			w := httptest.NewRecorder()
			srv.handler().ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodGet, "/items/7", nil))

			if w.Code != tt.wantStatus {
				t.Fatalf("status %d, want %d (body %s)", w.Code, tt.wantStatus, w.Body)
			}

			if tt.wantFailure != nil {
				if w.Header().Get("Content-Type") != "application/problem+json" || bytes.Contains(w.Body.Bytes(), []byte("quota")) ||
					!bytes.Contains(w.Body.Bytes(), []byte(`"title":"Internal Server Error"`)) {
					t.Fatalf("response %v %s, want the safe 500", w.Header(), w.Body)
				}
			} else if w.Body.String() != `{"code":"quota"}` {
				t.Fatalf("body %s, want the renderer's", w.Body)
			}

			if got := logs.failedRecord(t)["srvr.renderer.failure"]; got != tt.wantFailure {
				t.Fatalf("srvr.renderer.failure %v, want %v", got, tt.wantFailure)
			}
		})
	}
}
