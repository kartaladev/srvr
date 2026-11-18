package ginsrvr_test

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// TestGinDebugWarning covers gin-engine / Scenario: Gin mode at
// construction and the gin rows of engine-support / Scenario: Configuration
// warning record. Gin's mode is process-global, so the rows run in order,
// not in parallel, each restoring the test mode.
func TestGinDebugWarning(t *testing.T) { //nolint:paralleltest // gin's mode is process-global.
	tests := []struct {
		mode     string
		warnings int
	}{
		{gin.DebugMode, 1},
		{gin.ReleaseMode, 0},
		{gin.TestMode, 0},
	}

	for _, tt := range tests {
		t.Run(tt.mode, func(t *testing.T) {
			checkGoroutines(t)
			gin.SetMode(tt.mode)
			t.Cleanup(func() { gin.SetMode(gin.TestMode) })

			srv, logs := newServer(t)
			start(t, srv)

			warnings := logs.withMsg(t, "server.config.warning")
			if len(warnings) != tt.warnings {
				t.Fatalf("%d warnings, want %d", len(warnings), tt.warnings)
			}
			if tt.warnings == 1 && warnings[0]["srvr.warning.code"] != "gin_debug_mode" {
				t.Fatalf("warning %v", warnings[0])
			}
			if gin.Mode() != tt.mode {
				t.Fatalf("gin mode %q after construction, want %q", gin.Mode(), tt.mode)
			}
		})
	}
}

// TestGinDebugWarning_PanickingHandler covers the engine-support consumer
// fault row: in debug mode, a log handler whose Handle panics loses the
// gin_debug_mode warning, counted in LogWriteErrors, and construction
// still succeeds.
func TestGinDebugWarning_PanickingHandler(t *testing.T) { //nolint:paralleltest // gin's mode is process-global.
	checkGoroutines(t)
	gin.SetMode(gin.DebugMode)
	t.Cleanup(func() { gin.SetMode(gin.TestMode) })

	before := srvr.LogWriteErrors()

	srv, err := ginsrvr.New(srvr.WithLogHandler(panicHandler{}))
	if err != nil || srv == nil {
		t.Fatalf("New = %v, %v", srv, err)
	}

	if srvr.LogWriteErrors() <= before {
		t.Fatal("LogWriteErrors did not increase")
	}
}
