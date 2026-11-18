package fibersrvr_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
)

// TestFiberProtocolRejection covers fiber-engine / Scenario: Protocol
// rejection record, over a real listener: these answers come from
// fasthttp's error path, which app.Test does not reach.
func TestFiberProtocolRejection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opts   []srvr.Option
		send   func(t *testing.T, addr string) response
		status int
		title  string
	}{
		{"oversized body", nil, func(t *testing.T, addr string) response {
			return rawRequest(t, addr, http.MethodPost, "/upload", "", make([]byte, 4194305))
		}, 413, "Request Entity Too Large"},
		{"oversized headers", nil, func(t *testing.T, addr string) response {
			return rawRequest(t, addr, http.MethodPost, "/upload", "X-Pad: "+strings.Repeat("a", 70000)+"\r\n", nil)
		}, 431, "Request Header Fields Too Large"},
		{"stalled headers", []srvr.Option{srvr.WithReadTimeout(100 * time.Millisecond)}, stalledHeaders, 408, "Request Timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, logs := newServer(t, tt.opts...)
			if err := srv.Register(routes(func(r fiber.Router) { r.Post("/upload", ok) })); err != nil {
				t.Fatal(err)
			}

			start(t, srv)

			res := tt.send(t, srv.Addr())
			if res.status != tt.status || res.problem == nil || res.problem.Title != tt.title {
				t.Fatalf("response %d %s, want a %d problem titled %q", res.status, res.body, tt.status, tt.title)
			}

			var recs []map[string]any
			for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
				if recs = logs.access(t); len(recs) > 0 {
					break
				}
			}

			if len(recs) != 1 {
				t.Fatalf("%d access records, want 1: %v", len(recs), recs)
			}

			r := recs[0]
			problem, _ := r["problem"].(map[string]any)
			if r["msg"] != "http.request.failed" || r["level"] != "WARN" || problem["status"] != float64(tt.status) || problem["title"] != tt.title || r["http.route"] != nil {
				t.Fatalf("record %v", r)
			}
		})
	}
}

// stalledHeaders sends part of a request's headers and waits for the
// server's answer.
func stalledHeaders(t *testing.T, addr string) response {
	t.Helper()

	conn, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	_, _ = fmt.Fprintf(conn, "POST /upload HTTP/1.1\r\nHost: %s\r\n", addr)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer res.Body.Close()

	return parseResponse(t, res)
}

// TestFiberHeaderLimit covers fiber-engine / Scenario: Fiber header limit
// and the fiber rows of engine-support / Scenario: Configuration warning
// record, over a real listener (app.Test reports protocol rejections as
// transport errors).
func TestFiberHeaderLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		max      int // 0: default
		size     int
		buffer   int
		status   int
		warnings int
	}{
		{0, 60000, 65536, 200, 0},
		{0, 70000, 65536, 431, 0},
		{8192, 9000, 8192, 431, 0},
		{8192, 4000, 8192, 200, 0},
		{65536, 60000, 65536, 200, 0},
		{2097152, 70000, 65536, 431, 1},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("max %d size %d", tt.max, tt.size), func(t *testing.T) {
			t.Parallel()

			var opts []srvr.Option
			if tt.max != 0 {
				opts = append(opts, srvr.WithMaxHeaderBytes(tt.max))
			}

			srv, logs := newServer(t, opts...)
			if err := srv.Register(routes(func(r fiber.Router) { r.Get("/h", ok) })); err != nil {
				t.Fatal(err)
			}

			start(t, srv)

			if got := srv.App().Config().ReadBufferSize; got != tt.buffer {
				t.Fatalf("ReadBufferSize %d, want %d", got, tt.buffer)
			}

			res := rawRequest(t, srv.Addr(), http.MethodGet, "/h", "X-Pad: "+strings.Repeat("a", tt.size)+"\r\n", nil)
			if res.status != tt.status {
				t.Fatalf("status %d, want %d", res.status, tt.status)
			}

			warnings := 0
			for _, r := range logs.withMsg(t, "server.config.warning") {
				if r["srvr.warning.code"] == "fiber_header_limit_capped" {
					warnings++
				}
			}
			if warnings != tt.warnings {
				t.Fatalf("%d fiber_header_limit_capped warnings, want %d", warnings, tt.warnings)
			}
		})
	}
}

// blockingStream yields one chunk and then blocks until release closes,
// like a server-sent event stream.
type blockingStream struct {
	sent    bool
	release <-chan struct{}
}

func (s *blockingStream) Read(p []byte) (int, error) {
	if !s.sent {
		s.sent = true

		return copy(p, "data: one\n\n"), nil
	}

	<-s.release

	return 0, io.EOF
}

// TestFiberStreamNotBuffered: the access stage must not read a streamed
// body to measure it; the client gets the first chunk while the stream is
// still open.
func TestFiberStreamNotBuffered(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	defer close(release)

	srv, _ := newServer(t)
	if err := srv.Register(routes(func(r fiber.Router) {
		r.Get("/events", func(c fiber.Ctx) error {
			return c.SendStream(&blockingStream{release: release})
		})
	})); err != nil {
		t.Fatal(err)
	}

	base := start(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/events", nil)

	res, err := clientFor(req.URL.String()).Do(req)
	if err != nil {
		t.Fatalf("no response while the stream is open: %v", err)
	}
	defer res.Body.Close()

	buf := make([]byte, 11)
	if _, err := io.ReadFull(res.Body, buf); err != nil || string(buf) != "data: one\n\n" {
		t.Fatalf("first chunk %q, %v", buf, err)
	}
}

// TestFiberStreamThenError: an error after a body stream started keeps
// the stream, and the failed record says the response had started.
func TestFiberStreamThenError(t *testing.T) {
	t.Parallel()

	srv, logs := newServer(t)
	res := serve(t, srv, routes(func(r fiber.Router) {
		r.Get("/s", func(c fiber.Ctx) error {
			_ = c.SendStream(strings.NewReader("streamed"))

			return errors.New("late")
		})
	}), http.MethodGet, "/s", nil, nil)

	recs := logs.access(t)
	if res.body != "streamed" || len(recs) != 1 || recs[0]["srvr.response.committed"] != true {
		t.Fatalf("response %q, records %v", res.body, recs)
	}
}
