package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/internal/errchain"
)

// goldenTraceparent is the traceparent the golden responses of
// internal/paritytest were recorded with.
const goldenTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

// renderGolden is one net/http golden response of internal/paritytest's
// TestParity_DefaultRendering.
type renderGolden struct {
	Status int         `json:"status"`
	Header http.Header `json:"header"`
	Body   string      `json:"body"`
}

// netHTTPGoldens reads the net/http golden responses, recorded at v0.3.0.
func netHTTPGoldens(t *testing.T) map[string]renderGolden {
	t.Helper()

	b, err := os.ReadFile(filepath.FromSlash("../paritytest/testdata/default_rendering.json"))
	if err != nil {
		t.Fatal(err)
	}

	var all map[string]map[string]renderGolden
	if err := json.Unmarshal(b, &all); err != nil {
		t.Fatal(err)
	}

	return all["net/http"]
}

// renderCycleError unwraps to itself.
type renderCycleError struct{}

func (*renderCycleError) Error() string   { return "cyclic" }
func (e *renderCycleError) Unwrap() error { return e }

// TestDefaultRenderer covers error-rendering / Scenario: Default rendering
// is unchanged, for the default renderer value (design D7): for every
// row, given the error srvr hands a renderer, it answers the net/http
// golden response, headers included, with the
// problem's type and title for logs and hooks.
func TestDefaultRenderer(t *testing.T) {
	t.Parallel()

	errQuota := errors.New("quota exceeded")
	goldens := netHTTPGoldens(t)

	tests := []struct {
		row          string
		method, path string
		err          error
		opts         []Option
		clientClosed bool
		title        string
	}{
		{"handler returns prblm.ErrConflict", "GET", "/x", prblm.ErrConflict, nil, false, "Conflict"},
		{"handler returns boom", "GET", "/x", errors.New("boom"), nil, false, "Internal Server Error"},
		{"handler panics", "GET", "/x", &prblm.PanicError{Value: "boom"}, nil, false, "Internal Server Error"},
		{"no route", "GET", "/missing", errchain.ErrNotFound, nil, false, "Not Found"},
		{"method not allowed", "GET", "/x", MethodNotAllowed("POST"), nil, false, "Method Not Allowed"},
		{"body over limit", "POST", "/x", errchain.RequestTooLarge(&http.MaxBytesError{Limit: 10}), nil, false, "Request Entity Too Large"},
		{"static miss", "GET", "/assets/missing.js", errchain.ErrNotFound, nil, false, "Not Found"},
		{"client closed", "GET", "/x", errchain.ClientClosed(fmt.Errorf("q: %w", context.Canceled)), nil, true, "Client Closed Request"},
		{"cyclic chain", "GET", "/x", GuardChain(&renderCycleError{}), nil, false, "Internal Server Error"},
		{
			"consumer mapper", "GET", "/x", errQuota,
			[]Option{func(s *Settings) {
				s.ProblemOptions = []prblm.Option{prblm.WithMappers(prblm.Status(errQuota, http.StatusTooManyRequests))}
			}},
			false, "Too Many Requests",
		},
	}

	for _, tt := range tests {
		t.Run(tt.row, func(t *testing.T) {
			t.Parallel()

			want, ok := goldens[tt.row]
			if !ok {
				t.Fatalf("no golden response for %q", tt.row)
			}

			c, err := ResolveConfig(tt.opts)
			if err != nil {
				t.Fatal(err)
			}

			ctx := c.BeginRequest(t.Context(), []string{goldenTraceparent})
			if tt.clientClosed {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			req := ErrorRequest{Method: tt.method, Path: tt.path, Header: httpHeader{}}
			got := DefaultRenderer.Render(ctx, req, tt.err)

			if got.Status != want.Status || string(got.Body) != want.Body || !maps.EqualFunc(got.Header, want.Header, slices.Equal) {
				t.Fatalf("got %d %v %s\nwant %d %v %s", got.Status, got.Header, got.Body, want.Status, want.Header, want.Body)
			}

			if got.Type != "about:blank" || got.Title != tt.title {
				t.Fatalf("type %q title %q, want about:blank %q", got.Type, got.Title, tt.title)
			}
		})
	}
}

// TestDefaultRenderer_Edges covers the default renderer outside the rows:
// a nil error answers nothing, a context without a request uses srvr's
// built-in problems, and a nil header reader is an empty one.
func TestDefaultRenderer_Edges(t *testing.T) {
	t.Parallel()

	t.Run("nil error", func(t *testing.T) {
		t.Parallel()

		if got := DefaultRenderer.Render(t.Context(), ErrorRequest{}, nil); got.Status != 0 || got.Body != nil {
			t.Fatalf("got %+v, want the zero response", got)
		}
	})

	t.Run("no request in context, nil header", func(t *testing.T) {
		t.Parallel()

		got := DefaultRenderer.Render(t.Context(), ErrorRequest{Method: "GET", Path: "/y"}, MethodNotAllowed("GET, HEAD"))
		if got.Status != http.StatusMethodNotAllowed || got.Header.Get("Allow") != "GET, HEAD" || got.Title != "Method Not Allowed" {
			t.Fatalf("got %d %v %q", got.Status, got.Header, got.Title)
		}

		if want := `{"title":"Method Not Allowed","status":405,"instance":"/y"}`; string(got.Body) != want {
			t.Fatalf("body %s, want %s", got.Body, want)
		}
	})

	t.Run("Accept selects XML", func(t *testing.T) {
		t.Parallel()

		got := DefaultRenderer.Render(t.Context(), ErrorRequest{Header: httpHeader{"Accept": {"application/xml"}}}, errchain.ErrNotFound)
		if ct := got.Header.Get("Content-Type"); ct != "application/problem+xml" {
			t.Fatalf("Content-Type %q, want application/problem+xml", ct)
		}
	})
}
