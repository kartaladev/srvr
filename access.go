package srvr

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/kartaladev/prblm"
)

// failure is why a request failed: the error srvr's prblm hook received
// (a *prblm.PanicError for a panic), the problem sent and whether the
// response had already started when the error arrived.
type failure struct {
	err       error
	problem   *prblm.Problem
	committed bool
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
	req.failure = &failure{err: err, problem: p, committed: committed}
}

// logAccess returns the access stage: it records the response and writes
// one access record per request when the response finishes.
func (s *Server) logAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseRecorder{ResponseWriter: w}

		req := requestFrom(r.Context()) // set by startTrace, the outer stage
		req.rec = rec

		defer func() {
			// http.ErrAbortHandler is re-raised past the recovery stage so
			// net/http aborts the connection; it is recorded first.
			if v := recover(); v != nil {
				// Recovery re-raises only http.ErrAbortHandler; anything
				// else reaching here is still logged, as an abort.
				req.aborted = true

				s.writeAccess(r, req, rec, time.Since(start))
				panic(v)
			}
		}()

		next.ServeHTTP(rec, r)

		s.writeAccess(r, req, rec, time.Since(start))
	})
}

// writeAccess writes the access record of a finished request.
func (s *Server) writeAccess(r *http.Request, req *request, rec *responseRecorder, d time.Duration) {
	status := rec.statusCode()
	aborted := req.aborted
	fail := req.failure

	msg, level := outcome(status, aborted, fail)

	// Failed records are the triage record: suppression never applies.
	ctx := r.Context()
	if (msg == eventRequestCompleted && s.settings.access.suppressed(r.URL.Path, status)) || !s.log.Enabled(ctx, level) {
		return
	}

	record := newOwnRecord(level, msg)
	record.AddAttrs(slog.String(keyMethod, r.Method))

	if req.route != "" {
		record.AddAttrs(slog.String(keyRoute, req.route))
	}

	if !aborted || rec.wroteHeader {
		record.AddAttrs(slog.Int(keyStatus, status))
	}

	record.AddAttrs(
		slog.Int64(keyBodySize, rec.bytes),
		slog.Float64(keyDuration, millis(d)),
	)

	if ua := r.UserAgent(); ua != "" {
		record.AddAttrs(slog.String(keyUserAgent, ua))
	}

	s.addOptIns(&record, r)

	switch {
	case aborted:
		record.AddAttrs(s.abortedAttrs()...)
	case fail != nil:
		addFailure(&record, fail)
		record.AddAttrs(s.triageAttrs(r.Method, req, fail)...)
	}

	s.log.handle(ctx, record, true)
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
	if _, isPanic := f.err.(*prblm.PanicError); isPanic { //nolint:errorlint // srvr's hook receives prblm's own value.
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
func (s *Server) addOptIns(record *slog.Record, r *http.Request) {
	c := &s.settings.access
	if c.IncludePath {
		record.AddAttrs(slog.String(keyURLPath, r.URL.Path))
	}

	if c.IncludeQuery && r.URL.RawQuery != "" {
		record.AddAttrs(slog.String(keyURLQuery, r.URL.RawQuery))
	}

	if c.IncludeClientAddress {
		record.AddAttrs(slog.String(keyClientAddress, clientAddress(r.RemoteAddr)))
	}
}
