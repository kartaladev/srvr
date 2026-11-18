package ginsrvr_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// TestGinErrors covers gin-engine / Scenario: Gin error to problem.
func TestGinErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		handler     gin.HandlerFunc
		status      int
		contentType string
		hidden      string
	}{
		{"JSON success", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"ok": true}) }, 200, "application/json; charset=utf-8", ""},
		{"c.Error(ErrNotFound)", func(c *gin.Context) { _ = c.Error(prblm.ErrNotFound) }, 404, "application/problem+json", ""},
		{"AbortWithError 409", func(c *gin.Context) { _ = c.AbortWithError(http.StatusConflict, errors.New("dup key alice")) }, 409, "application/problem+json", "alice"},
		{"unmapped error", func(c *gin.Context) { _ = c.Error(errors.New("pq: auth failed")) }, 500, "application/problem+json", "pq:"},
		{"AbortWithStatus 403", func(c *gin.Context) { c.AbortWithStatus(http.StatusForbidden) }, 403, "application/problem+json", ""},
		{"two errors, the last wins", func(c *gin.Context) {
			_ = c.Error(errors.New("err1"))
			_ = c.Error(prblm.ErrConflict)
		}, 409, "application/problem+json", ""},
		{"c.Error(nil) panics in gin", func(c *gin.Context) { _ = c.Error(nil) }, 500, "application/problem+json", "err is nil"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, _ := newServer(t)
			if err := srv.Register(routes(func(g *gin.RouterGroup) { g.GET("/items/:id", tt.handler) })); err != nil {
				t.Fatal(err)
			}

			res := do(t, http.MethodGet, start(t, srv)+"/items/7", nil, nil)

			if res.status != tt.status || res.header.Get("Content-Type") != tt.contentType {
				t.Fatalf("response %d %q %s, want %d %q", res.status, res.header.Get("Content-Type"), res.body, tt.status, tt.contentType)
			}
			if tt.hidden != "" && strings.Contains(res.body, tt.hidden) {
				t.Fatalf("body %s contains %q", res.body, tt.hidden)
			}
			if tt.status >= 400 {
				if res.problem == nil {
					t.Fatalf("no problem in %s", res.body)
				}
				if id, _ := res.problem.Ext("trace_id"); len(fmt.Sprint(id)) != 32 {
					t.Fatalf("problem %s has no trace_id", res.body)
				}
			}
		})
	}
}

// TestGinUnmatched covers gin-engine / Scenario: Gin no route or wrong
// method, with POST /users registered before GET /users.
func TestGinUnmatched(t *testing.T) {
	t.Parallel()

	srv, _ := newServer(t)
	if err := srv.Register(routes(func(g *gin.RouterGroup) {
		g.POST("/users", ok)
		g.GET("/users", ok)
	})); err != nil {
		t.Fatal(err)
	}

	base := start(t, srv)

	// The client must see gin's answer, not follow a redirect.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	tests := []struct {
		method, path string
		status       int
		allow        string
	}{
		{http.MethodGet, "/nope", http.StatusNotFound, ""},
		{http.MethodDelete, "/users", http.StatusMethodNotAllowed, "GET, POST"},
		{http.MethodPatch, "/nope", http.StatusNotFound, ""},
		{http.MethodGet, "/users/", http.StatusNotFound, ""},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequestWithContext(t.Context(), tt.method, base+tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}

			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = res.Body.Close()

			if res.StatusCode != tt.status || res.Header.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("response %d %q, want %d problem", res.StatusCode, res.Header.Get("Content-Type"), tt.status)
			}
			if got := res.Header.Get("Allow"); got != tt.allow {
				t.Fatalf("Allow = %q, want %q", got, tt.allow)
			}
			if loc := res.Header.Get("Location"); loc != "" {
				t.Fatalf("Location = %q, want none", loc)
			}
		})
	}
}

// TestGinPanic covers gin-engine / Scenario: Gin panic. The "boom" row
// also checks the failed record describes a panic, although ginprblm hands
// srvr's hook its own wrapper type rather than *prblm.PanicError.
func TestGinPanic(t *testing.T) {
	t.Parallel()

	t.Run("boom", func(t *testing.T) {
		t.Parallel()

		srv, logs := newServer(t)
		if err := srv.Register(routes(func(g *gin.RouterGroup) {
			g.GET("/items/:id", func(*gin.Context) { panic("boom") })
		})); err != nil {
			t.Fatal(err)
		}

		res := do(t, http.MethodGet, start(t, srv)+"/items/7", nil, nil)
		if res.status != http.StatusInternalServerError || res.problem == nil || strings.Contains(res.body, "boom") {
			t.Fatalf("response %d %s, want a 500 problem without boom", res.status, res.body)
		}

		recs := logs.access(t)
		if len(recs) != 1 {
			t.Fatalf("%d access records, want 1", len(recs))
		}

		r := recs[0]
		if r["msg"] != "http.request.failed" || r["level"] != "ERROR" || r["error.kind"] != "panic" ||
			r["exception.type"] != "string" || r["exception.stacktrace"] == nil || r["http.route"] != "/items/:id" {
			t.Fatalf("record %v, want a failed panic record", r)
		}

		assertNoGinOutput(t)
	})

	t.Run("http.ErrAbortHandler", func(t *testing.T) {
		t.Parallel()

		srv, _ := newServer(t)
		if err := srv.Register(routes(func(g *gin.RouterGroup) {
			g.GET("/items/:id", func(*gin.Context) { panic(http.ErrAbortHandler) })
		})); err != nil {
			t.Fatal(err)
		}

		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, start(t, srv)+"/items/7", nil)
		if err != nil {
			t.Fatal(err)
		}

		if res, err := clientFor(req.URL.String()).Do(req); err == nil {
			_ = res.Body.Close()
			t.Fatalf("got response %d, want an aborted connection", res.StatusCode)
		}

		assertNoGinOutput(t)
	})
}

// TestGinAbortRecord covers gin-engine / Scenario: Gin aborted request
// record.
func TestGinAbortRecord(t *testing.T) {
	t.Parallel()

	srv, logs := newServer(t)
	if err := srv.Register(routes(func(g *gin.RouterGroup) {
		g.GET("/items/:id", func(*gin.Context) { panic(http.ErrAbortHandler) })
	})); err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, start(t, srv)+"/items/7", nil)
	if err != nil {
		t.Fatal(err)
	}

	if res, err := clientFor(req.URL.String()).Do(req); err == nil {
		_ = res.Body.Close()
	}

	recs := logs.access(t)
	if len(recs) != 1 {
		t.Fatalf("%d access records, want 1", len(recs))
	}

	r := recs[0]

	for key, want := range map[string]any{
		"msg":                   "http.request.failed",
		"level":                 "WARN",
		"error.kind":            "panic",
		"error.type":            "panic",
		"srvr.response.aborted": true,
		"http.route":            "/items/:id",
	} {
		if r[key] != want {
			t.Errorf("%s = %v, want %v", key, r[key], want)
		}
	}

	if _, has := r["problem"]; has {
		t.Errorf("record has a problem group: %v", r["problem"])
	}
}

// assertNoGinOutput fails when gin wrote a logger or recovery line.
func assertNoGinOutput(t *testing.T) {
	t.Helper()

	ginOutput.mu.Lock()
	out := ginOutput.buf.String()
	ginOutput.mu.Unlock()

	if strings.Contains(out, "[GIN]") || strings.Contains(out, "[Recovery]") {
		t.Fatalf("gin wrote %q", out)
	}
}

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
		if err := srv.Shutdown(t.Context()); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
	})

	t.Run("WithGinEngine hook panics with a hostile value", func(t *testing.T) {
		t.Parallel()

		srv, err := ginsrvr.New(srvr.WithLogOutput(io.Discard), ginsrvr.WithGinEngine(func(*gin.Engine) { panic(selfPanicError{}) }))
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

		ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}

		logs := &logBuffer{}
		srv, err := ginsrvr.New(srvr.WithListener(closePanicListener{ln}), srvr.WithLogOutput(logs))
		if err != nil {
			t.Fatal(err)
		}

		done := make(chan error, 1)
		go func() { done <- srv.Start(t.Context()) }()
		<-srv.Ready()

		if err := srv.Shutdown(t.Context()); err != nil {
			t.Fatalf("Shutdown = %v", err)
		}
		if err := <-done; err != nil {
			t.Fatalf("Start = %v", err)
		}
		if err := srv.Shutdown(t.Context()); err != nil {
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
			go func() { hookErr <- srv.Shutdown(context.Background()) }() //nolint:contextcheck,usetesting // the row calls Shutdown with context.Background(), from a hook goroutine that may outlive the test body.

			return nil
		}),
	)

	ctx, cancel := context.WithCancel(t.Context())
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
