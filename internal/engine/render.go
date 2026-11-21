package engine

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/internal/errchain"
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
	// Route is the matched route pattern, in the engine's syntax and
	// without the method (as the http.route log key carries it, say
	// /items/{id}); empty when no route matched.
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
	// body. srvr sends a copy: Content-Length (set from Body),
	// Transfer-Encoding and the hop-by-hop headers (Connection,
	// Keep-Alive, Proxy-*, Te, Trailer, Upgrade) are dropped; every other
	// header, Set-Cookie included, is sent as given.
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

// Fixed limits of a consumer's renderer (design D4, Defaults): neither is
// configurable in this release.
const (
	// defaultRenderDeadline bounds a renderer's call: ample for any
	// in-memory renderer; one doing I/O is a misuse the deadline exposes.
	defaultRenderDeadline = 5 * time.Second
	// maxErrorBody is the largest error body sent: a larger one is a bug
	// or an attack amplifier.
	maxErrorBody = 1 << 20
)

// The faults of a renderer whose answer srvr replaced with its safe 500,
// logged as srvr.renderer.failure.
const (
	faultPanic              = "panic"
	faultInvalidStatus      = "invalid_status"
	faultOversizedBody      = "oversized_body"
	faultMissingContentType = "missing_content_type"
	faultDeadline           = "deadline"
)

// ErrorTarget is where [Core.Render] writes an answer on one engine.
type ErrorTarget interface {
	// Committed reports whether the response already started.
	Committed() bool
	// WriteError writes res, whose headers [Core.Render] has already
	// copied and cleaned (no Content-Length, Transfer-Encoding or
	// hop-by-hop header), applying the engine's header merge rules.
	WriteError(res ErrorResponse)
}

// Render answers err, an error of the request of ctx described by req,
// with the consumer's renderer on t, keeping srvr's safety rules (design
// D4): once the response started the renderer is not called and the
// failure is recorded as committed; the renderer receives an error whose
// chain can be walked, srvr's own answers wrapped in srvr's errors, and a
// context keeping the request's values but not its cancellation, bounded
// by the render deadline; a faulty answer is replaced by the safe 500
// (design D5) and its fault recorded. The answer sent is recorded and the
// error hook called once. It must only be called when a renderer is set.
func (c *Core) Render(ctx context.Context, t ErrorTarget, req ErrorRequest, err error) {
	if t.Committed() {
		c.onError(ctx, err, nil)

		if state := requestFrom(ctx); state != nil {
			state.MarkCommitted()
		}

		return
	}

	err = GuardChain(err)

	res, fault := c.callRenderer(ctx, req, wrapAnswer(ctx, err))
	if fault != "" {
		res = safeAnswer(ctx, req)
	}

	sent := answer{status: res.Status, typ: res.Type, title: res.Title}
	recordAnswer(requestFrom(ctx), err, sent, fault)

	if c.settings.ErrorHook != nil {
		c.callHook(ctx, err, &prblm.Problem{Status: sent.status, Type: sent.typ, Title: sent.title})
	}
	t.WriteError(res)
}

// wrapAnswer returns err wrapped in srvr's error for an answer the
// renderer could not tell otherwise: a request the client closed (as
// [clientClosed] decides for prblm) or a body over the limit.
func wrapAnswer(ctx context.Context, err error) error {
	switch {
	case clientClosed(ctx, err) != nil:
		return errchain.ClientClosed(err)
	case !errors.Is(err, errchain.ErrRequestTooLarge) && errors.As(err, new(*http.MaxBytesError)):
		return errchain.RequestTooLarge(err)
	default:
		return err
	}
}

// callRenderer calls the consumer's renderer and returns its answer, its
// headers copied and cleaned, or the fault that disqualifies it.
func (c *Core) callRenderer(ctx context.Context, req ErrorRequest, err error) (res ErrorResponse, fault string) {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.renderDeadline)
	defer cancel()

	returned := false

	defer func() {
		// returned, not recover's result, tells a panic apart, so
		// panic(nil) is a fault under GODEBUG=panicnil=1 too.
		if !returned {
			_ = recover()
			res, fault = ErrorResponse{}, faultPanic
		}
	}()

	res = c.settings.ErrorRenderer.Render(rctx, req, err)
	returned = true
	res.Header = cleanHeader(res.Header)

	return res, renderFault(rctx, res)
}

// renderFault returns the rule res breaks, or "".
func renderFault(rctx context.Context, res ErrorResponse) string {
	switch {
	case rctx.Err() != nil:
		return faultDeadline
	case res.Status < http.StatusBadRequest || res.Status > 599:
		return faultInvalidStatus
	case len(res.Body) > maxErrorBody:
		return faultOversizedBody
	case len(res.Body) > 0 && res.Header.Get("Content-Type") == "":
		return faultMissingContentType
	default:
		return ""
	}
}

// droppedHeaders are the headers a renderer's answer never sets: srvr
// sets Content-Length from the body, and hop-by-hop headers belong to the
// connection (RFC 9110 section 7.6.1).
var droppedHeaders = map[string]bool{
	"Content-Length": true, "Transfer-Encoding": true, "Connection": true, "Keep-Alive": true,
	"Proxy-Connection": true, "Proxy-Authenticate": true, "Proxy-Authorization": true,
	"Te": true, "Trailer": true, "Upgrade": true,
}

// cleanHeader returns a copy of h with canonical keys and without
// [droppedHeaders], so a renderer changing h later changes nothing; nil
// for an empty h.
func cleanHeader(h http.Header) http.Header {
	if len(h) == 0 {
		return nil
	}

	out := make(http.Header, len(h))
	for k, v := range h {
		if k = http.CanonicalHeaderKey(k); !droppedHeaders[k] {
			out[k] = append(out[k], v...)
		}
	}

	return out
}

// errSafeAnswer is the error the safe 500 is resolved from: unmapped, so
// prblm answers its generic 500.
var errSafeAnswer = errors.New("srvr: renderer failed")

// safeAnswer returns srvr's fixed safe 500 problem for req (design D5):
// the bytes prblm sends for an unmapped error, with the trace id, built
// by srvr's built-in problem handler and never by the consumer's renderer.
func safeAnswer(ctx context.Context, req ErrorRequest) ErrorResponse {
	res, _ := builtinProblems.Respond(ctx, prblm.Request{Method: req.Method, Path: req.Path}, errSafeAnswer)

	return ErrorResponse{
		Status: res.Status, Header: res.Header, Body: res.Body,
		Type: "about:blank", Title: http.StatusText(http.StatusInternalServerError),
	}
}
