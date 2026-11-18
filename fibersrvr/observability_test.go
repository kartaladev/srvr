package fibersrvr_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"github.com/kartaladev/srvr/fibersrvr"
)

// TestFiberAccessRoute covers fiber-engine / Scenario: Fiber route
// attribute, with the route behind group middleware.
func TestFiberAccessRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method, path string
		msg, level   string
		route        any
		status       float64
	}{
		{http.MethodGet, "/users/42", "http.request.completed", "INFO", "/users/:id", 200},
		{http.MethodGet, "/nope", "http.request.failed", "WARN", nil, 404},
		{http.MethodDelete, "/users/42", "http.request.failed", "WARN", nil, 405},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			srv, logs := newServer(t)
			serve(t, srv, routes(func(r fiber.Router) {
				r.Group("/users", func(c fiber.Ctx) error { return c.Next() }).Get("/:id", ok)
			}), tt.method, tt.path, nil, nil)

			recs := logs.access(t)
			if len(recs) != 1 {
				t.Fatalf("%d access records, want 1", len(recs))
			}

			r := recs[0]
			if r["msg"] != tt.msg || r["level"] != tt.level || r["http.route"] != tt.route || r["http.response.status_code"] != tt.status {
				t.Fatalf("record %v, want msg %s level %s route %v status %v", r, tt.msg, tt.level, tt.route, tt.status)
			}
		})
	}
}

// TestFiberAccessLocation: a failed record names the route's handler, as
// on net/http and gin (triage-error-records / Scenario: Handler location).
func TestFiberAccessLocation(t *testing.T) {
	t.Parallel()

	srv, logs := newServer(t)
	serve(t, srv, routes(func(r fiber.Router) { r.Get("/f", failingHandler) }), http.MethodGet, "/f", nil, nil)

	recs := logs.access(t)
	if len(recs) != 1 || recs[0]["code.function.name"] != "github.com/kartaladev/srvr/fibersrvr_test.failingHandler" || recs[0]["code.line.number"] == nil {
		t.Fatalf("records %v, want the handler's location", recs)
	}
}

func failingHandler(fiber.Ctx) error { return errFailing }

var errFailing = errors.New("failing")

// recordingObserver records every Begin and end it sees, with the trace
// id at Begin.
type recordingObserver struct {
	mu      sync.Mutex
	begins  []begin
	results []srvr.ResultInfo
}

// begin is what the observer saw at Begin.
type begin struct{ method, traceID string }

func (o *recordingObserver) Begin(ctx context.Context, info srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
	tr, _ := srvr.TraceFromContext(ctx)

	o.mu.Lock()
	o.begins = append(o.begins, begin{info.Method, tr.TraceID})
	o.mu.Unlock()

	return ctx, func(res srvr.ResultInfo) {
		o.mu.Lock()
		defer o.mu.Unlock()

		o.results = append(o.results, res)
	}
}

func (o *recordingObserver) seen() ([]begin, []srvr.ResultInfo) {
	o.mu.Lock()
	defer o.mu.Unlock()

	return append([]begin(nil), o.begins...), append([]srvr.ResultInfo(nil), o.results...)
}

// TestObserver covers request-observer "Begin and end per engine" and
// "Observer view of the request", fiber rows, through app.Test.
func TestObserver(t *testing.T) {
	t.Parallel()

	const (
		incoming = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
		traceID  = "4bf92f3577b34da6a3ce929d0e0e4736"
	)

	tests := []struct {
		name    string
		handler fiber.Handler
		path    string
		header  map[string]string
		route   string
		status  int
		kind    srvr.ErrorKind
	}{
		{"writes 200", ok, "/users/42", map[string]string{"traceparent": incoming}, "/users/:id", 200, srvr.ErrorKindNone},
		{"ErrNotFound", func(fiber.Ctx) error { return prblm.ErrNotFound }, "/users/42", nil, "/users/:id", 404, srvr.ErrorKindError},
		{"boom", func(fiber.Ctx) error { return errors.New("boom") }, "/users/42", nil, "/users/:id", 500, srvr.ErrorKindError},
		{"panics", func(fiber.Ctx) error { panic("boom") }, "/users/42", nil, "/users/:id", 500, srvr.ErrorKindPanic},
		{"unmatched", ok, "/nope", nil, "", 404, srvr.ErrorKindError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs := &recordingObserver{}
			srv, _ := newServer(t, srvr.WithObserver(obs))
			conf := routes(func(r fiber.Router) { r.Get("/users/:id", tt.handler) })
			if got := serve(t, srv, conf, http.MethodGet, tt.path, nil, tt.header).status; got != tt.status {
				t.Fatalf("client status = %d, want %d", got, tt.status)
			}

			begins, results := obs.seen()
			if len(begins) != 1 || len(results) != 1 {
				t.Fatalf("Begin calls = %d, end calls = %d, want 1 and 1", len(begins), len(results))
			}

			continued := tt.header != nil
			if b := begins[0]; b.method != http.MethodGet || len(b.traceID) != 32 || (continued && b.traceID != traceID) {
				t.Errorf("Begin saw %+v", b)
			}

			if got := results[0]; got.Route != tt.route || got.Status != tt.status || got.ErrorKind != tt.kind {
				t.Errorf("end route, status, kind = %q, %d, %q, want %q, %d, %q", got.Route, got.Status, got.ErrorKind, tt.route, tt.status, tt.kind)
			}
		})
	}
}

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
		if res, err := clientFor(req.URL.String()).Do(req); err == nil {
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

// TestDefaultLoggerAfterConstruction covers engine-support / Scenario:
// Default logger after construction, fiber rows: a construction that fails
// leaves slog.Default() unchanged.
//
//nolint:paralleltest // slog.SetDefault is process-wide.
func TestDefaultLoggerAfterConstruction(t *testing.T) {
	tests := []struct {
		name    string
		opt     srvr.Option // nil adds nothing
		wantErr string      // "" when construction succeeds
	}{
		{"nothing else", nil, ""},
		{"a nil WithFiberConfig hook", fibersrvr.WithFiberConfig(nil), "WithFiberConfig"},
		{"a WithFiberConfig hook that panics", fibersrvr.WithFiberConfig(func(*fiber.Config) { panic("boom") }), "boom"},
		{"an option of another engine", engine.Only("gin", "ginsrvr.WithGinEngine", func() {}), "ginsrvr.WithGinEngine"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkGoroutines(t)

			before := slog.Default()
			t.Cleanup(func() { slog.SetDefault(before) })

			var buf bytes.Buffer

			opts := []srvr.Option{srvr.WithSetDefaultLogger(), srvr.WithLogHandler(slog.NewJSONHandler(&buf, nil))}
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}

			_, err := fibersrvr.New(opts...)

			wantOK := tt.wantErr == ""

			switch {
			case wantOK && err != nil:
				t.Fatalf("New = %v", err)
			case !wantOK && (!errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("New = %v, want an ErrConfig containing %s", err, tt.wantErr)
			}

			buf.Reset()
			slog.Info("probe")

			reached := strings.Contains(buf.String(), "probe")
			if reached != wantOK {
				t.Fatalf("a record through slog.Default() reached the server's handler: %v; New error: %v", reached, err)
			}
		})
	}
}
