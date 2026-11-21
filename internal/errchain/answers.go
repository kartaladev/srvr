package errchain

import (
	"errors"
	"fmt"

	"github.com/kartaladev/prblm"
)

// The errors of srvr's own answers (pluggable-error-renderer design D3),
// documented on the names package srvr re-exports them under. A renderer
// matches them with errors.Is and errors.As without importing prblm.
var (
	// ErrNotFound is the error of a request no route or static file
	// matched; see [notFoundError].
	ErrNotFound error = notFoundError{}
	// ErrRequestTooLarge is wrapped, with the engine's own error, by the
	// error of a request body over MaxRequestBodyBytes (see
	// [RequestTooLarge]).
	ErrRequestTooLarge = errors.New("srvr: request body too large")
	// ErrHeadersTooLarge is the error of a request whose headers exceed
	// the server's limit, which fiber rejects before routing.
	ErrHeadersTooLarge = errors.New("srvr: request headers too large")
	// ErrRequestTimeout is the error of a request the server's read
	// timeout ended before routing, on fiber.
	ErrRequestTimeout = errors.New("srvr: request timeout")
	// ErrClientClosed is wrapped, with the handler's error, by the error
	// of a request whose client went away (see [ClientClosed]).
	ErrClientClosed = errors.New("srvr: client closed request")
)

// notFoundError is [ErrNotFound]: a distinct error that keeps v0.3.0's
// prblm.ErrNotFound text and matches it through Is instead of wrapping
// it, so a 404's access record (exception.message, a one-link
// error.chain) reads as in v0.3.0 and the 404 costs no extra chain link.
type notFoundError struct{}

func (notFoundError) Error() string { return "prblm: not found" }

// Is reports whether target is prblm.ErrNotFound.
func (notFoundError) Is(target error) bool { return target == prblm.ErrNotFound }

// MethodNotAllowedError is the error of a request whose path a route or a
// static mount serves, but not with the request's method.
type MethodNotAllowedError struct {
	// Allow lists the methods the path serves, sorted, as the Allow header
	// sends them.
	Allow []string
}

func (*MethodNotAllowedError) Error() string { return "srvr: method not allowed" }

// RequestTooLarge returns err, the engine's error for a body over the
// limit, wrapped with [ErrRequestTooLarge]; nil for a nil err.
func RequestTooLarge(err error) error { return wrapAnswer(ErrRequestTooLarge, err) }

// ClientClosed returns err, the handler's error after its client went
// away, wrapped with [ErrClientClosed]; nil for a nil err.
func ClientClosed(err error) error { return wrapAnswer(ErrClientClosed, err) }

// wrapAnswer wraps err with sentinel, both matching errors.Is.
func wrapAnswer(sentinel, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%w: %w", sentinel, err)
}
