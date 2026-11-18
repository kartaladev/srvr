package ginsrvr_test

import (
	"bytes"
	"errors"
	"io"
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
			{"level and WithGinEngine in one list", []srvr.Option{srvr.WithLogLevel(slog.LevelDebug), ginsrvr.WithGinEngine(func(*gin.Engine) { hookRan = true })}, true},
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
			t.Fatal("WithGinEngine hook did not run")
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
		{"an option of another engine", []srvr.Option{engine.Only("fiber", "fibersrvr.WithFiberConfig", func() {})}, "fibersrvr.WithFiberConfig"},
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

// TestGinBodyLimit covers gin-engine / Scenario: Gin body limit.
func TestGinBodyLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opts   []srvr.Option
		size   int
		status int
	}{
		{"default at limit", nil, 4194304, 200},
		{"default past limit", nil, 4194305, 413},
		{"10 at limit", []srvr.Option{srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: 10})}, 10, 200},
		{"10 past limit", []srvr.Option{srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: 10})}, 11, 413},
		{"disabled", []srvr.Option{srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: -1})}, 8388608, 200},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t, tt.opts...)
			if err := srv.Register(routes(func(g *gin.RouterGroup) {
				g.POST("/upload", func(c *gin.Context) {
					if _, err := io.ReadAll(c.Request.Body); err != nil {
						_ = c.Error(err)

						return
					}

					c.Status(http.StatusOK)
				})
			})); err != nil {
				t.Fatal(err)
			}

			res := do(t, http.MethodPost, start(t, srv)+"/upload", bytes.NewReader(make([]byte, tt.size)), nil)
			if res.status != tt.status {
				t.Fatalf("status %d, want %d", res.status, tt.status)
			}
		})
	}
}
