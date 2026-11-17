package srvr_test

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"

	"github.com/kartaladev/srvr"
)

// TestReady covers server-lifecycle / Scenario: Ephemeral port.
func TestReady(t *testing.T) {
	t.Parallel()

	fixed := freeAddr(t)

	tests := []struct {
		addr  string
		check func(t *testing.T, bound string)
	}{
		{
			addr: "127.0.0.1:0",
			check: func(t *testing.T, bound string) {
				t.Helper()
				host, port, err := net.SplitHostPort(bound)
				if err != nil || host != "127.0.0.1" || port == "0" || port == "" {
					t.Fatalf("Addr() = %q, want 127.0.0.1:<non-zero port>", bound)
				}
			},
		},
		{
			// The Examples row uses 18080; a port free on this machine keeps the
			// row meaningful without depending on 18080 being unused.
			addr: fixed,
			check: func(t *testing.T, bound string) {
				t.Helper()
				if bound != fixed {
					t.Fatalf("Addr() = %q, want %q", bound, fixed)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			t.Parallel()

			srv, err := srvr.New(srvr.WithAddr(tt.addr))
			if err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- srv.Start(ctx) }()
			t.Cleanup(func() {
				cancel()
				if err := <-done; err != nil {
					t.Errorf("Start = %v", err)
				}
			})
			waitReady(t, srv, done)

			tt.check(t, srv.Addr())
		})
	}
}

// TestAddr_BeforeReady covers server-lifecycle / Scenario: Address before readiness.
func TestAddr_BeforeReady(t *testing.T) {
	t.Parallel()

	t.Run("has not been started", func(t *testing.T) {
		t.Parallel()

		if got := newServer(t).Addr(); got != "" {
			t.Fatalf("Addr() = %q, want empty", got)
		}
	})

	t.Run("failed to bind", func(t *testing.T) {
		t.Parallel()

		taken, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer taken.Close()

		srv, err := srvr.New(srvr.WithAddr(taken.Addr().String()))
		if err != nil {
			t.Fatal(err)
		}

		if err := srv.Start(context.Background()); !errors.Is(err, syscall.EADDRINUSE) {
			t.Fatalf("Start = %v, want address in use", err)
		}

		if got := srv.Addr(); got != "" {
			t.Fatalf("Addr() = %q, want empty", got)
		}
	})
}

// freeAddr returns a loopback address with a port that was free a moment ago.
func freeAddr(t *testing.T) string {
	t.Helper()

	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	return ln.Addr().String()
}
