package otelsetup_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kartaladev/srvr/otelsetup"
)

// logBuffer is a concurrency-safe log output.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

// records returns the JSON records with msg.
func (b *logBuffer) records(t *testing.T, msg string) []map[string]any {
	t.Helper()

	b.mu.Lock()
	defer b.mu.Unlock()

	var out []map[string]any

	for line := range strings.Lines(b.buf.String()) {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}

		if m["msg"] == msg {
			out = append(out, m)
		}
	}

	return out
}

// TestExportFailure covers telemetry-setup "Collector down during
// traffic", all rows. It sets the environment and the globals, so it is
// not parallel and resets them.
func TestExportFailure(t *testing.T) {
	resetGlobals(t)

	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "20") // milliseconds

	for _, tt := range []struct {
		name      string
		setGlobal bool
		records   int
	}{
		{"SetGlobal", true, 1},
		{"no SetGlobal", false, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			logs := &logBuffer{}
			opts := []otelsetup.Option{
				otelsetup.WithOTLP(otelsetup.OTLPConfig{Endpoint: unreachable(t), Metrics: true}),
				otelsetup.WithLogger(slog.New(slog.NewJSONHandler(logs, nil))),
			}

			if tt.setGlobal {
				opts = append(opts, otelsetup.WithSetGlobal())
			}

			s, err := otelsetup.New(context.Background(), opts...)
			if err != nil {
				t.Fatal(err)
			}

			base := app(t, s, nil)
			for range 100 {
				if status, _ := fetch(t, base+"/users/1"); status != 200 {
					t.Fatalf("status = %d", status)
				}
			}

			// Wait for the first record, or several export intervals when
			// none is expected; later failures must not add records.
			deadline := time.Now().Add(2 * time.Second)
			for tt.records > 0 && len(logs.records(t, "otel.export.failed")) == 0 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}

			time.Sleep(100 * time.Millisecond) // five more export intervals

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = s.Shutdown(ctx)

			recs := logs.records(t, "otel.export.failed")
			if len(recs) != tt.records {
				t.Fatalf("otel.export.failed records = %d, want %d", len(recs), tt.records)
			}

			if tt.records > 0 && (recs[0]["level"] != "WARN" || recs[0]["error.type"] == nil) {
				t.Errorf("record = %v", recs[0])
			}
		})
	}
}
