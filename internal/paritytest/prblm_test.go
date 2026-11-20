package paritytest

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// The tests of this file pin how srvr answers and records the errors whose
// handling prblm v0.3.0 changed (static-files task 7.3).

// unwrapPanicError is an error whose Unwrap method panics.
type unwrapPanicError struct{}

func (*unwrapPanicError) Error() string { return "unwrap panics" }
func (*unwrapPanicError) Unwrap() error { panic("Unwrap panicked") }

// isPanicError is an error whose Is method panics.
type isPanicError struct{}

func (isPanicError) Error() string { return "is panics" }
func (isPanicError) Is(error) bool { panic("Is panicked") }

// asPanicError is an error whose As method panics.
type asPanicError struct{}

func (asPanicError) Error() string { return "as panics" }
func (asPanicError) As(any) bool   { panic("As panicked") }

// deepError wraps next, to build a chain longer than prblm walks.
type deepError struct{ next error }

func (deepError) Error() string   { return "deep" }
func (e deepError) Unwrap() error { return e.next }

// deepChain returns a chain of n deepErrors around errors.New("bottom"),
// without a cycle.
func deepChain(n int) error {
	err := errors.New("bottom")
	for range n {
		err = deepError{err}
	}

	return err
}

// wideJoin returns errors.Join of n distinct errors.
func wideJoin(n int) error {
	errs := make([]error, n)
	for i := range errs {
		errs[i] = fmt.Errorf("e%d", i)
	}

	return errors.Join(errs...)
}

// reply is what the GET /e handler of replyServer does, in every engine's
// idiom: it sets header with each key as written, bypassing the engine's
// canonicalisation where the engine allows it, then panics with panicValue
// when set, and otherwise reports err.
type reply struct {
	header     map[string]string
	err        error
	panicValue any
}

// replyEngines start a server of each engine whose GET /e handler does r.
var replyEngines = []struct {
	name  string
	start func(t *testing.T, r reply, opts ...srvr.Option) (string, *logBuffer)
}{
	{"net/http", func(t *testing.T, r reply, opts ...srvr.Option) (string, *logBuffer) {
		t.Helper()

		return netServer(t, func(g *srvr.Group) {
			g.GET("/e", func(w http.ResponseWriter, _ *http.Request) error {
				for k, v := range r.header {
					w.Header()[k] = []string{v}
				}
				if r.panicValue != nil {
					panic(r.panicValue)
				}

				return r.err
			})
		}, opts...)
	}},
	{"gin", func(t *testing.T, r reply, opts ...srvr.Option) (string, *logBuffer) {
		t.Helper()

		return ginServer(t, func(g *gin.RouterGroup) {
			g.GET("/e", func(c *gin.Context) {
				for k, v := range r.header {
					c.Writer.Header()[k] = []string{v}
				}
				if r.panicValue != nil {
					panic(r.panicValue)
				}
				_ = c.Error(r.err)
			})
		}, opts...)
	}},
	{"fiber", func(t *testing.T, r reply, opts ...srvr.Option) (string, *logBuffer) {
		t.Helper()

		return fiberServer(t, func(router fiber.Router) {
			router.Get("/e", func(c fiber.Ctx) error {
				for k, v := range r.header {
					c.Set(k, v)
				}
				if r.panicValue != nil {
					panic(r.panicValue)
				}

				return r.err
			})
		}, opts...)
	}},
}

// TestParity_UnwalkableError covers static-files task 7.3 (a) and (b), an
// error whose chain prblm cannot walk (prblm v0.3.0 ErrUnwalkableError): the
// client gets the safe 500 problem and srvr's access record and observers
// describe the error the handler reported, not prblm's note about it. Each
// engine and error pair is one row.
func TestParity_UnwalkableError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		err     error
		errType string // the record's error.type, unchecked when empty
		message string // the start of the record's exception.message
		// ginPanic, when set, is the panic value gin's c.Error raises for
		// err: it calls errors.As, which calls the panicking method, so on
		// gin the handler itself panics and the record is a panic record.
		ginPanic string
		// observed is the type of the observer's Err: the returned error,
		// or srvr's stand-in for a chain its own guard cannot walk
		// (engine.GuardChain).
		observed string
	}{
		{"Unwrap panics", fmt.Errorf("w: %w", &unwrapPanicError{}), "*paritytest.unwrapPanicError", "w: unwrap panics", "Unwrap panicked", "*engine.unresolvable"},
		{"Is panics", isPanicError{}, "paritytest.isPanicError", "is panics", "", "paritytest.isPanicError"},
		{"As panics", asPanicError{}, "paritytest.asPanicError", "as panics", "As panicked", "paritytest.asPanicError"},
		{"1001 levels deep", deepChain(1001), "paritytest.deepError", "deep", "", "paritytest.deepError"},
		{"join of 1001", wideJoin(1001), "", "e0\ne1\n", "", "*errors.joinError"},
	}

	for _, tt := range tests {
		for _, e := range replyEngines {
			t.Run(e.name+" "+tt.name, func(t *testing.T) {
				t.Parallel()

				obs := &resultObserver{}
				base, logs := e.start(t, reply{err: tt.err}, srvr.WithObserver(obs))

				res, body, err := send(t, http.MethodGet, base+"/e", nil)
				must(t, err)
				if res.StatusCode != http.StatusInternalServerError || strings.Contains(string(body), "panic") {
					t.Fatalf("response %d %s, want the safe 500 problem", res.StatusCode, body)
				}

				r := one(t, logs)
				got := obs.only(t)

				if e.name == "gin" && tt.ginPanic != "" {
					if r["msg"] != "http.request.failed" || r["error.kind"] != "panic" || r["exception.message"] != tt.ginPanic ||
						got.ErrorKind != srvr.ErrorKindPanic {
						t.Fatalf("record %v, observer %q, want gin's panic %q", brief(r), got.ErrorKind, tt.ginPanic)
					}

					return
				}

				msg, _ := r["exception.message"].(string)
				if r["msg"] != "http.request.failed" || r["level"] != "ERROR" || r["error.kind"] != nil || r["exception.type"] != nil ||
					(tt.errType != "" && r["error.type"] != tt.errType) || !strings.HasPrefix(msg, tt.message) {
					t.Fatalf("record %v, want http.request.failed at ERROR describing the returned error (%s %q)", brief(r), tt.errType, tt.message)
				}

				// Types are compared: errors.Is would call the panicking Is.
				if got.ErrorKind != srvr.ErrorKindError || fmt.Sprintf("%T", got.Err) != tt.observed {
					t.Fatalf("observer ErrorKind %q Err %T, want %q with a %s", got.ErrorKind, got.Err, srvr.ErrorKindError, tt.observed)
				}
			})
		}
	}
}

// TestParity_UnwalkablePanic covers static-files task 7.3 (a), a panic
// whose value prblm cannot walk: the client gets the safe 500 problem and
// the access record describes the handler's panic, not prblm's note.
func TestParity_UnwalkablePanic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		value     any
		valueType string // the record's exception.type
		// fiberError marks the row fibersrvr records as a returned error,
		// not a panic: its guard replaces fiberprblm's recovered panic,
		// whose chain it cannot walk, with srvr's stand-in, which hides
		// the panic (a defect from before prblm v0.3.0, pinned here and
		// recorded in openspec/changes/static-files/notes.md, "fiber: a
		// panic whose value's Unwrap panics is recorded as an error").
		fiberError bool
	}{
		{"value whose Unwrap panics", fmt.Errorf("w: %w", &unwrapPanicError{}), "*fmt.wrapError", true},
		{"value whose Is panics", isPanicError{}, "paritytest.isPanicError", false},
	}

	for _, tt := range tests {
		for _, e := range replyEngines {
			t.Run(e.name+" "+tt.name, func(t *testing.T) {
				t.Parallel()

				obs := &resultObserver{}
				base, logs := e.start(t, reply{panicValue: tt.value}, srvr.WithObserver(obs))

				res, body, err := send(t, http.MethodGet, base+"/e", nil)
				must(t, err)
				if res.StatusCode != http.StatusInternalServerError || strings.Contains(string(body), "panic") {
					t.Fatalf("response %d %s, want the safe 500 problem", res.StatusCode, body)
				}

				r := one(t, logs)
				if e.name == "fiber" && tt.fiberError {
					if r["msg"] != "http.request.failed" || r["level"] != "ERROR" || r["error.kind"] != nil {
						t.Fatalf("record %v, want the pinned fiber error record", brief(r))
					}

					return
				}

				if r["msg"] != "http.request.failed" || r["level"] != "ERROR" || r["error.kind"] != "panic" ||
					r["exception.type"] != tt.valueType {
					t.Fatalf("record %v, want a panic record with exception.type %s", brief(r), tt.valueType)
				}

				if got := obs.only(t); got.ErrorKind != srvr.ErrorKindPanic {
					t.Fatalf("observer ErrorKind %q, want %q", got.ErrorKind, srvr.ErrorKindPanic)
				}
			})
		}
	}
}

// TestParity_StatusHintBeforeSentinel covers static-files task 7.3 (c), a
// status hint and a sentinel side by side: the first in errors.Is order
// decides (prblm v0.3.0).
func TestParity_StatusHintBeforeSentinel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		status int
	}{
		{"hint first", errors.Join(prblm.WithStatus(errors.New("x"), http.StatusTeapot), prblm.ErrNotFound), http.StatusTeapot},
		{"sentinel first", errors.Join(prblm.ErrNotFound, prblm.WithStatus(errors.New("x"), http.StatusTeapot)), http.StatusNotFound},
		{"hint around a sentinel", prblm.WithStatus(prblm.ErrNotFound, http.StatusTeapot), http.StatusTeapot},
	}

	for _, tt := range tests {
		for _, e := range replyEngines {
			t.Run(e.name+" "+tt.name, func(t *testing.T) {
				t.Parallel()

				base, logs := e.start(t, reply{err: tt.err})

				res, _, err := send(t, http.MethodGet, base+"/e", nil)
				must(t, err)
				if res.StatusCode != tt.status || res.Header.Get("Content-Type") != "application/problem+json" {
					t.Fatalf("response %d %q, want a %d problem", res.StatusCode, res.Header.Get("Content-Type"), tt.status)
				}

				r := one(t, logs)
				if problem, _ := r["problem"].(map[string]any); r["msg"] != "http.request.failed" || problem["status"] != float64(tt.status) {
					t.Fatalf("record %v, want http.request.failed with problem.status %d", r, tt.status)
				}
			})
		}
	}
}

// TestParity_NonCanonicalHeaderMerge covers static-files task 7.3 (d), a
// handler that set Cache-Control or Vary under a non-canonical key before
// failing: the problem response carries the same header lines as with the
// canonical key, one Cache-Control line with the handler's value (prblm's
// merge rules; v0.3.0 folds the spellings).
func TestParity_NonCanonicalHeaderMerge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header map[string]string
		cache  string
		vary   []string
	}{
		{"canonical", map[string]string{"Cache-Control": "public, max-age=600"}, "public, max-age=600", []string{"Accept"}},
		{"lower case", map[string]string{"cache-control": "public, max-age=600"}, "public, max-age=600", []string{"Accept"}},
		{"canonical Vary", map[string]string{"Vary": "Origin"}, "no-store", []string{"Origin", "Accept"}},
		{"lower case Vary", map[string]string{"vary": "Origin"}, "no-store", []string{"Origin", "Accept"}},
	}

	for _, tt := range tests {
		for _, e := range replyEngines {
			t.Run(e.name+" "+tt.name, func(t *testing.T) {
				t.Parallel()

				base, _ := e.start(t, reply{header: tt.header, err: prblm.ErrNotFound})

				res, _, err := send(t, http.MethodGet, base+"/e", nil)
				must(t, err)

				cache, vary := res.Header.Values("Cache-Control"), res.Header.Values("Vary")
				if res.StatusCode != http.StatusNotFound || len(cache) != 1 || cache[0] != tt.cache || !equalLines(vary, tt.vary) {
					t.Fatalf("response %d Cache-Control %q Vary %q, want 404 [%s] %q", res.StatusCode, cache, vary, tt.cache, tt.vary)
				}
			})
		}
	}
}

// brief returns the attributes of an access record that tell what it
// describes.
func brief(r map[string]any) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"msg", "level", "error.kind", "error.type", "error.chain", "exception.type", "exception.message", "srvr.error.truncated"} {
		if v, ok := r[k]; ok {
			out[k] = v
		}
	}

	return out
}

// equalLines reports whether the header lines got equal want, each with
// its comma-separated members compared after trimming.
func equalLines(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}

	for i := range got {
		if strings.ReplaceAll(got[i], " ", "") != strings.ReplaceAll(want[i], " ", "") {
			return false
		}
	}

	return true
}
