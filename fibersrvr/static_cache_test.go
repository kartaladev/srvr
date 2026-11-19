package fibersrvr_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
	"go.uber.org/goleak"
)

// cacheCleanerRunning reports whether a fasthttp open-file cache cleaner
// started after before is still running. It ignores the goroutines of a
// stopped server (serverLeakOptions), but not the cleaner.
func cacheCleanerRunning(before goleak.Option) bool {
	err := goleak.Find(slices.Concat(serverLeakOptions, []goleak.Option{before})...)

	return err != nil && strings.Contains(err.Error(), fasthttpCacheCleaner)
}

// TestStatic_SendFileCache covers static-files / Scenario: fiber file
// cache on and off.
//
//nolint:paralleltest // the cleaner check sees every goroutine of the process.
func TestStatic_SendFileCache(t *testing.T) {
	// The configuration as a loader fills it, decoded once for its row.
	var fromJSON srvr.StaticConfig
	mustNil(t, json.Unmarshal([]byte(`{"fiber_send_file_cache":3600000000000}`), &fromJSON))

	tests := []struct {
		name    string
		opts    []srvr.StaticOption
		body    string // the second response's body
		cleaner bool   // the cache cleaner still runs after Shutdown
	}{
		{"no options", nil, "v2", false},
		{"WithSendFileCache(time.Hour)", []srvr.StaticOption{fibersrvr.WithSendFileCache(time.Hour)}, "v1", true},
		{
			"config struct with an hour",
			[]srvr.StaticOption{srvr.WithStaticConfig(srvr.StaticConfig{FiberSendFileCache: time.Hour})},
			"v1", true,
		},
		{"config decoded from JSON", []srvr.StaticOption{srvr.WithStaticConfig(fromJSON)}, "v1", true},
		{"zero config struct", []srvr.StaticOption{srvr.WithStaticConfig(srvr.StaticConfig{})}, "v2", false},
		{
			"the config struct replaces the option",
			[]srvr.StaticOption{fibersrvr.WithSendFileCache(time.Hour), srvr.WithStaticConfig(srvr.StaticConfig{})},
			"v2", false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := goleak.IgnoreCurrent()

			dir := t.TempDir()
			file := filepath.Join(dir, "app.js")
			mustNil(t, os.WriteFile(file, []byte("v1"), 0o644))

			srv, _ := newServer(t)
			mustNil(t, srv.Register(fibersrvr.Static("/", os.DirFS(dir), tt.opts...)))
			base := start(t, srv)

			if res := do(t, http.MethodGet, base+"/app.js", nil, nil); res.status != http.StatusOK || res.body != "v1" {
				t.Fatalf("first GET /app.js = %d %q, want 200 \"v1\"", res.status, res.body)
			}

			// Replaced by a new file, as a deploy writes one: a cached open
			// file keeps reading the old one.
			next := filepath.Join(dir, "app.js.next")
			mustNil(t, os.WriteFile(next, []byte("v2"), 0o644))
			mustNil(t, os.Rename(next, file))

			if res := do(t, http.MethodGet, base+"/app.js", nil, nil); res.status != http.StatusOK || res.body != tt.body {
				t.Errorf("second GET /app.js = %d %q, want 200 %q", res.status, res.body, tt.body)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			mustNil(t, srv.Shutdown(ctx))
			clientFor(base).CloseIdleConnections()

			if got := cacheCleanerRunning(before); got != tt.cleaner {
				t.Errorf("cache cleaner running after Shutdown = %v, want %v", got, tt.cleaner)
			}
		})
	}
}
