package srvr

import (
	"errors"
	"reflect"
)

var (
	// ErrServerStarted is returned by Start and Register once the server
	// has been started.
	ErrServerStarted = errors.New("srvr: server already started")
	// ErrServerClosed is returned by Start and Register once the server has
	// stopped or was shut down before it started.
	ErrServerClosed = errors.New("srvr: server closed")
	// ErrRouteConfig is wrapped by the error Register or Start returns for
	// a broken route configuration: a nil configurer, an invalid or
	// conflicting pattern, or a configurer that panics.
	ErrRouteConfig = errors.New("srvr: invalid route configuration")
)

// state is the lifecycle state of a Server. It only moves forward:
// new -> starting -> running -> stopping -> closed. A server that stops
// without serving goes from new or starting to stopping (Shutdown) or
// closed (a failed Start).
type state int

const (
	stateNew state = iota
	stateStarting
	stateRunning
	stateStopping
	stateClosed
)

// startable returns nil when Start may begin from this state.
func (st state) startable() error {
	switch st {
	case stateNew:
		return nil
	case stateStarting, stateRunning:
		return ErrServerStarted
	default:
		return ErrServerClosed
	}
}

// isNil reports whether v is nil or an interface holding a nil pointer,
// map, slice, function or channel (a typed nil).
func isNil(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface, reflect.UnsafePointer:
		return rv.IsNil()
	default:
		return false
	}
}
