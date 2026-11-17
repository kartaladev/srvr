package srvr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

			req := &request{}
			ctx := context.WithValue(context.Background(), requestKey{}, req)
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

		recordFailure(context.Background(), boom, sent) // must not panic
	})
}

// TestCorrelation_EmptyDerivations: With without attributes and
// WithGroup("") return the handler itself, as log/slog's handlers do.
func TestCorrelation_EmptyDerivations(t *testing.T) {
	t.Parallel()

	h := &logHandler{base: slog.NewJSONHandler(io.Discard, nil)}
	if h.WithAttrs(nil) != h || h.WithGroup("") != h {
		t.Fatal("empty derivation returned a new handler")
	}
}

// TestClientAddress keeps a RemoteAddr without a port as is.
func TestClientAddress(t *testing.T) {
	t.Parallel()

	for remote, want := range map[string]string{"127.0.0.1:5000": "127.0.0.1", "[::1]:5000": "::1", "@": "@"} {
		if got := clientAddress(remote); got != want {
			t.Errorf("clientAddress(%q) = %q, want %q", remote, got, want)
		}
	}
}

// TestFuncName names functions and returns "" for anything else.
func TestFuncName(t *testing.T) {
	t.Parallel()

	var nilFunc func()
	for _, tt := range []struct {
		fn   any
		want string
	}{
		{TestFuncName, "github.com/kartaladev/srvr.TestFuncName"},
		{nilFunc, ""},
		{"not a func", ""},
	} {
		if got := funcName(tt.fn); got != tt.want {
			t.Errorf("funcName(%T) = %q, want %q", tt.fn, got, tt.want)
		}
	}
}
