package srvr_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kartaladev/srvr"
)

// TestUnmatched covers error-responses / Scenario: No route or wrong method.
func TestUnmatched(t *testing.T) {
	t.Parallel()

	srv := newServer(t)
	mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
		g.GET("/users", status(http.StatusOK))
		g.POST("/users", status(http.StatusOK))
	})))
	base := startServer(t, srv)

	tests := []struct {
		method, path string
		status       int
		allow        string
	}{
		{http.MethodGet, "/nope", 404, ""},
		{http.MethodDelete, "/users", 405, "GET, HEAD, POST"},
		{http.MethodGet, "/users/", 404, ""},
		{http.MethodPatch, "/nope", 404, ""},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			res := do(t, tt.method, base+tt.path, nil, nil)
			if res.status != tt.status || res.header.Get("Content-Type") != "application/problem+json" {
				t.Fatalf("got %d %q, want %d application/problem+json", res.status, res.header.Get("Content-Type"), tt.status)
			}
			if res.problem == nil || res.problem.Status != tt.status {
				t.Fatalf("problem = %+v", res.problem)
			}
			if got := res.header.Get("Allow"); got != tt.allow {
				t.Fatalf("Allow = %q, want %q", got, tt.allow)
			}
		})
	}
}

// TestUnmatched_Redirect: ServeMux redirects for non-canonical paths pass
// through unchanged; only 404 and 405 become problems.
func TestUnmatched_Redirect(t *testing.T) {
	t.Parallel()

	srv := newServer(t)
	mustNil(t, srv.Register(okRoute("/dir/")))
	base := startServer(t, srv)

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for p, want := range map[string]string{"/dir": "/dir/", "/a/../dir/": "/dir/", "//nope": "/nope"} {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+p, nil)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode < 300 || res.StatusCode > 399 || !strings.HasSuffix(res.Header.Get("Location"), want) {
			t.Fatalf("GET %s = %d Location %q, want a redirect to %s", p, res.StatusCode, res.Header.Get("Location"), want)
		}
	}
}
