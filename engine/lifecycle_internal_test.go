package engine

import (
	"errors"
	"net"
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
