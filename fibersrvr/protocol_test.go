package fibersrvr_test

import (
	"bufio"
	"context"
	"fmt"
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
