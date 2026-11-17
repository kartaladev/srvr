package srvr

import (
	"io"
	"sync/atomic"
)

// logWriteErrors counts records srvr could not write.
var logWriteErrors atomic.Uint64

// LogWriteErrors returns how many log records srvr's handlers failed to
// write: the output or a consumer handler returned an error, wrote short, or
// panicked (in Enabled, WithAttrs, WithGroup or Handle; the record is then
// skipped). The count is process-wide: one counter, shared by every server
// in the process and never reset, because loggers derived from
// slog.Default() do not know which server they belong to. srvr counts these failures instead of
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
