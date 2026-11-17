package engine

import (
	"log/slog"
	"testing"

	"github.com/kartaladev/prblm"
)

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
			for _, a := range core.triageAttrs("GET", &RequestState{Route: "/x"}, f) {
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

	for name, err := range map[string]error{"cyclic": selfError{}, "panicking Unwrap": panicUnwrapError{}} {
		if _, ok := panicOf(err); ok {
			t.Errorf("%s: reported a panic", name)
		}
	}
}

type panicUnwrapError struct{}

func (panicUnwrapError) Error() string { return "x" }
func (panicUnwrapError) Unwrap() error { panic("unwrap") }
