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
	"slices"
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

// renderingNotes returns the prblm notes about a mistake in rendering, not
// the request's own failure. It returns an array, a copy no caller can
// change for another and that needs no allocation.
func renderingNotes() [5]error {
	return [...]error{
		prblm.ErrInvalidStatus, prblm.ErrInvalidType, prblm.ErrUndocumentedType,
		prblm.ErrEncoding, prblm.ErrShadowedDocumentation,
	}
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

	for _, note := range renderingNotes() {
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
		if c.Observed() {
			if octx := c.BeginObservers(ctx, c.httpRequestInfo(r)); octx != ctx {
				r = r.WithContext(octx)
			}
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
// it reuses before passing it, except for a record AccessSuppressed
// reports suppressed: WriteAccess then reads Path only to decide and keeps
// no string of info past its return.
type AccessInfo struct {
	// Method is the request method.
	Method string
	// Path is the request path, logged only with IncludePath.
	Path string
	// RawQuery is the query string, logged only with IncludeQuery.
	RawQuery string
	// RemoteAddr is the client address, logged only with
	// IncludeClientAddress.
	RemoteAddr string
	// UserAgent is the User-Agent header.
	UserAgent string
	// Status is the response status.
	Status int
	// BodySize is the number of response body bytes written.
	BodySize int64
	// RequestBodySize is the request's declared Content-Length, 0 when
	// unknown.
	RequestBodySize int64
	// Duration is the time from the start of the request to the response.
	Duration time.Duration
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

// AccessSuppressed reports whether the access record of the request of
// ctx, ending now with status on path, is suppressed (Disabled,
// SkipPaths), so an engine can skip copying the values only the record
// reports. It is false outside a request.
func (c *Core) AccessSuppressed(ctx context.Context, path string, status int) bool {
	req := requestFrom(ctx)
	if req == nil {
		return false
	}

	msg, _ := outcome(status, req.aborted, req.failure)

	return c.accessSuppressed(msg, path, status)
}

// accessSuppressed reports whether an access record with event msg is
// suppressed. Failed records are the triage record: suppression never
// applies.
func (c *Core) accessSuppressed(msg, path string, status int) bool {
	return msg == eventRequestCompleted && c.settings.Access.suppressed(path, status)
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
	written := !c.accessSuppressed(msg, info.Path, status) && c.log.baseEnabled(ctx, level)
	summary := c.flushBuffer(req, status, written)

	if !written {
		return
	}

	record := newOwnRecord(level, msg)
	record.AddAttrs(slog.String(keyMethod, info.Method))

	if req.route != "" {
		record.AddAttrs(slog.String(keyRoute, req.route))
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

	record.AddAttrs(summary...)
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

// The optional interfaces responseRecorder keeps for the writer it wraps:
// io.Copy's sendfile path, and what http.ResponseController looks for.
var (
	_ io.ReaderFrom                             = (*responseRecorder)(nil)
	_ http.Hijacker                             = (*responseRecorder)(nil)
	_ interface{ FlushError() error }           = (*responseRecorder)(nil)
	_ interface{ Unwrap() http.ResponseWriter } = (*responseRecorder)(nil)
)

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

// AccessLogConfig configures the access record written for every request.
// Consumers use it as srvr.AccessLogConfig and pass it with
// srvr.WithAccessLogConfig; zero values are the defaults.
//
// Suppression (Disabled, SkipPaths) applies only to http.request.completed
// records with a status below 500: http.request.failed records (a returned
// error, a recovered panic, an aborted or unmatched request) are the triage
// record and are always written, whatever their status. The
// Include fields each loosen a privacy default: by default the access
// record holds the route pattern but not the path, the query string or the
// client address, which may carry personal data or credentials.
type AccessLogConfig struct {
	// Disabled suppresses http.request.completed records below status 500.
	Disabled bool `json:"disabled" yaml:"disabled" env:"DISABLED"`
	// SkipPaths suppresses http.request.completed records below status 500
	// for requests whose path equals one of these exactly, such as
	// "/healthz".
	SkipPaths []string `json:"skip_paths" yaml:"skip_paths" env:"SKIP_PATHS"`
	// IncludePath adds the request path as url.path.
	IncludePath bool `json:"include_path" yaml:"include_path" env:"INCLUDE_PATH"`
	// IncludeQuery adds the raw query string as url.query.
	IncludeQuery bool `json:"include_query" yaml:"include_query" env:"INCLUDE_QUERY"`
	// IncludeClientAddress adds the client's IP address as client.address.
	IncludeClientAddress bool `json:"include_client_address" yaml:"include_client_address" env:"INCLUDE_CLIENT_ADDRESS"`
}

// suppressed reports whether the http.request.completed record of a
// request to path with status is suppressed.
func (c *AccessLogConfig) suppressed(path string, status int) bool {
	return status < http.StatusInternalServerError && (c.Disabled || slices.Contains(c.SkipPaths, path))
}

// clientAddress returns the IP address of a RemoteAddr.
func clientAddress(remote string) string {
	if host, _, err := net.SplitHostPort(remote); err == nil {
		return host
	}

	return remote
}
