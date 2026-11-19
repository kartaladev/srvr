package otelsetup

import (
	"errors"
	"strings"
	"testing"
)

// selfUnwrapError unwraps to itself, so its chain never ends.
type selfUnwrapError struct{}

func (e *selfUnwrapError) Error() string { return "self" }
func (e *selfUnwrapError) Unwrap() error { return e }

// myError is an error type whose typed nil is recovered.
type myError struct{}

func (*myError) Error() string { return "my error" }

// unwrapPanicsError panics in Unwrap.
type unwrapPanicsError struct{}

func (*unwrapPanicsError) Error() string { return "unwrap panics" }
func (*unwrapPanicsError) Unwrap() error { panic("Unwrap panicked") }

// sliceError is an error of an uncomparable type.
type sliceError []string

func (e sliceError) Error() string { return strings.Join(e, ",") }

// TestPanicCause covers the branches of panicCause that http.ServeMux's
// panics never reach (design D4's failure modes; otelsrvr's
// TestConfigError covers the same rule through its public API).
func TestPanicCause(t *testing.T) {
	t.Parallel()

	errPlain := errors.New("plain")

	tests := []struct {
		name  string
		value any
		// kept reports whether the cause is the value; nil: no cause.
		kept func(cause error) bool
	}{
		{"an error", errPlain, func(c error) bool { return errors.Is(c, errPlain) }},
		{"a string", "boom", nil},
		{"a typed nil error", (*myError)(nil), nil},
		{"an error unwrapping to itself", &selfUnwrapError{}, nil},
		{"an error whose Unwrap panics", &unwrapPanicsError{}, nil},
		{"an error of an uncomparable type", sliceError{"a"}, func(c error) bool { _, ok := errors.AsType[sliceError](c); return ok }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := panicCause(tt.value)
			if tt.kept == nil {
				if got != nil {
					t.Errorf("panicCause(%T) = %v, want nil", tt.value, got)
				}
			} else if got == nil || !tt.kept(got) {
				t.Errorf("panicCause(%T) = %v, want the value itself", tt.value, got)
			}
		})
	}
}
