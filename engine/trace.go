package engine

import (
	"context"
	"net/http"

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
	// continued, as W3C Trace Context defines.
	IgnoreIncoming bool `json:"ignore_incoming" yaml:"ignore_incoming" env:"IGNORE_INCOMING"`
}

// stateKey is the context key of the request state.
type stateKey struct{}

// RequestState is srvr's state for one request, carried in its context:
// created by the outermost srvr stage, written by the engine's stages and
// read by the access stage. Later stages read it with [StateFrom]; a
// handler's own goroutines must not write it.
type RequestState struct {
	core  *Core
	trace Trace
	// rec records the response, set by the access stage.
	rec *responseRecorder
	// Route is the route pattern the router matched, in the engine's own
	// syntax, empty when unmatched.
	Route string
	// Location is the code location of the matched route's handler.
	Location Location
	// aborted is set when the request was aborted with http.ErrAbortHandler.
	aborted bool
	// failure is the error the request failed with, as srvr's prblm hook
	// or the unmatched interception recorded it.
	failure *failure
}

// Matched reports whether a route matched the request.
func (r *RequestState) Matched() bool { return r.Route != "" }

// Status returns the final status of the response so far: 200 when
// nothing was written yet, as net/http sends.
func (r *RequestState) Status() int {
	if r.rec == nil {
		return http.StatusOK
	}

	return r.rec.statusCode()
}

// Failed reports whether the request failed: a returned error or a
// recovered panic reached srvr's problem hook, or no route matched.
func (r *RequestState) Failed() bool { return r.failure != nil }

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

	req, _ := ctx.Value(stateKey{}).(*RequestState)

	return req
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
		var tc tracectx.Trace
		if c.settings.Trace.IgnoreIncoming {
			tc = tracectx.New()
		} else {
			tc = tracectx.FromHeaders(r.Header["Traceparent"]) // canonical key: no allocation
		}

		req := &RequestState{core: c, trace: Trace(tc)}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stateKey{}, req)))
	})
}

// traceIDEnricher adds the request's trace id to every problem as the
// trace_id extension member.
func traceIDEnricher(ctx context.Context, p *prblm.Problem) {
	if tr, ok := TraceFromContext(ctx); ok {
		_ = p.Set(keyTraceID, tr.TraceID)
	}
}
