package srvr

import "testing"

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
