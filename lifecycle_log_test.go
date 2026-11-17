package srvr_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

// TestLifecycleRecords covers server-lifecycle / Scenario: Lifecycle records.
func TestLifecycleRecords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		action, event, level, attribute string
		run                             func(t *testing.T, opts []srvr.Option)
	}{
		{"the server becomes ready", "server.started", "INFO", "server.address", stopByContext},
		{"shutdown begins", "server.stopping", "INFO", "service.name", stopByContext},
		{"shutdown completes", "server.stopped", "INFO", "srvr.drain.duration_ms", stopByContext},
		{"binding fails", "server.failed", "ERROR", "exception.message", func(t *testing.T, opts []srvr.Option) {
			t.Helper()
			taken := listen(t)
			srv, err := srvr.New(append(opts, srvr.WithAddr(taken.Addr().String()))...)
			mustNil(t, err)
			if err := srv.Start(context.Background()); err == nil {
				t.Fatal("Start = nil")
			}
		}},
		{"serving fails because the passed listener's Accept panics", "server.failed", "ERROR", "exception.message", func(t *testing.T, opts []srvr.Option) {
			t.Helper()
			srv, err := srvr.New(append(opts, srvr.WithListener(&panicListener{Listener: listen(t), acceptPanic: "accept"}))...)
			mustNil(t, err)
			if err := startBounded(context.Background(), t, srv); err == nil {
				t.Fatal("Start = nil")
			}
		}},
		{"the drain deadline passes", "server.stopped", "WARN", "srvr.drain.forced", func(t *testing.T, opts []srvr.Option) {
			t.Helper()
			started, release := make(chan struct{}, 1), make(chan struct{})
			t.Cleanup(func() { close(release) })
			srv := newServer(t, opts...)
			mustNil(t, srv.Register(workRoute(time.Hour, started, release)))
			done := startBackground(t, srv)
			status := inFlight("http://" + srv.Addr() + "/work")
			<-started
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			_ = srv.Shutdown(ctx)
			<-status
			mustNil(t, <-done)
		}},
		{"a shutdown hook returns an error", "server.shutdown_hook.failed", "WARN", "srvr.shutdown_hook.phase", func(t *testing.T, opts []srvr.Option) {
			t.Helper()
			stopByContext(t, append(opts, srvr.WithShutdownHook(srvr.AfterDrain, func(context.Context) error { return errors.New("flush failed") })))
		}},
		{"a shutdown hook panics", "server.shutdown_hook.failed", "WARN", "exception.message", func(t *testing.T, opts []srvr.Option) {
			t.Helper()
			stopByContext(t, append(opts, srvr.WithShutdownHook(srvr.BeforeDrain, func(context.Context) error { panic("boom") })))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			t.Parallel()

			out := &logBuffer{}
			tt.run(t, []srvr.Option{srvr.WithLogOutput(out), srvr.WithServiceName("registration-api")})

			var rec map[string]any
			for _, r := range out.find(t, tt.event) {
				if r["level"] == tt.level {
					rec = r
				}
			}
			if rec == nil {
				t.Fatalf("no %s %s record:\n%s", tt.level, tt.event, out.String())
			}
			_, hasTrace := rec["trace_id"]
			if _, ok := rec[tt.attribute]; !ok || hasTrace || rec["service.name"] != "registration-api" || rec["srvr.log.schema"] != float64(1) {
				t.Fatalf("record %v lacks %s or service attributes, or has trace_id", rec, tt.attribute)
			}
		})
	}
}

// stopByContext starts a server with opts and stops it by cancelling Start.
func stopByContext(t *testing.T, opts []srvr.Option) {
	t.Helper()

	srv, err := srvr.New(append(opts, srvr.WithAddr("127.0.0.1:0"))...)
	mustNil(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := startWith(ctx, t, srv)
	cancel()
	if err := <-done; err != nil && !errors.Is(err, srvr.ErrShutdownHook) {
		t.Fatalf("Start = %v", err)
	}
}
