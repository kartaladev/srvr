package paritytest

import (
	"net/http"
	"slices"
	"testing"
)

// TestParity_Log covers engine-parity / Scenario: Log parity: msg, level
// and http.route per engine, and the flattened key set equal to net/http's
// for the same outcome.
func TestParity_Log(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		outcome outcome
		method  string
		path    string
		msg     string
		level   string
		routed  bool
		skip    string // an engine the row does not cover
	}{
		{"succeeds", succeeds, http.MethodGet, "/items/7", "http.request.completed", "INFO", true, ""},
		{"fails with boom", failsBoom, http.MethodGet, "/items/7", "http.request.failed", "ERROR", true, ""},
		{"fails with ErrNotFound", failsNotFound, http.MethodGet, "/items/7", "http.request.failed", "WARN", true, ""},
		{"GET /nope", succeeds, http.MethodGet, "/nope", "http.request.failed", "WARN", false, ""},
		{"DELETE /items/7", succeeds, http.MethodDelete, "/items/7", "http.request.failed", "WARN", false, ""},
		// fiber recovers http.ErrAbortHandler as an ordinary panic: a
		// documented exception, pinned in TestParity_Exceptions.
		{"panics with http.ErrAbortHandler", panicsAbort, http.MethodGet, "/items/7", "http.request.failed", "WARN", true, "fiber"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var reference []string

			for _, e := range engines {
				if e.name == tt.skip {
					continue
				}

				base, logs := e.start(t, tt.outcome)
				_, _, _ = send(t, tt.method, base+tt.path, map[string]string{"User-Agent": "parity"})

				recs := logs.access(t)
				if len(recs) != 1 {
					t.Fatalf("%s: %d access records, want 1", e.name, len(recs))
				}

				r := recs[0]
				wantRoute := any(nil)
				if tt.routed {
					wantRoute = e.route
				}

				if r["msg"] != tt.msg || r["level"] != tt.level || r["http.route"] != wantRoute {
					t.Errorf("%s: msg %v level %v route %v, want %s %s %v", e.name, r["msg"], r["level"], r["http.route"], tt.msg, tt.level, wantRoute)
				}

				keys := flatKeys(r)
				if reference == nil {
					reference = keys

					continue
				}

				if !slices.Equal(keys, reference) {
					t.Errorf("%s: keys %v\nnet/http: %v", e.name, keys, reference)
				}
			}
		})
	}
}

// flatKeys returns the sorted keys of r, groups flattened with ".".
func flatKeys(r map[string]any) []string {
	var keys []string

	for k, v := range r {
		if g, ok := v.(map[string]any); ok {
			for _, sub := range flatKeys(g) {
				keys = append(keys, k+"."+sub)
			}

			continue
		}

		keys = append(keys, k)
	}

	slices.Sort(keys)

	return keys
}
