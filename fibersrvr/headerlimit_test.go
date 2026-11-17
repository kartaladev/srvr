package fibersrvr_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
)

// TestFiberHeaderLimit covers fiber-engine / Scenario: Fiber header limit
// and the fiber rows of engine-support / Scenario: Configuration warning
// record, over a real listener (app.Test reports protocol rejections as
// transport errors).
func TestFiberHeaderLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		max      int // 0: default
		size     int
		buffer   int
		status   int
		warnings int
	}{
		{0, 60000, 65536, 200, 0},
		{0, 70000, 65536, 431, 0},
		{8192, 9000, 8192, 431, 0},
		{8192, 4000, 8192, 200, 0},
		{65536, 60000, 65536, 200, 0},
		{2097152, 70000, 65536, 431, 1},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("max %d size %d", tt.max, tt.size), func(t *testing.T) {
			t.Parallel()

			var opts []srvr.Option
			if tt.max != 0 {
				opts = append(opts, srvr.WithMaxHeaderBytes(tt.max))
			}

			srv, logs := newServer(t, opts...)
			if err := srv.Register(routes(func(r fiber.Router) { r.Get("/h", ok) })); err != nil {
				t.Fatal(err)
			}

			start(t, srv)

			if got := srv.App().Config().ReadBufferSize; got != tt.buffer {
				t.Fatalf("ReadBufferSize %d, want %d", got, tt.buffer)
			}

			res := rawRequest(t, srv.Addr(), http.MethodGet, "/h", "X-Pad: "+strings.Repeat("a", tt.size)+"\r\n", nil)
			if res.status != tt.status {
				t.Fatalf("status %d, want %d", res.status, tt.status)
			}

			warnings := 0
			for _, r := range logs.withMsg(t, "server.config.warning") {
				if r["srvr.warning.code"] == "fiber_header_limit_capped" {
					warnings++
				}
			}
			if warnings != tt.warnings {
				t.Fatalf("%d fiber_header_limit_capped warnings, want %d", warnings, tt.warnings)
			}
		})
	}
}
