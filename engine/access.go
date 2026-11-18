package engine

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/kartaladev/prblm"
)

// failure is why a request failed: the error srvr's prblm hook received,
// the panic it reports (nil for a returned error), the problem sent and
// whether the response had already started when the error arrived.
type failure struct {
	err       error
	panic     *prblm.PanicError
	problem   *prblm.Problem
	committed bool
}

// newFailure returns the failure of err, looking up the panic it reports
// once, so later readers do not walk the chain again.
func newFailure(err error, p *prblm.Problem, committed bool) *failure {
	pe, _ := panicOf(err)

	return &failure{err: err, panic: pe, problem: p, committed: committed}
}

// renderingNotes are the prblm notes about a mistake in rendering, not the
// request's own failure.
var renderingNotes = []error{
	prblm.ErrInvalidStatus, prblm.ErrInvalidType, prblm.ErrUndocumentedType,
	prblm.ErrEncoding, prblm.ErrShadowedDocumentation,
}

// recordFailure keeps the failure of the request in ctx. The first failure
// with a problem wins and later ones are dropped; a failure without one (a
// panic after the response started, on a broken connection, or while
// answering) is kept only until one with a problem arrives, which replaces
// it. Notes about rendering mistakes are not failures and are dropped
// without a record (design D4).
func recordFailure(ctx context.Context, err error, p *prblm.Problem) {
	req := requestFrom(ctx)
	if req == nil {
		return
	}

	for _, note := range renderingNotes {
		if errors.Is(err, note) {
			return
		}
	}

	if req.failure != nil && (req.failure.problem != nil || p == nil) {
		return
	}

	committed := req.rec != nil && req.rec.wroteHeader
	req.failure = newFailure(err, p, committed)
}

// errRejected is the failure of a request the engine's server rejected
// before routing it (oversized body or headers, read timeout).
var errRejected = errors.New("srvr: request rejected before routing")

// RecordRejection records, for the request of ctx, the failure of a
// request the server rejected with status before routing it: the engine
// renders the client's problem itself, after the access record is written,
// so the record's problem group is built here from the status and its
// standard title.
func (c *Core) RecordRejection(ctx context.Context, status int) {
	p := prblm.New(status)
	p.Title = http.StatusText(status)

	recordFailure(ctx, fmt.Errorf("%w: %d %s", errRejected, status, p.Title), p)
}

// LogAccess returns the access stage: it records the response and writes
// one access record per request when the response finishes.
func (c *Core) LogAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w}

		ctx := r.Context()
		req := requestFrom(ctx) // set by StartTrace, the outer stage
		req.rec = rec

		// Observers begin inside the access stage and end in its record,
		// so they see the final status, including an abort.
		if octx := c.BeginObservers(ctx, httpRequestInfo(r)); octx != ctx {
			r = r.WithContext(octx)
		}

		defer func() {
			// http.ErrAbortHandler is re-raised past the recovery stage so
			// net/http aborts the connection; it is recorded first.
			if v := recover(); v != nil {
				// Recovery re-raises only http.ErrAbortHandler; anything
				// else reaching here is still logged, as an abort.
				req.aborted = true

				info := httpAccess(r, rec, time.Since(start))
				if !rec.wroteHeader {
					info.Status = 0 // the response never started
				}

				c.writeAccess(ctx, req, info)
				panic(v)
			}
		}()

		next.ServeHTTP(rec, r)

		c.writeAccess(ctx, req, httpAccess(r, rec, time.Since(start)))
	})
}

// AccessInfo is what the access record of a request reports about it,
// besides the request state. An engine copies every value out of buffers
// it reuses before passing it.
type AccessInfo struct {
	Method     string
	Path       string
	RawQuery   string
	RemoteAddr string
	UserAgent  string
	Status     int
	BodySize   int64
	// RequestBodySize is the request's declared Content-Length, 0 when
	// unknown.
	RequestBodySize int64
	Duration        time.Duration
}

// httpAccess returns the access information of a net/http request.
func httpAccess(r *http.Request, rec *responseRecorder, d time.Duration) AccessInfo {
	return AccessInfo{
		Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, RemoteAddr: r.RemoteAddr, UserAgent: r.UserAgent(),
		Status: rec.statusCode(), BodySize: rec.bytes, RequestBodySize: max(r.ContentLength, 0), Duration: d,
	}
}

// WriteAccess writes the access record of a finished request of an engine
// that is not served through net/http; ctx carries the request state.
func (c *Core) WriteAccess(ctx context.Context, info AccessInfo) {
	if req := requestFrom(ctx); req != nil {
		req.status = info.Status
		c.writeAccess(ctx, req, info)
	}
}

// writeAccess runs the request's observer end functions, then writes its
// access record; a zero info.Status, an aborted request whose response
// never started, omits the status.
func (c *Core) writeAccess(ctx context.Context, req *RequestState, info AccessInfo) {
	c.endObservers(ctx, req, req.result(info))

	status := info.Status
	aborted := req.aborted
	fail := req.failure

	msg, level := outcome(status, aborted, fail)

	// Failed records are the triage record: suppression never applies.
	if (msg == eventRequestCompleted && c.settings.Access.suppressed(info.Path, status)) || !c.log.Enabled(ctx, level) {
		return
	}

	record := newOwnRecord(level, msg)
	record.AddAttrs(slog.String(keyMethod, info.Method))

	if req.Route != "" {
		record.AddAttrs(slog.String(keyRoute, req.Route))
	}

	if status != 0 {
		record.AddAttrs(slog.Int(keyStatus, status))
	}

	record.AddAttrs(
		slog.Int64(keyBodySize, info.BodySize),
		slog.Float64(keyDuration, millis(info.Duration)),
	)

	if info.UserAgent != "" {
		record.AddAttrs(slog.String(keyUserAgent, info.UserAgent))
	}

	c.addOptIns(&record, info)

	switch {
	case aborted:
		record.AddAttrs(c.abortedAttrs()...)
	case fail != nil:
		addFailure(&record, fail)
		record.AddAttrs(c.triageAttrs(info.Method, req, fail)...)
	}

	c.log.handle(ctx, record, true)
}

// addFailure adds what the client saw: the problem sent, or that the
// response had already started.
func addFailure(record *slog.Record, fail *failure) {
	if fail.committed || fail.problem == nil {
		record.AddAttrs(slog.Bool(keyCommitted, true))

		return
	}

	p := fail.problem
	record.AddAttrs(slog.Group(keyProblem,
		slog.Int(keyProblemStatus, p.Status),
		slog.String(keyProblemType, p.EffectiveType()),
		slog.String(keyProblemTitle, p.Title),
	))
}

// outcome returns the event and level of the access record of a request
// that sent status.
func outcome(status int, aborted bool, fail *failure) (string, slog.Level) {
	switch {
	case aborted:
		// The client lost the connection by design; the server did not fail.
		return eventRequestFailed, slog.LevelWarn
	case fail != nil:
		return eventRequestFailed, fail.level(status)
	default:
		return eventRequestCompleted, levelFor(status)
	}
}

// level returns the level of the failed record of a request that sent
// status: ERROR for a panic, otherwise the higher of the levels of the
// status sent and of the problem the error resolved to, which a response
// that had already started did not get.
func (f *failure) level(status int) slog.Level {
	if f.panic != nil {
		return slog.LevelError
	}

	level := levelFor(status)
	if f.problem != nil {
		level = max(level, levelFor(f.problem.Status))
	}

	return level
}

// levelFor returns INFO below 400, WARN for 4xx and ERROR for 5xx.
func levelFor(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// responseRecorder records the status and size of a response and whether
// it has started.
type responseRecorder struct {
	http.ResponseWriter

	status      int
	bytes       int64
	wroteHeader bool
}

// statusCode returns the final status sent, 200 when the handler wrote
// none (net/http's default), even after an informational one.
func (w *responseRecorder) statusCode() int {
	return cmp.Or(w.status, http.StatusOK)
}

func (w *responseRecorder) WriteHeader(code int) {
	// 1xx informational responses neither start the final response nor
	// set the status the client finally receives.
	if !w.wroteHeader && code >= http.StatusOK {
		w.status, w.wroteHeader = code, true
	}

	w.ResponseWriter.WriteHeader(code)
}

func (w *responseRecorder) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	n, err := w.ResponseWriter.Write(b)
	w.bytes += int64(n)

	return n, err
}

// ReadFrom keeps net/http's sendfile path for io.Copy and ServeContent.
func (w *responseRecorder) ReadFrom(src io.Reader) (int64, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	n, err := io.Copy(w.ResponseWriter, src)
	w.bytes += n

	return n, err
}

// FlushError starts the response, as net/http does, and flushes it.
func (w *responseRecorder) FlushError() error {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	return http.NewResponseController(w.ResponseWriter).Flush()
}

// Hijack hands the connection over; once it succeeds the response counts
// as started.
func (w *responseRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, buf, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err == nil {
		w.wroteHeader = true
	}

	return conn, buf, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *responseRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// addOptIns adds the attributes the access log configuration opts into.
func (c *Core) addOptIns(record *slog.Record, info AccessInfo) {
	opt := &c.settings.Access
	if opt.IncludePath {
		record.AddAttrs(slog.String(keyURLPath, info.Path))
	}

	if opt.IncludeQuery && info.RawQuery != "" {
		record.AddAttrs(slog.String(keyURLQuery, info.RawQuery))
	}

	if opt.IncludeClientAddress {
		record.AddAttrs(slog.String(keyClientAddress, clientAddress(info.RemoteAddr)))
	}
}
