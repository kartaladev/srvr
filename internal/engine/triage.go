package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/internal/errchain"
)

// Limits of the error records srvr writes.
const (
	maxMessageBytes = 8 << 10
	maxStackBytes   = 32 << 10
)

// errorKindPanic is error.type and error.kind for a recovered panic: one
// value per kind of failure on every signal.
const errorKindPanic = "panic"

// MethodOther is the [MethodLabel] of every method outside the set the
// OpenTelemetry HTTP semantic conventions name.
const MethodOther = "_OTHER"

// MethodLabel returns method when the OpenTelemetry HTTP semantic
// conventions name it (CONNECT, DELETE, GET, HEAD, OPTIONS, PATCH, POST,
// PUT, QUERY, TRACE; case-sensitive), and [MethodOther] for any other
// method. Fingerprints and metrics use it so a client choosing methods
// cannot create unbounded groups or label values.
func MethodLabel(method string) string {
	switch method {
	case http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead, http.MethodOptions,
		http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace, "QUERY":
		return method
	default:
		return MethodOther
	}
}

// redact applies the configured redactor (errchain.RedactSecrets by default) to
// text; a redactor that panics gives "[redaction failed]".
func (c *Core) redact(text string) (out string) {
	defer func() {
		if recover() != nil {
			out = "[redaction failed]"
		}
	}()

	return c.settings.Redactor(text)
}

// exceptionMessage returns the exception.message attribute of every record
// srvr builds itself, request failures and lifecycle events alike: text
// passed through the consumer's redactor, then cut to maxMessageBytes and
// marked as srvr's own text so MaxAttrBytes does not cut it again.
func (c *Core) exceptionMessage(text string) slog.Attr {
	msg, _ := cutString(c.redact(text), maxMessageBytes)

	return ownAttr(keyExceptionMsg, slog.StringValue(msg))
}

// triageAttrs returns the attributes that describe a failure for triage.
func (c *Core) triageAttrs(method string, req *RequestState, fail *failure) []slog.Attr {
	var attrs []slog.Attr

	if fail.panic != nil {
		attrs = c.panicAttrs(method, req, fail.panic)
	} else if u, ok := fail.err.(*unresolvable); ok { //nolint:errorlint // the stand-in srvr made is the failure itself.
		attrs = c.errorAttrs(method, req, u.err)
	} else {
		attrs = c.errorAttrs(method, req, fail.err)
	}

	if loc := req.location; loc.function != "" {
		attrs = append(attrs, slog.String(keyFunction, loc.function))
		if loc.file != "" {
			attrs = append(attrs, slog.String(keyFilePath, loc.file), slog.Int(keyLineNumber, loc.line))
		}
	}

	return append(attrs, c.serviceAttrs...)
}

// errorAttrs describes a returned error.
func (c *Core) errorAttrs(method string, req *RequestState, err error) []slog.Attr {
	chain := errchain.WalkChain(err)

	attrs := []slog.Attr{
		slog.String(keyErrorType, chain.Innermost),
		c.exceptionMessage(errchain.ErrorText(err)),
		ownAttr(keyErrorChain, slog.AnyValue(chain.Types)),
		slog.String(keyFingerprint, fingerprint(append([]string{MethodLabel(method), req.route}, chain.Types...)...)),
	}

	if chain.Truncated {
		attrs = append(attrs, slog.Bool(keyErrorTruncated, true))
	}

	return attrs
}

// panicAttrs describes a recovered panic.
func (c *Core) panicAttrs(method string, req *RequestState, pe *prblm.PanicError) []slog.Attr {
	stack, stackCut := cutString(c.redact(string(pe.Stack)), maxStackBytes)
	valueType := errchain.TypeName(pe.Value)

	return append(panicKindAttrs(),
		slog.String(keyExceptionType, valueType),
		c.exceptionMessage(errchain.ValueText(pe.Value)),
		ownAttr(keyStacktrace, slog.StringValue(stack)),
		slog.Bool(keyStackTruncated, stackCut),
		slog.String(keyFingerprint,
			fingerprint(MethodLabel(method), req.route, errorKindPanic, valueType, topFrame(pe.Stack))),
	)
}

// panicKindAttrs are error.kind and error.type of any panic.
func panicKindAttrs() []slog.Attr {
	return []slog.Attr{slog.String(keyErrorKind, errorKindPanic), slog.String(keyErrorType, errorKindPanic)}
}

// abortedAttrs describes a request aborted with http.ErrAbortHandler.
func (c *Core) abortedAttrs() []slog.Attr {
	return slices.Concat(panicKindAttrs(), []slog.Attr{slog.Bool(keyAborted, true)}, c.serviceAttrs)
}

// unresolvable stands in for an error whose chain has a cycle when it is
// resolved into a problem, so the request gets the safe 500 problem; the
// access record still describes the original error.
type unresolvable struct{ err error }

func (u *unresolvable) Error() string { return "srvr: error chain cannot be resolved" }

// GuardChain makes err safe to resolve into a problem: prblm resolves an
// error with errors.Is and errors.As, which loop forever or crash on a
// chain that cycles or whose Unwrap panics. It returns err unchanged when
// its chain can be walked, and otherwise an error of srvr's own unexported
// type, resolved as the safe 500 problem (callers may tell the two apart
// by type); the access record still describes err itself.
func GuardChain(err error) error {
	if err != nil && errchain.HasCycle(err) {
		return &unresolvable{err: err}
	}

	return err
}

// panicOf returns the recovered panic err reports: err itself or, for a
// prblm adapter that hands srvr its own wrapper type, the
// *prblm.PanicError the wrapper unwraps to. The walk is bounded like
// walkChain and survives a panicking Unwrap, because errors.As would loop
// on a cyclic chain. srvr's stand-in for an unwalkable chain is seen
// through, so [GuardChain] does not hide a recovered panic whose value's
// Unwrap panics.
func panicOf(err error) (*prblm.PanicError, bool) {
	if u, ok := err.(*unresolvable); ok { //nolint:errorlint // the stand-in srvr made, not one it wraps.
		err = u.err
	}

	found := errchain.FindInChain(err, func(e error) bool {
		_, ok := e.(*prblm.PanicError) //nolint:errorlint // this walk is the bounded errors.As.

		return ok
	})
	pe, ok := found.(*prblm.PanicError) //nolint:errorlint // found by the walk above.

	return pe, ok
}

// fingerprint returns 16 hex characters derived from parts, never from
// message text.
func fingerprint(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))

	return hex.EncodeToString(sum[:8])
}

// topFrame returns the first function in a panic stack after the panic
// that belongs to the application: not runtime, prblm or srvr.
func topFrame(stack []byte) string {
	afterPanic := false
	for line := range strings.Lines(string(stack)) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" || line[0] == '\t' || strings.HasPrefix(line, "goroutine ") {
			continue
		}

		fn := line
		if before, _, found := strings.CutLast(fn, "("); found && before != "" {
			fn = before
		}

		if fn == "panic" {
			afterPanic = true

			continue
		}

		if afterPanic && !isLibraryFrame(fn) {
			return fn
		}
	}

	return ""
}

// isLibraryFrame reports whether fn belongs to the runtime, net/http,
// prblm or any package of the srvr module tree (engine modules included).
func isLibraryFrame(fn string) bool {
	for _, prefix := range []string{
		"runtime.", "runtime/debug.", "github.com/kartaladev/prblm.",
		"github.com/kartaladev/srvr.", "github.com/kartaladev/srvr/", "net/http.",
	} {
		if strings.HasPrefix(fn, prefix) {
			return true
		}
	}

	return false
}
