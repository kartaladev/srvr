package engine_test

import (
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

// TestEngineConfig covers engine-support / Scenario: Same validation on
// every engine (net/http rows) and Scenario: Engine-specific option
// placement (net/http row), driving the shared resolution directly and
// through srvr.New.
func TestEngineConfig(t *testing.T) {
	t.Parallel()

	foreign := engine.ForEngine("gin", "ginsrvr.WithEngine", func() {})

	tests := []struct {
		name    string
		opts    []engine.Option
		message string // empty: resolution succeeds
	}{
		{name: "none"},
		{name: "debug level", opts: []engine.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "debug"})}},
		{name: "invalid level", opts: []engine.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "verbose"})}, message: "Level"},
		{name: "negative ShutdownTimeout", opts: []engine.Option{srvr.WithShutdownTimeout(-1)}, message: "ShutdownTimeout"},
		{name: "nil option", opts: []engine.Option{nil}, message: "Option"},
		{name: "invalid problem options", opts: []engine.Option{srvr.WithProblemOptions(prblm.WithNamespace("not a uri"))}, message: "ProblemOptions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			core, err := engine.ResolveConfig(tt.opts)
			if tt.message == "" {
				if err != nil || core == nil {
					t.Fatalf("ResolveConfig = %v, %v; want a core", core, err)
				}

				return
			}

			if core != nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("ResolveConfig = %v, %v; want an error wrapping srvr.ErrConfig naming %q", core, err, tt.message)
			}
		})
	}

	t.Run("an option of another engine fails net/http", func(t *testing.T) {
		t.Parallel()

		srv, err := srvr.New(srvr.WithLogLevel(slog.LevelDebug), foreign)
		if srv != nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), "ginsrvr.WithEngine") {
			t.Fatalf("New = %v, %v; want an error wrapping ErrConfig naming ginsrvr.WithEngine", srv, err)
		}
	})

	t.Run("engine options are read by their engine only", func(t *testing.T) {
		t.Parallel()

		core, err := engine.ResolveConfig([]engine.Option{foreign, engine.ForEngine("fiber", "fibersrvr.WithFiberConfig", 7)})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := core.EngineOptions("fiber"); err == nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), "ginsrvr.WithEngine") {
			t.Fatalf("EngineOptions(fiber) error = %v, want one naming ginsrvr.WithEngine", err)
		}

		core, err = engine.ResolveConfig([]engine.Option{engine.ForEngine("fiber", "fibersrvr.WithFiberConfig", 7)})
		if err != nil {
			t.Fatal(err)
		}

		got, err := core.EngineOptions("fiber")
		if err != nil || len(got) != 1 || got[0] != 7 {
			t.Fatalf("EngineOptions(fiber) = %v, %v; want [7]", got, err)
		}
	})
}

// TestSrvrNamesAreEngineDeclarations covers engine-support / Scenario: srvr
// names are the engine declarations (configuration rows; the lifecycle
// rows are in TestEngineLifecycle).
func TestSrvrNamesAreEngineDeclarations(t *testing.T) {
	t.Parallel()

	invalid := srvr.WithLogConfig(srvr.LogConfig{Level: "verbose"})

	_, err := engine.ResolveConfig([]engine.Option{invalid})
	if !errors.Is(err, srvr.ErrConfig) {
		t.Errorf("engine.ResolveConfig error %v does not match srvr.ErrConfig", err)
	}

	_, err = srvr.New(invalid)
	if !errors.Is(err, engine.ErrConfig) {
		t.Errorf("srvr.New error %v does not match engine.ErrConfig", err)
	}
}
