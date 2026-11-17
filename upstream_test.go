package srvr_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/kartaladev/prblm"
)

// TestUpstream_PrblmOnErrorComposes reproduces the prblm limitation srvr
// works around with WithErrorHook: prblm.OnError keeps a single hook, so of
// two OnError options only the last one runs. It is the RED step of the
// upstream fix (openspec/changes/core-server/notes.md, "Ask prblm for a
// composable OnError ...") and runs only with SRVR_UPSTREAM_TESTS=1, since
// it fails against prblm v0.1.0 by design.
func TestUpstream_PrblmOnErrorComposes(t *testing.T) {
	if os.Getenv("SRVR_UPSTREAM_TESTS") != "1" {
		t.Skip("upstream reproduction; set SRVR_UPSTREAM_TESTS=1 to run")
	}

	var first, second bool
	h := prblm.NewHandler(
		prblm.OnError(func(context.Context, error, *prblm.Problem) { first = true }),
		prblm.OnError(func(context.Context, error, *prblm.Problem) { second = true }),
	)

	h.Write(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), errors.New("boom"))

	if !first || !second {
		t.Fatalf("OnError hooks called: first=%v second=%v, want both", first, second)
	}
}
