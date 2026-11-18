package srvr

import "github.com/kartaladev/srvr/engine"

type (
	// Observer is told about every request the server handles; see
	// [engine.Observer]. Register it with [WithObserver].
	Observer = engine.Observer
	// RequestInfo is what an observer learns about a request before
	// routing; see [engine.RequestInfo].
	RequestInfo = engine.RequestInfo
	// ResultInfo is what an observer learns about a finished request; see
	// [engine.ResultInfo].
	ResultInfo = engine.ResultInfo
	// ErrorKind is how a request failed; see [engine.ErrorKind].
	ErrorKind = engine.ErrorKind
	// TraceSource is an optional extension of an Observer that supplies
	// the request's trace context; see [engine.TraceSource].
	TraceSource = engine.TraceSource
	// HeaderReader reads request headers during an observer's Begin; see
	// [engine.HeaderReader].
	HeaderReader = engine.HeaderReader
)

const (
	// ErrorKindNone is a request that did not fail.
	ErrorKindNone = engine.ErrorKindNone
	// ErrorKindError is a request that failed with a returned error or
	// matched no route.
	ErrorKindError = engine.ErrorKindError
	// ErrorKindPanic is a request whose handler panicked.
	ErrorKindPanic = engine.ErrorKindPanic
)

// WithObserver adds o to the server's observers, which every engine calls
// once per request, between the access log and panic recovery; observers
// run on the request path and must be fast (see [engine.Observer]).
// Default: no observer, so a server without one does no observer work. A
// nil or typed-nil o fails New with an error wrapping [ErrConfig].
func WithObserver(o Observer) Option {
	return func(s *engine.Settings) { s.Observers = append(s.Observers, o) }
}
