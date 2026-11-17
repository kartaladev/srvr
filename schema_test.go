package srvr_test

import (
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/kartaladev/srvr"
)

// goldenSchemaVersion is the srvr.log.schema the golden keys belong to.
// Renaming or removing a key, or changing its meaning or type, must bump
// it (and LOGGING.md); adding a key keeps it.
const goldenSchemaVersion = 1

// goldenKeys pins the keys of every event srvr writes, groups flattened
// with ".", for a request with a User-Agent. Optional keys a scenario
// adds (url.path, url.query, client.address, srvr.attr.truncated, ...)
// are covered by their own tests.
var goldenKeys = map[string][]string{
	"http.request.completed": {
		"duration_ms", "http.request.method", "http.response.body.size", "http.response.status_code", "http.route",
		"level", "msg", "span_id", "srvr.log.schema", "time", "trace_id", "user_agent.original",
	},
	"http.request.failed": {
		"duration_ms", "http.request.method", "http.response.body.size", "http.response.status_code", "http.route",
		"level", "msg", "problem.status", "problem.title", "problem.type", "span_id", "srvr.log.schema", "time",
		"trace_id", "user_agent.original",
	},
}

// TestLogSchemaGolden covers structured-logging / Scenario: Schema version
// attribute and pins the keys of every event.
func TestLogSchemaGolden(t *testing.T) {
	t.Parallel()

	base, out := loggedServer(t, func(g *srvr.Group) {
		g.GET("/ok", func(_ http.ResponseWriter, r *http.Request) error {
			srvr.LoggerFromContext(r.Context()).Info("handler.own")

			return nil
		})
		g.GET("/fail", func(http.ResponseWriter, *http.Request) error { return errors.New("boom") })
	})
	get(t, base+"/ok")
	get(t, base+"/fail")

	for event, want := range goldenKeys {
		rec := out.waitRecord(t, event)
		if got := flatKeys(rec, ""); !slices.Equal(got, want) {
			t.Errorf("%s keys = %v\nwant %v (bump srvr.log.schema on a rename or removal)", event, got, want)
		}
		if rec["srvr.log.schema"] != float64(goldenSchemaVersion) {
			t.Errorf("%s srvr.log.schema = %v, want %d", event, rec["srvr.log.schema"], goldenSchemaVersion)
		}
	}

	if _, has := out.waitRecord(t, "handler.own")["srvr.log.schema"]; has {
		t.Error("a handler's own record carries srvr.log.schema")
	}
}

// flatKeys returns the sorted keys of rec, nested groups joined with ".".
func flatKeys(rec map[string]any, prefix string) []string {
	var keys []string
	for _, k := range slices.Sorted(maps.Keys(rec)) {
		if sub, ok := rec[k].(map[string]any); ok {
			keys = append(keys, flatKeys(sub, prefix+k+".")...)

			continue
		}
		keys = append(keys, prefix+k)
	}
	slices.SortFunc(keys, strings.Compare)

	return keys
}
