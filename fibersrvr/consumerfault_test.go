package fibersrvr_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
)

// selfPanicError's Error panics with itself.
type selfPanicError struct{}

func (selfPanicError) Error() string { panic(selfPanicError{}) }

// MyError is a consumer error type, used as a typed nil.
type MyError struct{}

func (*MyError) Error() string { return "my error" }

// cyclicError unwraps to itself.
type cyclicError struct{}

func (*cyclicError) Error() string   { return "cyclic" }
func (e *cyclicError) Unwrap() error { return e }

// hostileAPI panics in Configure with a selfPanicError.
type hostileAPI struct{}

func (*hostileAPI) Configure(fiber.Router) { panic(selfPanicError{}) }

// withAttrsPanicHandler panics in WithAttrs.
type withAttrsPanicHandler struct{ slog.Handler }

func (withAttrsPanicHandler) Enabled(context.Context, slog.Level) bool { return true }
func (withAttrsPanicHandler) WithAttrs([]slog.Attr) slog.Handler       { panic("with attrs") }

// faultyListener misbehaves in one method.
type faultyListener struct {
	net.Listener

	acceptPanics bool
	closeErr     bool
	nilAddr      bool
	closed       chan struct{}
}

func (l *faultyListener) Accept() (net.Conn, error) {
	if l.acceptPanics {
		panic("accept")
	}

	return l.Listener.Accept()
}

func (l *faultyListener) Close() error {
	_ = l.Listener.Close()
	if l.closed != nil {
		close(l.closed)
	}
	if l.closeErr {
		return errors.New("close failed")
	}

	return nil
}

func (l *faultyListener) Addr() net.Addr {
	if l.nilAddr {
		return nil
	}

	return l.Listener.Addr()
}

func loopback(t *testing.T) net.Listener {
	t.Helper()

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	return ln
}

// TestFiberConsumerFaults covers engine-support / Scenario: Consumer faults
// on every engine, fiber rows (the route-check row is covered with route
// checks).
func TestFiberConsumerFaults(t *testing.T) {
	t.Parallel()

	failed := func(t *testing.T, h fiber.Handler, opts ...srvr.Option) (response, map[string]any) {
		t.Helper()

		srv, logs := newServer(t, opts...)
		res := serve(t, srv, routes(func(r fiber.Router) { r.Get("/f", h) }), http.MethodGet, "/f", nil, nil)

		recs := logs.access(t)
		if len(recs) != 1 {
			t.Fatalf("%d access records, want 1", len(recs))
		}

		return res, recs[0]
	}

	t.Run("configurer panics with a hostile value", func(t *testing.T) {
		t.Parallel()

		srv, _ := newServer(t)
		if err := srv.Register(&hostileAPI{}); err != nil {
			t.Fatal(err)
		}

		err := startErr(t, srv)
		if !errors.Is(err, srvr.ErrRouteConfig) || !strings.Contains(err.Error(), "<panic in Error(): fibersrvr_test.selfPanicError>") {
			t.Fatalf("Start = %v", err)
		}
		if err := srv.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
	})

	t.Run("typed-nil error", func(t *testing.T) {
		t.Parallel()

		res, r := failed(t, func(fiber.Ctx) error {
			var e *MyError

			return e
		})
		if res.status != 500 || res.problem == nil || r["error.type"] != "*fibersrvr_test.MyError" || r["exception.message"] != "<nil *fibersrvr_test.MyError>" {
			t.Fatalf("response %d, record %v", res.status, r)
		}
	})

	t.Run("error whose Error panics", func(t *testing.T) {
		t.Parallel()

		res, r := failed(t, func(fiber.Ctx) error { return selfPanicError{} })
		if res.status != 500 || res.problem == nil || r["exception.message"] != "<panic in Error(): fibersrvr_test.selfPanicError>" {
			t.Fatalf("response %d, record %v", res.status, r)
		}
	})

	t.Run("cyclic error", func(t *testing.T) {
		t.Parallel()

		begin := time.Now()
		res, r := failed(t, func(fiber.Ctx) error { return &cyclicError{} })
		chain, _ := r["error.chain"].([]any)
		if res.status != 500 || res.problem == nil || time.Since(begin) > time.Second || len(chain) > 32 || r["srvr.error.truncated"] != true {
			t.Fatalf("response %d in %v, record %v", res.status, time.Since(begin), r)
		}
	})

	t.Run("panic with a hostile value", func(t *testing.T) {
		t.Parallel()

		res, r := failed(t, func(fiber.Ctx) error { panic(selfPanicError{}) })
		if res.status != 500 || r["error.kind"] != "panic" || r["exception.message"] != "<panic in Error(): fibersrvr_test.selfPanicError>" {
			t.Fatalf("response %d, record %v", res.status, r)
		}
	})

	t.Run("log handler WithAttrs panics", func(t *testing.T) {
		t.Parallel()

		before := srvr.LogWriteErrors()

		srv, err := fibersrvr.New(srvr.WithLogHandler(withAttrsPanicHandler{}))
		if err != nil {
			t.Fatal(err)
		}
		if res := serve(t, srv, routes(func(r fiber.Router) { r.Get("/f", ok) }), http.MethodGet, "/f", nil, nil); res.status != 200 {
			t.Fatalf("status %d", res.status)
		}
		if srvr.LogWriteErrors() <= before {
			t.Fatal("LogWriteErrors did not increase")
		}
	})

	t.Run("listener Accept panics", func(t *testing.T) {
		t.Parallel()

		srv, err := fibersrvr.New(srvr.WithListener(&faultyListener{Listener: loopback(t), acceptPanics: true}), srvr.WithLogOutput(&logBuffer{}))
		if err != nil {
			t.Fatal(err)
		}

		done := make(chan error, 1)
		go func() { done <- srv.Start(context.Background()) }()

		select {
		case err := <-done:
			if !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), "Listener") {
				t.Fatalf("Start = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Start did not return")
		}
	})

	t.Run("listener Close fails", func(t *testing.T) {
		t.Parallel()

		logs := &logBuffer{}
		srv, err := fibersrvr.New(srvr.WithListener(&faultyListener{Listener: loopback(t), closeErr: true}), srvr.WithLogOutput(logs))
		if err != nil {
			t.Fatal(err)
		}

		done := make(chan error, 1)
		go func() { done <- srv.Start(context.Background()) }()
		<-srv.Ready()

		if err := srv.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
		if err := <-done; err != nil {
			t.Fatalf("Start = %v", err)
		}
		if err := srv.Shutdown(context.Background()); err != nil {
			t.Fatalf("second Shutdown = %v", err)
		}

		stopped := logs.withMsg(t, "server.stopped")
		if len(stopped) != 1 || stopped[0]["srvr.drain.forced"] != nil {
			t.Fatalf("server.stopped %v", stopped)
		}
	})

	t.Run("listener with a nil address", func(t *testing.T) {
		t.Parallel()

		closed := make(chan struct{})
		srv, err := fibersrvr.New(srvr.WithListener(&faultyListener{Listener: loopback(t), nilAddr: true, closed: closed}), srvr.WithLogOutput(&logBuffer{}))
		if err != nil {
			t.Fatal(err)
		}

		if err := srv.Start(context.Background()); !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), "Listener") {
			t.Fatalf("Start = %v", err)
		}

		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("listener not closed")
		}
	})

	t.Run("configurer calls back into the server", func(t *testing.T) {
		t.Parallel()

		srv, _ := newServer(t)

		var (
			addr   string
			regErr error
		)

		if err := srv.Register(routes(func(r fiber.Router) {
			addr = srv.Addr()
			regErr = srv.Register(routes(func(fiber.Router) {}))
			r.Get("/f", ok)
		})); err != nil {
			t.Fatal(err)
		}

		start(t, srv)

		if addr != "" || !errors.Is(regErr, srvr.ErrServerStarted) {
			t.Fatalf("Addr = %q, Register = %v", addr, regErr)
		}
	})
}

// TestFiberShutdownHooks_Reentrant covers engine-support / Scenario: Hooks
// per engine and stop path, the fiber re-entrant row.
func TestFiberShutdownHooks_Reentrant(t *testing.T) {
	t.Parallel()

	var srv *fibersrvr.Server

	hookErr := make(chan error, 1)

	srv, _ = newServer(t,
		srvr.WithShutdownHookTimeout(200*time.Millisecond),
		srvr.WithShutdownHook(srvr.AfterDrain, func(context.Context) error {
			go func() { hookErr <- srv.Shutdown(context.Background()) }() //nolint:contextcheck // the row calls Shutdown with context.Background().

			return nil
		}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()
	<-srv.Ready()
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start deadlocked")
	}

	select {
	case err := <-hookErr:
		if err != nil {
			t.Fatalf("hook's Shutdown = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hook's Shutdown deadlocked")
	}
}
