package engine

import (
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/kartaladev/prblm"
)

// TestTriage_StackCap covers the exact-size rows of triage-error-records /
// Scenario: Oversized stack.
func TestTriage_StackCap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		size int
		cut  bool
	}{
		{"at the cap", 32768, false},
		{"one past the cap", 32769, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, cut := cutString(strings.Repeat("s", tt.size), maxStackBytes)
			if cut != tt.cut || len(strings.TrimSuffix(got, truncatedSuffix)) > maxStackBytes {
				t.Errorf("%d bytes: cut %v (len %d), want cut %v", tt.size, cut, len(got), tt.cut)
			}
		})
	}
}

// FuzzErrorChain checks that walking and describing any chain of wrapped
// and joined errors terminates within the caps and never panics.
func FuzzErrorChain(f *testing.F) {
	f.Add(uint8(3), uint8(2), "boom")
	f.Add(uint8(100), uint8(0), "")

	f.Fuzz(func(t *testing.T, depth, width uint8, msg string) {
		err := errors.New(msg)
		for i := range int(depth) {
			if width > 0 && i%3 == 0 {
				errs := make([]error, int(width)%8+1)
				for j := range errs {
					errs[j] = err
				}
				err = errors.Join(errs...)
			} else {
				err = fmt.Errorf("w%d: %w", i, err)
			}
		}

		c := walkChain(err)
		if len(c.types) == 0 || len(c.types) > maxChainErrors {
			t.Fatalf("chain of %d types", len(c.types))
		}
		_ = errorText(err)
		_ = hasCycle(err)
	})
}

// TestUnresolvable: the stand-in for a cyclic chain has a fixed text and
// wraps nothing, so prblm renders the safe 500.
func TestUnresolvable(t *testing.T) {
	t.Parallel()

	u := GuardChain(selfError{})
	if u.Error() != "srvr: error chain cannot be resolved" || errors.Unwrap(u) != nil {
		t.Fatalf("guardChain = %v", u)
	}
	// otelsrvr and otelsetup tell the stand-in from err by its type.
	if _, ok := u.(*unresolvable); !ok { //nolint:errorlint // the stand-in itself, not one it wraps
		t.Fatalf("GuardChain(selfError{}) is %T, want *unresolvable", u)
	}
	if GuardChain(nil) != nil {
		t.Fatal("GuardChain(nil) != nil")
	}
}

type selfError struct{}

func (selfError) Error() string   { return "self" }
func (e selfError) Unwrap() error { return e }

// TestTopFrame skips runtime, prblm and srvr frames after the panic.
func TestTopFrame(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stack string
		want  string
	}{
		{
			name:  "first consumer frame after panic",
			stack: "goroutine 1 [running]:\nruntime/debug.Stack()\n\t/x.go:1\ngithub.com/kartaladev/prblm.(*Handler).recoverPanic(0x1)\n\t/y.go:2\npanic({0x1, 0x2})\n\t/z.go:3\nnet/http.HandlerFunc.ServeHTTP(0x1)\n\t/a.go:4\nmain.handler(0x1)\n\t/b.go:5\n",
			want:  "main.handler",
		},
		{name: "no panic frame", stack: "no panic here\n", want: ""},
		{name: "no trailing newline", stack: "panic({0x1})\n\t/z.go:3\nmain.h()", want: "main.h"},
		{name: "parenthesis first keeps the line", stack: "panic({0x1})\n(odd)\n", want: "(odd)"},
		{name: "no parenthesis keeps the line", stack: "panic({0x1})\nmain.h\n", want: "main.h"},
		{name: "empty", stack: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := topFrame([]byte(tt.stack)); got != tt.want {
				t.Fatalf("topFrame = %q, want %q", got, tt.want)
			}
		})
	}
}

// unhashableError has a comparable type but holds a slice, so hashing it
// panics.
type unhashableError struct{ v any }

func (unhashableError) Error() string { return "unhashable" }
func (unhashableError) Unwrap() error { return errors.New("inner") }

// TestHasCycle_Unhashable: an error whose type is comparable but whose
// value cannot be hashed does not crash the cycle check.
func TestHasCycle_Unhashable(t *testing.T) {
	t.Parallel()

	if hasCycle(unhashableError{v: []int{1}}) {
		t.Fatal("reported a cycle")
	}
}

// adapterPanicError stands in for ginprblm's panickedError and fiberprblm's
// recoveredError: an adapter's own type that unwraps to *prblm.PanicError.
type adapterPanicError struct{ pe *prblm.PanicError }

func (e *adapterPanicError) Error() string { return "panic: " + e.pe.Error() }
func (e *adapterPanicError) Unwrap() error { return e.pe }

// TestFailure_PanicThroughWrapper: a panic reported to srvr's hook inside
// an adapter's wrapper is a panic: ERROR whatever the status, and the
// triage attributes describe the panic, not a returned error (design D10).
func TestFailure_PanicThroughWrapper(t *testing.T) {
	t.Parallel()

	pe := &prblm.PanicError{Value: "boom", Stack: []byte("goroutine 1 [running]:\npanic({0x1})\n\t/z.go:3\nmain.h()\n\t/b.go:5\n")}

	tests := []struct {
		name string
		err  error
	}{
		{"prblm's own value", pe},
		{"an adapter's wrapper", &adapterPanicError{pe: pe}},
	}

	core, err := ResolveConfig(nil)
	if err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFailure(tt.err, nil, false)
			if got := f.level(200); got != slog.LevelError {
				t.Errorf("level = %v, want ERROR", got)
			}

			attrs := map[string]string{}
			for _, a := range core.triageAttrs("GET", &RequestState{route: "/x"}, f) {
				attrs[a.Key] = a.Value.String()
			}
			if attrs[keyErrorKind] != errorKindPanic || attrs[keyExceptionType] != "string" || attrs[keyFingerprint] == "" {
				t.Errorf("triage attrs = %v, want a panic description", attrs)
			}
		})
	}
}

// TestPanicOf_Bounded: a cyclic or panicking chain is not a panic and the
// lookup ends.
func TestPanicOf_Bounded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
	}{
		{"cyclic", selfError{}},
		{"panicking Unwrap", panicUnwrapError{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, ok := panicOf(tt.err); ok {
				t.Error("reported a panic")
			}
		})
	}
}

type panicUnwrapError struct{}

func (panicUnwrapError) Error() string { return "x" }
func (panicUnwrapError) Unwrap() error { panic("unwrap") }

// TestRedactSecrets pins what the default redactor keeps readable beyond
// the spec rows: stack frames and host:port pairs whose names contain a
// secret-looking word, and compact JSON pairs, which it masks.
func TestRedactSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, in, want string }{
		{"stack frame", "\t/usr/local/go/src/net/http/cookie.go:42 +0x1c", "\t/usr/local/go/src/net/http/cookie.go:42 +0x1c"},
		{"host and port", `Get "http://session-api.local:8443/x": refused`, `Get "http://session-api.local:8443/x": refused`},
		{"compact JSON pair", `body {"token":"abc","n":1}`, `body {"token":"***","n":1}`},
		{"spaced equals", "password = hunter2", "password = ***"},
		{"basic auth", "authorization: Basic dXNlcjpwYXNz", "authorization: ***"},
		// (?i) folds Unicode: the Kelvin sign matches k and the long s
		// matches s, so these keys are masked too.
		{"key with a Kelvin sign", "\u212Aey=hunter2", "\u212Aey=***"},
		{"session with a long s", "\u017Fession_id=7f3a", "\u017Fession_id=***"},
		{"upper case key", "API_TOKEN=abc", "API_TOKEN=***"},
		{"keyword ends the text", "no secret here but a token", "no secret here but a token"},
		{"no keyword", "store: find user 42: connection refused", "store: find user 42: connection refused"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := RedactSecrets(tt.in); got != tt.want {
				t.Fatalf("RedactSecrets(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// sliceError is an error whose type is not comparable.
type sliceError []string

func (e sliceError) Error() string { return "slice" }

// ifaceError is comparable, but a value holding a slice cannot be hashed.
type ifaceError struct{ v any }

func (ifaceError) Error() string { return "iface" }

// TestMarkSeen: a hashable error is tracked and reported on its second
// sighting; nil and an error that cannot be a map key are never tracked
// and never reported, instead of panicking.
func TestMarkSeen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantSecond bool
		wantSeen   int
	}{
		{"comparable", selfError{}, true, 1},
		{"nil", nil, false, 0},
		{"type not comparable", sliceError{"a"}, false, 0},
		{"comparable type, unhashable value", ifaceError{v: []int{1}}, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			seen := map[any]bool{}
			if markSeen(seen, tt.err) {
				t.Fatal("first sighting reported as a duplicate")
			}

			if got := markSeen(seen, tt.err); got != tt.wantSecond {
				t.Fatalf("second sighting = %v, want %v", got, tt.wantSecond)
			}

			if len(seen) != tt.wantSeen {
				t.Fatalf("seen holds %d errors, want %d", len(seen), tt.wantSeen)
			}
		})
	}
}

// TestRequestState_RouteAccessors: the engines record the matched route
// and its handler's location through the setters, and read them back.
func TestRequestState_RouteAccessors(t *testing.T) {
	t.Parallel()

	var req RequestState
	if req.Matched() || req.Route() != "" || req.Location() != (Location{}) {
		t.Fatalf("zero state = %q, %+v, matched %v; want none", req.Route(), req.Location(), req.Matched())
	}

	loc := Location{function: "main.Users.Get", file: "users.go", line: 7}
	req.SetRoute("/users/{id}")
	req.SetLocation(loc)

	if !req.Matched() || req.Route() != "/users/{id}" || req.Location() != loc {
		t.Fatalf("state = %q, %+v, matched %v; want /users/{id}, %+v, true", req.Route(), req.Location(), req.Matched(), loc)
	}
}

// TestFoldedLetters: foldedLetters holds every non-ASCII rune that (?i)
// folds onto a letter of secretWords, so the pre-scan in RedactSecrets
// never skips text secretPair would match.
func TestFoldedLetters(t *testing.T) {
	t.Parallel()

	for _, w := range secretWords {
		for _, c := range w {
			for r := unicode.SimpleFold(c); r != c; r = unicode.SimpleFold(r) {
				if r >= utf8.RuneSelf && !slices.Contains(foldedLetters, string(r)) {
					t.Errorf("%U folds onto %q of %q but is not in foldedLetters", r, c, w)
				}
			}
		}
	}
}
