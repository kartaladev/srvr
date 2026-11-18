package ginsrvr_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

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

		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, start(t, srv)+"/items/7", nil)
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

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, start(t, srv)+"/items/7", nil)
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
