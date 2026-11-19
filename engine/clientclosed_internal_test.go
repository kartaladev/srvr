package engine

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/kartaladev/prblm"
)

// TestClientClosed covers the mapper behind error-responses / Scenario:
// Client closed request, including the contexts and chains a public test
// cannot build.
func TestClientClosed(t *testing.T) {
	t.Parallel()

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	timedOut, stop := context.WithDeadline(t.Context(), time.Unix(0, 0))
	t.Cleanup(stop)

	tests := []struct {
		name   string
		ctx    context.Context //nolint:containedctx // a table input
		err    error
		status int // 0: declined
	}{
		{"nil context", nil, context.Canceled, 0},
		{"live context", t.Context(), context.Canceled, 0},
		{"deadline exceeded (a server-side timeout), context.Canceled", timedOut, context.Canceled, 0},
		{"cancelled, context.Canceled", cancelled, context.Canceled, statusClientClosedRequest},
		{"cancelled, wrapped context.Canceled", cancelled, fmt.Errorf("q: %w", context.Canceled), statusClientClosedRequest},
		{"cancelled, joined with context.DeadlineExceeded", cancelled, errors.Join(context.Canceled, context.DeadlineExceeded), 0},
		{"cancelled, sentinel wrapping context.Canceled", cancelled, fmt.Errorf("%w: %w", prblm.ErrUnavailable, context.Canceled), 0},
		{"cancelled, status hint 409", cancelled, prblm.WithStatus(context.Canceled, http.StatusConflict), 0},
		{"cancelled, status hint 500 (indistinguishable from the fallback)", cancelled, prblm.WithStatus(context.Canceled, 500), statusClientClosedRequest},
		{"cancelled, another error", cancelled, errors.New("boom"), 0},
		{"cancelled, nil error", cancelled, nil, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := clientClosed(tt.ctx, tt.err)
			switch {
			case tt.status == 0 && p != nil:
				t.Fatalf("problem %+v, want declined", p)
			case tt.status != 0 && (p == nil || p.Status != tt.status || p.Title != "Client Closed Request" || p.Detail != ""):
				t.Fatalf("problem %+v, want status %d titled Client Closed Request without detail", p, tt.status)
			}
		})
	}
}

// TestClientClosed_ProbeFallback guards the probe's assumption: an error
// that prblm's built-ins and hints do not recognise resolves to 500, the
// status clientClosed reads as "nothing decided". If prblm changes its
// fallback, this fails before every client-closed rule silently declines.
func TestClientClosed_ProbeFallback(t *testing.T) {
	t.Parallel()

	if got := builtinResolver.Resolve(t.Context(), context.Canceled).Status; got != http.StatusInternalServerError {
		t.Fatalf("prblm fallback status %d, want 500", got)
	}
}
