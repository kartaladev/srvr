package paritytest

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

const (
	traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	traceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	// internalBody is the problem every unrecognised failure gets.
	internalBody = `{"title":"Internal Server Error","status":500,"instance":"/items/7","trace_id":"` + traceID + `"}`
)

// TestParity_Problem covers engine-parity / Scenario: Problem parity: the
// decoded bodies are compared exactly, and the Content-Type, Vary and
// Cache-Control headers must equal net/http's.
func TestParity_Problem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outcome outcome
		status  int
		body    string
	}{
		{"prblm.ErrNotFound", failsNotFound, 404, `{"title":"Not Found","status":404,"detail":"The requested resource was not found.","instance":"/items/7","trace_id":"` + traceID + `"}`},
		{"errors.New(secret)", failsSecret, 500, internalBody},
		{"panic boom", panicsBoom, 500, internalBody},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var want map[string]any
			must(t, json.Unmarshal([]byte(tt.body), &want))

			var reference http.Header

			for _, e := range engines {
				base, _ := e.start(t, tt.outcome)

				res, body, err := send(t, http.MethodGet, base+"/items/7", map[string]string{"traceparent": traceparent})
				must(t, err)

				var got map[string]any
				if err := json.Unmarshal(body, &got); err != nil {
					t.Fatalf("%s: body %q: %v", e.name, body, err)
				}

				if res.StatusCode != tt.status || !reflect.DeepEqual(got, want) {
					t.Errorf("%s: %d %v, want %d %v", e.name, res.StatusCode, got, tt.status, want)
				}

				if reference == nil {
					reference = res.Header

					continue
				}

				for _, h := range []string{"Content-Type", "Vary", "Cache-Control"} {
					if got, want := res.Header.Values(h), reference.Values(h); !reflect.DeepEqual(got, want) {
						t.Errorf("%s: %s %q, net/http sends %q", e.name, h, got, want)
					}
				}
			}
		})
	}
}
