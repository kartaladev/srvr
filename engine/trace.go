package engine

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/internal/tracectx"
)

// Trace is the W3C trace context of a request (srvr.Trace): the trace id continued from
// a valid incoming traceparent header or newly generated, and the span id
// srvr generates for the request.
type Trace struct {
	// TraceID is 32 lowercase hex characters.
	TraceID string
	// SpanID is the request's own span id, 16 lowercase hex characters.
	SpanID string
	// ParentSpanID is the caller's span id from traceparent, or empty.
	ParentSpanID string
	// Sampled is the sampled flag of the incoming traceparent.
	Sampled bool
}

// TraceConfig configures trace context handling. Consumers use it as
// srvr.TraceConfig and pass it with srvr.WithTraceConfig.
type TraceConfig struct {
	// IgnoreIncoming starts a new trace for every request, ignoring any
	// traceparent header. Default false: a valid incoming traceparent is
	// continued, as W3C Trace Context defines, so a trace spans services.
	// On a server not behind a trusted gateway any client chooses that
	// trace_id, and with it which records share one: set IgnoreIncoming
	// there.
	IgnoreIncoming bool `json:"ignore_incoming" yaml:"ignore_incoming" env:"IGNORE_INCOMING"`
}

// stateKey is the context key of the request state.
type stateKey struct{}

// RequestState is srvr's state for one request, carried in its context:
// created by the outermost srvr stage, written by the engine's stages and
// read by the access stage. Later stages read it with [StateFrom]; a
// handler's own goroutines must not write it.
type RequestState struct {
	core *Core
	// trace is the request's trace: srvr's own, or the one an observer's
	// TraceSource supplied, which replaces it. Request loggers are bound to
	// it by pointer, so they follow the replacement.
	trace Trace
	// rec records the response, set by the net/http access stage.
	rec *responseRecorder
	// status is the response status an engine without rec reported to
	// WriteAccess.
	status int
	// route is the route pattern the router matched, in the engine's own
	// syntax, empty when unmatched.
	route string
	// location is the code location of the matched route's handler.
	location Location
	// aborted is set when the request was aborted with http.ErrAbortHandler.
	aborted bool
	// routerAnswered is set by the engine's stage that saw the router
	// itself answer the request with a 404 or 405 (see
	// [RequestState.MarkRouterAnswered]).
	routerAnswered bool
	// failure is the error the request failed with, as srvr's prblm hook
	// or the unmatched interception recorded it.
	failure *failure
	// buffer holds the request's below-level records when debug buffering
	// is on; nil otherwise.
	buffer *requestBuffer
	// ends are the non-nil end functions of the request's observers, in
	// Begin order.
	ends []endFunc
	// logger is the request logger LoggerFromContext returns, built by
	// its first call.
	logger atomic.Pointer[slog.Logger]
}

// Route returns the route pattern the router matched, in the engine's own
// syntax, or "" when no route matched.
func (r *RequestState) Route() string { return r.route }

// SetRoute records the route pattern the engine's router matched. Once
// [RequestState.MarkRouterAnswered] marked the request it does nothing: an
// answer of the router means no route matched, even for an engine whose
// stage records the route after the chain, such as fiber's after a static
// mount's miss.
func (r *RequestState) SetRoute(pattern string) {
	if !r.routerAnswered {
		r.route = pattern
	}
}

// Location returns the code location of the matched route's handler.
func (r *RequestState) Location() Location { return r.location }

// SetLocation records the code location of the matched route's handler.
// Like [RequestState.SetRoute], it does nothing once the request is marked
// as answered by the router.
func (r *RequestState) SetLocation(loc Location) {
	if !r.routerAnswered {
		r.location = loc
	}
}

// Matched reports whether a route matched the request.
func (r *RequestState) Matched() bool { return r.route != "" }

// Status returns the final status of the response so far: the status the
// access stage recorded, or 200 when nothing was written yet, as net/http
// sends.
func (r *RequestState) Status() int {
	switch {
	case r.rec != nil:
		return r.rec.statusCode()
	case r.status != 0:
		return r.status
	default:
		return http.StatusOK
	}
}

// Failed reports whether the request failed: a returned error or a
// recovered panic reached srvr's problem hook, or no route matched.
func (r *RequestState) Failed() bool { return r.failure != nil }

// Panicked reports whether the request's failure is a recovered panic.
func (r *RequestState) Panicked() bool { return r.failure != nil && r.failure.panic != nil }

// ClearFailure forgets the request's failure, for an engine whose problem
// adapter reports to srvr's hook answers that are not failures, such as
// ginprblm for a status-only abort.
func (r *RequestState) ClearFailure() { r.failure = nil }

// MarkCommitted records that the request's failure arrived after its
// response had started, for an engine without a response recorder (fiber
// keeps a body stream that started before the error).
func (r *RequestState) MarkCommitted() {
	if r.failure != nil {
		r.failure.committed = true
	}
}

// MarkRouterAnswered records that the router itself answered the request
// because no route matched: net/http's unmatched interception, gin's no-route
// chains, fiber's router returning fiber.ErrNotFound or
// fiber.ErrMethodNotAllowed, or a static mount answering 404 or 405, which
// is the router's answer for the paths it serves. Only such a request counts towards the
// unmatched-record limit; a 404 or 405 consumer code reported never does.
// It also forgets the route an earlier stage recorded, and later
// [RequestState.SetRoute] calls do nothing: an answer of the router means
// no route matched.
func (r *RequestState) MarkRouterAnswered() {
	r.routerAnswered = true
	r.route = ""
}

// MarkRouterAnswered marks the request of ctx as answered by the router
// ([RequestState.MarkRouterAnswered]); outside srvr's pipeline, including
// for a nil ctx, it does nothing.
func MarkRouterAnswered(ctx context.Context) {
	if req := requestFrom(ctx); req != nil {
		req.MarkRouterAnswered()
	}
}

// StateFrom returns the request state of ctx, and false outside srvr's
// pipeline, including for a nil ctx.
func StateFrom(ctx context.Context) (*RequestState, bool) {
	req := requestFrom(ctx)

	return req, req != nil
}

// requestFrom returns the request state of ctx, or nil.
func requestFrom(ctx context.Context) *RequestState {
	if ctx == nil {
		return nil
	}

	if req, ok := ctx.Value(stateKey{}).(*RequestState); ok {
		return req
	}

	// An engine context such as fiber.Ctx answers Value from its own store
	// and exposes the request's context through Context().
	if ec, ok := ctx.(interface{ Context() context.Context }); ok {
		if inner := ec.Context(); inner != nil && inner != ctx {
			req, _ := inner.Value(stateKey{}).(*RequestState)

			return req
		}
	}

	return nil
}

// TraceFromContext returns the trace context of a request handled by the
// server, and false for any other context, including nil.
func TraceFromContext(ctx context.Context) (Trace, bool) {
	req := requestFrom(ctx)
	if req == nil {
		return Trace{}, false
	}

	return req.trace, true
}

// StartTrace returns the outermost stage: it reads or generates the trace
// context and adds the request state to the request context.
func (c *Core) StartTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The canonical key reads the header values without allocating.
		next.ServeHTTP(w, r.WithContext(c.BeginRequest(r.Context(), r.Header["Traceparent"])))
	})
}

// BeginRequest starts srvr's state for one request of an engine that is
// not served through net/http: it continues the trace of traceparent (the
// request's traceparent header values, copied by the caller) unless the
// server ignores incoming trace context, and returns ctx carrying the new
// state, which the engine must make the request's context.
func (c *Core) BeginRequest(ctx context.Context, traceparent []string) context.Context {
	if c.settings.Trace.IgnoreIncoming {
		return c.withState(ctx, tracectx.New())
	}

	return c.withState(ctx, tracectx.FromHeaders(traceparent))
}

// withState returns ctx carrying a new request state for trace tc.
func (c *Core) withState(ctx context.Context, tc tracectx.Trace) context.Context {
	req := &RequestState{core: c, trace: Trace(tc)}
	if c.log.buffering != nil {
		req.buffer = &requestBuffer{policy: c.log.buffering}
	}

	return context.WithValue(ctx, stateKey{}, req)
}

// traceIDEnricher adds the request's trace id to every problem as the
// trace_id extension member.
func traceIDEnricher(ctx context.Context, p *prblm.Problem) {
	if tr, ok := TraceFromContext(ctx); ok {
		_ = p.Set(keyTraceID, tr.TraceID)
	}
}
