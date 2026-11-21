package errchain

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/kartaladev/prblm"
)

// TestTypedErrors covers error-rendering / Scenario: Custom renderer on
// every engine, match column (design D3): srvr's own answers are srvr
// errors a renderer can match without prblm.
func TestTypedErrors(t *testing.T) {
	t.Parallel()

	tooLarge := &http.MaxBytesError{Limit: 10}
	canceled := fmt.Errorf("q: %w", context.Canceled)

	tests := []struct {
		name  string
		match func() bool
	}{
		{"ErrNotFound matches prblm.ErrNotFound", func() bool {
			return errors.Is(ErrNotFound, prblm.ErrNotFound)
		}},
		{"ErrNotFound is distinct, keeps v0.3.0 text and wraps nothing", func() bool {
			_, distinct := errors.AsType[notFoundError](ErrNotFound)

			return distinct && errors.Unwrap(ErrNotFound) == nil && ErrNotFound.Error() == prblm.ErrNotFound.Error()
		}},
		{"MethodNotAllowedError carries Allow", func() bool {
			var mna *MethodNotAllowedError
			err := fmt.Errorf("route: %w", &MethodNotAllowedError{Allow: []string{"GET", "HEAD"}})

			return errors.As(err, &mna) && slices.Equal(mna.Allow, []string{"GET", "HEAD"}) &&
				mna.Error() == "srvr: method not allowed"
		}},
		{"RequestTooLarge wraps ErrRequestTooLarge and *http.MaxBytesError", func() bool {
			var mbe *http.MaxBytesError
			err := RequestTooLarge(tooLarge)

			return errors.Is(err, ErrRequestTooLarge) && errors.As(err, &mbe) && mbe == tooLarge
		}},
		{"ClientClosed wraps ErrClientClosed and context.Canceled", func() bool {
			err := ClientClosed(canceled)

			return errors.Is(err, ErrClientClosed) && errors.Is(err, context.Canceled) && errors.Is(err, canceled)
		}},
		{"ErrHeadersTooLarge and ErrRequestTimeout are distinct", func() bool {
			return !errors.Is(ErrHeadersTooLarge, ErrRequestTimeout) && ErrHeadersTooLarge != nil && ErrRequestTimeout != nil
		}},
		{"RequestTooLarge(nil) and ClientClosed(nil) are nil", func() bool {
			return RequestTooLarge(nil) == nil && ClientClosed(nil) == nil
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !tt.match() {
				t.Fatal("no match")
			}
		})
	}
}
