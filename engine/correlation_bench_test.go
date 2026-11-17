package engine

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

// BenchmarkCorrelation compares ungrouped and grouped request records: a
// grouped logger replays its WithGroup and WithAttrs steps per record.
func BenchmarkCorrelation(b *testing.B) {
	base := &logHandler{base: slog.NewJSONHandler(io.Discard, nil)}
	ctx := context.WithValue(context.Background(), stateKey{}, &RequestState{trace: Trace{TraceID: "4bf92f3577b34da6a3ce929d0e0e4736", SpanID: "00f067aa0ba902b7"}})

	loggers := map[string]*slog.Logger{
		"ungrouped": slog.New(base),
		"grouped":   slog.New(base).With("table", "users").WithGroup("db"),
	}

	for name, l := range loggers {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				l.InfoContext(ctx, "query", "k", "v")
			}
		})
	}
}
