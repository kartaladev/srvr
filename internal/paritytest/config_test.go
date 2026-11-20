package paritytest

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// TestProblemOptionErrors_Joined covers server-lifecycle / Scenario:
// Problem-option errors are joined on every engine.
func TestProblemOptionErrors_Joined(t *testing.T) {
	t.Parallel()

	for _, b := range constructors {
		// One case per Examples row: the engine and the field the joined
		// error must name.
		for _, field := range []string{"ProblemOptions", "ShutdownTimeout"} {
			t.Run(b.name+"/"+field, func(t *testing.T) {
				t.Parallel()

				err := b.new(
					srvr.WithProblemOptions(prblm.WithNamespace("not a uri")),
					srvr.WithConfig(srvr.Config{ShutdownTimeout: -time.Second}),
				)
				if !errors.Is(err, srvr.ErrConfig) {
					t.Fatalf("New error = %v, want one wrapping srvr.ErrConfig", err)
				}

				if got := configFields(err); !slices.Contains(got, field) {
					t.Errorf("New error has *srvr.ConfigError fields %q, want %q among them; error: %v", got, field, err)
				}
			})
		}
	}
}

// configFields returns the Field of every *srvr.ConfigError a joined
// construction error holds, in order; errors.As alone stops at the first.
func configFields(err error) []string {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		errs := joined.Unwrap()
		fields := make([]string, 0, len(errs))
		for _, e := range errs {
			fields = append(fields, configFields(e)...)
		}

		return fields
	}

	if ce, ok := errors.AsType[*srvr.ConfigError](err); ok {
		return []string{ce.Field}
	}

	return nil
}

// TestSrvrSentinels_EveryEngine covers engine-support / Scenario: srvr
// names are the engine declarations, the ginsrvr.New and fibersrvr.New
// rows (the internal rows are in internal/engine, the srvr.New row too):
// every engine's construction error matches srvr.ErrConfig.
func TestSrvrSentinels_EveryEngine(t *testing.T) {
	t.Parallel()

	for _, b := range constructors {
		t.Run(b.name, func(t *testing.T) {
			t.Parallel()

			err := b.new(srvr.WithLogConfig(srvr.LogConfig{Level: "loud"}))
			if !errors.Is(err, srvr.ErrConfig) {
				t.Fatalf("New error = %v, want one wrapping srvr.ErrConfig", err)
			}
		})
	}
}
