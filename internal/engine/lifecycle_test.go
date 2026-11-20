package engine_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/internal/engine"
	"github.com/kartaladev/srvr/internal/errchain"
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

	ctx, cancel := context.WithCancel(context.Background()) //nolint:usetesting // Start must run until the cleanup cancels it and waits for its result
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
		state string                                              // "running", "never started" or "stopped"
		call  func(context.Context, *engine.Lifecycle[int]) error // the context is the test's
		want  error                                               // nil: the call returns nil
		isCfg bool                                                // the call returns an error wrapping ErrConfig
	}{
		{name: "Start while running", state: "running", call: func(ctx context.Context, l *engine.Lifecycle[int]) error {
			return l.Start(ctx, func([]int) (engine.Runner, engine.Routes, error) { return &fakeRunner{}, nil, nil })
		}, want: srvr.ErrServerStarted},
		{name: "Shutdown never started", state: "never started", call: func(ctx context.Context, l *engine.Lifecycle[int]) error { return l.Shutdown(ctx) }},
		{name: "Start after stop", state: "stopped", call: func(ctx context.Context, l *engine.Lifecycle[int]) error {
			return l.Start(ctx, func([]int) (engine.Runner, engine.Routes, error) { return &fakeRunner{}, nil, nil })
		}, want: srvr.ErrServerClosed},
		{name: "Register while running", state: "running", call: func(_ context.Context, l *engine.Lifecycle[int]) error { return l.Register(1) }, want: srvr.ErrServerStarted},
		{name: "Register after stop", state: "stopped", call: func(_ context.Context, l *engine.Lifecycle[int]) error { return l.Register(1) }, want: srvr.ErrServerClosed},
		{name: "Shutdown(nil) while running", state: "running", call: func(_ context.Context, l *engine.Lifecycle[int]) error {
			return l.Shutdown(nil) //nolint:staticcheck,contextcheck // a nil context is the row under test.
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
				if err := l.Shutdown(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := wait(); err != nil {
					t.Fatalf("Start = %v after Shutdown", err)
				}
			}

			err := tt.call(t.Context(), l)

			switch {
			case tt.isCfg:
				if !errors.Is(err, srvr.ErrConfig) || !errors.Is(err, errchain.ErrConfig) {
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

// TestServiceAttributes covers structured-logging / Scenario: Service
// attributes, with an injected build-info reader.
func TestServiceAttributes(t *testing.T) {
	t.Parallel()

	exe := "unknown_service:" + filepath.Base(os.Args[0])
	tagged := func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v1.4.2"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "9c1e2ab"}}}, true
	}
	devel := func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true }
	none := func() (*debug.BuildInfo, bool) { return nil, false }

	tests := []struct {
		name                  string
		opts                  []srvr.Option
		build                 func() (*debug.BuildInfo, bool)
		wantName, wantVersion string
		wantRevision, wantEnv string
	}{
		{"config", []srvr.Option{srvr.WithServiceConfig(srvr.ServiceConfig{Name: "registration-api", Version: "1.4.2"})}, tagged, "registration-api", "1.4.2", "9c1e2ab", ""},
		{"tagged build", nil, tagged, exe, "v1.4.2", "9c1e2ab", ""},
		{"go run", nil, devel, exe, "(devel)", "", ""},
		{"no build info", nil, none, exe, "", "", ""},
		{"config then WithServiceName", []srvr.Option{srvr.WithServiceConfig(srvr.ServiceConfig{Name: "registration-api"}), srvr.WithServiceName("orders")}, tagged, "orders", "v1.4.2", "9c1e2ab", ""},
		{"WithServiceName then empty config", []srvr.Option{srvr.WithServiceName("orders"), srvr.WithServiceConfig(srvr.ServiceConfig{})}, tagged, exe, "v1.4.2", "9c1e2ab", ""},
		{"version and environment options", []srvr.Option{srvr.WithServiceVersion("2.0.0"), srvr.WithServiceEnvironment("staging")}, tagged, exe, "2.0.0", "9c1e2ab", "staging"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			core, err := engine.ResolveConfig(tt.opts)
			if err != nil {
				t.Fatal(err)
			}

			got := map[string]string{}
			for _, a := range engine.ServiceAttrs(core, tt.build) {
				got[a.Key] = a.Value.String()
			}

			want := map[string]string{"service.name": tt.wantName}
			for k, v := range map[string]string{"service.version": tt.wantVersion, "vcs.revision": tt.wantRevision, "deployment.environment.name": tt.wantEnv} {
				if v != "" {
					want[k] = v
				}
			}
			if len(got) != len(want) {
				t.Fatalf("attrs = %v, want %v", got, want)
			}
			for k, v := range want {
				if got[k] != v {
					t.Fatalf("attrs = %v, want %v", got, want)
				}
			}
		})
	}
}

// TestConfigWarning covers engine-support / Scenario: Configuration warning
// record: the record an engine writes for a warning (the gin and fiber
// rows are in their modules), and the net/http row, which has none.
func TestConfigWarning(t *testing.T) {
	t.Parallel()

	t.Run("record", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer

		core, err := engine.ResolveConfig([]engine.Option{srvr.WithLogOutput(&out), srvr.WithServiceName("orders")})
		if err != nil {
			t.Fatal(err)
		}

		core.Warn(t.Context(), engine.WarnGinDebugMode, "gin runs in debug mode; set GIN_MODE=release")

		var rec map[string]any
		if err := json.Unmarshal(out.Bytes(), &rec); err != nil {
			t.Fatalf("record %q: %v", out.String(), err)
		}

		delete(rec, "service.version") // from the test binary's build information
		want := []string{"level", "msg", "service.name", "srvr.log.schema", "srvr.warning.code", "srvr.warning.message", "time"}
		if got := slices.Sorted(maps.Keys(rec)); !slices.Equal(got, want) {
			t.Fatalf("keys %v, want %v", got, want)
		}

		if rec["level"] != "WARN" || rec["msg"] != "server.config.warning" || rec["srvr.warning.code"] != "gin_debug_mode" {
			t.Fatalf("record %v", rec)
		}
	})

	t.Run("net/http writes none", func(t *testing.T) {
		t.Parallel()

		var out bytes.Buffer

		srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(&out))
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- srv.Start(ctx) }()
		<-srv.Ready()
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}

		if n := strings.Count(out.String(), "server.config.warning"); n != 0 {
			t.Fatalf("%d warnings", n)
		}
	})
}
