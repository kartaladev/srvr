package fibersrvr_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
	"github.com/kartaladev/srvr/fibersrvr"
)

// TestDefaultLoggerAfterConstruction covers engine-support / Scenario:
// Default logger after construction, fiber rows: a construction that fails
// leaves slog.Default() unchanged.
//
//nolint:paralleltest // slog.SetDefault is process-wide.
func TestDefaultLoggerAfterConstruction(t *testing.T) {
	tests := []struct {
		name    string
		opt     srvr.Option // nil adds nothing
		wantErr string      // "" when construction succeeds
	}{
		{"nothing else", nil, ""},
		{"a nil WithFiberConfig hook", fibersrvr.WithFiberConfig(nil), "WithFiberConfig"},
		{"a WithFiberConfig hook that panics", fibersrvr.WithFiberConfig(func(*fiber.Config) { panic("boom") }), "boom"},
		{"an option of another engine", engine.ForEngine("gin", "ginsrvr.WithEngine", func() {}), "ginsrvr.WithEngine"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkGoroutines(t)

			before := slog.Default()
			t.Cleanup(func() { slog.SetDefault(before) })

			var buf bytes.Buffer

			opts := []srvr.Option{srvr.WithSetDefaultLogger(), srvr.WithLogHandler(slog.NewJSONHandler(&buf, nil))}
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}

			_, err := fibersrvr.New(opts...)

			wantOK := tt.wantErr == ""

			switch {
			case wantOK && err != nil:
				t.Fatalf("New = %v", err)
			case !wantOK && (!errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("New = %v, want an ErrConfig containing %s", err, tt.wantErr)
			}

			buf.Reset()
			slog.Info("probe")

			reached := strings.Contains(buf.String(), "probe")
			if reached != wantOK {
				t.Fatalf("a record through slog.Default() reached the server's handler: %v; New error: %v", reached, err)
			}
		})
	}
}
