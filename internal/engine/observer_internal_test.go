package engine

import (
	"context"
	"sync/atomic"
	"testing"
)

// countingObserver counts its Begin calls.
type countingObserver struct{ begun atomic.Int32 }

func (o *countingObserver) Begin(ctx context.Context, _ RequestInfo) (context.Context, func(ResultInfo)) {
	o.begun.Add(1)

	return ctx, func(ResultInfo) {}
}

// TestBeginObservers_Skipped: without request state or without observers,
// BeginObservers returns the context it got, calls no observer and keeps
// no end function.
func TestBeginObservers_Skipped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		state    bool
		observed bool
	}{
		{"no request state, an observer", false, true},
		{"request state, no observer", true, false},
		{"no request state, no observer", false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs := &countingObserver{}
			c := &Core{}
			if tt.observed {
				c.observers = guardObservers([]Observer{obs})
			}

			ctx := t.Context()
			req := &RequestState{}
			if tt.state {
				ctx = context.WithValue(ctx, stateKey{}, req)
			}

			if got := c.BeginObservers(ctx, RequestInfo{Method: "GET"}); got != ctx {
				t.Errorf("BeginObservers returned %v, want the context it got", got)
			}

			if n := obs.begun.Load(); n != 0 {
				t.Errorf("Begin ran %d times, want 0", n)
			}

			if req.ends != nil {
				t.Errorf("request state keeps %d end functions, want none", len(req.ends))
			}
		})
	}
}
