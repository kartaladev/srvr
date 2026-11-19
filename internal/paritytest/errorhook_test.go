package paritytest

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// TestParity_ConsumerOnError covers error-responses / Scenario: Consumer
// prblm error hook on every engine: a consumer's prblm.OnError passed
// through WithProblemOptions runs once on every engine, whether it returns
// or panics, and srvr still records the failure. Each engine and behaviour
// pair is one Examples row.
func TestParity_ConsumerOnError(t *testing.T) {
	t.Parallel()

	behaviours := []struct {
		name   string
		panics bool
	}{
		{"returns", false},
		{"panics", true},
	}

	for _, b := range behaviours {
		for _, e := range engines {
			t.Run(e.name+" "+b.name, func(t *testing.T) {
				t.Parallel()

				var (
					calls atomic.Int32
					wrong atomic.Bool // a call reported something other than prblm.ErrNotFound with 404
				)
				fn := func(_ context.Context, err error, p *prblm.Problem) {
					calls.Add(1)
					if !errors.Is(err, prblm.ErrNotFound) || p == nil || p.Status != http.StatusNotFound {
						wrong.Store(true)
					}
					if b.panics {
						panic("consumer OnError panics")
					}
				}

				base, logs := e.start(t, failsNotFound, srvr.WithProblemOptions(prblm.OnError(fn)))

				res, _, err := send(t, http.MethodGet, base+"/items/7", nil)
				must(t, err)
				if res.StatusCode != http.StatusNotFound || res.Header.Get("Content-Type") != "application/problem+json" {
					t.Fatalf("response %d %q, want a 404 problem", res.StatusCode, res.Header.Get("Content-Type"))
				}

				r := one(t, logs)
				problem, _ := r["problem"].(map[string]any)
				if r["msg"] != "http.request.failed" || problem["status"] != float64(http.StatusNotFound) {
					t.Fatalf("record %v, want http.request.failed with problem.status 404", r)
				}

				if n := calls.Load(); n != 1 || wrong.Load() {
					t.Fatalf("consumer OnError called %d times (unexpected report: %v), want once with prblm.ErrNotFound and status 404", n, wrong.Load())
				}
			})
		}
	}
}
