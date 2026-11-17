package srvr

import (
	"context"
	"net/http"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/internal/tracectx"
)

// Trace is the W3C trace context of a request: the trace id continued from
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

// TraceConfig configures trace context handling. Pass it with
// [WithTraceConfig].
type TraceConfig struct {
	// IgnoreIncoming starts a new trace for every request, ignoring any
	// traceparent header. Default false: a valid incoming traceparent is
	// continued, as W3C Trace Context defines.
	IgnoreIncoming bool `json:"ignore_incoming" yaml:"ignore_incoming" env:"IGNORE_INCOMING"`
}

// WithTraceConfig sets every field of [TraceConfig].
func WithTraceConfig(c TraceConfig) Option {
	return func(s *settings) { s.trace = c }
}

// WithIgnoreIncomingTraceContext starts a new trace for every request,
// for a server facing clients whose trace ids must not be trusted.
func WithIgnoreIncomingTraceContext() Option {
	return func(s *settings) { s.trace.IgnoreIncoming = true }
}

// requestKey is the context key of the request state.
type requestKey struct{}

// request is srvr's state for one request, carried in its context.
type request struct {
	server *Server
	trace  Trace
	// rec records the response, set by the access stage.
	rec *responseRecorder
	// route is the pattern path the router matched, empty when unmatched.
	route string
	// failure is the error the request failed with, as srvr's prblm hook
	// or the unmatched interception recorded it.
	failure *failure
}

// requestFrom returns the request state of ctx, or nil.
func requestFrom(ctx context.Context) *request {
	if ctx == nil {
		return nil
	}

	req, _ := ctx.Value(requestKey{}).(*request)

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

// startTrace returns the outermost stage: it reads or generates the trace
// context and adds the request state to the request context.
func (s *Server) startTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var tc tracectx.Trace
		if s.settings.trace.IgnoreIncoming {
			tc = tracectx.New()
		} else {
			tc = tracectx.FromHeaders(r.Header["Traceparent"]) // canonical key: no allocation
		}

		req := &request{server: s, trace: Trace(tc)}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestKey{}, req)))
	})
}

// traceIDEnricher adds the request's trace id to every problem as the
// trace_id extension member.
func traceIDEnricher(ctx context.Context, p *prblm.Problem) {
	if tr, ok := TraceFromContext(ctx); ok {
		_ = p.Set(keyTraceID, tr.TraceID)
	}
}
