package ginsrvr_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// renderer answers 429 with a test body.
var renderer = srvr.ErrorRendererFunc(func(context.Context, srvr.ErrorRequest, error) srvr.ErrorResponse {
	return srvr.ErrorResponse{Status: 429, Header: map[string][]string{"Content-Type": {"application/vnd.test+json"}}, Body: []byte(`{"code":"quota"}`)}
})

// TestWithErrorRenderer covers error-rendering / Scenario: Rejected
// renderer configuration, rows of this engine: until this engine renders
// through an ErrorRenderer, a valid one is rejected too.
func TestWithErrorRenderer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, reason string
		opts         []srvr.Option
	}{
		{"nil", "is nil", []srvr.Option{srvr.WithErrorRenderer(nil)}},
		{"problem options then renderer", "cannot be combined with ProblemOptions", []srvr.Option{srvr.WithProblemOptions(prblm.WithNamespace("https://errors.example/p")), srvr.WithErrorRenderer(renderer)}},
		{"renderer then empty problem options", "cannot be combined with ProblemOptions", []srvr.Option{srvr.WithErrorRenderer(renderer), srvr.WithProblemOptions()}},
		{"valid renderer", "not supported by this engine yet", []srvr.Option{srvr.WithErrorRenderer(renderer)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, err := ginsrvr.New(tt.opts...)
			ce, ok := errors.AsType[*srvr.ConfigError](err)
			if srv != nil || !ok || ce.Field != "ErrorRenderer" || ce.Reason != tt.reason {
				t.Fatalf("New = %v, %v; want a ConfigError for ErrorRenderer: %s", srv, err, tt.reason)
			}
		})
	}
}
