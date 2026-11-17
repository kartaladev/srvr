package engine

import (
	"errors"
	"testing"
)

// TestServedErr covers both outcomes of Serve returning: on its own (its
// error is Start's) and because the stop sequence closed the listener
// (ignored), deterministically rather than through timing.
func TestServedErr(t *testing.T) {
	t.Parallel()

	boom := errors.New("accept failed")

	tests := []struct {
		name     string
		stopping bool
		want     error
	}{
		{"serving failed on its own", false, boom},
		{"the stop closed the listener", true, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := &run{stopping: make(chan struct{})}
			if tt.stopping {
				close(r.stopping)
			}

			if got := r.servedErr(boom); !errors.Is(got, tt.want) || (tt.want == nil && got != nil) {
				t.Fatalf("servedErr = %v, want %v", got, tt.want)
			}
		})
	}
}
