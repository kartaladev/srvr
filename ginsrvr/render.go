package ginsrvr

import (
	"bufio"
	"net"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr/internal/engine"
	"github.com/kartaladev/srvr/internal/errchain"
)

// renderErrors is srvr's error middleware for a server with a consumer
// renderer, in place of ginprblm's: once the chain returned, it answers
// through engine.Core.RenderHTTP the last error recorded in c.Errors
// (srvr's recovery records a panic there too), or the router's 404 or 405
// for a request no route or mount matched. A status-only abort such as
// c.AbortWithStatus(401) is no error: its bare status is sent, as
// without a renderer. The response writer defers the status, because gin
// commits it early (c.AbortWithError writes its status before the error
// is answered), so the renderer's status replaces it.
func renderErrors(core *engine.Core) gin.HandlerFunc {
	return func(c *gin.Context) {
		w := &deferredWriter{ResponseWriter: c.Writer, committed: c.Writer.Written()}
		c.Writer = w

		defer func() { c.Writer = w.ResponseWriter }()

		c.Next()

		if err := answeredError(c, w); err != nil {
			core.RenderHTTP(w, c.Request, err)

			return
		}

		w.commitNow()
	}
}

// answeredError returns the error srvr answers for the request of c, nil
// for none: the last error in c.Errors, else, for a request no route
// matched (gin presets 404, or 405 with its Allow header, which
// requestStage sorted), srvr's error for the router's answer.
func answeredError(c *gin.Context, w *deferredWriter) error {
	if last := c.Errors.Last(); last != nil {
		return last.Err
	}

	if state, _ := engine.StateFrom(c.Request.Context()); !routerAnswers(state) {
		return nil
	}

	switch w.Status() {
	case http.StatusNotFound:
		return errchain.ErrNotFound
	case http.StatusMethodNotAllowed:
		return engine.MethodNotAllowed(w.Header().Get("Allow"))
	default:
		return nil
	}
}

// routerAnswers reports whether gin's router answers the request of state
// (no route or mount matched it), so an error status without an error is
// the router's 404 or 405; for a matched request it is a status-only
// abort such as c.AbortWithStatus(401), which did not fail (design D2).
// Outside srvr's pipeline (nil state) it cannot tell and reports false.
func routerAnswers(state *engine.RequestState) bool { return state != nil && !state.Matched() }

// recoverPanics is srvr's recovery for a server with a consumer renderer,
// in place of ginprblm's: it records a panic, with the panicking frames'
// stack, as the request's last error for [renderErrors] to answer, and
// aborts the chain. It re-raises http.ErrAbortHandler, so net/http aborts
// the connection, as ginprblm does.
func recoverPanics(c *gin.Context) {
	returned := false

	defer func() {
		// returned, not recover's result, tells a panic apart, so
		// panic(nil) is answered under GODEBUG=panicnil=1 too.
		if returned {
			return
		}

		err := engine.RecoveredPanic(recover())

		c.Abort()
		_ = c.Error(err)
	}()

	c.Next()

	returned = true
}

// deferredWriter holds back the header a handler commits until the body
// is written or the chain returned, so an error answered after gin
// committed a status early gets the renderer's status. gin's WriteHeader
// only records the status (the header goes out with WriteHeaderNow or the
// first write), so holding back WriteHeaderNow is enough. It is the engine.ErrorTarget
// of [renderErrors].
type deferredWriter struct {
	gin.ResponseWriter

	// committed reports whether the response reached the underlying writer.
	committed bool
}

var _ engine.ErrorTarget = (*deferredWriter)(nil)

// WriteHeaderNow commits only a response already committed: gin's
// c.AbortWithStatus calls it, which must not commit the status yet.
func (w *deferredWriter) WriteHeaderNow() {
	if w.committed {
		w.ResponseWriter.WriteHeaderNow()
	}
}

func (w *deferredWriter) Write(b []byte) (int, error) {
	w.commit()

	return w.ResponseWriter.Write(b)
}

func (w *deferredWriter) WriteString(s string) (int, error) {
	w.commit()

	return w.ResponseWriter.WriteString(s)
}

// Flush commits the response and flushes it.
func (w *deferredWriter) Flush() {
	w.commit()
	w.ResponseWriter.Flush()
}

// Hijack hands the connection over; the response counts as committed.
func (w *deferredWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := w.ResponseWriter.Hijack()
	if err == nil {
		w.committed = true
	}

	return conn, rw, err
}

// Unwrap returns the underlying writer, for http.ResponseController.
func (w *deferredWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Committed reports whether the response already started.
func (w *deferredWriter) Committed() bool { return w.committed }

// WriteError writes res through the deferred status, replacing a status
// gin set early.
func (w *deferredWriter) WriteError(res engine.ErrorResponse) {
	engine.WriteHTTPError(w, res)
}

// commit marks the response committed; gin's writer sends the status it
// recorded with the first write.
func (w *deferredWriter) commit() { w.committed = true }

// commitNow commits and writes the header without a body.
func (w *deferredWriter) commitNow() {
	w.commit()
	w.ResponseWriter.WriteHeaderNow()
}
