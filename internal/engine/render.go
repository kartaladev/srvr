package engine

import (
	"context"
	"net/http"

	"github.com/kartaladev/prblm"
)

// ErrorRenderer turns an error srvr answers into the response sent to the
// client. Consumers use it as srvr.ErrorRenderer.
type ErrorRenderer interface {
	// Render returns the response for err, an error of the request req.
	// ctx carries the request's values, srvr's trace among them.
	Render(ctx context.Context, req ErrorRequest, err error) ErrorResponse
}

// ErrorRendererFunc adapts a function to [ErrorRenderer]. Consumers use it
// as srvr.ErrorRendererFunc.
type ErrorRendererFunc func(ctx context.Context, req ErrorRequest, err error) ErrorResponse

// Render calls f.
func (f ErrorRendererFunc) Render(ctx context.Context, req ErrorRequest, err error) ErrorResponse {
	return f(ctx, req, err)
}

// ErrorRequest is what a renderer knows of the request whose error it
// renders. The query string is never passed. Consumers use it as
// srvr.ErrorRequest.
type ErrorRequest struct {
	// Method is the request method.
	Method string
	// Path is the URL path without the query string: for the response
	// (a problem's instance, say), never logged by srvr.
	Path string
	// Route is the matched route pattern, in the engine's syntax; empty
	// when no route matched.
	Route string
	// TraceID is the request's trace id.
	TraceID string
	// Header reads the request headers; nil reads none. On fiber it is
	// valid only during Render.
	Header HeaderReader
}

// ErrorResponse is a renderer's answer. Consumers use it as
// srvr.ErrorResponse.
type ErrorResponse struct {
	// Status is the response status, 400 to 599.
	Status int
	// Header holds the response headers; Content-Type is required with a
	// body.
	Header http.Header
	// Body is the response body.
	Body []byte
	// Type and Title describe the answer in logs and error hooks; srvr
	// never writes them to the response.
	Type, Title string
}

// DefaultRenderer is the prblm renderer every server uses by default, as
// an [ErrorRenderer] a consumer's renderer can delegate to (design D7):
// it answers with the problem handler of the request's server, so with
// its prblm options, mappers and trace id; outside a request, with srvr's
// built-in problems (prblm's defaults plus srvr's own answers). A nil
// error answers the zero response. Consumers use it through
// srvr.DefaultErrorRenderer.
var DefaultRenderer ErrorRenderer = defaultRenderer{}

// builtinProblems answers for DefaultRenderer outside a request.
var builtinProblems = prblm.NewHandler(srvrProblemOptions(captureProblem)...)

type defaultRenderer struct{}

func (defaultRenderer) Render(ctx context.Context, req ErrorRequest, err error) ErrorResponse {
	h := builtinProblems
	if state := requestFrom(ctx); state != nil && state.core != nil && state.core.problems != nil {
		h = state.core.problems
	}

	var sent *prblm.Problem

	res, ok := h.Respond(context.WithValue(ctx, captureKey{}, &sent), prblm.Request{
		Method: req.Method, Path: req.Path, Accept: headerValue(req.Header, "Accept"),
	}, err)
	if !ok {
		return ErrorResponse{}
	}

	out := ErrorResponse{Status: res.Status, Header: res.Header, Body: res.Body}
	if sent != nil {
		out.Type, out.Title = sent.EffectiveType(), sent.Title
	}

	return out
}

// headerValue returns h's first value of key; "" for a nil h.
func headerValue(h HeaderReader, key string) string {
	if h == nil {
		return ""
	}

	return h.Get(key)
}

// captureKey keys the slot DefaultRenderer reads the sent problem from.
type captureKey struct{}

// captureProblem stores p, the problem sent for err, in the slot of ctx
// set by DefaultRenderer, if any; prblm's notes are not the answer.
func captureProblem(ctx context.Context, err error, p *prblm.Problem) {
	if slot, ok := ctx.Value(captureKey{}).(**prblm.Problem); ok && p != nil && !isNote(err) {
		*slot = p
	}
}
