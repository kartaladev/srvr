package srvr_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

// TestStart_Fails covers server-lifecycle / Scenario: Start fails.
func TestStart_Fails(t *testing.T) {
	t.Parallel()

	tests := []struct {
		condition string
		// setup returns the server and the Start context.
		setup func(t *testing.T) (*srvr.Server, context.Context)
		want  func(error) bool
	}{
		{
			condition: "the address is already in use",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()
				taken := listen(t)

				srv, err := srvr.New(srvr.WithAddr(taken.Addr().String()))
				if err != nil {
					t.Fatal(err)
				}

				return srv, context.Background()
			},
			want: func(err error) bool {
				var opErr *net.OpError

				return errors.As(err, &opErr)
			},
		},
		{
			condition: "the Start context is already cancelled",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()
				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				return newServer(t), ctx
			},
			want: func(err error) bool { return errors.Is(err, context.Canceled) },
		},
		{
			condition: "Start(nil) is called",
			setup: func(t *testing.T) (*srvr.Server, context.Context) {
				t.Helper()

				return newServer(t), nil
			},
			want: func(err error) bool { return errors.Is(err, srvr.ErrConfig) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.condition, func(t *testing.T) {
			t.Parallel()

			srv, ctx := tt.setup(t)
			done := make(chan error, 1)
			go func() { done <- srv.Start(ctx) }()

			select {
			case err := <-done:
				if !tt.want(err) {
					t.Fatalf("Start = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Start did not return")
			}

			select {
			case <-srv.Ready():
				t.Fatal("Ready() was closed")
			default:
			}
		})
	}
}
