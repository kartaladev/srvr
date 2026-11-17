package ginsrvr_test

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"github.com/kartaladev/srvr/ginsrvr"
)

// TestGinConfig covers engine-support / Scenario: Same validation on every
// engine and Scenario: Engine-specific option placement, gin rows.
func TestGinConfig(t *testing.T) {
	t.Parallel()

	t.Run("accepted", func(t *testing.T) {
		t.Parallel()

		var hookRan bool

		tests := []struct {
			name  string
			opts  []srvr.Option
			debug bool // DEBUG records are written
		}{
			{"none", nil, false},
			{"debug level", []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "debug"})}, true},
			{"config then level", []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "warn"}), srvr.WithLogLevel(slog.LevelDebug)}, true},
			{"level and WithEngine in one list", []srvr.Option{srvr.WithLogLevel(slog.LevelDebug), ginsrvr.WithEngine(func(*gin.Engine) { hookRan = true })}, true},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				srv, logs := newServer(t, tt.opts...)
				if err := srv.Register(routes(func(g *gin.RouterGroup) {
					g.GET("/d", func(c *gin.Context) {
						srvr.LoggerFromContext(c).DebugContext(c, "debug.note")
						c.Status(http.StatusOK)
					})
				})); err != nil {
					t.Fatal(err)
				}

				do(t, http.MethodGet, start(t, srv)+"/d", nil, nil)

				if got := len(logs.withMsg(t, "debug.note")) == 1; got != tt.debug {
					t.Errorf("DEBUG written %v, want %v", got, tt.debug)
				}
			})
		}

		if !hookRan {
			t.Fatal("WithEngine hook did not run")
		}
	})

	t.Run("ignores incoming trace", func(t *testing.T) {
		t.Parallel()

		srv, _ := newServer(t, srvr.WithTraceConfig(srvr.TraceConfig{IgnoreIncoming: true}))
		var got string
		if err := srv.Register(routes(func(g *gin.RouterGroup) {
			g.GET("/t", func(c *gin.Context) {
				tr, _ := srvr.TraceFromContext(c)
				got = tr.TraceID
			})
		})); err != nil {
			t.Fatal(err)
		}

		do(t, http.MethodGet, start(t, srv)+"/t", nil, map[string]string{"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"})

		if got == "" || got == "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Fatalf("trace id %q, want a new one", got)
		}
	})

	rejected := []struct {
		name string
		opts []srvr.Option
		want string
	}{
		{"invalid level", []srvr.Option{srvr.WithLogConfig(srvr.LogConfig{Level: "verbose"})}, "Level"},
		{"negative ShutdownTimeout", []srvr.Option{srvr.WithConfig(srvr.Config{ShutdownTimeout: -time.Second})}, "ShutdownTimeout"},
		{"an option of another engine", []srvr.Option{engine.ForEngine("fiber", "fibersrvr.WithFiberConfig", func() {})}, "fibersrvr.WithFiberConfig"},
	}

	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, err := ginsrvr.New(tt.opts...)
			if srv != nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New = %v, %v; want an error wrapping ErrConfig naming %q", srv, err, tt.want)
			}
		})
	}
}
