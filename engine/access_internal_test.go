package engine

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kartaladev/prblm"
)

// TestRecordFailure checks which hook reports become the request's failure.
func TestRecordFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	panicked := &prblm.PanicError{Value: "mapper"}
	sent := prblm.New(500)

	type report struct {
		err error
		p   *prblm.Problem
	}

	tests := []struct {
		name    string
		reports []report
		want    error // nil: no failure
	}{
		{"one error", []report{{boom, sent}}, boom},
		{"a note is not a failure", []report{{fmt.Errorf("%w: 200", prblm.ErrInvalidStatus), sent}, {boom, sent}}, boom},
		{"a panic without problem gives way to an error with one", []report{{panicked, nil}, {boom, sent}}, boom},
		{"a late panic alone stays", []report{{panicked, nil}}, panicked},
		{"the first error with a problem wins", []report{{boom, sent}, {panicked, nil}, {errors.New("later"), sent}}, boom},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := &RequestState{}
			ctx := context.WithValue(t.Context(), stateKey{}, req)
			for _, r := range tt.reports {
				recordFailure(ctx, r.err, r.p)
			}

			if req.failure == nil || !errors.Is(req.failure.err, tt.want) {
				t.Fatalf("failure = %+v, want %v", req.failure, tt.want)
			}
		})
	}

	t.Run("outside a request", func(t *testing.T) {
		t.Parallel()

		recordFailure(t.Context(), boom, sent) // must not panic
	})
}

// TestCorrelation_EmptyDerivations: With without attributes and
// WithGroup("") return the handler itself, as log/slog's handlers do.
func TestCorrelation_EmptyDerivations(t *testing.T) {
	t.Parallel()

	h := &logHandler{base: slog.DiscardHandler}
	if h.WithAttrs(nil) != h || h.WithGroup("") != h {
		t.Fatal("empty derivation returned a new handler")
	}
}

// TestClientAddress keeps a RemoteAddr without a port as is.
func TestClientAddress(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, remote, want string }{
		{"IPv4 with port", "127.0.0.1:5000", "127.0.0.1"},
		{"IPv6 with port", "[::1]:5000", "::1"},
		{"no port", "@", "@"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := clientAddress(tt.remote); got != tt.want {
				t.Errorf("clientAddress(%q) = %q, want %q", tt.remote, got, tt.want)
			}
		})
	}
}

// TestFuncName names functions and returns "" for anything else.
func TestFuncName(t *testing.T) {
	t.Parallel()

	var nilFunc func()

	tests := []struct {
		name string
		fn   any
		want string
	}{
		{"function", TestFuncName, "github.com/kartaladev/srvr/engine.TestFuncName"},
		{"nil function", nilFunc, ""},
		{"nil", nil, ""},
		{"not a function", "not a func", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := FuncLocation(tt.fn).Function(); got != tt.want {
				t.Errorf("FuncLocation(%T).Function() = %q, want %q", tt.fn, got, tt.want)
			}
		})
	}
}

// TestResponseRecorder_ReadFrom covers the sendfile path of the access
// recorder: ReadFrom before WriteHeader starts the response with 200 and
// counts the bytes copied.
func TestResponseRecorder_ReadFrom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		header     int // 0: ReadFrom first
		wantStatus int
	}{
		{"ReadFrom before WriteHeader", 0, http.StatusOK},
		{"ReadFrom after WriteHeader", http.StatusCreated, http.StatusCreated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := httptest.NewRecorder()
			rec := &responseRecorder{ResponseWriter: w}
			if tt.header != 0 {
				rec.WriteHeader(tt.header)
			}

			n, err := rec.ReadFrom(strings.NewReader("hello"))
			if err != nil || n != 5 {
				t.Fatalf("ReadFrom = %d, %v; want 5, nil", n, err)
			}
			if !rec.wroteHeader || rec.statusCode() != tt.wantStatus || rec.bytes != 5 {
				t.Fatalf("recorder started=%v status=%d bytes=%d; want true %d 5", rec.wroteHeader, rec.statusCode(), rec.bytes, tt.wantStatus)
			}
			if w.Code != tt.wantStatus || w.Body.String() != "hello" {
				t.Fatalf("response = %d %q", w.Code, w.Body.String())
			}
		})
	}
}

// TestAccessSuppressed: an engine asks whether a request's access record
// is suppressed before copying the values only the record reports; the
// answer is the access stage's own decision: completed requests on a skip
// path or with the access log disabled, never a failure or a 5xx.
func TestAccessSuppressed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		access AccessLogConfig
		path   string
		status int
		state  *RequestState // nil: a context without a request
		want   bool
	}{
		{"not skipped", AccessLogConfig{SkipPaths: []string{"/health"}}, "/users", 200, &RequestState{}, false},
		{"skip path", AccessLogConfig{SkipPaths: []string{"/health"}}, "/health", 200, &RequestState{}, true},
		{"skip path, 404", AccessLogConfig{SkipPaths: []string{"/health"}}, "/health", 404, &RequestState{}, true},
		{"skip path, 503", AccessLogConfig{SkipPaths: []string{"/health"}}, "/health", 503, &RequestState{}, false},
		{"access log disabled", AccessLogConfig{Disabled: true}, "/users", 200, &RequestState{}, true},
		{
			"failed request on a skip path",
			AccessLogConfig{SkipPaths: []string{"/health"}},
			"/health", 200,
			&RequestState{failure: &failure{err: errors.New("boom")}}, false,
		},
		{"aborted request, disabled", AccessLogConfig{Disabled: true}, "/users", 200, &RequestState{aborted: true}, false},
		{"no request in the context", AccessLogConfig{Disabled: true}, "/users", 200, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := bufferedCore(t, func(s *Settings) { s.Access = tt.access })

			ctx := t.Context()
			if tt.state != nil {
				ctx = context.WithValue(ctx, stateKey{}, tt.state)
			}

			if got := c.AccessSuppressed(ctx, tt.path, tt.status); got != tt.want {
				t.Fatalf("AccessSuppressed(%s, %d) = %v, want %v", tt.path, tt.status, got, tt.want)
			}
		})
	}
}
