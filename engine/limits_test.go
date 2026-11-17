package engine_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

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
