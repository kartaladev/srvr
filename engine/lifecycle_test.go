package engine_test

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

// fakeRunner serves by accepting and closing connections until the
// listener is closed, and counts Drain calls.
type fakeRunner struct {
	drains atomic.Int32
}

func (f *fakeRunner) Serve(ln net.Listener) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}

		_ = conn.Close()
	}
}

func (f *fakeRunner) Drain(context.Context) error {
	f.drains.Add(1)

	return nil
}

// newLifecycle returns a lifecycle on an ephemeral loopback port.
func newLifecycle(t *testing.T) *engine.Lifecycle[int] {
	t.Helper()

	core, err := engine.ResolveConfig([]engine.Option{srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard)})
	if err != nil {
		t.Fatal(err)
	}

	return engine.NewLifecycle[int](core)
}

// startLifecycle starts l with runner in the background and waits until
// it is ready; it returns a function waiting for Start's result.
func startLifecycle(t *testing.T, l *engine.Lifecycle[int], runner engine.Runner) (context.CancelFunc, func() error) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- l.Start(ctx, func([]int) (engine.Runner, engine.Routes, error) { return runner, nil, nil })
	}()

	select {
	case <-l.Ready():
	case err := <-done:
		t.Fatalf("Start returned before readiness: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("not ready")
	}

	wait := sync.OnceValue(func() error {
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			return errors.New("start did not return")
		}
	})

	t.Cleanup(func() {
		cancel()
		_ = wait()
	})

	return cancel, wait
}

// TestEngineLifecycle covers engine-support / Scenario: Lifecycle misuse on
// every engine, as rows run against the shared state machine with a fake
// runner, and the lifecycle rows of Scenario: srvr names are the engine
// declarations (each error matches both the srvr and the engine sentinel).
func TestEngineLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state string // "running", "never started" or "stopped"
		call  func(*engine.Lifecycle[int]) error
		want  error // nil: the call returns nil
		isCfg bool  // the call returns an error wrapping ErrConfig
	}{
		{name: "Start while running", state: "running", call: func(l *engine.Lifecycle[int]) error {
			return l.Start(context.Background(), func([]int) (engine.Runner, engine.Routes, error) { return &fakeRunner{}, nil, nil })
		}, want: srvr.ErrServerStarted},
		{name: "Shutdown never started", state: "never started", call: func(l *engine.Lifecycle[int]) error { return l.Shutdown(context.Background()) }},
		{name: "Start after stop", state: "stopped", call: func(l *engine.Lifecycle[int]) error {
			return l.Start(context.Background(), func([]int) (engine.Runner, engine.Routes, error) { return &fakeRunner{}, nil, nil })
		}, want: srvr.ErrServerClosed},
		{name: "Register while running", state: "running", call: func(l *engine.Lifecycle[int]) error { return l.Register(1) }, want: srvr.ErrServerStarted},
		{name: "Register after stop", state: "stopped", call: func(l *engine.Lifecycle[int]) error { return l.Register(1) }, want: srvr.ErrServerClosed},
		{name: "Shutdown(nil) while running", state: "running", call: func(l *engine.Lifecycle[int]) error {
			return l.Shutdown(nil) //nolint:staticcheck // a nil context is the row under test.
		}, isCfg: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := newLifecycle(t)
			runner := &fakeRunner{}

			switch tt.state {
			case "running":
				startLifecycle(t, l, runner)
			case "stopped":
				_, wait := startLifecycle(t, l, runner)
				if err := l.Shutdown(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := wait(); err != nil {
					t.Fatalf("Start = %v after Shutdown", err)
				}
			}

			err := tt.call(l)

			switch {
			case tt.isCfg:
				if !errors.Is(err, srvr.ErrConfig) || !errors.Is(err, engine.ErrConfig) {
					t.Fatalf("call = %v, want an error wrapping ErrConfig", err)
				}
				select {
				case <-l.Ready():
				default:
					t.Fatal("not ready after Shutdown(nil)")
				}
				if runner.drains.Load() != 0 {
					t.Fatal("Shutdown(nil) drained the server")
				}
			case tt.want == nil:
				if err != nil {
					t.Fatalf("call = %v, want nil", err)
				}
			default:
				if !errors.Is(err, tt.want) {
					t.Fatalf("call = %v, want %v", err, tt.want)
				}
			}
		})
	}
}
