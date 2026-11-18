package otelsetup

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/kartaladev/srvr/engine"
)

// exportErrorInterval is how often one kind of export error is logged.
const exportErrorInterval = time.Minute

// exportErrorHandler logs OpenTelemetry errors (failed exports, mostly) as
// otel.export.failed at WARN, at most once per exportErrorInterval per
// error kind (the error's Go type), so a collector outage cannot flood the
// log. The logger is called without holding the lock, and a panic from it
// is recovered and the record dropped: the SDK calls Handle from its
// export goroutines, where an escaping panic would crash the process.
type exportErrorHandler struct {
	logger *slog.Logger
	now    func() time.Time

	mu   sync.Mutex
	last map[string]time.Time
}

func newExportErrorHandler(logger *slog.Logger) *exportErrorHandler {
	return &exportErrorHandler{logger: logger, now: time.Now, last: map[string]time.Time{}}
}

// Handle implements otel.ErrorHandler.
func (h *exportErrorHandler) Handle(err error) {
	if err == nil {
		return
	}

	kind := engine.TypeName(err)
	if !h.due(kind) {
		return
	}

	defer func() { _ = recover() }() // a faulty consumer slog.Handler

	// The error text is read through ValueText: an Error method that
	// panics gives a placeholder.
	h.logger.LogAttrs(context.Background(), slog.LevelWarn, "otel.export.failed",
		slog.Int("srvr.log.schema", engine.LogSchemaVersion), // adding this event kept the version
		slog.String("error.type", kind),
		slog.String("exception.message", engine.ValueText(err)),
	)
}

// due reports whether kind may be logged now, and records it.
func (h *exportErrorHandler) due(kind string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := h.now()
	if last, ok := h.last[kind]; ok && now.Sub(last) < exportErrorInterval {
		return false
	}

	h.last[kind] = now

	return true
}
