package srvr_test

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"os"
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
		"code.file.path", "code.function.name", "code.line.number", "duration_ms", "error.chain", "error.fingerprint",
		"error.type", "exception.message", "http.request.method", "http.response.body.size", "http.response.status_code",
		"http.route", "level", "msg", "problem.status", "problem.title", "problem.type", "service.name", "service.version",
		"span_id", "srvr.log.schema", "time", "trace_id", "user_agent.original",
	},
}

// goldenPanicKeys and goldenAbortedKeys pin the failed record of a
// recovered panic and of a request aborted with http.ErrAbortHandler.
var (
	goldenPanicKeys = []string{
		"code.file.path", "code.function.name", "code.line.number", "duration_ms", "error.fingerprint", "error.kind",
		"error.type", "exception.message", "exception.stacktrace", "exception.type", "http.request.method",
		"http.response.body.size", "http.response.status_code", "http.route", "level", "msg", "problem.status",
		"problem.title", "problem.type", "service.name", "service.version", "span_id", "srvr.log.schema",
		"srvr.stacktrace.truncated", "time", "trace_id", "user_agent.original",
	}
	goldenAbortedKeys = []string{
		"duration_ms", "error.kind", "error.type", "http.request.method", "http.response.body.size", "http.route", "level",
		"msg", "service.name", "service.version", "span_id", "srvr.log.schema", "srvr.response.aborted", "time",
		"trace_id", "user_agent.original",
	}
)

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
		g.GET("/panic", func(http.ResponseWriter, *http.Request) error { panic("boom") })
		g.GET("/abort", func(http.ResponseWriter, *http.Request) error { panic(http.ErrAbortHandler) })
	}, srvr.WithServiceVersion("1.4.2"))
	get(t, base+"/ok")
	get(t, base+"/fail")

	for event, want := range goldenKeys {
		var rec map[string]any
		eventually(t, func() bool {
			for _, r := range out.find(t, event) {
				if r["http.route"] == "/ok" || r["http.route"] == "/fail" {
					rec = r
				}
			}

			return rec != nil
		}, "no "+event+" record")
		if got := flatKeys(rec, ""); !slices.Equal(got, want) {
			t.Errorf("%s keys = %v\nwant %v (bump srvr.log.schema on a rename or removal)", event, got, want)
		}
		if rec["srvr.log.schema"] != float64(goldenSchemaVersion) {
			t.Errorf("%s srvr.log.schema = %v, want %d", event, rec["srvr.log.schema"], goldenSchemaVersion)
		}
	}

	for path, want := range map[string][]string{"/panic": goldenPanicKeys, "/abort": goldenAbortedKeys} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+path, nil)
		if res, err := http.DefaultClient.Do(req); err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
		}

		var rec map[string]any
		eventually(t, func() bool {
			for _, r := range out.find(t, "http.request.failed") {
				if r["http.route"] == path {
					rec = r
				}
			}

			return rec != nil
		}, "no failed record for "+path)
		if got := flatKeys(rec, ""); !slices.Equal(got, want) {
			t.Errorf("%s failed keys = %v\nwant %v (bump srvr.log.schema on a rename or removal)", path, got, want)
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

// goldenObserverFaultKeys pins srvr.observer.fault, an additive event
// (srvr.log.schema unchanged).
var goldenObserverFaultKeys = []string{
	"exception.message", "level", "msg", "service.name", "service.version", "span_id", "srvr.log.schema",
	"srvr.observer.fault.kind", "srvr.observer.type", "time", "trace_id",
}

// TestLogSchemaGolden_ObserverFault pins the keys of srvr.observer.fault.
func TestLogSchemaGolden_ObserverFault(t *testing.T) {
	t.Parallel()

	obs := observerFunc(func(context.Context, srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) { panic("boom") })
	base, out := loggedServer(t, func(g *srvr.Group) {
		g.GET("/ok", func(http.ResponseWriter, *http.Request) error { return nil })
	}, srvr.WithServiceVersion("1.4.2"), srvr.WithObserver(obs))
	get(t, base+"/ok")

	rec := out.waitRecord(t, "srvr.observer.fault")
	if got := flatKeys(rec, ""); !slices.Equal(got, goldenObserverFaultKeys) {
		t.Errorf("srvr.observer.fault keys = %v\nwant %v", got, goldenObserverFaultKeys)
	}

	if rec["srvr.log.schema"] != float64(goldenSchemaVersion) {
		t.Errorf("srvr.log.schema = %v", rec["srvr.log.schema"])
	}
}

// TestLogSchemaDocumented checks that LOGGING.md lists every pinned key.
func TestLogSchemaDocumented(t *testing.T) {
	t.Parallel()

	doc, err := os.ReadFile("LOGGING.md")
	mustNil(t, err)

	keys := slices.Concat(goldenPanicKeys, goldenAbortedKeys, goldenObserverFaultKeys, []string{"srvr.observer.fault"})
	for event, k := range goldenKeys {
		keys = append(keys, event)
		keys = append(keys, k...)
	}
	for event, k := range goldenLifecycleKeys {
		keys = append(keys, event)
		keys = append(keys, k...)
	}

	for _, k := range keys {
		name := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(k, "problem."), "problem"), ".")
		if !strings.Contains(string(doc), "`"+k+"`") && !strings.Contains(string(doc), "`"+name+"`") {
			t.Errorf("LOGGING.md does not list %q", k)
		}
	}
}
