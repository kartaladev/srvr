package srvr_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

// TestNew_Config covers server-lifecycle / Scenario: Valid and invalid configuration.
func TestNew_Config(t *testing.T) {
	t.Parallel()

	ln := listen(t)

	hook := func(context.Context) error { return nil }

	tests := []struct {
		name    string
		opts    []srvr.Option
		message string // empty: a server is returned
	}{
		// accepted
		{name: "none"},
		{name: "Addr 127.0.0.1:0", opts: []srvr.Option{srvr.WithAddr("127.0.0.1:0")}},
		{name: "ReadHeaderTimeout -1s", opts: []srvr.Option{srvr.WithConfig(srvr.Config{ReadHeaderTimeout: -time.Second})}},
		{name: "MaxRequestBodyBytes -1", opts: []srvr.Option{srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: -1})}},
		{name: "WithListener", opts: []srvr.Option{srvr.WithListener(ln)}},
		{name: "shutdown hook", opts: []srvr.Option{srvr.WithShutdownHook(srvr.AfterDrain, hook)}},
		// rejected
		{name: "Addr port out of range", opts: []srvr.Option{srvr.WithAddr("localhost:99999")}, message: "Addr"},
		{name: "Addr not an address", opts: []srvr.Option{srvr.WithConfig(srvr.Config{Addr: "not an address"})}, message: "Addr"},
		{name: "ShutdownTimeout -1s", opts: []srvr.Option{srvr.WithConfig(srvr.Config{ShutdownTimeout: -time.Second})}, message: "ShutdownTimeout"},
		{name: "MaxHeaderBytes 0 via option", opts: []srvr.Option{srvr.WithMaxHeaderBytes(0)}, message: "MaxHeaderBytes"},
		{name: "WithListener nil", opts: []srvr.Option{srvr.WithListener(nil)}, message: "Listener"},
		{name: "WithListener and Addr", opts: []srvr.Option{srvr.WithListener(ln), srvr.WithAddr(":9090")}, message: "Listener"},
		{name: "WithListener typed nil", opts: []srvr.Option{srvr.WithListener((*net.TCPListener)(nil))}, message: "Listener"},
		{name: "WithListener typed nil and Addr", opts: []srvr.Option{srvr.WithListener((*net.TCPListener)(nil)), srvr.WithAddr(":8080")}, message: "Listener"},
		{
			name:    "WithListener with a panicking Addr and Addr",
			opts:    []srvr.Option{srvr.WithListener(addrListener{Listener: ln, addr: func() net.Addr { panic("addr") }}), srvr.WithAddr(":8080")},
			message: "Listener",
		},
		{name: "nil shutdown hook", opts: []srvr.Option{srvr.WithShutdownHook(srvr.BeforeDrain, nil)}, message: "ShutdownHook"},
		{name: "invalid shutdown phase", opts: []srvr.Option{srvr.WithShutdownHook(srvr.ShutdownPhase(9), hook)}, message: "ShutdownPhase"},
		{name: "MaxHeaderBytes negative via Config", opts: []srvr.Option{srvr.WithConfig(srvr.Config{MaxHeaderBytes: -1})}, message: "MaxHeaderBytes"},
		{name: "WithLogOutput nil", opts: []srvr.Option{srvr.WithLogOutput(nil)}, message: "Output"},
		{name: "WithLogLevelVar nil", opts: []srvr.Option{srvr.WithLogLevelVar(nil)}, message: "LevelVar"},
		{name: "nil option", opts: []srvr.Option{nil}, message: "Option"},
		{name: "WithLogger zero Logger", opts: []srvr.Option{srvr.WithLogger(&slog.Logger{})}, message: "Logger"},
		{name: "WithLogger typed-nil handler", opts: []srvr.Option{srvr.WithLogger(slog.New((*slog.JSONHandler)(nil)))}, message: "Logger"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, err := newRecovered(tt.opts...)
			if tt.message == "" {
				if err != nil || srv == nil {
					t.Fatalf("New = %v, %v; want a server", srv, err)
				}

				return
			}

			if srv != nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("New = %v, %v; want an error wrapping ErrConfig naming %q", srv, err, tt.message)
			}
		})
	}
}

// TestBodyLimit covers error-responses / Scenario: Body limit.
func TestBodyLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		limit  []srvr.Option
		size   int
		status int
	}{
		{nil, 4194304, http.StatusOK},
		{nil, 4194305, http.StatusRequestEntityTooLarge},
		{[]srvr.Option{srvr.WithMaxRequestBodyBytes(10)}, 10, http.StatusOK},
		{[]srvr.Option{srvr.WithMaxRequestBodyBytes(10)}, 11, http.StatusRequestEntityTooLarge},
		{[]srvr.Option{srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: -1})}, 8388608, http.StatusOK},
		{nil, 0, http.StatusOK},
	}

	for i, tt := range tests {
		t.Run(strconv.Itoa(i)+"/"+strconv.Itoa(tt.size), func(t *testing.T) {
			t.Parallel()

			srv := newServer(t, tt.limit...)
			err := srv.Register(configFunc(func(g *srvr.Group) {
				g.POST("/upload", func(_ http.ResponseWriter, r *http.Request) error {
					_, err := io.Copy(io.Discard, r.Body)

					return err
				})
			}))
			if err != nil {
				t.Fatal(err)
			}
			base := startServer(t, srv)

			res := do(t, http.MethodPost, base+"/upload", bytes.NewReader(make([]byte, tt.size)), nil)
			if res.status != tt.status {
				t.Fatalf("status = %d, want %d", res.status, tt.status)
			}
		})
	}
}

// TestCoreHasNoOTel guards the core module's dependency set: no package of
// the core, test-only or not, may import OpenTelemetry (design, Context).
func TestCoreHasNoOTel(t *testing.T) {
	t.Parallel()

	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", "-test", "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}

	for pkg := range strings.Lines(string(out)) {
		if strings.HasPrefix(pkg, "go.opentelemetry.io/") {
			t.Errorf("the core module imports %s", strings.TrimSpace(pkg))
		}
	}
}

// TestEndToEnd registers a route, starts the server, gets a problem for a
// failing handler and shuts the server down.
func TestEndToEnd(t *testing.T) {
	t.Parallel()

	srv := newServer(t)
	err := srv.Register(configFunc(func(g *srvr.Group) {
		g.POST("/orders", func(http.ResponseWriter, *http.Request) error {
			return errors.New("insert order: connection refused")
		})
	}))
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- srv.Start(t.Context()) }()
	waitReady(t, srv, done)

	res := do(t, http.MethodPost, "http://"+srv.Addr()+"/orders", nil, nil)

	if p := res.problem; res.status != 500 || p == nil || p.Status != 500 || p.Title != "Internal Server Error" || p.Detail != "" {
		t.Fatalf("got %d %+v, want a 500 problem without detail", res.status, p)
	}

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Start = %v", err)
	}
}

// TestEndToEnd_TraceCorrelation: the problem's trace_id equals the access
// record's, so one id links what the client saw to the log.
func TestEndToEnd_TraceCorrelation(t *testing.T) {
	t.Parallel()

	base, out := loggedServer(t, func(g *srvr.Group) {
		g.GET("/x", func(http.ResponseWriter, *http.Request) error { return errors.New("boom") })
	})
	res := do(t, http.MethodGet, base+"/x", nil, map[string]string{"traceparent": incoming})

	id, _ := res.problem.Ext("trace_id")
	rec := out.waitRecord(t, "http.request.failed")
	if rec["error.type"] != "*errors.errorString" || rec["exception.message"] != "boom" || rec["error.fingerprint"] == nil {
		t.Fatalf("record is not self-contained: %v", rec)
	}
	if id != incomingTrace || rec["trace_id"] != id {
		t.Fatalf("problem trace_id %v, record trace_id %v, want both %s", id, rec["trace_id"], incomingTrace)
	}
}
