package otelsetup

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

type kindAError struct{}

func (kindAError) Error() string { return "a" }

// panicsError is an error whose Error method panics.
type panicsError struct{}

func (panicsError) Error() string { panic("Error panicked") }

// TestExportErrorHandler pins the rate limit per error kind and the
// placeholder for an error whose Error panics.
func TestExportErrorHandler(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	now := time.Unix(0, 0)
	h := newExportErrorHandler(slog.New(slog.NewJSONHandler(&buf, nil)))
	h.now = func() time.Time { return now }

	h.Handle(kindAError{})
	h.Handle(kindAError{})             // same kind within the minute: dropped
	h.Handle(errors.New("other kind")) // another kind: logged
	h.Handle(nil)                      // nothing

	now = now.Add(exportErrorInterval)
	h.Handle(kindAError{}) // a minute later: logged again
	h.Handle(panicsError{})

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("records = %d, want 4:\n%s", len(lines), buf.String())
	}

	if !strings.Contains(lines[3], `"error.type":"otelsetup.panicsError"`) || !strings.Contains(lines[3], "panic") {
		t.Errorf("panicking error record = %s", lines[3])
	}
}

// TestLogSchemaGolden pins the keys of otel.export.failed, an additive
// event of srvr's log schema (srvr.log.schema stays 1).
func TestLogSchemaGolden(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	newExportErrorHandler(slog.New(slog.NewJSONHandler(&buf, nil))).Handle(kindAError{})

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatal(err)
	}

	keys := slices.Sorted(maps.Keys(rec))
	want := []string{"error.type", "exception.message", "level", "msg", "srvr.log.schema", "time"}

	if !slices.Equal(keys, want) || rec["msg"] != "otel.export.failed" || rec["level"] != "WARN" || rec["srvr.log.schema"] != float64(1) {
		t.Errorf("record = %v, want keys %v", rec, want)
	}
}

// TestExportError_Redacted covers telemetry-setup / Scenario: Export error
// with a secret: the handler setup installs with SetGlobal writes the
// error text through srvr's default redactor.
func TestExportError_Redacted(t *testing.T) {
	t.Parallel()

	tests := []struct{ text, message string }{
		{`Post "https://otlp.local/v1/metrics?token=abc": dial tcp: refused`, `Post "https://otlp.local/v1/metrics?token=***": dial tcp: refused`},
		{"exporter: authorization: Bearer xyz rejected", "exporter: authorization: *** rejected"},
		{"context deadline exceeded", "context deadline exceeded"},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			newExportErrorHandler(slog.New(slog.NewJSONHandler(&buf, nil))).Handle(errors.New(tt.text))

			var rec map[string]any
			if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
				t.Fatalf("record %q: %v", buf.String(), err)
			}
			if got := rec["exception.message"]; got != tt.message {
				t.Fatalf("exception.message = %v, want %q", got, tt.message)
			}
		})
	}
}
