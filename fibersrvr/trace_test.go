package fibersrvr_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
)

// TestFiberTraceAccess covers fiber-engine / Scenario: Reading trace
// through fiber.Ctx.
func TestFiberTraceAccess(t *testing.T) {
	t.Parallel()

	const (
		traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		traceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	)

	tests := []struct {
		name   string
		read   func(c fiber.Ctx) (string, bool)
		logged bool
	}{
		{"TraceFromContext(c)", func(c fiber.Ctx) (string, bool) {
			tr, ok := srvr.TraceFromContext(c)

			return tr.TraceID, ok
		}, false},
		{"TraceFromContext(c.Context())", func(c fiber.Ctx) (string, bool) {
			tr, ok := srvr.TraceFromContext(c.Context())

			return tr.TraceID, ok
		}, false},
		{"LoggerFromContext(c.Context())", func(c fiber.Ctx) (string, bool) {
			srvr.LoggerFromContext(c.Context()).InfoContext(context.Background(), "handler.note")

			return "", true
		}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				gotID string
				gotOK bool
			)

			srv, logs := newServer(t)
			serve(t, srv, routes(func(r fiber.Router) {
				r.Get("/t", func(c fiber.Ctx) error {
					gotID, gotOK = tt.read(c)

					return c.SendStatus(http.StatusOK)
				})
			}), http.MethodGet, "/t", nil, map[string]string{"traceparent": traceparent})

			if tt.logged {
				notes := logs.withMsg(t, "handler.note")
				if len(notes) != 1 || notes[0]["trace_id"] != traceID {
					t.Fatalf("handler records %v, want one with trace_id %s", notes, traceID)
				}

				return
			}

			if !gotOK || gotID != traceID {
				t.Fatalf("trace = %q, %v; want %s, true", gotID, gotOK, traceID)
			}
		})
	}
}

// TestFiberForcedCancel covers fiber-engine / Scenario: Forced shutdown
// cancels handlers.
func TestFiberForcedCancel(t *testing.T) {
	t.Parallel()

	srv, _ := newServer(t)
	started, cancelled := make(chan struct{}), make(chan struct{})

	if err := srv.Register(routes(func(r fiber.Router) {
		r.Get("/block", func(c fiber.Ctx) error {
			close(started)
			<-c.Context().Done()
			close(cancelled)

			return nil
		})
	})); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- srv.Start(context.Background()) }()
	<-srv.Ready()

	go func() {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+srv.Addr()+"/block", nil)
		if res, err := http.DefaultClient.Do(req); err == nil {
			_ = res.Body.Close()
		}
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	begin := time.Now()
	if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want DeadlineExceeded", err)
	}
	if d := time.Since(begin); d > time.Second {
		t.Fatalf("Shutdown took %v", d)
	}

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("handler context was not cancelled")
	}

	if err := <-done; err != nil {
		t.Errorf("Start = %v", err)
	}
}
