package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/kartaladev/prblm"
)

// Limits of the error records srvr writes.
const (
	maxChainDepth   = 32
	maxChainErrors  = 64
	maxMessageBytes = 8 << 10
	maxStackBytes   = 32 << 10
)

// errorKindPanic is error.type and error.kind for a recovered panic: one
// value per kind of failure on every signal.
const errorKindPanic = "panic"

// errorChain is a walked error chain.
type errorChain struct {
	types     []string
	innermost string
	truncated bool
}

// walkChain walks err breadth-first through Unwrap() error and
// Unwrap() []error, at most maxChainDepth levels and maxChainErrors
// errors, recovering a panicking Unwrap. The innermost type follows
// Unwrap() error only; an error wrapping several errors is itself the
// innermost.
func walkChain(err error) errorChain {
	var c errorChain

	level, linear := []error{err}, true
	for depth := 0; len(level) > 0; depth++ {
		if depth == maxChainDepth {
			c.truncated = true

			break
		}

		var next []error

		for _, e := range level {
			if len(c.types) == maxChainErrors {
				c.truncated = true

				return c
			}

			name := TypeName(e)
			c.types = append(c.types, name)
			if linear {
				c.innermost = name
			}

			children, ok := unwrap(e)
			if !ok {
				c.truncated, linear = true, false

				continue
			}

			linear = linear && len(children) <= 1
			next = append(next, children...)
		}

		level = next
	}

	return c
}

// unwrap returns the errors e wraps, and false when Unwrap panicked. A
// typed nil is not unwrapped: its methods may dereference it.
func unwrap(e error) (children []error, ok bool) {
	if IsNil(e) {
		return nil, true
	}

	defer func() {
		if recover() != nil {
			children, ok = nil, false
		}
	}()

	switch u := e.(type) { //nolint:errorlint // e itself is unwrapped, one level at a time.
	case interface{ Unwrap() error }:
		if c := u.Unwrap(); c != nil {
			return []error{c}, true
		}
	case interface{ Unwrap() []error }:
		for _, c := range u.Unwrap() {
			if c != nil {
				children = append(children, c)
			}
		}
	}

	return children, true
}

// TypeName returns the Go type of v, as %T prints it.
func TypeName(v any) string { return fmt.Sprintf("%T", v) }

// errorText returns e.Error(), guarded against a typed nil and a panic.
func errorText(e error) (text string) {
	if IsNil(e) {
		return "<nil " + TypeName(e) + ">"
	}

	defer func() {
		if recover() != nil {
			text = "<panic in Error(): " + TypeName(e) + ">"
		}
	}()

	return e.Error()
}

// guardedError wraps a consumer error so that fmt, which guards a
// panicking Error method only one level deep, reads its text through
// errorText. Unwrap keeps errors.Is and errors.As working on a chain that
// can be walked, and hides one that cycles or whose Unwrap panics, on
// which they would loop forever or crash in the caller of Start or
// Shutdown.
type guardedError struct {
	err      error
	walkable bool
}

// guard returns err wrapped in a guardedError, its chain checked once.
func guard(err error) *guardedError { return &guardedError{err: err, walkable: !hasCycle(err)} }

func (e *guardedError) Error() string { return errorText(e.err) }

func (e *guardedError) Unwrap() error {
	if !e.walkable {
		return nil
	}

	return e.err
}

// ValueText formats a panic value, guarded against a panicking String or
// Error method (fmt would print its own %!v(PANIC=...) text instead).
func ValueText(v any) string {
	text, _ := valueText(v)

	return text
}

// valueText returns the text of v like ValueText, and whether formatting
// it panicked.
func valueText(v any) (text string, panicked bool) {
	defer func() {
		if recover() != nil {
			text, panicked = panicFormatting, true
		}
	}()

	switch x := v.(type) {
	case error:
		return errorText(x), false
	case fmt.Stringer:
		return x.String(), false
	default:
		return fmt.Sprint(v), false
	}
}

// panicFormatting is the text of a value whose formatting panicked.
const panicFormatting = "<panic formatting value>"

// maxCycleCheck bounds the errors hasCycle visits.
const maxCycleCheck = 1 << 12

// hasCycle reports whether walking err would never end: an error that
// unwraps to an error already on its path (compared with == when its type
// is comparable), or an Unwrap that panics. errors.Is and errors.As, which
// prblm uses to resolve an error, loop forever or crash on such a chain.
func hasCycle(err error) bool {
	if !wraps(err) {
		return false // the common case: nothing to walk, nothing allocated
	}

	seen := map[any]bool{}
	stack := []error{err}

	for visited := 0; len(stack) > 0 && visited < maxCycleCheck; visited++ {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if markSeen(seen, e) {
			return true
		}

		children, ok := unwrap(e)
		if !ok {
			return true
		}

		stack = append(stack, children...)
	}

	return len(stack) > 0
}

// wraps reports whether err has an Unwrap method.
func wraps(err error) bool {
	switch err.(type) { //nolint:errorlint // only err's own methods matter here.
	case interface{ Unwrap() error }, interface{ Unwrap() []error }:
		return true
	default:
		return false
	}
}

// markSeen records e in seen and reports whether it was there already. An
// error whose type is comparable but whose value cannot be hashed (it holds
// a slice in an interface field) is not tracked.
func markSeen(seen map[any]bool, e error) (dup bool) {
	if t := reflect.TypeOf(e); t == nil || !t.Comparable() {
		return false
	}

	defer func() {
		if recover() != nil {
			dup = false
		}
	}()

	dup = seen[e]
	seen[e] = true

	return dup
}

// unresolvable stands in for an error whose chain has a cycle when it is
// resolved into a problem, so the request gets the safe 500 problem; the
// access record still describes the original error.
type unresolvable struct{ err error }

func (u *unresolvable) Error() string { return "srvr: error chain cannot be resolved" }

// GuardChain returns err, or an *unresolvable wrapping nothing when err's
// chain has a cycle.
func GuardChain(err error) error {
	if err != nil && hasCycle(err) {
		return &unresolvable{err: err}
	}

	return err
}

// panicOf returns the recovered panic err reports: err itself or, for a
// prblm adapter that hands srvr its own wrapper type, the
// *prblm.PanicError the wrapper unwraps to. The walk is bounded like
// walkChain and survives a panicking Unwrap, because errors.As would loop
// on a cyclic chain.
func panicOf(err error) (*prblm.PanicError, bool) {
	level := []error{err}
	for seen := 0; len(level) > 0 && seen < maxChainErrors; {
		var next []error

		for _, e := range level {
			if pe, ok := e.(*prblm.PanicError); ok { //nolint:errorlint // this walk is the bounded errors.As.
				return pe, true
			}

			if seen++; seen == maxChainErrors {
				return nil, false
			}

			children, _ := unwrap(e)
			next = append(next, children...)
		}

		level = next
	}

	return nil, false
}

// fingerprint returns 16 hex characters derived from parts, never from
// message text.
func fingerprint(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))

	return hex.EncodeToString(sum[:8])
}

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

// topFrame returns the first function in a panic stack after the panic
// that belongs to the application: not runtime, prblm or srvr.
func topFrame(stack []byte) string {
	lines := strings.Split(string(stack), "\n")

	afterPanic := false
	for _, line := range lines {
		if line == "" || line[0] == '\t' || strings.HasPrefix(line, "goroutine ") {
			continue
		}

		fn := line
		if i := strings.LastIndexByte(fn, '('); i > 0 {
			fn = fn[:i]
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
	for _, prefix := range []string{"runtime.", "runtime/debug.", "github.com/kartaladev/prblm.", "github.com/kartaladev/srvr.", "github.com/kartaladev/srvr/", "net/http."} {
		if strings.HasPrefix(fn, prefix) {
			return true
		}
	}

	return false
}

// Parts of the patterns RedactSecrets uses.
const (
	// redactedValue replaces every masked value.
	redactedValue = "***"
	// secretKey is a key containing a secret-looking word.
	secretKey = `[\w.-]*(?:token|key|secret|password|passwd|authorization|cookie|session|credential)[\w.-]*` //nolint:gosec // G101: a pattern that finds secrets, not a credential
	// secretSeparator is "=" or ":" between a key and its value, with
	// optional quotes and spaces. A colon needs a space or a quote after
	// it, so "session.go:42" in a stack trace and "host:port" stay
	// readable.
	secretSeparator = `"?\s*(?:=\s*"?|:\s+"?|:")` //nolint:gosec // G101: a pattern that finds secrets, not a credential
	// secretValue is the value up to a space or delimiter, with an auth
	// scheme before it masked as part of it.
	secretValue = `(?:(?:bearer|basic|digest|negotiate|token)\s+)?[^\s;,&"'<>]+` //nolint:gosec // G101: a pattern that finds secrets, not a credential
)

var (
	// queryValue matches a URL query parameter with a non-empty name and
	// value ("?api_key=SECRET", "&amount=5"); group 1 keeps "?name=".
	queryValue = regexp.MustCompile(`([?&][^=&?#\s"']+=)[^&#\s"']+`)
	// secretPair matches a pair whose key contains a secret-looking word
	// ("password=hunter2", "Authorization: Bearer abc", "session_id=7f3a",
	// `"token":"x"`); group 1 keeps the key and separator.
	secretPair = regexp.MustCompile(`(?i)(` + secretKey + secretSeparator + `)` + secretValue)
)

// RedactSecrets returns text with the values of URL query parameters and
// of secret-looking key=value or "key: value" pairs (keys containing
// token, key, secret, password, passwd, authorization, cookie, session or
// credential, in any case) replaced by "***". It is srvr's default error
// redactor: error text is free-form, so a pattern over the text is the
// only general approach; it masks too much rather than too little, and a
// consumer needing more installs its own redactor. Both patterns are RE2,
// linear in the length of text.
func RedactSecrets(text string) string {
	if strings.ContainsAny(text, "?&") {
		text = queryValue.ReplaceAllString(text, "${1}"+redactedValue)
	}

	return secretPair.ReplaceAllString(text, "${1}"+redactedValue)
}

// redact applies the configured redactor (RedactSecrets by default) to
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
	chain := walkChain(err)

	attrs := []slog.Attr{
		slog.String(keyErrorType, chain.innermost),
		c.exceptionMessage(errorText(err)),
		ownAttr(keyErrorChain, slog.AnyValue(chain.types)),
		slog.String(keyFingerprint, fingerprint(append([]string{MethodLabel(method), req.route}, chain.types...)...)),
	}

	if chain.truncated {
		attrs = append(attrs, slog.Bool(keyErrorTruncated, true))
	}

	return attrs
}

// panicAttrs describes a recovered panic.
func (c *Core) panicAttrs(method string, req *RequestState, pe *prblm.PanicError) []slog.Attr {
	stack, stackCut := cutString(c.redact(string(pe.Stack)), maxStackBytes)
	valueType := TypeName(pe.Value)

	return append(panicKindAttrs(),
		slog.String(keyExceptionType, valueType),
		c.exceptionMessage(ValueText(pe.Value)),
		ownAttr(keyStacktrace, slog.StringValue(stack)),
		slog.Bool(keyStackTruncated, stackCut),
		slog.String(keyFingerprint, fingerprint(MethodLabel(method), req.route, errorKindPanic, valueType, topFrame(pe.Stack))),
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
