package engine

import (
	"context"
	"net/http"
	"time"
)

// Observer is told about every request a server handles, on every engine:
// Begin runs before the handler, inside trace context handling and the
// access log, and the end function it returns runs once the response is
// final, outside panic recovery and error rendering. Consumers use it as
// srvr.Observer and register it with srvr.WithObserver.
//
// Begin returns the context the rest of the request runs with (the
// incoming one, or one derived from it) and the end function. Both run on
// the request's goroutine: an observer that blocks delays the request.
type Observer interface {
	Begin(ctx context.Context, info RequestInfo) (context.Context, func(ResultInfo))
}

// RequestInfo is what an observer learns about a request before routing.
// Every value is a copy the observer may keep.
type RequestInfo struct {
	// Method is the request method as the client sent it.
	Method string
	// Scheme is "http" or "https".
	Scheme string
	// Host is the request's host, as the client sent it.
	Host string
}

// ErrorKind is how a request failed: [ErrorKindNone], [ErrorKindError] or
// [ErrorKindPanic].
type ErrorKind string

const (
	// ErrorKindNone is a request that did not fail.
	ErrorKindNone ErrorKind = "none"
	// ErrorKindError is a request that failed with a returned error or
	// matched no route.
	ErrorKindError ErrorKind = "error"
	// ErrorKindPanic is a request whose handler panicked, including with
	// http.ErrAbortHandler.
	ErrorKindPanic ErrorKind = "panic"
)

// ResultInfo is what an observer learns about a finished request. Every
// value is a copy the observer may keep.
type ResultInfo struct {
	// Route is the matched route template in the engine's own syntax,
	// empty when no route matched.
	Route string
	// Status is the final status sent; 0 for a request aborted before its
	// response started.
	Status int
	// Duration is the time from Begin to the end of the response.
	Duration time.Duration
	// RequestBodySize is the declared Content-Length of the request, 0
	// when unknown.
	RequestBodySize int64
	// ResponseBodySize is the size of the response body, as the access
	// record reports it.
	ResponseBodySize int64
	// ErrorKind is how the request failed.
	ErrorKind ErrorKind
	// Err is the error the request failed with: the returned error, the
	// recovered panic as reported by prblm, or the error of an unmatched
	// route; nil when it did not fail.
	Err error
	// Committed is true when the error arrived after the response started.
	Committed bool
	// Aborted is true for a request aborted with http.ErrAbortHandler.
	Aborted bool
}

// BeginObservers calls Begin of every observer of the server for the
// request of ctx, in registration order, each with the context the
// previous one returned, and keeps their end functions in the request
// state: the access record runs them, in reverse order, before it is
// written. It returns the context the rest of the request runs with. The
// net/http access stage calls it; an engine with its own access stage
// calls it before routing.
func (c *Core) BeginObservers(ctx context.Context, info RequestInfo) context.Context {
	req := requestFrom(ctx)
	if req == nil || len(c.settings.Observers) == 0 {
		return ctx
	}

	req.ends = make(endFuncs, 0, len(c.settings.Observers))
	for _, o := range c.settings.Observers {
		var end func(ResultInfo)

		ctx, end = o.Begin(ctx, info)
		req.ends = append(req.ends, end)
	}

	return ctx
}

// endFuncs are the end functions of a request's observers, in Begin order.
type endFuncs []func(ResultInfo)

// run calls the end functions in reverse Begin order.
func (e endFuncs) run(res ResultInfo) {
	for i := len(e) - 1; i >= 0; i-- {
		e[i](res)
	}
}

// httpRequestInfo returns the request information of a net/http request.
func httpRequestInfo(r *http.Request) RequestInfo {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return RequestInfo{Method: r.Method, Scheme: scheme, Host: r.Host}
}

// result returns the result information of the finished request info
// describes.
func (r *RequestState) result(info AccessInfo) ResultInfo {
	res := ResultInfo{
		Route: r.Route, Status: info.Status, Duration: info.Duration,
		RequestBodySize: info.RequestBodySize, ResponseBodySize: info.BodySize,
		ErrorKind: r.errorKind(), Aborted: r.aborted,
	}

	if f := r.failure; f != nil {
		res.Err, res.Committed = f.err, f.committed
	}

	return res
}

// errorKind returns how the request failed.
func (r *RequestState) errorKind() ErrorKind {
	switch {
	case r.aborted || r.Panicked():
		return ErrorKindPanic
	case r.Failed():
		return ErrorKindError
	default:
		return ErrorKindNone
	}
}
