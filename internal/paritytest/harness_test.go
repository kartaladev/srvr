package paritytest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

func init() { gin.SetMode(gin.ReleaseMode) }

// outcome is what the /items/{id} handler does, in every engine's idiom.
type outcome int

const (
	succeeds outcome = iota
	failsBoom
	failsNotFound
	failsSecret
	panicsBoom
	panicsAbort
)

// failure returns the error a handler reports for o, or nil.
func (o outcome) failure() error {
	switch o {
	case failsBoom:
		return errors.New("boom")
	case failsNotFound:
		return prblm.ErrNotFound
	case failsSecret:
		return errors.New("secret")
	default:
		return nil
	}
}

// panicValue returns the value a handler panics with for o, or nil.
func (o outcome) panicValue() any {
	switch o {
	case panicsBoom:
		return "boom"
	case panicsAbort:
		return http.ErrAbortHandler
	default:
		return nil
	}
}

// engineCase builds a server of one engine serving GET /items/{id} in its
// own syntax and returns its base URL, its log and the route template.
type engineCase struct {
	name  string
	route string
	start func(t *testing.T, o outcome, opts ...srvr.Option) (string, *logBuffer)
}

var engines = []engineCase{
	{"net/http", "/items/{id}", startNetHTTP},
	{"gin", "/items/:id", startGin},
	{"fiber", "/items/:id", startFiber},
}

type groupConfig func(*srvr.Group)

func (f groupConfig) Configure(g *srvr.Group) { f(g) }

type ginConfig func(*gin.RouterGroup)

func (f ginConfig) Configure(g *gin.RouterGroup) { f(g) }

type fiberConfig func(fiber.Router)

func (f fiberConfig) Configure(r fiber.Router) { f(r) }

// server is the lifecycle every engine shares.
type server interface {
	Start(ctx context.Context) error
	Ready() <-chan struct{}
	Addr() string
}

func options(logs *logBuffer, opts []srvr.Option) []srvr.Option {
	return append(opts, srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(logs))
}

// ginServer, fiberServer and netServer start one engine with conf.
func ginServer(t *testing.T, conf func(*gin.RouterGroup), opts ...srvr.Option) (string, *logBuffer) {
	t.Helper()

	logs := &logBuffer{}
	srv, err := ginsrvr.New(options(logs, opts)...)
	must(t, err)
	must(t, srv.Register(ginConfig(conf)))

	return run(t, srv), logs
}

// idleWorkerDuration is the fasthttp MaxIdleWorkerDuration of fiberServer's
// servers. fasthttp's idle-worker cleaner sleeps that long between checks
// of its stop channel (10s by default), so with this value it ends within
// goleak's retry window after Shutdown and the test binary needs no ignore
// for it.
const idleWorkerDuration = 20 * time.Millisecond

func fiberServer(t *testing.T, conf func(fiber.Router), opts ...srvr.Option) (string, *logBuffer) {
	t.Helper()

	return fiberServerIdle(t, idleWorkerDuration, conf, opts...)
}

// fiberServerIdle is fiberServer with fasthttp's MaxIdleWorkerDuration set
// to idle; 0 keeps fasthttp's default. Every fiber server of this module
// is built here, so none keeps the default cleaner by accident.
func fiberServerIdle(t *testing.T, idle time.Duration, conf func(fiber.Router), opts ...srvr.Option) (string, *logBuffer) {
	t.Helper()

	logs := &logBuffer{}
	srv, err := fibersrvr.New(options(logs, opts)...)
	must(t, err)
	// Set while configuring, before fasthttp's Serve reads the value.
	must(t, srv.Register(fiberConfig(func(r fiber.Router) {
		app, ok := r.(*fiber.App)
		if !ok {
			panic(fmt.Sprintf("paritytest: fiber configurer got %T, want *fiber.App to set MaxIdleWorkerDuration", r))
		}
		app.Server().MaxIdleWorkerDuration = idle
		conf(r)
	})))

	return run(t, srv), logs
}

func netServer(t *testing.T, conf func(*srvr.Group), opts ...srvr.Option) (string, *logBuffer) {
	t.Helper()

	logs := &logBuffer{}
	srv, err := srvr.New(options(logs, opts)...)
	must(t, err)
	must(t, srv.Register(groupConfig(conf)))

	return run(t, srv), logs
}

func startNetHTTP(t *testing.T, o outcome, opts ...srvr.Option) (string, *logBuffer) {
	t.Helper()

	return netServer(t, func(g *srvr.Group) {
		g.GET("/items/{id}", func(w http.ResponseWriter, _ *http.Request) error {
			if v := o.panicValue(); v != nil {
				panic(v)
			}
			if err := o.failure(); err != nil {
				return err
			}
			w.WriteHeader(http.StatusOK)

			return nil
		})
	}, opts...)
}

func startGin(t *testing.T, o outcome, opts ...srvr.Option) (string, *logBuffer) {
	t.Helper()

	return ginServer(t, func(g *gin.RouterGroup) {
		g.GET("/items/:id", func(c *gin.Context) {
			if v := o.panicValue(); v != nil {
				panic(v)
			}
			if err := o.failure(); err != nil {
				_ = c.Error(err)

				return
			}
			c.Status(http.StatusOK)
		})
	}, opts...)
}

func startFiber(t *testing.T, o outcome, opts ...srvr.Option) (string, *logBuffer) {
	t.Helper()

	return fiberServer(t, func(r fiber.Router) {
		r.Get("/items/:id", func(c fiber.Ctx) error {
			if v := o.panicValue(); v != nil {
				panic(v)
			}
			if err := o.failure(); err != nil {
				return err
			}

			c.Status(http.StatusOK)

			return nil
		})
	}, opts...)
}

// run starts srv until the test ends and returns its base URL.
func run(t *testing.T, srv server) string {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Logf("Start = %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Start did not return")
		}
	})

	select {
	case <-srv.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("not ready")
	}

	registerClient(t, srv.Addr()) // its cleanup runs before the one above

	return "http://" + srv.Addr()
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

// dial opens a TCP connection to the server at base.
func dial(t *testing.T, base string) net.Conn {
	t.Helper()

	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", strings.TrimPrefix(base, "http://"))
	must(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// send sends a request and returns the status, headers and body; err is
// a transport error (for example an aborted connection).
func send(t *testing.T, method, url string, header map[string]string) (*http.Response, []byte, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), method, url, nil)
	must(t, err)

	for k, v := range header {
		req.Header.Set(k, v)
	}

	res, err := clientFor(url).Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()

	b, err := io.ReadAll(res.Body)

	return res, b, err
}

// logBuffer collects JSON log lines safely across goroutines.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

// access returns the access records, waiting briefly for the first one.
func (b *logBuffer) access(t *testing.T) []map[string]any {
	t.Helper()

	return b.accessWithin(t, time.Second)
}

// accessWithin returns the access records, waiting up to wait for the
// first one.
func (b *logBuffer) accessWithin(t *testing.T, wait time.Duration) []map[string]any {
	t.Helper()

	for deadline := time.Now().Add(wait); ; time.Sleep(5 * time.Millisecond) {
		var out []map[string]any

		b.mu.Lock()
		sc := bufio.NewScanner(bytes.NewReader(b.buf.Bytes()))
		b.mu.Unlock()
		sc.Buffer(nil, 1<<20)

		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Fatalf("log line %q: %v", sc.Text(), err)
			}
			if msg, _ := m["msg"].(string); strings.HasPrefix(msg, "http.request.") {
				out = append(out, m)
			}
		}

		if len(out) > 0 || time.Now().After(deadline) {
			return out
		}
	}
}
