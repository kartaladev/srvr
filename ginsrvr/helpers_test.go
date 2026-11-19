package ginsrvr_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
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

	// Not t.Context(): that is cancelled before the cleanups run, which
	// would stop srv before registerClient's cleanup closes idle connections
	// and before the cleanup below checks Start's result.
	ctx, cancel := context.WithCancel(context.Background()) //nolint:usetesting // Start's context must outlive the test body until its cleanup.
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

	registerClient(t, srv.Addr()) // its cleanup runs before the one above

	return "http://" + srv.Addr()
}

// startErr starts srv and returns Start's result when it fails before
// readiness, failing the test when it becomes ready instead.
func startErr(t *testing.T, srv *ginsrvr.Server) error {
	t.Helper()

	done := make(chan error, 1)

	go func() { done <- srv.Start(t.Context()) }()

	select {
	case err := <-done:
		return err
	case <-srv.Ready():
		_ = srv.Shutdown(t.Context())
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

	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	if err != nil {
		t.Fatal(err)
	}

	for k, v := range header {
		req.Header.Set(k, v)
	}

	res, err := clientFor(url).Do(req)
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
	if r, err := prblm.Parse(res); err == nil {
		out.problem = &r.Problem
	}

	return out
}

func ok(c *gin.Context) { c.Status(http.StatusOK) }

// clients holds the HTTP client of every server the helpers started, by
// address. Each server has its own, so a test's cleanup closes only its
// own idle connections: closing those of http.DefaultClient, shared by
// parallel tests, broke another test's request in flight ("http:
// CloseIdleConnections called").
var clients sync.Map // string -> *http.Client

// registerClient gives the server at addr a client of its own; when the
// test ends, before the server stops (cleanups run last-in first-out), it
// closes that client's idle connections, which would otherwise hold the
// drain for up to 5s.
func registerClient(t *testing.T, addr string) {
	t.Helper()

	dt, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("http.DefaultTransport is %T, want *http.Transport", http.DefaultTransport)
	}

	tr := dt.Clone()
	tr.MaxIdleConnsPerHost = 16 // parallel requests reuse connections instead of exhausting ports
	c := &http.Client{Transport: tr}
	clients.Store(addr, c)

	t.Cleanup(func() {
		c.CloseIdleConnections()
		clients.CompareAndDelete(addr, c)
	})
}

// clientFor returns the client of the server rawURL points at, or
// http.DefaultClient for a server the helpers did not start.
func clientFor(rawURL string) *http.Client {
	if u, err := url.Parse(rawURL); err == nil {
		if c, ok := clients.Load(u.Host); ok {
			if hc, ok := c.(*http.Client); ok {
				return hc
			}
		}
	}

	return http.DefaultClient
}

// mustNil fails the test when err is not nil.
func mustNil(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// String returns everything written so far, for failure messages.
func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// eventually polls cond until it holds, failing with msg after a second.
func eventually(t *testing.T, cond func() bool, msg string) {
	t.Helper()

	for deadline := time.Now().Add(time.Second); !cond(); time.Sleep(time.Millisecond) {
		if !time.Now().Before(deadline) {
			t.Fatal(msg)
		}
	}
}

// duplicateWindow is how long waitRecord watches for a second record.
const duplicateWindow = 10 * time.Millisecond

// waitRecord waits for the one record with msg: access records are
// written as the response finishes, which can be just after the client
// has it. It fails when a second one appears within duplicateWindow.
func (b *logBuffer) waitRecord(t *testing.T, msg string) map[string]any {
	t.Helper()

	var got []map[string]any
	eventually(t, func() bool { got = b.withMsg(t, msg); return len(got) > 0 }, "no "+msg+" record in:\n"+b.String())

	for quiet := time.Now().Add(duplicateWindow); ; time.Sleep(time.Millisecond) {
		if got = b.withMsg(t, msg); len(got) != 1 {
			t.Fatalf("%d %s records, want 1:\n%s", len(got), msg, b.String())
		}

		if !time.Now().Before(quiet) {
			return got[0]
		}
	}
}

// get sends a GET request without headers.
func get(t *testing.T, url string) response {
	t.Helper()

	return do(t, http.MethodGet, url, nil, nil)
}
