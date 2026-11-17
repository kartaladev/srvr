package srvr

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"reflect"
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

// WithErrorRedactor applies fn to exception.message and
// exception.stacktrace before they are logged, for example to mask e-mail
// addresses in database errors. A redactor that panics replaces the
// attribute with "[redaction failed]", never the unredacted text. fn must
// not be nil.
func WithErrorRedactor(fn func(string) string) Option {
	return func(s *settings) {
		if fn == nil {
			s.errs = append(s.errs, configError("ErrorRedactor", "<nil>", "is nil"))

			return
		}

		s.redactor = fn
	}
}

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

			name := typeName(e)
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
	if isNil(e) {
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

func typeName(v any) string { return fmt.Sprintf("%T", v) }

// errorText returns e.Error(), guarded against a typed nil and a panic.
func errorText(e error) (text string) {
	if isNil(e) {
		return "<nil " + typeName(e) + ">"
	}

	defer func() {
		if recover() != nil {
			text = "<panic in Error(): " + typeName(e) + ">"
		}
	}()

	return e.Error()
}

// valueText formats a panic value, guarded against a panicking String or
// Error method (fmt would print its own %!v(PANIC=...) text instead).
func valueText(v any) (text string) {
	defer func() {
		if recover() != nil {
			text = "<panic formatting value>"
		}
	}()

	switch x := v.(type) {
	case error:
		return x.Error()
	case fmt.Stringer:
		return x.String()
	default:
		return fmt.Sprint(v)
	}
}

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

// guardChain returns err, or an *unresolvable wrapping nothing when err's
// chain has a cycle.
func guardChain(err error) error {
	if err != nil && hasCycle(err) {
		return &unresolvable{err: err}
	}

	return err
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

// isLibraryFrame reports whether fn belongs to the runtime, prblm or srvr.
func isLibraryFrame(fn string) bool {
	for _, prefix := range []string{"runtime.", "runtime/debug.", "github.com/kartaladev/prblm.", "github.com/kartaladev/srvr.", "github.com/kartaladev/srvr/internal/", "net/http."} {
		if strings.HasPrefix(fn, prefix) {
			return true
		}
	}

	return false
}

// redact applies the consumer's redactor to text.
func (s *Server) redact(text string) (out string) {
	if s.settings.redactor == nil {
		return text
	}

	defer func() {
		if recover() != nil {
			out = "[redaction failed]"
		}
	}()

	return s.settings.redactor(text)
}

// triageAttrs returns the attributes that describe a failure for triage.
func (s *Server) triageAttrs(method string, req *request, fail *failure) []slog.Attr {
	var attrs []slog.Attr

	switch err := fail.err.(type) { //nolint:errorlint // the failure itself is the panic or stand-in srvr made.
	case *prblm.PanicError:
		attrs = s.panicAttrs(method, req, err)
	case *unresolvable:
		attrs = s.errorAttrs(method, req, err.err)
	default:
		attrs = s.errorAttrs(method, req, err)
	}

	if loc := req.location; loc.function != "" {
		attrs = append(attrs, slog.String(keyFunction, loc.function))
		if loc.file != "" {
			attrs = append(attrs, slog.String(keyFilePath, loc.file), slog.Int(keyLineNumber, loc.line))
		}
	}

	return append(attrs, s.serviceAttrs...)
}

// errorAttrs describes a returned error.
func (s *Server) errorAttrs(method string, req *request, err error) []slog.Attr {
	c := walkChain(err)
	msg, _ := cutString(s.redact(errorText(err)), maxMessageBytes)

	attrs := []slog.Attr{
		slog.String(keyErrorType, c.innermost),
		slog.Any(keyExceptionMsg, ownText(msg)),
		slog.Any(keyErrorChain, c.types),
		slog.String(keyFingerprint, fingerprint(append([]string{method, req.route}, c.types...)...)),
	}

	if c.truncated {
		attrs = append(attrs, slog.Bool(keyErrorTruncated, true))
	}

	return attrs
}

// panicAttrs describes a recovered panic.
func (s *Server) panicAttrs(method string, req *request, pe *prblm.PanicError) []slog.Attr {
	msg, _ := cutString(s.redact(valueText(pe.Value)), maxMessageBytes)
	stack, stackCut := cutString(s.redact(string(pe.Stack)), maxStackBytes)
	valueType := typeName(pe.Value)

	return append(panicKindAttrs(),
		slog.String(keyExceptionType, valueType),
		slog.Any(keyExceptionMsg, ownText(msg)),
		slog.Any(keyStacktrace, ownText(stack)),
		slog.Bool(keyStackTruncated, stackCut),
		slog.String(keyFingerprint, fingerprint(method, req.route, errorKindPanic, valueType, topFrame(pe.Stack))),
	)
}

// panicKindAttrs are error.kind and error.type of any panic.
func panicKindAttrs() []slog.Attr {
	return []slog.Attr{slog.String(keyErrorKind, errorKindPanic), slog.String(keyErrorType, errorKindPanic)}
}

// abortedAttrs describes a request aborted with http.ErrAbortHandler.
func (s *Server) abortedAttrs() []slog.Attr {
	return slices.Concat(panicKindAttrs(), []slog.Attr{slog.Bool(keyAborted, true)}, s.serviceAttrs)
}
