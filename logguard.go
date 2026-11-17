package srvr

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
)

// logWriteErrors counts records srvr could not write.
var logWriteErrors atomic.Uint64

// LogWriteErrors returns how many log records srvr's handlers in this
// process failed to write: the output or a consumer handler returned an
// error, wrote short, or panicked. srvr counts these failures instead of
// logging them, because the log is what failed, and a request never fails
// because its log record could not be written.
func LogWriteErrors() uint64 { return logWriteErrors.Load() }

// guardedWriter reports a short write without an error as
// io.ErrShortWrite, so it is counted like any other write failure.
type guardedWriter struct{ w io.Writer }

func (g *guardedWriter) Write(p []byte) (int, error) {
	n, err := g.w.Write(p)
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}

	return n, err
}

// handleGuarded calls h.Handle, counting an error or a panic instead of
// passing it on.
func handleGuarded(ctx context.Context, h slog.Handler, r slog.Record) {
	defer func() {
		if v := recover(); v != nil {
			logWriteErrors.Add(1)
		}
	}()

	if err := h.Handle(ctx, r); err != nil {
		logWriteErrors.Add(1)
	}
}
