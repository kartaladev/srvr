package srvr_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

// TestNew_Config covers server-lifecycle / Scenario: Valid and invalid configuration.
func TestNew_Config(t *testing.T) {
	t.Parallel()

	ln := listen(t)

	hook := func(context.Context) error { return nil }

	tests := []struct {
		name    string
		opts    []srvr.Option
		message string // empty: a server is returned
	}{
		// accepted
		{name: "none"},
		{name: "Addr 127.0.0.1:0", opts: []srvr.Option{srvr.WithAddr("127.0.0.1:0")}},
		{name: "ReadHeaderTimeout -1s", opts: []srvr.Option{srvr.WithConfig(srvr.Config{ReadHeaderTimeout: -time.Second})}},
		{name: "MaxRequestBodyBytes -1", opts: []srvr.Option{srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: -1})}},
		{name: "WithListener", opts: []srvr.Option{srvr.WithListener(ln)}},
		{name: "shutdown hook", opts: []srvr.Option{srvr.WithShutdownHook(srvr.AfterDrain, hook)}},
		// rejected
		{name: "Addr port out of range", opts: []srvr.Option{srvr.WithAddr("localhost:99999")}, message: "Addr"},
		{name: "Addr not an address", opts: []srvr.Option{srvr.WithConfig(srvr.Config{Addr: "not an address"})}, message: "Addr"},
		{name: "ShutdownTimeout -1s", opts: []srvr.Option{srvr.WithConfig(srvr.Config{ShutdownTimeout: -time.Second})}, message: "ShutdownTimeout"},
		{name: "MaxHeaderBytes 0 via option", opts: []srvr.Option{srvr.WithMaxHeaderBytes(0)}, message: "MaxHeaderBytes"},
		{name: "WithListener nil", opts: []srvr.Option{srvr.WithListener(nil)}, message: "Listener"},
		{name: "WithListener and Addr", opts: []srvr.Option{srvr.WithListener(ln), srvr.WithAddr(":9090")}, message: "Listener"},
		{name: "WithListener typed nil", opts: []srvr.Option{srvr.WithListener((*net.TCPListener)(nil))}, message: "Listener"},
		{name: "WithListener typed nil and Addr", opts: []srvr.Option{srvr.WithListener((*net.TCPListener)(nil)), srvr.WithAddr(":8080")}, message: "Listener"},
		{
			name:    "WithListener with a panicking Addr and Addr",
			opts:    []srvr.Option{srvr.WithListener(addrListener{Listener: ln, addr: func() net.Addr { panic("addr") }}), srvr.WithAddr(":8080")},
			message: "Listener",
		},
		{name: "nil shutdown hook", opts: []srvr.Option{srvr.WithShutdownHook(srvr.BeforeDrain, nil)}, message: "ShutdownHook"},
		{name: "invalid shutdown phase", opts: []srvr.Option{srvr.WithShutdownHook(srvr.ShutdownPhase(9), hook)}, message: "ShutdownPhase"},
		{name: "MaxHeaderBytes negative via Config", opts: []srvr.Option{srvr.WithConfig(srvr.Config{MaxHeaderBytes: -1})}, message: "MaxHeaderBytes"},
		{name: "WithLogOutput nil", opts: []srvr.Option{srvr.WithLogOutput(nil)}, message: "Output"},
		{name: "WithLogLevelVar nil", opts: []srvr.Option{srvr.WithLogLevelVar(nil)}, message: "LevelVar"},
		{name: "nil option", opts: []srvr.Option{nil}, message: "Option"},
		{name: "WithLogger zero Logger", opts: []srvr.Option{srvr.WithLogger(&slog.Logger{})}, message: "Logger"},
		{name: "WithLogger typed-nil handler", opts: []srvr.Option{srvr.WithLogger(slog.New((*slog.JSONHandler)(nil)))}, message: "Logger"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, err := newRecovered(tt.opts...)
			if tt.message == "" {
				if err != nil || srv == nil {
					t.Fatalf("New = %v, %v; want a server", srv, err)
				}

				return
			}

			if srv != nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("New = %v, %v; want an error wrapping ErrConfig naming %q", srv, err, tt.message)
			}
		})
	}
}
