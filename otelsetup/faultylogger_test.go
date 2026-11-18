package otelsetup_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kartaladev/srvr/otelsetup"
	"go.opentelemetry.io/otel"
)

// faultyHandler is a consumer slog.Handler that runs fault on its first
// record and records the messages of the later ones.
type faultyHandler struct {
	fault func()
	calls atomic.Int32

	mu  sync.Mutex
	got []string
}

func (*faultyHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *faultyHandler) WithAttrs([]slog.Attr) slog.Handler     { return h }
func (h *faultyHandler) WithGroup(string) slog.Handler          { return h }

func (h *faultyHandler) Handle(_ context.Context, r slog.Record) error {
	if h.calls.Add(1) == 1 {
		h.fault()

		return nil
	}

	var kind string

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "error.type" {
			kind = a.Value.String()
		}

		return true
	})

	h.mu.Lock()
	h.got = append(h.got, kind)
	h.mu.Unlock()

	return nil
}

// kindBError is an export error of a second kind.
type kindBError struct{}

func (kindBError) Error() string { return "b" }

// kindCError is an export error of a third kind.
type kindCError struct{}

func (kindCError) Error() string { return "c" }

// TestFaultyLogger covers telemetry-setup "Faulty logger", all rows. It
// sets the globals, so it is not parallel and resets them.
func TestFaultyLogger(t *testing.T) {
	resetGlobals(t)

	tests := []struct {
		name  string
		fault func()
		want  []string // error.type of the records after the faulty one
	}{
		{"panics", func() { panic("log handler boom") }, []string{"otelsetup_test.kindBError"}},
		// A lock held across the logger call would deadlock here.
		{"calls otel.Handle again", func() { otel.Handle(kindCError{}) }, []string{"otelsetup_test.kindCError", "otelsetup_test.kindBError"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &faultyHandler{fault: tt.fault}
			setUp(t, otelsetup.WithPrometheusConfig(otelsetup.PrometheusConfig{Enabled: true, DisableListener: true}), otelsetup.WithSetGlobal(), otelsetup.WithLogger(slog.New(h)))

			done := make(chan error, 1)

			go func() {
				done <- func() (err error) {
					defer func() {
						if v := recover(); v != nil {
							err = fmt.Errorf("panic escaped otel.Handle: %v", v)
						}
					}()

					otel.Handle(errors.New("export failed"))
					otel.Handle(kindBError{})

					return nil
				}()
			}()

			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("otel.Handle did not return: deadlock")
			}

			h.mu.Lock()
			defer h.mu.Unlock()

			if fmt.Sprint(h.got) != fmt.Sprint(tt.want) {
				t.Errorf("records after the faulty one = %v, want %v", h.got, tt.want)
			}
		})
	}
}
