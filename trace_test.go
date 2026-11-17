package srvr_test

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"sync"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

const (
	incoming      = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	incomingTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
)

var hexTraceID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// traceEcho registers GET /trace writing the request's trace id.
func traceEcho(g *srvr.Group) {
	g.GET("/trace", func(w http.ResponseWriter, r *http.Request) error {
		tr, _ := srvr.TraceFromContext(r.Context())
		_, err := io.WriteString(w, tr.TraceID)

		return err
	})
}

// TestTrace_Ignore covers trace-context / Scenario: Ignoring incoming context.
func TestTrace_Ignore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		opts      []srvr.Option
		continued bool
	}{
		{"none", nil, true},
		{"WithIgnoreIncomingTraceContext", []srvr.Option{srvr.WithIgnoreIncomingTraceContext()}, false},
		{"WithTraceConfig", []srvr.Option{srvr.WithTraceConfig(srvr.TraceConfig{IgnoreIncoming: true})}, false},
		{"WithIgnoreIncomingTraceContext then empty WithTraceConfig", []srvr.Option{srvr.WithIgnoreIncomingTraceContext(), srvr.WithTraceConfig(srvr.TraceConfig{})}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t, tt.opts...)
			mustNil(t, srv.Register(configFunc(traceEcho)))
			base := startServer(t, srv)

			got := do(t, http.MethodGet, base+"/trace", nil, map[string]string{"traceparent": incoming}).body
			if (got == incomingTrace) != tt.continued || !hexTraceID.MatchString(got) {
				t.Fatalf("trace id = %q, continued want %v", got, tt.continued)
			}
		})
	}
}

// TestTraceFromContext covers trace-context / Scenario: Reading the trace context.
func TestTraceFromContext(t *testing.T) {
	t.Parallel()

	t.Run("a request context handled by the server", func(t *testing.T) {
		t.Parallel()

		var (
			got Trace
			ok  bool
		)
		srv := newServer(t)
		mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
			g.GET("/x", func(http.ResponseWriter, *http.Request) error { return nil })
			g.GET("/read", func(_ http.ResponseWriter, r *http.Request) error {
				got, ok = srvr.TraceFromContext(r.Context())

				return nil
			})
		})))
		base := startServer(t, srv)
		do(t, http.MethodGet, base+"/read", nil, map[string]string{"traceparent": incoming})

		if !ok || got.TraceID != incomingTrace || len(got.SpanID) != 16 || got.ParentSpanID != "00f067aa0ba902b7" || !got.Sampled {
			t.Fatalf("TraceFromContext = %+v, %v", got, ok)
		}
	})

	for name, ctx := range map[string]context.Context{"background": context.Background(), "nil": nil} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tr, ok := srvr.TraceFromContext(ctx); ok || tr != (Trace{}) {
				t.Fatalf("TraceFromContext = %+v, %v; want zero, false", tr, ok)
			}
		})
	}
}

// Trace aliases srvr.Trace for brevity.
type Trace = srvr.Trace

// TestTrace_Unique covers trace-context / Scenario: Concurrent generation.
func TestTrace_Unique(t *testing.T) {
	t.Parallel()

	srv := newServer(t)
	mustNil(t, srv.Register(configFunc(traceEcho)))
	base := startServer(t, srv)

	const n = 1000
	var (
		mu  sync.Mutex
		ids = map[string]bool{}
		wg  sync.WaitGroup
	)
	const workers = 50
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: workers}}
	defer client.CloseIdleConnections()

	sem := make(chan struct{}, workers)
	for range n {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/trace", nil)
			res, err := client.Do(req)
			if err != nil {
				t.Error(err)

				return
			}
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			id := string(b)
			mu.Lock()
			ids[id] = true
			mu.Unlock()
		})
	}
	wg.Wait()

	if len(ids) != n {
		t.Fatalf("distinct trace ids = %d, want %d", len(ids), n)
	}
}

// TestProblemTraceID covers error-responses / Scenario: Trace id in the problem.
func TestProblemTraceID(t *testing.T) {
	t.Parallel()

	tests := []struct{ traceparent, want string }{
		{incoming, incomingTrace},
		{"", ""}, // a generated id
	}

	for _, tt := range tests {
		t.Run(tt.traceparent, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
				g.GET("/items/{id}", func(http.ResponseWriter, *http.Request) error { return prblm.ErrNotFound })
			})))
			base := startServer(t, srv)

			hdr := map[string]string{}
			if tt.traceparent != "" {
				hdr["traceparent"] = tt.traceparent
			}
			res := do(t, http.MethodGet, base+"/items/7?token=x", nil, hdr)
			if res.problem == nil {
				t.Fatalf("no problem in %q", res.body)
			}
			id, _ := res.problem.Ext("trace_id")
			s, _ := id.(string)
			if (tt.want != "" && s != tt.want) || !hexTraceID.MatchString(s) {
				t.Fatalf("trace_id = %v, want %q", id, tt.want)
			}
			if res.problem.Instance != "/items/7" {
				t.Fatalf("instance = %q, want /items/7", res.problem.Instance)
			}
		})
	}
}
