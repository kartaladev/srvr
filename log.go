package srvr

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
)

// WithLogOutput sets where the server's JSON log lines go. The default is
// standard output, where a log agent ships them. w must not be nil.
func WithLogOutput(w io.Writer) Option {
	return func(s *settings) {
		if isNil(w) {
			s.errs = append(s.errs, configError("Output", "<nil>", "is nil"))

			return
		}

		s.logOutput = w
	}
}

// WithSetDefaultLogger makes the server's logger the process default
// (slog.SetDefault) when the server is constructed, so slog.Default()
// records written with a request context carry its trace correlation. It
// is opt-in because it changes global state.
func WithSetDefaultLogger() Option {
	return func(s *settings) { s.setDefaultLogger = true }
}

// newCorrelation returns the server's log handler: JSON lines to the
// configured output at INFO, without source, wrapped in the correlation
// handler.
func newCorrelation(s *settings) *correlation {
	out := s.logOutput
	if out == nil {
		out = os.Stdout
	}

	return &correlation{base: slog.NewJSONHandler(out, nil)}
}

// LoggerFromContext returns the request-scoped logger of a request handled
// by the server: every record it writes carries the request's trace_id
// and span_id as top-level keys. For any other context, including nil, it
// returns slog.Default().
func LoggerFromContext(ctx context.Context) *slog.Logger {
	req := requestFrom(ctx)
	if req == nil {
		return slog.Default()
	}

	return slog.New(req.server.correlation.bind(&req.trace))
}

// correlation is the handler srvr wraps around the configured one. It adds
// trace_id and span_id as top-level keys to every record written with a
// request context (or by a request logger). Because an attribute added in
// Handle would land inside any open group, it does not forward WithAttrs
// and WithGroup: it records them and replays them, in order, after adding
// the correlation keys.
type correlation struct {
	base  slog.Handler
	steps []step
	// trace is the trace a request logger is bound to, or nil.
	trace *Trace
}

// step is one recorded WithAttrs (attrs) or WithGroup (group) call.
type step struct {
	group string
	attrs []slog.Attr
}

func (h *correlation) Enabled(ctx context.Context, l slog.Level) bool {
	return h.base.Enabled(ctx, l)
}

func (h *correlation) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	return h.with(step{attrs: slices.Clone(attrs)})
}

func (h *correlation) WithGroup(name string) slog.Handler {
	if name == "" {
		return h // as log/slog does
	}

	return h.with(step{group: name})
}

func (h *correlation) with(st step) *correlation {
	return &correlation{base: h.base, steps: append(slices.Clip(h.steps), st), trace: h.trace}
}

// bind returns h bound to tr, for a request logger.
func (h *correlation) bind(tr *Trace) *correlation {
	return &correlation{base: h.base, steps: h.steps, trace: tr}
}

func (h *correlation) Handle(ctx context.Context, r slog.Record) error {
	target := h.base

	tr, ok := h.traceOf(ctx)
	if ok {
		target = target.WithAttrs([]slog.Attr{slog.String(keyTraceID, tr.TraceID), slog.String(keySpanID, tr.SpanID)})
	}

	for _, st := range h.steps {
		if st.group != "" {
			target = target.WithGroup(st.group)
		} else {
			target = target.WithAttrs(st.attrs)
		}
	}

	return target.Handle(ctx, r)
}

// traceOf returns the bound trace, or the trace of ctx.
func (h *correlation) traceOf(ctx context.Context) (Trace, bool) {
	if h.trace != nil {
		return *h.trace, true
	}

	return TraceFromContext(ctx)
}
