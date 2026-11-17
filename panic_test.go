package srvr_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// TestPanicRecovery covers error-responses / Scenario: Recovered panic.
func TestPanicRecovery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   any
		aborted bool
		hidden  string
	}{
		{name: "string", value: "boom", hidden: "boom"},
		{name: "error", value: errors.New("db down"), hidden: "db down"},
		{name: "nil", value: nil},
		{name: "http.ErrAbortHandler", value: http.ErrAbortHandler, aborted: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
				g.GET("/panic", func(http.ResponseWriter, *http.Request) error { panic(tt.value) })
				g.GET("/ok", status(http.StatusOK))
			})))
			base := startServer(t, srv)

			if tt.aborted {
				req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/panic", nil)
				if res, err := http.DefaultClient.Do(req); err == nil {
					res.Body.Close()
					t.Fatalf("got a response %d, want an aborted connection", res.StatusCode)
				}
			} else {
				got := do(t, http.MethodGet, base+"/panic", nil, nil)
				if got.status != http.StatusInternalServerError || got.problem == nil || got.problem.Title != "Internal Server Error" {
					t.Fatalf("got %d %q, want a 500 problem", got.status, got.body)
				}
				if tt.hidden != "" && strings.Contains(got.body, tt.hidden) {
					t.Fatalf("body %q leaks %q", got.body, tt.hidden)
				}
				if strings.Contains(got.body, "goroutine") || strings.Contains(got.body, ".go:") {
					t.Fatalf("body %q leaks a stack trace", got.body)
				}
			}

			if got := get(t, base+"/ok").status; got != http.StatusOK {
				t.Fatalf("next request = %d, want 200", got)
			}
		})
	}
}

// TestLateError covers error-responses / Scenario: Late error, the client
// response and the access record.
func TestLateError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		written int
		fail    func() error
		level   string
	}{
		{"200 then error", http.StatusOK, func() error { return errors.New("late") }, "ERROR"},
		{"200 then panic", http.StatusOK, func() error { panic("late") }, "ERROR"},
		{"201 then conflict", http.StatusCreated, func() error { return prblm.ErrConflict }, "WARN"},
		{"500 then conflict", http.StatusInternalServerError, func() error { return prblm.ErrConflict }, "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) {
				g.GET("/late", late(tt.written, tt.fail))
			})

			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, base+"/late", nil)
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			body := make([]byte, 7)
			n, _ := res.Body.Read(body)
			if res.StatusCode != tt.written || !strings.HasPrefix(string(body[:n]), "partial") {
				t.Fatalf("got %d %q, want %d starting with partial", res.StatusCode, body[:n], tt.written)
			}

			rec := out.waitRecord(t, "http.request.failed")
			if _, hasProblem := rec["problem"]; hasProblem || rec["srvr.response.committed"] != true || rec["level"] != tt.level {
				t.Fatalf("record = %v, want level %s, srvr.response.committed and no problem", rec, tt.level)
			}
		})
	}
}
