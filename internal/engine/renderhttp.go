package engine

import (
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/kartaladev/prblm"
)

// HTTPErrors answers the errors of a net/http pipeline: prblm's problem
// handler, or the consumer's renderer through [Core.Render]. Its methods
// are those of *prblm.Handler.
type HTTPErrors interface {
	// Wrap returns a handler answering the error fn returns.
	Wrap(fn func(http.ResponseWriter, *http.Request) error) http.Handler
	// Recover returns next answering a panic in it; http.ErrAbortHandler
	// is re-raised.
	Recover(next http.Handler) http.Handler
	// Write answers err.
	Write(w http.ResponseWriter, r *http.Request, err error)
}

// HTTPErrors returns how the net/http pipeline answers errors: the
// problem handler without a consumer renderer, the renderer otherwise.
func (c *Core) HTTPErrors() HTTPErrors {
	if c.settings.ErrorRenderer == nil {
		return c.problems
	}

	return httpRenderer{c}
}

// httpRenderer answers net/http errors with the consumer's renderer.
type httpRenderer struct{ core *Core }

func (h httpRenderer) Wrap(fn func(http.ResponseWriter, *http.Request) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			h.Write(w, r, err)
		}
	})
}

func (h httpRenderer) Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		returned := false

		defer func() {
			// returned, not recover's result, tells a panic apart, so
			// panic(nil) is answered under GODEBUG=panicnil=1 too.
			if returned {
				return
			}

			// The panicking frames are still on the stack here.
			h.Write(w, r, RecoveredPanic(recover()))
		}()

		next.ServeHTTP(w, r)

		returned = true
	})
}

// RecoveredPanic returns the error answering v, a value recovered from a
// panic, with the stack of the panicking frames, which must still be on
// the stack (call it in the deferred function). It re-raises
// http.ErrAbortHandler, so net/http aborts the connection, as prblm does.
func RecoveredPanic(v any) *prblm.PanicError {
	//nolint:errorlint // only the sentinel itself aborts, as in prblm.
	if err, ok := v.(error); ok && err == http.ErrAbortHandler {
		panic(v)
	}

	return &prblm.PanicError{Value: v, Stack: debug.Stack()}
}

func (h httpRenderer) Write(w http.ResponseWriter, r *http.Request, err error) {
	h.core.RenderHTTP(httpTarget{w: w, state: requestFrom(r.Context())}, r, err)
}

// RenderHTTP is [Core.Render] for err, an error of the net/http request
// r, which any engine built on net/http describes the same way: method,
// path and headers from r, route and trace id from its request state.
func (c *Core) RenderHTTP(t ErrorTarget, r *http.Request, err error) {
	ctx := r.Context()

	req := ErrorRequest{Method: r.Method, Path: r.URL.Path, Header: httpHeader(r.Header)}
	if state := requestFrom(ctx); state != nil {
		req.Route, req.TraceID = state.route, state.trace.TraceID
	}

	c.Render(ctx, t, req, err)
}

// httpTarget writes an answer through a net/http response writer.
type httpTarget struct {
	w     http.ResponseWriter
	state *RequestState
}

// Committed reads the access stage's recorder; outside srvr's pipeline it
// cannot tell, and reports false.
func (t httpTarget) Committed() bool { return t.state != nil && t.state.committed() }

// WriteError writes res with [WriteHTTPError].
func (t httpTarget) WriteError(res ErrorResponse) { WriteHTTPError(t.w, res) }

// WriteHTTPError writes res on w with prblm v0.3.0's header rules (design
// D6): the renderer's Vary values are merged into the handler's, its
// other headers replace the handler's, Cache-Control included, and
// Content-Length is set from the body. A write error (the client went
// away) is ignored.
func WriteHTTPError(w http.ResponseWriter, res ErrorResponse) {
	h := w.Header()
	for k, v := range res.Header {
		if k == "Vary" {
			addVary(h, v)

			continue
		}

		h[k] = v
	}

	h.Del("Transfer-Encoding") // one the handler set; res has none
	h.Set("Content-Length", strconv.Itoa(len(res.Body)))
	w.WriteHeader(res.Status)
	_, _ = w.Write(res.Body)
}

// addVary adds to h each field name of add that h's Vary does not list
// yet, compared case-insensitively, as prblm merges Vary.
func addVary(h http.Header, add []string) {
	for _, line := range add {
		for name := range strings.SplitSeq(line, ",") {
			if name = strings.TrimSpace(name); name != "" && !varies(h, name) {
				h.Add("Vary", name)
			}
		}
	}
}

// varies reports whether h's Vary lists name.
func varies(h http.Header, name string) bool {
	for _, line := range h.Values("Vary") {
		for listed := range strings.SplitSeq(line, ",") {
			if strings.EqualFold(strings.TrimSpace(listed), name) {
				return true
			}
		}
	}

	return false
}
