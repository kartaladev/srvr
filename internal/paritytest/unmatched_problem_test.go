package paritytest

import (
	"net/http"
	"regexp"
	"testing"
)

// TestUnmatchedProblemParity reproduces the defect recorded in
// openspec/changes/otel-observability/notes.md "Unmatched 404 problems
// differ between net/http and gin/fiber": engine-parity "Identical
// problems across engines" requires the same problem members for the same
// failure, and a request matching no route is the same failure on every
// engine. It is skipped until the fix, whose RED step it is.
func TestUnmatchedProblemParity(t *testing.T) {
	t.Skip("known defect, see notes.md \"Unmatched 404 problems differ between net/http and gin/fiber\"")
	t.Parallel()

	traceID := regexp.MustCompile(`"trace_id":"[0-9a-f]{32}"`)

	var want string

	for _, e := range engines {
		base, _ := e.start(t, succeeds)

		res, body, err := send(t, http.MethodGet, base+"/nope", nil)
		must(t, err)

		got := traceID.ReplaceAllString(string(body), `"trace_id":"…"`)
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%s status = %d, want 404", e.name, res.StatusCode)
		}

		if want == "" {
			want = got

			continue
		}

		if got != want {
			t.Errorf("%s problem = %s, net/http %s", e.name, got, want)
		}
	}
}
