package errchain

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"unicode"
	"unicode/utf8"
)

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

		c := WalkChain(err)
		if len(c.Types) == 0 || len(c.Types) > maxChainErrors {
			t.Fatalf("chain of %d types", len(c.Types))
		}
		_ = ErrorText(err)
		_ = HasCycle(err)
	})
}

// selfError is declared in each internal test package that needs it:
// test types cannot be shared across packages.
type selfError struct{}

func (selfError) Error() string { return "self" }

func (e selfError) Unwrap() error { return e }

// unhashableError has a comparable type but holds a slice, so hashing it
// panics.
type unhashableError struct{ v any }

func (unhashableError) Error() string { return "unhashable" }

func (unhashableError) Unwrap() error { return errors.New("inner") }

// TestHasCycle_Unhashable: an error whose type is comparable but whose
// value cannot be hashed does not crash the cycle check.
func TestHasCycle_Unhashable(t *testing.T) {
	t.Parallel()

	if HasCycle(unhashableError{v: []int{1}}) {
		t.Fatal("reported a cycle")
	}
}

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

// FuzzRedactSecrets: the keyword pre-scan never changes what RedactSecrets
// returns, which equals both patterns applied without it, including for
// text whose non-ASCII runes (?i) folds onto the key words.
func FuzzRedactSecrets(f *testing.F) {
	for _, seed := range []string{
		"",
		"password=hunter2",
		"Key=v",
		"ſession: abc",
		"Get \"https://x.test/?api_key=abc&page=2\": timeout",
		"TOKEN:\"x\"",
		"no secret at all",
		"pass word=x",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, text string) {
		want := secretPair.ReplaceAllString(queryValue.ReplaceAllString(text, "${1}"+redactedValue), "${1}"+redactedValue)
		if got := RedactSecrets(text); got != want {
			t.Fatalf("RedactSecrets(%q) = %q, want %q", text, got, want)
		}
	})
}
