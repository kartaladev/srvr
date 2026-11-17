package srvr_test

import (
	"context"
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

// goldenLifecycleKeys pins the keys of srvr's lifecycle events, for a
// server with a service name and version (vcs.revision and
// deployment.environment.name are added when known).
var goldenLifecycleKeys = map[string][]string{
	"server.started":              {"level", "msg", "server.address", "service.name", "service.version", "srvr.log.schema", "time"},
	"server.stopping":             {"level", "msg", "service.name", "service.version", "srvr.log.schema", "time"},
	"server.stopped":              {"level", "msg", "service.name", "service.version", "srvr.drain.duration_ms", "srvr.log.schema", "time"},
	"server.shutdown_hook.failed": {"code.function.name", "exception.message", "level", "msg", "service.name", "service.version", "srvr.log.schema", "srvr.shutdown_hook.index", "srvr.shutdown_hook.phase", "time"},
	"server.failed":               {"exception.message", "level", "msg", "service.name", "service.version", "srvr.log.schema", "time"},
}

// TestLogSchemaGolden_Lifecycle pins the keys of the lifecycle events.
func TestLogSchemaGolden_Lifecycle(t *testing.T) {
	t.Parallel()

	out := &logBuffer{}
	opts := []srvr.Option{
		srvr.WithLogOutput(out),
		srvr.WithServiceConfig(srvr.ServiceConfig{Name: "registration-api", Version: "1.4.2"}),
		srvr.WithShutdownHook(srvr.AfterDrain, func(context.Context) error { return errors.New("flush failed") }),
	}
	stopByContext(t, opts)

	taken := listen(t)
	srv, err := srvr.New(append(opts, srvr.WithAddr(taken.Addr().String()))...)
	mustNil(t, err)
	_ = srv.Start(context.Background())

	for event, want := range goldenLifecycleKeys {
		recs := out.find(t, event)
		if len(recs) == 0 {
			t.Fatalf("no %s record:\n%s", event, out.String())
		}
		got := slices.DeleteFunc(flatKeys(recs[0], ""), func(k string) bool { return k == "vcs.revision" })
		if !slices.Equal(got, want) {
			t.Errorf("%s keys = %v\nwant %v (bump srvr.log.schema on a rename or removal)", event, got, want)
		}
		if recs[0]["srvr.log.schema"] != float64(goldenSchemaVersion) {
			t.Errorf("%s srvr.log.schema = %v", event, recs[0]["srvr.log.schema"])
		}
	}
}
