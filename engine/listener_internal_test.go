package engine

import (
	"net"
	"testing"
)

// connListener returns the same connection from every Accept.
type connListener struct {
	net.Listener

	conn net.Conn
}

func (l connListener) Accept() (net.Conn, error) { return l.conn, nil }

// TestGuardedListener_AcceptAllocs: the guard adds no allocation per
// accepted connection; it is only a deferred recover.
func TestGuardedListener_AcceptAllocs(t *testing.T) {
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
