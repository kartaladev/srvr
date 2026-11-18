package otelsrvr_test

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"sync"
	"testing"
)

// TestCardinality covers otel-metrics "Cardinality under path scanning".
func TestCardinality(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     func(n int) string
		distinct int
	}{
		{"random paths", func(int) string { return fmt.Sprintf("/%x/%x", rand.Uint64(), rand.Uint64()) }, 0},
		{"user ids", func(n int) string { return fmt.Sprintf("/users/%d", n) }, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, reader := metered(t, usersRoute)

			const requests, workers = 10000, 32

			var wg sync.WaitGroup

			for w := range workers {
				wg.Go(func() {
					for n := w; n < requests; n += workers {
						sendRaw(t, http.MethodGet, base+tt.path(n))
					}
				})
			}

			wg.Wait()

			routes := map[string]bool{}

			var count uint64

			for _, p := range histogram(t, collect(t, reader), durationName) {
				count += p.Count
				if route, ok := attr(p.Attributes, "http.route"); ok {
					routes[route] = true
				}
			}

			if count != requests {
				t.Errorf("recorded %d requests, want %d", count, requests)
			}

			if len(routes) != tt.distinct {
				t.Errorf("distinct http.route values = %d (%v), want %d", len(routes), routes, tt.distinct)
			}
		})
	}
}
