package engine

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/internal/errchain"
)

// TestAnswerProblem covers the default problems of srvr's own answers
// (design D3, "Default problem" column), including those no
// net/http request produces: each typed error resolves to the problem
// srvr sent for that answer before the errors existed.
func TestAnswerProblem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
		title  string
		detail string
		allow  []string // nil: no Allow header
	}{
		{"405 with Allow", &errchain.MethodNotAllowedError{Allow: []string{"GET", "HEAD"}}, 405, "Method Not Allowed", "", []string{"GET, HEAD"}},
		{"405 wrapped", fmt.Errorf("w: %w", &errchain.MethodNotAllowedError{Allow: []string{"POST"}}), 405, "Method Not Allowed", "", []string{"POST"}},
		{"405 without methods has no Allow", &errchain.MethodNotAllowedError{}, 405, "Method Not Allowed", "", nil},
		{"413 wrapping *http.MaxBytesError", errchain.RequestTooLarge(&http.MaxBytesError{Limit: 1}), 413, "Request Entity Too Large", "The request body is too large.", nil},
		{"413 alone", errchain.ErrRequestTooLarge, 413, "Request Entity Too Large", "The request body is too large.", nil},
		{"431", errchain.ErrHeadersTooLarge, 431, "Request Header Fields Too Large", "", nil},
		{"408", errchain.ErrRequestTimeout, 408, "Request Timeout", "", nil},
		{"404", errchain.ErrNotFound, 404, "Not Found", "The requested resource was not found.", nil},
		{"another error is left to prblm", errors.New("boom"), 500, "Internal Server Error", "", nil},
	}

	h := prblm.NewHandler(prblm.WithMappers(answerMapper))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := h.Resolve(t.Context(), tt.err)
			if p.Status != tt.status || p.Title != tt.title || p.Detail != tt.detail || !slices.Equal(p.Header.Values("Allow"), tt.allow) {
				t.Fatalf("problem %d %q %q Allow %q, want %d %q %q Allow %q",
					p.Status, p.Title, p.Detail, p.Header.Values("Allow"), tt.status, tt.title, tt.detail, tt.allow)
			}
		})
	}
}

// TestMethodNotAllowed covers the 405 error built from an Allow header.
func TestMethodNotAllowed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		allow string
		want  []string
	}{
		{"GET, HEAD", []string{"GET", "HEAD"}},
		{"POST", []string{"POST"}},
		{"", nil},
	}

	for _, tt := range tests {
		t.Run(tt.allow, func(t *testing.T) {
			t.Parallel()

			mna, ok := errors.AsType[*errchain.MethodNotAllowedError](MethodNotAllowed(tt.allow))
			if !ok || !slices.Equal(mna.Allow, tt.want) {
				t.Fatalf("MethodNotAllowed(%q) = %v, want Allow %q", tt.allow, mna, tt.want)
			}
		})
	}
}
