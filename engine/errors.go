package engine

import (
	"errors"
	"reflect"
)

// The sentinel errors of every srvr engine, documented on the names
// package srvr re-exports them under (srvr.ErrConfig and so on), so
// errors.Is matches either name.
var (
	ErrConfig        = errors.New("srvr: invalid configuration")
	ErrServerStarted = errors.New("srvr: server already started")
	ErrServerClosed  = errors.New("srvr: server closed")
	ErrRouteConfig   = errors.New("srvr: invalid route configuration")
	ErrShutdownHook  = errors.New("srvr: shutdown hook failed")
)

// IsNil reports whether v is nil or an interface holding a nil pointer,
// map, slice, function or channel (a typed nil).
func IsNil(v any) bool {
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
