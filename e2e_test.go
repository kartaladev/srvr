package srvr_test

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

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
	go func() { done <- srv.Start(context.Background()) }()
	waitReady(t, srv, done)

	res := do(t, http.MethodPost, "http://"+srv.Addr()+"/orders", nil, nil)

	if p := res.problem; res.status != 500 || p == nil || p.Status != 500 || p.Title != "Internal Server Error" || p.Detail != "" {
		t.Fatalf("got %d %+v, want a 500 problem without detail", res.status, p)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
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
