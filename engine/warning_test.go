package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

// TestConfigWarning covers engine-support / Scenario: Configuration warning
// record: the record an engine writes for a warning (the gin and fiber
// rows are in their modules), and the net/http row, which has none.
func TestConfigWarning(t *testing.T) {
	t.Parallel()

	t.Run("record", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer

		core, err := engine.ResolveConfig([]engine.Option{srvr.WithLogOutput(&out), srvr.WithServiceName("orders")})
		if err != nil {
			t.Fatal(err)
		}

		core.Warn(context.Background(), engine.WarnGinDebugMode, "gin runs in debug mode; set GIN_MODE=release")

		var rec map[string]any
		if err := json.Unmarshal(out.Bytes(), &rec); err != nil {
			t.Fatalf("record %q: %v", out.String(), err)
		}

		delete(rec, "service.version") // from the test binary's build information
		want := []string{"level", "msg", "service.name", "srvr.log.schema", "srvr.warning.code", "srvr.warning.message", "time"}
		if got := slices.Sorted(maps.Keys(rec)); !slices.Equal(got, want) {
			t.Fatalf("keys %v, want %v", got, want)
		}

		if rec["level"] != "WARN" || rec["msg"] != "server.config.warning" || rec["srvr.warning.code"] != "gin_debug_mode" {
			t.Fatalf("record %v", rec)
		}
	})

	t.Run("net/http writes none", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer

		srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(&out))
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- srv.Start(ctx) }()
		<-srv.Ready()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}

		if n := strings.Count(out.String(), "server.config.warning"); n != 0 {
			t.Fatalf("%d warnings", n)
		}
	})
}
