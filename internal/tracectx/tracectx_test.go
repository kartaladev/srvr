package tracectx_test

import (
	"encoding/hex"
	"regexp"
	"strings"
	"testing"

	"github.com/kartaladev/srvr/internal/tracectx"
)

var (
	traceIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)
	spanIDRe  = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

// TestTraceparent_Valid covers trace-context / Scenario: Valid traceparent.
func TestTraceparent_Valid(t *testing.T) {
	t.Parallel()

	tests := []struct{ header, traceID, parent string }{
		{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01", "4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7"},
		{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00", "4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7"},
		{"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-future", "4bf92f3577b34da6a3ce929d0e0e4736", "00f067aa0ba902b7"},
	}

	for _, tt := range tests {
		t.Run(tt.header, func(t *testing.T) {
			t.Parallel()

			tr := tracectx.FromHeaders([]string{tt.header})
			if tr.TraceID != tt.traceID || tr.ParentSpanID != tt.parent {
				t.Fatalf("got %+v, want trace %s parent %s", tr, tt.traceID, tt.parent)
			}
			if !spanIDRe.MatchString(tr.SpanID) || tr.SpanID == tt.parent {
				t.Fatalf("span id %q is not a new 16-character hex id", tr.SpanID)
			}
		})
	}
}

// TestTraceparent_New covers trace-context / Scenario: New trace.
func TestTraceparent_New(t *testing.T) {
	t.Parallel()

	valid := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	tests := []struct {
		name    string
		headers []string
	}{
		// absent or malformed
		{"none", nil},
		{"empty", []string{""}},
		{"garbage", []string{"garbage"}},
		{"uppercase", []string{"00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-01"}},
		{"short trace id", []string{"00-4bf92f3577b34da6a3ce929d0e0e473-00f067aa0ba902b7-01"}},
		{"version 00 with extra", []string{valid + "-extra"}},
		{"misplaced separator", []string{"00_4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}},
		{"future version without separator", []string{"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01x"}},
		{"bad flags", []string{"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-zz"}},
		// invalid values
		{"version ff", []string{"ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}},
		{"zero trace id", []string{"00-00000000000000000000000000000000-00f067aa0ba902b7-01"}},
		{"zero parent id", []string{"00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01"}},
		{"two headers", []string{valid, "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"}},
		{"4096 bytes", []string{"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-" + strings.Repeat("x", 4096-56)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr := tracectx.FromHeaders(tt.headers)
			if !traceIDRe.MatchString(tr.TraceID) || tr.TraceID == strings.Repeat("0", 32) || tr.TraceID == "4bf92f3577b34da6a3ce929d0e0e4736" {
				t.Fatalf("trace id %q is not a new id", tr.TraceID)
			}
			if !spanIDRe.MatchString(tr.SpanID) || tr.ParentSpanID != "" {
				t.Fatalf("got %+v, want a new span and no parent", tr)
			}
		})
	}
}

// FuzzTraceparent covers test-quality / Scenario: Fuzzed properties, row
// FuzzTraceparent: no header crashes the parser; an accepted header keeps
// its trace id and parent id, its sampled flag is the flags byte's low bit
// and the span id is new; a rejected one yields a fresh valid trace.
func FuzzTraceparent(f *testing.F) {
	for _, seed := range []string{
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-fe",
		"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-x",
		"00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-0B",                             // upper-case hex
		"00-00000000000000000000000000000000-0000000000000000-01",                             // all-zero ids
		"ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",                             // version ff
		"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-" + strings.Repeat("x", 600), // over-long
		"",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, header string) {
		tr := tracectx.FromHeaders([]string{header})
		if !traceIDRe.MatchString(tr.TraceID) || !spanIDRe.MatchString(tr.SpanID) {
			t.Fatalf("invalid ids %+v for %q", tr, header)
		}

		if tr.ParentSpanID == "" { // rejected: a fresh, unsampled trace
			if tr.Sampled || (len(header) >= 35 && tr.TraceID == header[3:35]) {
				t.Fatalf("rejected %q gave %+v, want a fresh unsampled trace", header, tr)
			}

			return
		}

		flags, err := hex.DecodeString(header[53:55])
		if err != nil {
			t.Fatalf("accepted %q whose flags are not hex: %v", header, err)
		}
		if tr.TraceID != header[3:35] || tr.ParentSpanID != header[36:52] || tr.Sampled != (flags[0]&1 == 1) || tr.SpanID == tr.ParentSpanID {
			t.Fatalf("accepted %q gave %+v, want its ids, a new span and sampled = flags bit 0", header, tr)
		}
	})
}
