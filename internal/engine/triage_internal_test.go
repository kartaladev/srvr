package engine

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

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

// selfError is an error that unwraps to itself: a cyclic chain.
// selfError is declared in each internal test package that needs it:
// test types cannot be shared across packages.
type selfError struct{}

func (selfError) Error() string   { return "self" }
func (e selfError) Unwrap() error { return e }

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

// TestPanicOf_StandIn: srvr's stand-in for an unwalkable chain does not
// hide the panic it stands in for (fibersrvr guards fiberprblm's recovered
// panic before rendering it).
func TestPanicOf_StandIn(t *testing.T) {
	t.Parallel()

	pe := &prblm.PanicError{Value: fmt.Errorf("w: %w", panicUnwrapError{})}
	tests := []struct {
		name string
		err  error
		want *prblm.PanicError
	}{
		{"a panic whose value's Unwrap panics", GuardChain(fmt.Errorf("recovered: %w", pe)), pe},
		{"a cyclic error", GuardChain(selfError{}), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, _ := panicOf(tt.err); got != tt.want {
				t.Errorf("panicOf(%T) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

type panicUnwrapError struct{}

func (panicUnwrapError) Error() string { return "x" }

func (panicUnwrapError) Unwrap() error { panic("unwrap") }

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
