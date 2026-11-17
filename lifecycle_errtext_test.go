package srvr_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kartaladev/srvr"
)

// TestLifecycleErrorText covers server-lifecycle / Scenario: Lifecycle
// error text is redacted and limited.
func TestLifecycleErrorText(t *testing.T) {
	t.Parallel()

	secretMasker := func(s string) string { return strings.ReplaceAll(s, "secret", "***") }
	panicky := func(string) string { panic("redactor") }
	expander := func(string) string { return xs(9000) }

	// hook registers fn as an AfterDrain hook and calls Shutdown before Start.
	hook := func(fn func(context.Context) error) func(*testing.T, []srvr.Option) {
		return func(t *testing.T, opts []srvr.Option) {
			t.Helper()
			srv := newServer(t, append(opts, srvr.WithShutdownHook(srvr.AfterDrain, fn))...)
			_ = srv.Shutdown(context.Background())
		}
	}
	hookReturns := func(err error) func(*testing.T, []srvr.Option) {
		return hook(func(context.Context) error { return err })
	}
	hookPanics := hook(func(context.Context) error { panic("secret") })
	startDone := func(err error) func(*testing.T, []srvr.Option) {
		return func(t *testing.T, opts []srvr.Option) {
			t.Helper()
			srv := newServer(t, opts...)
			_ = srv.Start(newDoneContext(err))
		}
	}

	const hookFailed, failed = "server.shutdown_hook.failed", "server.failed"

	dbErr, stopErr := errors.New("close db: secret"), errors.New("stop: secret")

	tests := []struct {
		name     string
		redactor func(string) string
		failure  func(*testing.T, []srvr.Option)
		event    string
		message  string
	}{
		{"hook, no redactor", nil, hookReturns(dbErr), hookFailed, "close db: secret"},
		{"hook, masker", secretMasker, hookReturns(dbErr), hookFailed, "close db: ***"},
		{"hook, panicking redactor", panicky, hookReturns(dbErr), hookFailed, "[redaction failed]"},
		{"hook panics, masker", secretMasker, hookPanics, hookFailed, "panic: ***"},
		{"start, masker", secretMasker, startDone(stopErr), failed, "stop: ***"},
		{"start, panicking redactor", panicky, startDone(stopErr), failed, "[redaction failed]"},
		{"hook, 5000 bytes", nil, hookReturns(errors.New(xs(5000))), hookFailed, xs(5000)},
		{"hook, 9000 bytes", nil, hookReturns(errors.New(xs(9000))), hookFailed, xs(8192) + cut},
		{"hook, expanding redactor", expander, hookReturns(dbErr), hookFailed, xs(8192) + cut},
		{"start, 9000 bytes", nil, startDone(errors.New(xs(9000))), failed, xs(8192) + cut},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			out := &logBuffer{}
			opts := []srvr.Option{srvr.WithLogOutput(out)}
			if tt.redactor != nil {
				opts = append(opts, srvr.WithErrorRedactor(tt.redactor))
			}
			tt.failure(t, opts)

			recs := out.find(t, tt.event)
			if len(recs) != 1 {
				t.Fatalf("%d %s records, want 1:\n%s", len(recs), tt.event, out.String())
			}
			got, _ := recs[0]["exception.message"].(string)
			if got != tt.message {
				t.Fatalf("exception.message = %.80q (%d bytes), want %.80q (%d bytes)", got, len(got), tt.message, len(tt.message))
			}
		})
	}
}
