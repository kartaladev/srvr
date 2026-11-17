package fibersrvr_test

import (
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
)

// TestFiberContextReuse covers fiber-engine / Scenario: Context reuse: with
// fiber reusing its pooled contexts and buffers, every access record keeps
// its own request's route and trace id.
func TestFiberContextReuse(t *testing.T) {
	t.Parallel()

	const (
		requests = 1000
		routeN   = 10
	)

	for _, immutable := range []bool{false, true} {
		t.Run(fmt.Sprint("immutable ", immutable), func(t *testing.T) {
			t.Parallel()

			var opts []srvr.Option
			if immutable {
				opts = append(opts, fibersrvr.WithFiberConfig(func(c *fiber.Config) { c.Immutable = true }))
			}

			srv, logs := newServer(t, opts...)
			conf := routes(func(r fiber.Router) {
				for i := range routeN {
					r.Get(fmt.Sprintf("/r%d/:id", i), ok)
				}
			})

			// Configure once before the concurrent requests.
			serve(t, srv, conf, http.MethodGet, "/r0/warmup", nil, nil)

			want := map[string]string{} // trace id -> route
			var wg sync.WaitGroup

			for i := range requests {
				traceID := fmt.Sprintf("%032x", i+1)
				route := fmt.Sprintf("/r%d/:id", i%routeN)
				want[traceID] = route

				wg.Go(func() {
					serve(t, srv, conf, http.MethodGet, fmt.Sprintf("/r%d/%d", i%routeN, i), nil,
						map[string]string{"traceparent": "00-" + traceID + "-00f067aa0ba902b7-01"})
				})
			}

			wg.Wait()

			got := 0
			for _, r := range logs.access(t) {
				id, _ := r["trace_id"].(string)
				route, known := want[id]
				if !known {
					continue // the warm-up request
				}

				got++
				if r["http.route"] != route {
					t.Fatalf("record %v: route %v, want %s", id, r["http.route"], route)
				}
			}

			if got != requests {
				t.Fatalf("%d matching records, want %d", got, requests)
			}
		})
	}
}
