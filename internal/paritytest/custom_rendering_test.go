package paritytest

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// testRenderer is the spec's R: it answers srvr's own errors and errQuota
// with their status, Content-Type application/vnd.test+json and body
// {"code":"<status>"}, adds Allow to a 405, and sends every error it
// renders on errs.
type testRenderer struct{ errs chan error }

func (r testRenderer) Render(_ context.Context, _ srvr.ErrorRequest, err error) srvr.ErrorResponse {
	r.errs <- err

	h := http.Header{"Content-Type": {"application/vnd.test+json"}}
	status := http.StatusInternalServerError

	var mna *srvr.MethodNotAllowedError

	switch {
	case errors.Is(err, errQuota):
		status = http.StatusTooManyRequests
	case errors.Is(err, srvr.ErrNotFound):
		status = http.StatusNotFound
	case errors.As(err, &mna):
		status = http.StatusMethodNotAllowed
		h.Set("Allow", strings.Join(mna.Allow, ", "))
	case errors.Is(err, srvr.ErrRequestTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, srvr.ErrHeadersTooLarge):
		status = http.StatusRequestHeaderFieldsTooLarge
	case errors.Is(err, srvr.ErrClientClosed):
		status = 499
	}

	return srvr.ErrorResponse{Status: status, Header: h, Body: []byte(`{"code":"` + strconv.Itoa(status) + `"}`)}
}

// customAnswer is what one engine answers a custom-rendering row: R's
// answer for status with header allow when called, or the engine's own
// response (status, body) when R is not called.
type customAnswer struct {
	called bool
	status int
	allow  string
	body   string
}

// rendered is R's answer for status.
func rendered(status int, allow string) customAnswer {
	return customAnswer{called: true, status: status, allow: allow}
}

// customRow is one Examples row of error-rendering / Scenario: Custom
// renderer on every engine, plus the Hostile rows whose answer differs
// between engines. want is every engine's answer; except replaces it for
// the engines of a recorded engine-parity exception, whose error R must
// match with exceptMatch (match when nil).
type customRow struct {
	row         renderRow
	match       func(error) bool
	want        customAnswer
	except      map[string]customAnswer
	exceptMatch func(error) bool
}

func isPanic(err error) bool {
	pe, ok := errors.AsType[*srvr.PanicError](err)

	return ok && pe.Value == "boom"
}

func isMethodNotAllowed(allow ...string) func(error) bool {
	return func(err error) bool {
		mna, ok := errors.AsType[*srvr.MethodNotAllowedError](err)

		return ok && slices.Equal(mna.Allow, allow)
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, srvr.ErrNotFound) && errors.Is(err, prblm.ErrNotFound)
}

func isAny(error) bool { return true }

// goHeaderReject is Go's own plain-text answer to headers over
// MaxHeaderBytes, sent before srvr runs.
var goHeaderReject = customAnswer{status: http.StatusRequestHeaderFieldsTooLarge, body: "431 Request Header Fields Too Large"}

var customRows = []customRow{
	{
		row:   renderRow{name: "handler returns ErrQuota", method: "GET", path: "/x", route: "/x", routeMethod: "GET", fail: func() error { return errQuota }},
		match: func(err error) bool { return errors.Is(err, errQuota) },
		want:  rendered(http.StatusTooManyRequests, ""),
	},
	{
		row:   renderRow{name: "handler panics", method: "GET", path: "/x", route: "/x", routeMethod: "GET", panics: true},
		match: isPanic,
		want:  rendered(http.StatusInternalServerError, ""),
	},
	{
		row:   renderRow{name: "no route", method: "GET", path: "/missing", route: "/x", routeMethod: "GET", fail: func() error { return nil }},
		match: isNotFound,
		want:  rendered(http.StatusNotFound, ""),
	},
	{
		row:   renderRow{name: "method not allowed", method: "GET", path: "/x", route: "/x", routeMethod: "POST", fail: func() error { return nil }},
		match: isMethodNotAllowed("POST"),
		want:  rendered(http.StatusMethodNotAllowed, "POST"),
	},
	{
		row:   renderRow{name: "static miss", method: "GET", path: "/assets/missing.js", static: true},
		match: isNotFound,
		want:  rendered(http.StatusNotFound, ""),
	},
	{
		row:   renderRow{name: "static method not allowed", method: "DELETE", path: "/assets/app.js", static: true},
		match: isMethodNotAllowed("GET", "HEAD"),
		want:  rendered(http.StatusMethodNotAllowed, "GET, HEAD"),
	},
	{
		row: renderRow{
			name: "body over limit", method: "POST", path: "/x", body: []byte("01234567890"), route: "/x", routeMethod: "POST",
			readBody: true, opts: []srvr.Option{srvr.WithMaxRequestBodyBytes(10)},
		},
		match: func(err error) bool { return errors.Is(err, srvr.ErrRequestTooLarge) },
		want:  rendered(http.StatusRequestEntityTooLarge, ""),
	},
	{
		row: renderRow{name: "client closed", method: "GET", path: "/x", route: "/x", routeMethod: "GET", clientCloses: true, fail: func() error { return fmt.Errorf("q: %w", context.Canceled) }},
		match: func(err error) bool {
			return errors.Is(err, srvr.ErrClientClosed) && errors.Is(err, context.Canceled)
		},
		want: rendered(499, ""),
		// A disconnect never cancels fiber's context: the handler's
		// context.Canceled reaches R as an ordinary error.
		except: map[string]customAnswer{"fiber": rendered(http.StatusInternalServerError, "")},
		exceptMatch: func(err error) bool {
			return errors.Is(err, context.Canceled) && !errors.Is(err, srvr.ErrClientClosed)
		},
	},
	{
		row: renderRow{
			name: "headers over limit", method: "GET", path: "/x", route: "/x", routeMethod: "GET", fail: func() error { return nil },
			bigHeader: true, opts: []srvr.Option{srvr.WithMaxHeaderBytes(4096)},
		},
		match: func(err error) bool { return errors.Is(err, srvr.ErrHeadersTooLarge) },
		want:  rendered(http.StatusRequestHeaderFieldsTooLarge, ""),
		// net/http (and gin on it) rejects oversized headers before srvr
		// runs, with Go's own plain-text 431.
		except: map[string]customAnswer{"net/http": goHeaderReject, "gin": goHeaderReject},
	},
	{
		row:   renderRow{name: "writes 200 then fails", method: "GET", path: "/x", route: "/x", routeMethod: "GET", writeFirst: true, fail: func() error { return errors.New("late") }},
		match: isAny,
		want:  customAnswer{status: http.StatusOK, body: "ok"},
		// fasthttp sends nothing before the handler returns: only a
		// started body stream commits a fiber response, so R replaces
		// the buffered body.
		except: map[string]customAnswer{"fiber": rendered(http.StatusInternalServerError, "")},
	},
}

// TestParity_CustomRendering covers error-rendering / Scenario: Custom
// renderer on every engine: every row, on every engine, calls R once with
// a matching error and answers R's status, Content-Type, Allow and body
// unchanged, logged as http.request.failed with problem.status and no
// problem.type. A recorded engine difference is an explicit except entry.
func TestParity_CustomRendering(t *testing.T) {
	t.Parallel()

	for _, row := range customRows {
		for _, engine := range engineNames {
			t.Run(engine+"/"+row.row.name, func(t *testing.T) {
				t.Parallel()

				want := row.want
				e, excepted := row.except[engine]
				if excepted {
					want = e
				}

				r := testRenderer{errs: make(chan error, 4)}
				rr := row.row
				rr.opts = append(append([]srvr.Option(nil), rr.opts...), srvr.WithErrorRenderer(r))

				base, logs := startRenderRow(t, engine, rr)
				g := sendRenderRow(t, base, rr)

				if !want.called {
					if g.Status != want.status || !strings.Contains(g.Body, want.body) {
						t.Errorf("got %d %q, want %d %q", g.Status, g.Body, want.status, want.body)
					}
					if len(r.errs) != 0 {
						t.Errorf("R called with %v, want not called", <-r.errs)
					}

					return
				}

				wantBody := `{"code":"` + strconv.Itoa(want.status) + `"}`
				if g.Status != want.status || g.Body != wantBody ||
					g.Header.Get("Content-Type") != "application/vnd.test+json" || g.Header.Get("Allow") != want.allow {
					t.Errorf("got %d %v %s, want %d %s with Allow %q", g.Status, g.Header, g.Body, want.status, wantBody, want.allow)
				}

				if n := len(r.errs); n != 1 {
					t.Fatalf("R called %d times, want once", n)
				}
				match := row.match
				if excepted && row.exceptMatch != nil {
					match = row.exceptMatch
				}
				if err := <-r.errs; !match(err) {
					t.Errorf("R got %#v, which does not match", err)
				}

				recs := logs.access(t)
				if len(recs) != 1 {
					t.Fatalf("got %d access records, want 1", len(recs))
				}
				problem, _ := recs[0]["problem"].(map[string]any)
				if recs[0]["msg"] != "http.request.failed" || problem["status"] != float64(want.status) || problem["type"] != nil {
					t.Errorf("record %v, want http.request.failed with problem.status %d and no problem.type", recs[0], want.status)
				}
			})
		}
	}
}
