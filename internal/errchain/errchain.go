// Package errchain holds srvr's error values (the sentinels, ConfigError and
// RouteErrorf) and the safe inspection of errors and faulty consumer values:
// chain walking bounded against cycles and panicking Unwrap, guards for
// typed nils and panicking Error or String methods, type and value text,
// and redaction of error text.
//
// It is internal to srvr's modules (internal/engine and the engine modules)
// and imports no other srvr package. GuardChain, the stand-in for an
// unwalkable chain, stays in internal/engine: its type name appears in
// telemetry.
package errchain

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

// Limits of a chain walk.
const (
	maxChainDepth  = 32
	maxChainErrors = 64
)

// Chain is a walked error chain.
type Chain struct {
	Types     []string
	Innermost string
	Truncated bool
}

// WalkChain walks err breadth-first through Unwrap() error and
// Unwrap() []error, at most maxChainDepth levels and maxChainErrors
// errors, recovering a panicking Unwrap. The innermost type follows
// Unwrap() error only; an error wrapping several errors is itself the
// innermost.
func WalkChain(err error) Chain {
	var c Chain

	level, linear := []error{err}, true
	for depth := 0; len(level) > 0; depth++ {
		if depth == maxChainDepth {
			c.Truncated = true

			break
		}

		var next []error

		for _, e := range level {
			if len(c.Types) == maxChainErrors {
				c.Truncated = true

				return c
			}

			name := TypeName(e)
			c.Types = append(c.Types, name)
			if linear {
				c.Innermost = name
			}

			children, ok := unwrap(e)
			if !ok {
				c.Truncated, linear = true, false

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

// ErrorText returns e.Error(), guarded against a typed nil and a panic.
func ErrorText(e error) (text string) {
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
// ErrorText. Unwrap keeps errors.Is and errors.As working on a chain that
// can be walked, and hides one that cycles or whose Unwrap panics, on
// which they would loop forever or crash in the caller of Start or
// Shutdown.
type guardedError struct {
	err      error
	walkable bool
}

// Guard returns err wrapped in a guardedError, its chain checked once.
func Guard(err error) error { return &guardedError{err: err, walkable: !HasCycle(err)} }

func (e *guardedError) Error() string { return ErrorText(e.err) }

func (e *guardedError) Unwrap() error {
	if !e.walkable {
		return nil
	}

	return e.err
}

// ValueText formats a panic value, guarded against a panicking String or
// Error method (fmt would print its own %!v(PANIC=...) text instead).
func ValueText(v any) string {
	text, _ := TryValueText(v)

	return text
}

// TryValueText returns the text of v like ValueText, and whether formatting
// it panicked.
func TryValueText(v any) (text string, panicked bool) {
	defer func() {
		if recover() != nil {
			text, panicked = PanicFormatting, true
		}
	}()

	switch x := v.(type) {
	case error:
		return ErrorText(x), false
	case fmt.Stringer:
		return x.String(), false
	default:
		return fmt.Sprint(v), false
	}
}

// PanicFormatting is the text of a value whose formatting panicked.
const PanicFormatting = "<panic formatting value>"

// maxCycleCheck bounds the errors HasCycle visits.
const maxCycleCheck = 1 << 12

// HasCycle reports whether walking err would never end: an error that
// unwraps to an error already on its path (compared with == when its type
// is comparable), or an Unwrap that panics. errors.Is and errors.As, which
// prblm uses to resolve an error, loop forever or crash on such a chain.
func HasCycle(err error) bool {
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

// FindInChain returns the first error of err's chain, breadth first, that
// match accepts, or nil. The walk visits at most maxChainErrors errors and
// survives a panicking Unwrap, because errors.Is and errors.As would loop
// on a cyclic chain or crash.
func FindInChain(err error, match func(error) bool) error {
	level := []error{err}
	for seen := 0; len(level) > 0 && seen < maxChainErrors; {
		var next []error

		for _, e := range level {
			if match(e) {
				return e
			}

			if seen++; seen == maxChainErrors {
				return nil
			}

			children, _ := unwrap(e)
			next = append(next, children...)
		}

		level = next
	}

	return nil
}

// Parts of the patterns RedactSecrets uses.
//
//nolint:gosec // G101: patterns that find secrets, not credentials.
const (
	// redactedValue replaces every masked value.
	redactedValue = "***"
	// secretSeparator is "=" or ":" between a key and its value, with
	// optional quotes and spaces. A colon needs a space or a quote after
	// it, so "session.go:42" in a stack trace and "host:port" stay
	// readable.
	secretSeparator = `"?\s*(?:=\s*"?|:\s+"?|:")`
	// secretValue is the value up to a space or delimiter, with an auth
	// scheme before it masked as part of it.
	secretValue = `(?:(?:bearer|basic|digest|negotiate|token)\s+)?[^\s;,&"'<>]+`
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

// secretWords are the words a secret-looking key contains, in lower case.
var secretWords = []string{"token", "key", "secret", "password", "passwd", "authorization", "cookie", "session", "credential"}

// secretKey is a key containing one of secretWords. \w is ASCII-only in
// RE2, so only the words' letters can match a non-ASCII rune.
var secretKey = `[\w.-]*(?:` + strings.Join(secretWords, "|") + `)[\w.-]*`

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

	if !mayHoldSecretKey(text) {
		return text // secretPair cannot match: skip its scan, the costly part
	}

	return secretPair.ReplaceAllString(text, "${1}"+redactedValue)
}

// foldedLetters are the only non-ASCII runes (?i) folds onto a letter of
// secretWords: the Kelvin sign onto k and the long s onto s.
var foldedLetters = []string{"\u212A", "\u017F"}

// mayHoldSecretKey reports whether secretPair may match text: text holds
// one of secretWords in any ASCII case, or a rune (?i) folds onto one of
// their letters.
func mayHoldSecretKey(text string) bool {
	for _, r := range foldedLetters {
		if strings.Contains(text, r) {
			return true
		}
	}

	for i := range len(text) {
		c := text[i]

		lower := c | 0x20 // an ASCII letter in lower case
		for _, w := range secretWords {
			if w[0] == lower && len(text)-i >= len(w) && strings.EqualFold(text[i:i+len(w)], w) {
				return true
			}
		}
	}

	return false
}

// The sentinel errors of every srvr engine, documented on the names
// package srvr re-exports them under (srvr.ErrConfig and so on), so
// errors.Is matches either name.
var (
	// ErrConfig is wrapped by every error construction returns for invalid
	// configuration, and by Start for a nil context or a faulty listener.
	ErrConfig = errors.New("srvr: invalid configuration")
	// ErrServerStarted is returned by Start and Register once the server
	// has been started.
	ErrServerStarted = errors.New("srvr: server already started")
	// ErrServerClosed is returned by Start and Register once the server
	// has stopped, and by a Start that Shutdown interrupted.
	ErrServerClosed = errors.New("srvr: server closed")
	// ErrRouteConfig is wrapped by the error Start returns when a
	// configurer panics or registers an invalid route, or a route check
	// fails.
	ErrRouteConfig = errors.New("srvr: invalid route configuration")
	// ErrShutdownHook is wrapped by every shutdown hook failure Start and
	// Shutdown return.
	ErrShutdownHook = errors.New("srvr: shutdown hook failed")
)

// IsNil reports whether v is nil or an interface holding a nil pointer,
// map, slice, function or channel (a typed nil).
func IsNil(v any) bool {
	if v == nil {
		return true
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface, reflect.UnsafePointer:
		return rv.IsNil()
	default:
		return false
	}
}

// ConfigError is the error construction returns for one invalid field:
// errors.As reads Field, Value and Reason, and errors.Is matches
// [ErrConfig].
type ConfigError struct {
	// Field names the setting, such as "Level" or "Listener".
	Field string
	// Value is the offending value's text.
	Value string
	// Reason says what is wrong with it.
	Reason string
}

func (e *ConfigError) Error() string {
	return ErrConfig.Error() + ": " + e.Field + " " + e.Value + ": " + e.Reason
}

// Unwrap returns [ErrConfig].
func (e *ConfigError) Unwrap() error { return ErrConfig }

// NewConfigError returns a *ConfigError naming field and value, whose text
// is read through ValueText: a value may be consumer code.
func NewConfigError(field string, value any, reason string) error {
	return &ConfigError{Field: field, Value: ValueText(value), Reason: reason}
}

// RouteErrorf returns an error wrapping [ErrRouteConfig] with the message
// format and args describe.
func RouteErrorf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrRouteConfig}, args...)...)
}
