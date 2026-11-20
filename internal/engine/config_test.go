package engine_test

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/internal/engine"
	"github.com/kartaladev/srvr/internal/errchain"
)

// TestEngineConfig covers engine-support / Scenario: Same validation on
// every engine (net/http rows) and Scenario: Engine-specific option
// placement (net/http row), driving the shared resolution directly and
// through srvr.New.
func TestEngineConfig(t *testing.T) {
	t.Parallel()

	foreign := engine.Only("gin", "ginsrvr.WithGinEngine", func() {})

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
		if srv != nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), "ginsrvr.WithGinEngine") {
			t.Fatalf("New = %v, %v; want an error wrapping ErrConfig naming ginsrvr.WithGinEngine", srv, err)
		}
	})

	t.Run("engine options are read by their engine only", func(t *testing.T) {
		t.Parallel()

		core, err := engine.ResolveConfig([]engine.Option{foreign, engine.Only("fiber", "fibersrvr.WithFiberConfig", 7)})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := core.EngineOptions("fiber"); err == nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), "ginsrvr.WithGinEngine") {
			t.Fatalf("EngineOptions(fiber) error = %v, want one naming ginsrvr.WithGinEngine", err)
		}

		core, err = engine.ResolveConfig([]engine.Option{engine.Only("fiber", "fibersrvr.WithFiberConfig", 7)})
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
	if !errors.Is(err, errchain.ErrConfig) {
		t.Errorf("srvr.New error %v does not match errchain.ErrConfig", err)
	}
}

// TestEffectiveLimits covers server-lifecycle / Scenario: Effective limits.
// A disabled timeout is passed to http.Server as a negative value, because
// http.Server reads a zero ReadHeaderTimeout or IdleTimeout as "use
// ReadTimeout".
func TestEffectiveLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    []srvr.Option
		setting string
		want    any
	}{
		// defaults
		{"default addr", nil, "addr", ":8080"},
		{"default ReadHeaderTimeout", nil, "ReadHeaderTimeout", 10 * time.Second},
		{"default ReadTimeout", nil, "ReadTimeout", 60 * time.Second},
		{"default WriteTimeout", nil, "WriteTimeout", 60 * time.Second},
		{"default IdleTimeout", nil, "IdleTimeout", 120 * time.Second},
		{"default MaxHeaderBytes", nil, "MaxHeaderBytes", 1 << 20},
		{"default MaxRequestBodyBytes", nil, "MaxRequestBodyBytes", int64(4 << 20)},
		{"default ShutdownTimeout", nil, "ShutdownTimeout", 15 * time.Second},
		{"default ShutdownHookTimeout", nil, "ShutdownHookTimeout", 5 * time.Second},
		// overrides
		{"unbounded ShutdownHookTimeout", []srvr.Option{srvr.WithConfig(srvr.Config{ShutdownHookTimeout: -1})}, "ShutdownHookTimeout", time.Duration(-1)},
		{"WithShutdownHookTimeout", []srvr.Option{srvr.WithShutdownHookTimeout(time.Second)}, "ShutdownHookTimeout", time.Second},
		{"WithReadHeaderTimeout", []srvr.Option{srvr.WithReadHeaderTimeout(2 * time.Second)}, "ReadHeaderTimeout", 2 * time.Second},
		{"zero ReadHeaderTimeout", []srvr.Option{srvr.WithConfig(srvr.Config{ReadHeaderTimeout: 0})}, "ReadHeaderTimeout", 10 * time.Second},
		{"disabled WriteTimeout", []srvr.Option{srvr.WithConfig(srvr.Config{WriteTimeout: -1})}, "WriteTimeout", time.Duration(-1)},
		{"disabled ReadHeaderTimeout", []srvr.Option{srvr.WithConfig(srvr.Config{ReadHeaderTimeout: -1})}, "ReadHeaderTimeout", time.Duration(-1)},
		{"disabled IdleTimeout", []srvr.Option{srvr.WithIdleTimeout(-1)}, "IdleTimeout", time.Duration(-1)},
		{"unlimited body", []srvr.Option{srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: -1})}, "MaxRequestBodyBytes", int64(-1)},
		{"Config addr", []srvr.Option{srvr.WithConfig(srvr.Config{Addr: ":9090"})}, "addr", ":9090"},
		{"WithReadTimeout", []srvr.Option{srvr.WithReadTimeout(time.Second)}, "ReadTimeout", time.Second},
		{"WithWriteTimeout", []srvr.Option{srvr.WithWriteTimeout(time.Second)}, "WriteTimeout", time.Second},
		{"WithMaxHeaderBytes", []srvr.Option{srvr.WithMaxHeaderBytes(512)}, "MaxHeaderBytes", 512},
		{"WithMaxRequestBodyBytes", []srvr.Option{srvr.WithMaxRequestBodyBytes(10)}, "MaxRequestBodyBytes", int64(10)},
		{"WithShutdownTimeout", []srvr.Option{srvr.WithShutdownTimeout(time.Second)}, "ShutdownTimeout", time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			core, err := engine.ResolveConfig(tt.opts)
			if err != nil {
				t.Fatal(err)
			}

			c := core.Config()
			hs := engine.HTTPServerOf(c, http.NotFoundHandler())
			got := map[string]any{
				"addr":                c.Addr,
				"ReadHeaderTimeout":   hs.ReadHeaderTimeout,
				"ReadTimeout":         hs.ReadTimeout,
				"WriteTimeout":        hs.WriteTimeout,
				"IdleTimeout":         hs.IdleTimeout,
				"MaxHeaderBytes":      hs.MaxHeaderBytes,
				"MaxRequestBodyBytes": c.MaxRequestBodyBytes,
				"ShutdownTimeout":     c.ShutdownTimeout,
				"ShutdownHookTimeout": c.ShutdownHookTimeout,
			}[tt.setting]

			if got != tt.want {
				t.Fatalf("%s = %v, want %v", tt.setting, got, tt.want)
			}
		})
	}
}
