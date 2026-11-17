package ginsrvr_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// ginOutput collects everything gin writes through its package writers,
// so tests can check that srvr's pipeline makes gin print nothing.
var ginOutput = &logBuffer{}

func init() {
	gin.SetMode(gin.TestMode)
	gin.DefaultWriter = ginOutput
	gin.DefaultErrorWriter = ginOutput
}

// routes adapts a function to srvr.RouteConfigurer for gin.
type routes func(*gin.RouterGroup)

func (f routes) Configure(g *gin.RouterGroup) { f(g) }

// logBuffer collects log lines safely across goroutines.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

// records returns the decoded JSON records written so far.
func (b *logBuffer) records(t *testing.T) []map[string]any {
	t.Helper()

	b.mu.Lock()
	defer b.mu.Unlock()

	var out []map[string]any

	sc := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
	sc.Buffer(nil, 1<<20)

	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("log line %q: %v", sc.Text(), err)
		}

		out = append(out, m)
	}

	return out
}

// withMsg returns the records whose msg is msg.
func (b *logBuffer) withMsg(t *testing.T, msg string) []map[string]any {
	t.Helper()

	var out []map[string]any

	for _, r := range b.records(t) {
		if r["msg"] == msg {
			out = append(out, r)
		}
	}

	return out
}

// access returns the access records (completed or failed requests).
func (b *logBuffer) access(t *testing.T) []map[string]any {
	t.Helper()

	var out []map[string]any

	for _, r := range b.records(t) {
		if m, _ := r["msg"].(string); strings.HasPrefix(m, "http.request.") {
			out = append(out, r)
		}
	}

	return out
}

// newServer constructs a gin server on an ephemeral loopback port logging
// to a returned buffer; the address and output follow opts, so a
// srvr.WithConfig in opts does not reset them.
func newServer(t *testing.T, opts ...srvr.Option) (*ginsrvr.Server, *logBuffer) {
	t.Helper()

	logs := &logBuffer{}

	srv, err := ginsrvr.New(append(opts, srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(logs))...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return srv, logs
}

// start starts srv, waits until it is ready, stops it at cleanup and
// returns its base URL.
func start(t *testing.T, srv *ginsrvr.Server) string {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()

	wait := sync.OnceValue(func() error {
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			return errors.New("start did not return")
		}
	})

	t.Cleanup(func() {
		http.DefaultClient.CloseIdleConnections()
		cancel()
		if err := wait(); err != nil {
			t.Errorf("Start = %v at cleanup", err)
		}
	})

	select {
	case <-srv.Ready():
	case err := <-done:
		done <- err // for the cleanup
		t.Fatalf("Start returned before readiness: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("not ready")
	}

	return "http://" + srv.Addr()
}

// startErr starts srv and returns Start's result when it fails before
// readiness, failing the test when it becomes ready instead.
func startErr(t *testing.T, srv *ginsrvr.Server) error {
	t.Helper()

	done := make(chan error, 1)

	go func() { done <- srv.Start(context.Background()) }()

	select {
	case err := <-done:
		return err
	case <-srv.Ready():
		_ = srv.Shutdown(context.Background())
		t.Fatalf("Start became ready, want a failure; it returned %v", <-done)
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return")
	}

	return nil
}

// response is the part of an HTTP response a test asserts.
type response struct {
	status  int
	header  http.Header
	body    string
	problem *prblm.Problem
}

// do sends a request and reads the whole response.
func do(t *testing.T, method, url string, body io.Reader, header map[string]string) response {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, body)
	if err != nil {
		t.Fatal(err)
	}

	for k, v := range header {
		req.Header.Set(k, v)
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	out := response{status: res.StatusCode, header: res.Header, body: string(b)}

	res.Body = io.NopCloser(bytes.NewReader(b))
	if p, err := prblm.Parse(res); err == nil {
		out.problem = p
	}

	return out
}

func ok(c *gin.Context) { c.Status(http.StatusOK) }
