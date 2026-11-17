package srvr

import (
	"log/slog"
	"testing"
)

// TestOptionPrecedence covers server-lifecycle / Scenario: Option and config
// precedence: options apply in argument order and a With<Struct> option
// sets every field of its struct.
func TestOptionPrecedence(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		opts    []Option
		setting func(*Server) any
		want    any
	}{
		{"WithConfig addr", []Option{WithConfig(Config{Addr: ":9090"})}, listenAddr, ":9090"},
		{"WithConfig then WithAddr", []Option{WithConfig(Config{Addr: ":9090"}), WithAddr(":7070")}, listenAddr, ":7070"},
		{"WithAddr then WithConfig", []Option{WithAddr(":7070"), WithConfig(Config{Addr: ":9090"})}, listenAddr, ":9090"},
		{"WithAddr then empty WithConfig", []Option{WithAddr(":7070"), WithConfig(Config{})}, listenAddr, ":8080"},
		{"WithLogConfig then WithLogLevel", []Option{WithLogConfig(LogConfig{Level: "debug"}), WithLogLevel(slog.LevelWarn)}, logLevel, "WARN"},
		{"WithAccessLogSkipPaths then empty WithAccessLogConfig", []Option{WithAccessLogSkipPaths("/healthz"), WithAccessLogConfig(AccessLogConfig{})}, skipPaths, 0},
		{"WithServiceConfig then WithServiceName", []Option{WithServiceConfig(ServiceConfig{Name: "registration-api"}), WithServiceName("orders")}, serviceName, "orders"},
		{"WithTraceConfig ignores incoming", []Option{WithTraceConfig(TraceConfig{IgnoreIncoming: true})}, ignoresIncoming, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, err := New(tt.opts...)
			if err != nil {
				t.Fatal(err)
			}

			if got := tt.setting(srv); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func listenAddr(s *Server) any { return s.cfg.Addr }

func ignoresIncoming(s *Server) any { return s.settings.trace.IgnoreIncoming }

func logLevel(s *Server) any { return s.settings.log.Level }

func skipPaths(s *Server) any { return len(s.settings.access.SkipPaths) }

func serviceName(s *Server) any { return s.settings.service.Name }
