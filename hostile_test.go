package srvr_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

// selfPanicError is an error whose Error method panics with itself, so fmt's
// own guard, which formats the panic value, panics again.
type selfPanicError struct{}

func (selfPanicError) Error() string { panic(selfPanicError{}) }

// safeText formats v for a test message, guarded against a value whose
// formatting panics at any depth.
func safeText(v any) (text string) {
	defer func() {
		if recover() != nil {
			text = fmt.Sprintf("<unprintable %T>", v)
		}
	}()

	return fmt.Sprint(v)
}

// addrListener is a listener whose Addr method is replaced by addr.
type addrListener struct {
	net.Listener

	addr func() net.Addr
}

func (l addrListener) Addr() net.Addr { return l.addr() }

// hostileAddr is a net.Addr whose String panics with selfPanicError.
type hostileAddr struct{}

func (hostileAddr) Network() string { return "tcp" }
func (hostileAddr) String() string  { panic(selfPanicError{}) }

// HostileAPI panics in Configure with a value whose Error panics with
// itself.
type HostileAPI struct{}

func (*HostileAPI) Configure(*srvr.Group) { panic(selfPanicError{}) }

// hostileContext is a context that is done after a delay and whose Err
// then returns err.
type hostileContext struct {
	context.Context

	done chan struct{}
	err  error
}

func newHostileContext(t *testing.T, after time.Duration, err error) hostileContext {
	t.Helper()

	c := hostileContext{Context: context.Background(), done: make(chan struct{}), err: err}
	timer := time.AfterFunc(after, func() { close(c.done) })
	t.Cleanup(func() { timer.Stop() })

	return c
}

func (c hostileContext) Done() <-chan struct{} { return c.done }

func (c hostileContext) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}
