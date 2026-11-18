package engine

import (
	"context"
	"errors"
	"net"
	"testing"
	"testing/synctest"
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

// connListener returns the same connection from every Accept.
type connListener struct {
	net.Listener

	conn net.Conn
}

func (l connListener) Accept() (net.Conn, error) { return l.conn, nil }

// TestGuardedListener_AcceptAllocs: the guard adds no allocation per
// accepted connection; it is only a deferred recover.
func TestGuardedListener_AcceptAllocs(t *testing.T) { //nolint:paralleltest // testing.AllocsPerRun counts allocations process-wide and panics in a parallel test
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _, _ = c1.Close(), c2.Close() })

	ln := &guardedListener{Listener: connListener{conn: c1}}

	allocs := testing.AllocsPerRun(1000, func() {
		if _, err := ln.Accept(); err != nil {
			t.Fatal(err)
		}
	})
	if allocs != 0 {
		t.Fatalf("Accept allocates %v times per call, want 0", allocs)
	}
}

// finishedContext is a cancelled context whose Done waits, inside a
// synctest bubble, until every other goroutine of the bubble is durably
// blocked or has ended: runGuarded's goroutine has then delivered fn's
// result before runGuarded sees the context end.
type finishedContext struct {
	context.Context

	done chan struct{}
}

func (c finishedContext) Done() <-chan struct{} {
	synctest.Wait()

	return c.done
}

func (finishedContext) Err() error { return context.Canceled }

// TestRunGuarded_FinishedAsContextEnds: when fn has finished by the time
// runGuarded sees ctx end, fn's result counts and the call is not
// abandoned, whichever of the two ready cases the select picks. Each row
// runs many times, so the select also takes the ctx.Done case.
func TestRunGuarded_FinishedAsContextEnds(t *testing.T) {
	t.Parallel()

	failed := errors.New("failed")

	tests := []struct {
		name    string
		fn      func(context.Context) error
		wantErr error
		panics  bool
	}{
		{"nil result", func(context.Context) error { return nil }, nil, false},
		{"error result", func(context.Context) error { return failed }, failed, false},
		{"panic", func(context.Context) error { panic("boom") }, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				done := make(chan struct{})
				close(done)
				ctx := finishedContext{Context: t.Context(), done: done}

				for range 64 {
					err, abandoned := runGuarded(ctx, tt.fn)
					if abandoned {
						t.Fatal("runGuarded abandoned fn, which had finished")
					}

					var pe *panicError
					if tt.panics {
						if !errors.As(err, &pe) || pe.value != "boom" {
							t.Fatalf("err = %v, want the panic boom", err)
						}

						continue
					}

					if !errors.Is(err, tt.wantErr) || (tt.wantErr == nil && err != nil) {
						t.Fatalf("err = %v, want %v", err, tt.wantErr)
					}
				}
			})
		})
	}
}

// TestFuncPointer: a function value gives a stable, non-zero key; nil, a
// nil function and a value that is not a function give 0.
func TestFuncPointer(t *testing.T) {
	t.Parallel()

	var nilFunc func()

	tests := []struct {
		name     string
		fn       any
		wantZero bool
	}{
		{"function", TestFuncPointer, false},
		{"nil", nil, true},
		{"nil function", nilFunc, true},
		{"not a function", "not a func", true},
		{"pointer", &nilFunc, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := FuncPointer(tt.fn)
			if (got == 0) != tt.wantZero {
				t.Fatalf("FuncPointer(%T) = %#x, want zero %v", tt.fn, got, tt.wantZero)
			}

			if again := FuncPointer(tt.fn); again != got {
				t.Fatalf("FuncPointer(%T) = %#x then %#x, want the same key", tt.fn, got, again)
			}
		})
	}
}
