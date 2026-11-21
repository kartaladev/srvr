package fibersrvr

import (
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr/internal/engine"
	"github.com/kartaladev/srvr/internal/errchain"
)

// answer answers err, the error the chain returned for c: through
// fiberprblm without a consumer renderer, through engine.Core.Render
// with one. Fiber's router answers become srvr's errors on the renderer
// path (routed reports [routerAnswered]), so a renderer matches them
// without importing fiber.
func (s *Server) answer(c fiber.Ctx, err error, routed bool) {
	if !s.core.HasErrorRenderer() {
		_ = s.renderError(c, err)

		return
	}

	if routed {
		err = routerError(c, err)
	}

	s.render(c, err, false)
}

// render answers err for c through engine.Core.Render; with hold the
// answer is kept in c's locals instead of written (see [fiberTarget]).
func (s *Server) render(c fiber.Ctx, err error, hold bool) {
	s.core.Render(c.Context(), fiberTarget{c: c, hold: hold}, errorRequest(c), err)
}

// routerError returns srvr's error for err, fiber's router answer for c:
// fiber.ErrNotFound or fiber.ErrMethodNotAllowed, whose Allow header
// fiber already set.
func routerError(c fiber.Ctx, err error) error {
	if err == fiber.ErrNotFound { //nolint:errorlint // routerAnswered compared by identity
		return errchain.ErrNotFound
	}

	return engine.MethodNotAllowed(string(c.Response().Header.Peek(fiber.HeaderAllow)))
}

// rejectionError returns the error a renderer answers for a request
// fasthttp rejected before routing with status: srvr's error for the
// statuses srvr documents (431, 408, 413), fiber's error otherwise.
func rejectionError(status int) error {
	switch status {
	case http.StatusRequestHeaderFieldsTooLarge:
		return errchain.ErrHeadersTooLarge
	case http.StatusRequestTimeout:
		return errchain.ErrRequestTimeout
	case http.StatusRequestEntityTooLarge:
		return errchain.RequestTooLarge(fiber.ErrRequestEntityTooLarge)
	default:
		return fiber.NewError(status)
	}
}

// heldAnswer keys, in the request's locals, the renderer's answer to a
// pre-routing rejection, which the error handler sends.
type heldAnswer struct{}

// handleError is the app's error handler. fiber calls it only for a
// request fasthttp rejected before routing (requestStage answers every
// other error itself): without a renderer fiberprblm renders the problem;
// with one it sends the answer requestStage held, or, when fiber did not
// replay the chain (a method it does not know), renders the rejection
// with the status fiber set.
func (s *Server) handleError(c fiber.Ctx, err error) error {
	if !s.core.HasErrorRenderer() {
		return s.renderError(c, err)
	}

	if res, held := c.Locals(heldAnswer{}).(engine.ErrorResponse); held {
		writeAnswer(c, res)

		return nil
	}

	s.render(c, rejectionError(c.Response().StatusCode()), false)

	return nil
}

// errorRequest describes the request of c to a renderer. The path is a
// copy, since fiber reuses its buffer; the header reader is valid only
// during Render.
func errorRequest(c fiber.Ctx) engine.ErrorRequest {
	ctx := c.Context()
	req := engine.ErrorRequest{Method: c.Method(), Path: strings.Clone(c.Path()), Header: fiberHeader{&c.Request().Header}}

	if state, ok := engine.StateFrom(ctx); ok {
		req.Route = state.Route()
	}

	if tr, ok := engine.TraceFromContext(ctx); ok {
		req.TraceID = tr.TraceID
	}

	return req
}

// fiberTarget is the engine.ErrorTarget of a fiber request. With hold,
// the answer is also kept in the request's locals: for a request fasthttp
// rejected before routing, requestStage renders while fiber replays the
// chain, so the access record has the answer sent, but fiber then resets
// the body and calls its error handler, which sends the held answer
// ([Server.handleError]).
type fiberTarget struct {
	c    fiber.Ctx
	hold bool
}

var _ engine.ErrorTarget = fiberTarget{}

// Committed reports whether a body stream was set: fasthttp sends nothing
// before the chain returns, so a buffered body is still replaceable, as
// fiberprblm treats it.
func (t fiberTarget) Committed() bool { return t.c.Response().IsBodyStream() }

// WriteError writes res and, with hold, also keeps it: the access record
// and the observers then read the status and body the client gets,
// although fiber resets the body before its error handler sends the held
// answer again (writing it twice is idempotent: Vary values merge once).
func (t fiberTarget) WriteError(res engine.ErrorResponse) {
	writeAnswer(t.c, res)

	if t.hold {
		t.c.Locals(heldAnswer{}, res)
	}
}

// writeAnswer writes res on c's response with prblm v0.3.0's header rules
// (design D6), as engine.WriteHTTPError does on net/http: the renderer's
// Vary values merged into the handler's, its other headers replacing the
// handler's, and Content-Length set from the body by fasthttp, which
// also drops a Transfer-Encoding the handler set on a buffered body.
func writeAnswer(c fiber.Ctx, res engine.ErrorResponse) {
	h := &c.Response().Header

	for k, v := range res.Header {
		if k == fiber.HeaderVary {
			v = mergedVary(h.PeekAll(fiber.HeaderVary), v)
		}

		h.Del(k)

		for _, value := range v {
			h.Add(k, value)
		}
	}

	c.Status(res.Status)
	_ = c.Send(res.Body)
}

// mergedVary returns the Vary lines of current with each field name of
// add they do not list yet.
func mergedVary(current [][]byte, add []string) []string {
	h := make(http.Header, 1)
	for _, line := range current {
		h.Add(fiber.HeaderVary, string(line))
	}

	engine.AddVary(h, add)

	return h.Values(fiber.HeaderVary)
}
