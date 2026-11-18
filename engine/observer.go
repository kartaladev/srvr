package engine

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// Observer is told about every request a server handles, on every engine:
// Begin runs before the handler, inside trace context handling and the
// access log, and the end function it returns runs once the response is
// final, outside panic recovery and error rendering. Consumers use it as
// srvr.Observer and register it with srvr.WithObserver.
//
// Begin returns the context the rest of the request runs with (the
// incoming one, or one derived from it) and the end function, which may
// be nil. Several observers compose: Begin runs in registration order,
// each with the context the previous one returned, and the end functions
// run in reverse order.
//
// Observers run synchronously on the request path, on every engine: a
// slow Begin or end function delays the response and is not detected.
// They must be fast; slow work such as exporting telemetry belongs in the
// OpenTelemetry SDK's batch span processors and periodic metric readers,
// which export asynchronously.
//
// A faulty observer never changes the response: a panic in Begin or in
// the end function (even with http.ErrAbortHandler) is contained and
// logged once per observer and kind as srvr.observer.fault; a nil context,
// or one not derived from the context Begin got, is replaced by that
// context.
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
	// Err is the error consumer code failed with: the returned error
	// (after srvr's guard against a cyclic chain) or the recovered panic as
	// reported by prblm. It is nil when the request did not fail, and when
	// srvr answered it itself without running a route (no route matched,
	// or the server rejected it before routing), which has no consumer
	// error; ErrorKind is still ErrorKindError then.
	Err error
	// Committed is true when the error arrived after the response started.
	Committed bool
	// Aborted is true for a request aborted with http.ErrAbortHandler.
	Aborted bool
}

// Observed reports whether the server has observers, so an engine can skip
// building the [RequestInfo] for a server without one.
func (c *Core) Observed() bool { return len(c.observers) > 0 }

// BeginObservers calls Begin of every observer of the server for the
// request of ctx, in registration order, each with the context the
// previous one returned, and keeps their end functions in the request
// state: the access record runs them, in reverse order, before it is
// written. It returns the context the rest of the request runs with. The
// net/http access stage calls it; an engine with its own access stage
// calls it before routing.
func (c *Core) BeginObservers(ctx context.Context, info RequestInfo) context.Context {
	req := requestFrom(ctx)
	if req == nil || !c.Observed() {
		return ctx
	}

	req.ends = make([]endFunc, 0, len(c.observers))
	for _, o := range c.observers {
		var end func(ResultInfo)

		ctx, end = c.begin(ctx, req, o, info)
		if end != nil {
			req.ends = append(req.ends, endFunc{observer: o, fn: end})
		}
	}

	return ctx
}

// endFunc is the end function an observer returned.
type endFunc struct {
	observer *guardedObserver
	fn       func(ResultInfo)
}

// guardedObserver is a registered observer with the fault kinds it has
// reported, so each is logged once per server.
type guardedObserver struct {
	Observer

	reported [numFaultKinds]atomic.Bool
}

// guardObservers wraps every one of observers.
func guardObservers(observers []Observer) []*guardedObserver {
	guarded := make([]*guardedObserver, len(observers))
	for i, o := range observers {
		guarded[i] = &guardedObserver{Observer: o}
	}

	return guarded
}

// begin calls Begin of o under recover. It returns ctx and no end
// function when Begin panics, and ctx when the returned context is nil or
// does not carry the request state req (an observer must derive its
// context from the one it got), so the request keeps srvr's state.
func (c *Core) begin(ctx context.Context, req *RequestState, o *guardedObserver, info RequestInfo) (next context.Context, end func(ResultInfo)) {
	defer func() {
		if v := recover(); v != nil {
			c.observerFault(ctx, o, faultBeginPanic, v)
			next, end = ctx, nil
		}
	}()

	next, end = o.Begin(ctx, info)
	if next != ctx && (next == nil || requestFrom(next) != req) {
		next = ctx
	}

	return next, end
}

// endObservers calls the request's end functions in reverse Begin order,
// each under recover: a panic, even with http.ErrAbortHandler, is reported
// and never re-raised.
func (c *Core) endObservers(ctx context.Context, req *RequestState, res ResultInfo) {
	for i := len(req.ends) - 1; i >= 0; i-- {
		c.end(ctx, req.ends[i], res)
	}
}

func (c *Core) end(ctx context.Context, e endFunc, res ResultInfo) {
	defer func() {
		if v := recover(); v != nil {
			c.observerFault(ctx, e.observer, faultEndPanic, v)
		}
	}()

	e.fn(res)
}

// faultKind is an observer fault, logged as srvr.observer.fault.kind.
type faultKind int

const (
	faultBeginPanic faultKind = iota
	faultEndPanic
	numFaultKinds
)

// String returns the srvr.observer.fault.kind value.
func (k faultKind) String() string {
	return [numFaultKinds]string{faultBeginPanic: "begin_panic", faultEndPanic: "end_panic"}[k]
}

// observerFault writes srvr.observer.fault for o the first time it
// reports kind; v is the panic value, read through ValueText.
func (c *Core) observerFault(ctx context.Context, o *guardedObserver, kind faultKind, v any) {
	if o.reported[kind].Swap(true) {
		return
	}

	c.logEvent(ctx, slog.LevelError, eventObserverFault,
		slog.String(keyObserverFaultKind, kind.String()),
		slog.String(keyObserverType, TypeName(o.Observer)),
		c.exceptionMessage(ValueText(v)),
	)
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
		res.Committed = f.committed
		if r.Matched() || f.panic != nil { // consumer code ran
			res.Err = f.err
		}
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
