package srvr_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// TestHandlerResult covers route-registration / Scenario: Handler result.
func TestHandlerResult(t *testing.T) {
	t.Parallel()

	tests := []struct {
		behaviour   string
		handler     srvr.HandlerFunc
		status      int
		contentType string
	}{
		{
			behaviour: "writes 200 JSON and returns nil",
			handler: func(w http.ResponseWriter, _ *http.Request) error {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"ok":true}`))

				return nil
			},
			status:      http.StatusOK,
			contentType: "application/json",
		},
		{
			behaviour:   "returns nil without writing",
			handler:     func(http.ResponseWriter, *http.Request) error { return nil },
			status:      http.StatusOK,
			contentType: "",
		},
		{
			behaviour:   "returns prblm.ErrNotFound",
			handler:     func(http.ResponseWriter, *http.Request) error { return prblm.ErrNotFound },
			status:      http.StatusNotFound,
			contentType: "application/problem+json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.behaviour, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			if err := srv.Register(configFunc(func(g *srvr.Group) { g.GET("/items", tt.handler) })); err != nil {
				t.Fatal(err)
			}
			base := startServer(t, srv)

			res := get(t, base+"/items")
			if res.status != tt.status {
				t.Fatalf("status = %d, want %d", res.status, tt.status)
			}
			if got := res.header.Get("Content-Type"); got != tt.contentType {
				t.Fatalf("Content-Type = %q, want %q", got, tt.contentType)
			}
		})
	}
}

// TestErrorToProblem covers error-responses / Scenario: Error to problem.
func TestErrorToProblem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		err         error
		accept      string
		status      int
		contentType string
		title       string
		hidden      string
	}{
		// mapped
		{"not found", prblm.ErrNotFound, "application/json", 404, "application/problem+json", "Not Found", ""},
		{"wrapped conflict", fmt.Errorf("item 7: %w", prblm.ErrConflict), "*/*", 409, "application/problem+json", "Conflict", "item 7"},
		{"status hint", prblm.WithStatus(errors.New("gone"), 410), "", 410, "application/problem+json", "Gone", "gone"},
		{"deadline", context.DeadlineExceeded, "application/json", 504, "application/problem+json", "Gateway Timeout", "deadline"},
		// unmapped and negotiated
		{"unmapped", errors.New("pq: password authentication failed for user app"), "application/json", 500, "application/problem+json", "Internal Server Error", "pq:"},
		{"xml", errors.New("x"), "application/xml", 500, "application/problem+xml", "Internal Server Error", "x</"},
		{"html", errors.New("x"), "text/html", 500, "application/problem+json", "Internal Server Error", `x"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			err := srv.Register(configFunc(func(g *srvr.Group) {
				g.GET("/items/{id}", func(http.ResponseWriter, *http.Request) error { return tt.err })
			}))
			if err != nil {
				t.Fatal(err)
			}
			base := startServer(t, srv)

			hdr := map[string]string{}
			if tt.accept != "" {
				hdr["Accept"] = tt.accept
			}
			res := do(t, http.MethodGet, base+"/items/7", nil, hdr)

			if res.status != tt.status {
				t.Fatalf("status = %d, want %d", res.status, tt.status)
			}
			if got := res.header.Get("Content-Type"); got != tt.contentType {
				t.Fatalf("Content-Type = %q, want %q", got, tt.contentType)
			}
			if res.problem == nil || res.problem.Title != tt.title || res.problem.Status != tt.status {
				t.Fatalf("problem = %+v, want title %q and status %d", res.problem, tt.title, tt.status)
			}
			if tt.hidden != "" && strings.Contains(res.body, tt.hidden) {
				t.Fatalf("body %q leaks %q", res.body, tt.hidden)
			}
		})
	}
}
