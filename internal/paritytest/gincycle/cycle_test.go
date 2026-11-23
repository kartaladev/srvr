package gincycle

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/goleak"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

func init() { gin.SetMode(gin.ReleaseMode) }

// routes adapts a function to srvr.RouteConfigurer for gin.
type routes func(*gin.RouterGroup)

func (f routes) Configure(g *gin.RouterGroup) { f(g) }

// cyclicError's chain is cyclic: Unwrap returns the error itself.
type cyclicError struct{}

func (*cyclicError) Error() string   { return "cyclic" }
func (e *cyclicError) Unwrap() error { return e }

// cyclicHandler reports a cyclic error chain through c.Error, which never
// returns in gin. It is a named function so leakOptions can ignore exactly
// its goroutine.
func cyclicHandler(c *gin.Context) { _ = c.Error(&cyclicError{}) }

// cyclicOnce sends the cyclic request once per test binary, so a single
// handler goroutine spins however many tests need it.
var cyclicOnce sync.Once

// sendCyclic runs requestCyclic with t unless an earlier test already did.
func sendCyclic(t *testing.T) {
	t.Helper()

	cyclicOnce.Do(func() { requestCyclic(t) })
}

// requestCyclic sends GET /x to a gin server whose handler is
// cyclicHandler and fails t unless no response arrives within 300ms. The
// server stops at t's cleanup after a 100ms drain, leaving the handler's
// goroutine stuck.
func requestCyclic(t *testing.T) {
	t.Helper()

	srv, err := ginsrvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard),
		srvr.WithShutdownTimeout(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	if err := srv.Register(routes(func(g *gin.RouterGroup) { g.GET("/x", cyclicHandler) })); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- srv.Run(context.Background()) }() //nolint:usetesting // Start's result is read in the cleanup; t.Context() would stop the server before the cleanup's Shutdown runs

	t.Cleanup(func() {
		// The drain deadline passes, because the handler never returns, so
		// both report it.
		_ = srv.Shutdown(context.Background()) //nolint:usetesting // runs in a cleanup, after t.Context() is cancelled
		if err := <-done; err != nil {
			t.Logf("Start = %v", err)
		}
	})

	select {
	case <-srv.Ready():
	case err := <-done:
		t.Fatalf("Start returned before readiness: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("not ready")
	}

	dt, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport is %T, want *http.Transport", http.DefaultTransport)
	}

	client := &http.Client{Transport: dt.Clone()}
	t.Cleanup(client.CloseIdleConnections)

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv.Addr()+"/x", nil)
	if res, err := client.Do(req); err == nil {
		_ = res.Body.Close()
		t.Fatalf("got %d, want no response", res.StatusCode)
	}
}

// TestParity_GinCyclicError covers the engine-parity / Scenario: Documented
// differences row "gin never completes c.Error with a cyclic chain".
func TestParity_GinCyclicError(t *testing.T) {
	t.Parallel()

	sendCyclic(t)
}

// TestLeakIgnores covers test-quality / Scenario: Each ignore is needed and
// narrow (gincycle row): once the cyclic request was sent, the check without
// leakOptions reports cyclicHandler and the check with them reports
// nothing. Not parallel: goleak.Find sees every goroutine of the process.
//
//nolint:paralleltest // goleak.Find sees every goroutine of the process
func TestLeakIgnores(t *testing.T) {
	t.Run("cyclic request", sendCyclic) // its cleanups stop the server

	if err := goleak.Find(); err == nil || !strings.Contains(err.Error(), cyclicHandlerName) {
		t.Fatalf("without leakOptions the check = %v, want a report naming %s", err, cyclicHandlerName)
	}

	if err := goleak.Find(leakOptions...); err != nil {
		t.Fatalf("with leakOptions the check reports: %v", err)
	}
}
