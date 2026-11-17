package ginsrvr_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// selfPanicError's Error panics with itself, so its text can only be read
// guarded.
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

func (*hostileAPI) Configure(*gin.RouterGroup) { panic(selfPanicError{}) }

// panicHandler is a slog handler that panics in Enabled or Handle.
type panicHandler struct {
	slog.Handler

	inEnabled bool
}

func (h panicHandler) Enabled(context.Context, slog.Level) bool {
	if h.inEnabled {
		panic("enabled")
	}

	return true
}

func (h panicHandler) Handle(context.Context, slog.Record) error { panic("handle") }

// closePanicListener panics in Close.
type closePanicListener struct{ net.Listener }

func (l closePanicListener) Close() error {
	_ = l.Listener.Close()
	panic("close")
}

// TestGinConsumerFaults covers engine-support / Scenario: Consumer faults
// on every engine, gin rows (the route-check row is covered with route
// checks), plus a cyclic chain that reaches c.Errors by assignment.
func TestGinConsumerFaults(t *testing.T) {
	t.Parallel()

	// failed serves one request to a handler and returns the response and
	// the single access record.
	failed := func(t *testing.T, h gin.HandlerFunc, opts ...srvr.Option) (response, map[string]any) {
		t.Helper()

		srv, logs := newServer(t, opts...)
		if err := srv.Register(routes(func(g *gin.RouterGroup) { g.GET("/f", h) })); err != nil {
			t.Fatal(err)
		}

		res := do(t, http.MethodGet, start(t, srv)+"/f", nil, nil)

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
		if !errors.Is(err, srvr.ErrRouteConfig) || !strings.Contains(err.Error(), "<panic in Error(): ginsrvr_test.selfPanicError>") {
			t.Fatalf("Start = %v", err)
		}
		if err := srv.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
	})

	t.Run("WithEngine hook panics with a hostile value", func(t *testing.T) {
		t.Parallel()

		srv, err := ginsrvr.New(srvr.WithLogOutput(io.Discard), ginsrvr.WithEngine(func(*gin.Engine) { panic(selfPanicError{}) }))
		if srv != nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), "<panic in Error(): ginsrvr_test.selfPanicError>") {
			t.Fatalf("New = %v, %v", srv, err)
		}
	})

	t.Run("c.Error with a typed nil", func(t *testing.T) {
		t.Parallel()

		res, r := failed(t, func(c *gin.Context) {
			var e *MyError
			_ = c.Error(e)
		})
		if res.status != 500 || res.problem == nil || r["error.type"] != "*ginsrvr_test.MyError" || r["exception.message"] != "<nil *ginsrvr_test.MyError>" {
			t.Fatalf("response %d, record %v", res.status, r)
		}
	})

	t.Run("c.Error with a panicking Error", func(t *testing.T) {
		t.Parallel()

		res, r := failed(t, func(c *gin.Context) { _ = c.Error(selfPanicError{}) })
		if res.status != 500 || res.problem == nil || r["exception.message"] != "<panic in Error(): ginsrvr_test.selfPanicError>" {
			t.Fatalf("response %d, record %v", res.status, r)
		}
	})

	t.Run("handler panics with a hostile value", func(t *testing.T) {
		t.Parallel()

		res, r := failed(t, func(*gin.Context) { panic(selfPanicError{}) })
		if res.status != 500 || r["error.kind"] != "panic" || r["exception.message"] != "<panic in Error(): ginsrvr_test.selfPanicError>" {
			t.Fatalf("response %d, record %v", res.status, r)
		}
	})

	t.Run("redactor panics", func(t *testing.T) {
		t.Parallel()

		res, r := failed(t, func(c *gin.Context) { _ = c.Error(errors.New("boom")) },
			srvr.WithErrorRedactor(func(string) string { panic("redact") }))
		if res.status != 500 || r["exception.message"] != "[redaction failed]" {
			t.Fatalf("response %d, record %v", res.status, r)
		}
	})

	t.Run("cyclic chain assigned to c.Errors", func(t *testing.T) {
		t.Parallel()

		res, r := failed(t, func(c *gin.Context) {
			c.Errors = append(c.Errors, &gin.Error{Err: &cyclicError{}, Type: gin.ErrorTypePrivate})
		})
		if res.status != 500 || res.problem == nil || r["error.type"] != "*ginsrvr_test.cyclicError" {
			t.Fatalf("response %d %s, record %v", res.status, res.body, r)
		}
	})

	t.Run("log handler Enabled panics", func(t *testing.T) {
		t.Parallel()

		before := srvr.LogWriteErrors()

		srv, err := ginsrvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogHandler(panicHandler{inEnabled: true}))
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.Register(routes(func(g *gin.RouterGroup) { g.GET("/f", ok) })); err != nil {
			t.Fatal(err)
		}

		if res := do(t, http.MethodGet, start(t, srv)+"/f", nil, nil); res.status != 200 {
			t.Fatalf("status %d", res.status)
		}
		if srvr.LogWriteErrors() <= before {
			t.Fatal("LogWriteErrors did not increase")
		}
	})

	t.Run("listener Close panics", func(t *testing.T) {
		t.Parallel()

		ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		logs := &logBuffer{}
		srv, err := ginsrvr.New(srvr.WithListener(closePanicListener{ln}), srvr.WithLogOutput(logs))
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

	t.Run("configurer calls back into the server", func(t *testing.T) {
		t.Parallel()

		srv, _ := newServer(t)

		var (
			addr   string
			regErr error
		)

		if err := srv.Register(routes(func(g *gin.RouterGroup) {
			addr = srv.Addr()
			regErr = srv.Register(routes(func(*gin.RouterGroup) {}))
			g.GET("/f", ok)
		})); err != nil {
			t.Fatal(err)
		}

		start(t, srv)

		if addr != "" || !errors.Is(regErr, srvr.ErrServerStarted) {
			t.Fatalf("Addr = %q, Register = %v", addr, regErr)
		}
	})
}

// TestGinShutdownHooks_Reentrant covers engine-support / Scenario: Hooks
// per engine and stop path, the gin re-entrant row: an AfterDrain hook
// calling Shutdown neither deadlocks nor fails.
func TestGinShutdownHooks_Reentrant(t *testing.T) {
	t.Parallel()

	var srv *ginsrvr.Server

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
