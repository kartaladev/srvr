package paritytest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// updateGolden rewrites the golden responses of TestParity_DefaultRendering
// from the code under test. The goldens were recorded at v0.3.0's
// behaviour (the base commit of pluggable-error-renderer) and must not be
// rewritten by a change that promises no behaviour change.
var updateGolden = flag.Bool("update-golden", false, "rewrite testdata/default_rendering.json")

// goldenPath holds the golden responses: engine name to row name to response.
const goldenPath = "testdata/default_rendering.json"

// goldenResponse is one recorded answer: status, headers but Date, and body.
type goldenResponse struct {
	Status int         `json:"status"`
	Header http.Header `json:"header"`
	Body   string      `json:"body"`
}

// errQuota is the consumer error of the mapper row.
var errQuota = errors.New("quota exceeded")

// renderCyclicError unwraps to itself.
type renderCyclicError struct{}

func (*renderCyclicError) Error() string   { return "cyclic" }
func (e *renderCyclicError) Unwrap() error { return e }

// renderRow is one Examples row of error-rendering / Scenario: Default
// rendering is unchanged, in every engine's idiom. A nil handler of an
// engine leaves the row out for it; fail is the error the handler reports,
// or panics with when panics is set.
type renderRow struct {
	name   string
	method string
	path   string
	body   []byte
	opts   []srvr.Option
	// route is the route's method and path; empty for no route.
	route, routeMethod string
	fail               func() error
	panics             bool
	// readBody makes the handler read the request body and report the error.
	readBody bool
	// static mounts the static fixture at /assets.
	static bool
	// clientCloses makes the client half-close its connection after the
	// request and the handler wait for the cancellation (net/http and gin).
	clientCloses bool
	// gin reports the error by assigning c.Errors instead of c.Error,
	// which never returns for a cyclic chain.
	ginAssign bool
}

var renderRows = []renderRow{
	{name: "handler returns prblm.ErrConflict", method: "GET", path: "/x", route: "/x", routeMethod: "GET", fail: func() error { return prblm.ErrConflict }},
	{name: "handler returns boom", method: "GET", path: "/x", route: "/x", routeMethod: "GET", fail: func() error { return errors.New("boom") }},
	{name: "handler panics", method: "GET", path: "/x", route: "/x", routeMethod: "GET", panics: true},
	{name: "no route", method: "GET", path: "/missing", route: "/x", routeMethod: "GET", fail: func() error { return nil }},
	{name: "method not allowed", method: "GET", path: "/x", route: "/x", routeMethod: "POST", fail: func() error { return nil }},
	{
		name: "body over limit", method: "POST", path: "/x", body: []byte("01234567890"), route: "/x", routeMethod: "POST",
		readBody: true, opts: []srvr.Option{srvr.WithMaxRequestBodyBytes(10)},
	},
	{name: "static miss", method: "GET", path: "/assets/missing.js", static: true},
	{name: "client closed", method: "GET", path: "/x", route: "/x", routeMethod: "GET", clientCloses: true, fail: func() error { return fmt.Errorf("q: %w", context.Canceled) }},
	{name: "cyclic chain", method: "GET", path: "/x", route: "/x", routeMethod: "GET", ginAssign: true, fail: func() error { return &renderCyclicError{} }},
	{
		name: "consumer mapper", method: "GET", path: "/x", route: "/x", routeMethod: "GET", fail: func() error { return errQuota },
		opts: []srvr.Option{srvr.WithProblemOptions(prblm.WithMappers(prblm.Status(errQuota, http.StatusTooManyRequests)))},
	},
}

// TestParity_DefaultRendering covers error-rendering / Scenario: Default
// rendering is unchanged: every row, on every engine, answers exactly the
// golden response recorded at v0.3.0 (status, headers but Date, body).
func TestParity_DefaultRendering(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		want map[string]map[string]goldenResponse
	)

	if *updateGolden {
		want = map[string]map[string]goldenResponse{}
		t.Cleanup(func() {
			b, err := json.MarshalIndent(want, "", "  ")
			must(t, err)
			must(t, os.WriteFile(filepath.FromSlash(goldenPath), append(b, '\n'), 0o644))
		})
	} else {
		b, err := os.ReadFile(filepath.FromSlash(goldenPath))
		must(t, err)
		must(t, json.Unmarshal(b, &want))
	}

	for _, row := range renderRows {
		for _, engine := range []string{"net/http", "gin", "fiber"} {
			if row.clientCloses && engine == "fiber" {
				continue // fiber's context is never cancelled by a disconnect
			}

			t.Run(engine+"/"+row.name, func(t *testing.T) {
				t.Parallel()

				g := sendRenderRow(t, startRenderRow(t, engine, row), row)

				mu.Lock()
				defer mu.Unlock()

				if *updateGolden {
					if want[engine] == nil {
						want[engine] = map[string]goldenResponse{}
					}
					want[engine][row.name] = g

					return
				}

				w, ok := want[engine][row.name]
				if !ok {
					t.Fatal("no golden response")
				}

				if g.Status != w.Status || g.Body != w.Body || !equalHeader(g.Header, w.Header) {
					t.Errorf("\n got %d %v %s\nwant %d %v %s", g.Status, g.Header, g.Body, w.Status, w.Header, w.Body)
				}
			})
		}
	}
}

// equalHeader compares headers, a nil and an empty one being equal.
func equalHeader(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}

	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}

	return true
}

// startRenderRow starts engine's server for row and returns its base URL.
func startRenderRow(t *testing.T, engine string, row renderRow) string {
	t.Helper()

	logs := &logBuffer{}
	fx := newStaticFixture(t)

	switch engine {
	case "net/http":
		srv := newNetServer(t, logs, func(g *srvr.Group) {
			if row.route != "" {
				g.Handle(row.routeMethod, row.route, func(_ http.ResponseWriter, r *http.Request) error {
					return row.outcome(r)
				})
			}
		}, row.opts...)
		if row.static {
			must(t, srv.Register(srvr.Static("/assets", fx.mapSite)))
		}

		return run(t, srv)
	case "gin":
		srv := newGinServer(t, logs, func(g *gin.RouterGroup) {
			if row.route != "" {
				g.Handle(row.routeMethod, row.route, func(c *gin.Context) {
					err := row.outcome(c.Request)
					switch {
					case err == nil:
						c.Status(http.StatusOK)
					case row.ginAssign:
						c.Errors = append(c.Errors, &gin.Error{Err: err, Type: gin.ErrorTypePrivate})
					default:
						_ = c.Error(err)
					}
				})
			}
		}, row.opts...)
		if row.static {
			must(t, srv.Register(ginsrvr.Static("/assets", fx.mapSite)))
		}

		return run(t, srv)
	default:
		srv := newFiberServer(t, logs, idleWorkerDuration, func(r fiber.Router) {
			if row.route != "" {
				r.Add([]string{row.routeMethod}, row.route, func(fiber.Ctx) error {
					if row.readBody {
						return nil // fasthttp rejects the body before routing
					}

					return row.outcome(nil)
				})
			}
		}, row.opts...)
		if row.static {
			must(t, srv.Register(fibersrvr.Static("/assets", fx.mapSite)))
		}

		return run(t, srv)
	}
}

// outcome runs the row's handler behaviour for r (nil on fiber): it panics,
// reads the body, waits for the client to go away, or returns fail's error.
func (row renderRow) outcome(r *http.Request) error {
	switch {
	case row.panics:
		panic("boom")
	case row.readBody:
		_, err := io.ReadAll(r.Body)

		return err
	case row.clientCloses:
		<-r.Context().Done()
	}

	return row.fail()
}

// sendRenderRow sends row's request over a raw connection, so a row whose
// client closes its side can still read the answer, and returns it.
func sendRenderRow(t *testing.T, base string, row renderRow) goldenResponse {
	t.Helper()

	conn := dial(t, base)

	var req bytes.Buffer
	fmt.Fprintf(&req, "%s %s HTTP/1.1\r\nHost: test\r\nTraceparent: %s\r\nConnection: close\r\n", row.method, row.path, traceparent)
	if row.body != nil {
		fmt.Fprintf(&req, "Content-Length: %d\r\n", len(row.body))
	}
	req.WriteString("\r\n")
	req.Write(row.body)

	_, err := conn.Write(req.Bytes())
	must(t, err)

	if row.clientCloses {
		tcp, ok := conn.(*net.TCPConn)
		if !ok {
			t.Fatalf("conn is %T, want *net.TCPConn", conn)
		}
		must(t, tcp.CloseWrite())
	}

	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	must(t, err)
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	must(t, err)

	res.Header.Del("Date")

	return goldenResponse{Status: res.StatusCode, Header: res.Header, Body: string(body)}
}
