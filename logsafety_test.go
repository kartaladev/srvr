package srvr_test

import (
	"errors"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// failingWriter fails every write with err, or writes short when err is nil.
type failingWriter struct{ err error }

func (w failingWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}

	return len(p) / 2, nil
}

// TestBrokenOutput covers structured-logging / Scenario: Broken output. It
// reads the process-wide counter, so it does not run in parallel with
// other tests that break their output.
//
//nolint:paralleltest // srvr.LogWriteErrors is process-wide.
func TestBrokenOutput(t *testing.T) {
	tests := []struct {
		fault string
		out   interface{ Write([]byte) (int, error) }
		count uint64
	}{
		{"EPIPE on every write", failingWriter{err: syscall.EPIPE}, 1},
		{"short write", failingWriter{}, 1},
		{"works", &logBuffer{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.fault, func(t *testing.T) {
			srv := newServer(t, srvr.WithLogOutput(tt.out))
			mustNil(t, srv.Register(okRoute("/ping")))
			base := startServer(t, srv)

			before := srvr.LogWriteErrors()
			if got := get(t, base+"/ping").status; got != http.StatusOK {
				t.Fatalf("status = %d, want 200", got)
			}
			eventually(t, func() bool { return srvr.LogWriteErrors()-before >= tt.count }, "write errors not counted")
			if got := srvr.LogWriteErrors() - before; got != tt.count {
				t.Fatalf("LogWriteErrors increased by %d, want %d", got, tt.count)
			}
		})
	}
}

// TestAccessLogConfig covers structured-logging / Scenario: Access log configuration.
func TestAccessLogConfig(t *testing.T) {
	t.Parallel()

	skip := srvr.AccessLogConfig{SkipPaths: []string{"/healthz"}}
	tests := []struct {
		name    string
		opts    []srvr.Option
		path    string
		status  int
		written bool
	}{
		{"none", nil, "/healthz", 200, true},
		{"skip path", []srvr.Option{srvr.WithAccessLogConfig(skip)}, "/healthz", 200, false},
		{"skip path returning 500", []srvr.Option{srvr.WithAccessLogConfig(skip)}, "/healthz", 500, true},
		{"skip path exact match", []srvr.Option{srvr.WithAccessLogConfig(skip)}, "/healthz/deep", 200, true},
		{"disabled", []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{Disabled: true})}, "/ping", 200, false},
		{"disabled returning 500", []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{Disabled: true})}, "/ping", 500, true},
		{"WithAccessLogSkipPaths", []srvr.Option{srvr.WithAccessLogSkipPaths("/healthz")}, "/healthz", 200, false},
		{"WithAccessLogDisabled", []srvr.Option{srvr.WithAccessLogDisabled()}, "/ping", 200, false},
		{"WithAccessLogDisabled then empty config", []srvr.Option{srvr.WithAccessLogDisabled(), srvr.WithAccessLogConfig(srvr.AccessLogConfig{})}, "/ping", 200, true},
		{"WithAccessLogSkipPaths then empty config", []srvr.Option{srvr.WithAccessLogSkipPaths("/healthz"), srvr.WithAccessLogConfig(srvr.AccessLogConfig{})}, "/healthz", 200, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) {
				for _, p := range []string{"/healthz", "/healthz/deep", "/ping", "/barrier"} {
					g.GET(p, func(w http.ResponseWriter, r *http.Request) error {
						if r.URL.Query().Get("fail") != "" {
							return errors.New("boom")
						}
						w.WriteHeader(http.StatusOK)

						return nil
					})
				}
			}, tt.opts...)

			url := base + tt.path
			if tt.status == 500 {
				url += "?fail=1"
			}
			get(t, url)
			// The next request on the same keep-alive connection is read only
			// after the previous one, access record included, has finished.
			get(t, base+"/barrier?fail=1")
			eventually(t, func() bool {
				for _, rec := range out.records(t) {
					if rec["http.route"] == "/barrier" {
						return true
					}
				}

				return false
			}, "no barrier record")

			written := false
			for _, rec := range out.records(t) {
				if rec["http.route"] == tt.path {
					written = true
				}
			}
			if written != tt.written {
				t.Fatalf("record written = %v, want %v:\n%s", written, tt.written, out.String())
			}
		})
	}
}

// TestRedaction covers structured-logging / Scenario: Redaction at every level.
func TestRedaction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		opts         []srvr.Option
		forbidden    string
		allowedKey   string
		allowedValue string // empty: only the key must be present
	}{
		{"INFO", nil, "s3cr3t", "http.route", "/users/{id}"},
		{"DEBUG authorization", []srvr.Option{srvr.WithLogLevel(-4)}, "abc.def", "http.route", ""},
		{"DEBUG cookie", []srvr.Option{srvr.WithLogLevel(-4)}, "sid=xyz", "http.route", ""},
		{"DEBUG path", []srvr.Option{srvr.WithLogLevel(-4)}, "alice@example.com", "http.route", ""},
		{"IncludePath", []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{IncludePath: true})}, "s3cr3t", "url.path", "/users/alice@example.com"},
		{"IncludeQuery", []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{IncludeQuery: true})}, "abc.def", "url.query", "token=s3cr3t"},
		{"IncludeClientAddress", []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{IncludeClientAddress: true})}, "sid=xyz", "client.address", "127.0.0.1"},
		{"WithAccessLogIncludePath", []srvr.Option{srvr.WithAccessLogIncludePath()}, "s3cr3t", "url.path", "/users/alice@example.com"},
		{"WithAccessLogIncludeQuery", []srvr.Option{srvr.WithAccessLogIncludeQuery()}, "abc.def", "url.query", "token=s3cr3t"},
		{"WithAccessLogIncludeClientAddress", []srvr.Option{srvr.WithAccessLogIncludeClientAddress()}, "sid=xyz", "client.address", "127.0.0.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, func(g *srvr.Group) {
				g.GET("/users/{id}", func(http.ResponseWriter, *http.Request) error { return errors.New("boom") })
			}, tt.opts...)
			do(t, http.MethodGet, base+"/users/alice@example.com?token=s3cr3t", nil, map[string]string{
				"Authorization": "Bearer abc.def",
				"Cookie":        "sid=xyz",
			})

			rec := out.waitRecord(t, "http.request.failed")
			if strings.Contains(out.String(), tt.forbidden) {
				t.Fatalf("log contains %q:\n%s", tt.forbidden, out.String())
			}
			if v, ok := rec[tt.allowedKey]; !ok || (tt.allowedValue != "" && v != tt.allowedValue) {
				t.Fatalf("%s = %v, want %q", tt.allowedKey, v, tt.allowedValue)
			}
		})
	}
}

// TestAccessLogSuppressed covers structured-logging / Scenario: Failures
// while the access log is suppressed: failed records are always written.
func TestAccessLogSuppressed(t *testing.T) {
	t.Parallel()

	routes := func(g *srvr.Group) {
		g.GET("/healthz", returning(prblm.ErrNotFound))
		g.GET("/ping", returning(prblm.ErrNotFound))
		g.GET("/late/panic", late(http.StatusOK, func() error { panic("late") }))
		g.GET("/late/error", late(http.StatusOK, func() error { return errors.New("late") }))
	}

	tests := []struct {
		name  string
		opts  []srvr.Option
		path  string
		level string
	}{
		{"skip path returning not found", []srvr.Option{srvr.WithAccessLogConfig(srvr.AccessLogConfig{SkipPaths: []string{"/healthz"}})}, "/healthz", "WARN"},
		{"disabled returning not found", []srvr.Option{srvr.WithAccessLogDisabled()}, "/ping", "WARN"},
		{"disabled with no route", []srvr.Option{srvr.WithAccessLogDisabled()}, "/nope", "WARN"},
		{"disabled with a late panic", []srvr.Option{srvr.WithAccessLogDisabled()}, "/late/panic", "ERROR"},
		{"disabled with a late error", []srvr.Option{srvr.WithAccessLogDisabled()}, "/late/error", "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, out := loggedServer(t, routes, tt.opts...)
			get(t, base+tt.path)

			if rec := out.waitRecord(t, "http.request.failed"); rec["level"] != tt.level {
				t.Fatalf("record = %v, want level %s", rec, tt.level)
			}
		})
	}
}
