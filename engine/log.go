package engine

import (
	"cmp"
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
)

// LogConfig configures srvr's logger. Consumers use it as srvr.LogConfig
// and pass it with srvr.WithLogConfig; every
// field's zero value is the default. Text fields are case-insensitive and
// parsed when the server is constructed, so a mistake fails New naming the
// field.
type LogConfig struct {
	// Level is the minimum level: a log/slog level name with an optional
	// offset, such as "debug", "WARN" or "info-4". Default "info".
	Level string `json:"level" yaml:"level" env:"LEVEL"`
	// Format is "json" (the default, for log agents) or "text".
	Format string `json:"format" yaml:"format" env:"FORMAT"`
	// AddSource adds the caller's file and line as "source". Default off:
	// least noise.
	AddSource bool `json:"add_source" yaml:"add_source" env:"ADD_SOURCE"`
	// MaxAttrBytes caps every string attribute, error text and message.
	// Default 1024 bytes; a negative value disables the cap, which loosens
	// a safety default. Error values still reach the handler as their
	// guarded text, a plain string, whatever the cap.
	MaxAttrBytes int `json:"max_attr_bytes" yaml:"max_attr_bytes" env:"MAX_ATTR_BYTES"`
}

// defaultMaxAttrBytes keeps one hostile or careless value from flooding
// the log pipeline.
const defaultMaxAttrBytes = 1024

// LogSettings is the logging part of [Settings], set by srvr's log
// options.
type LogSettings struct {
	LogConfig

	Output     io.Writer
	LevelVar   *slog.LevelVar
	Handler    slog.Handler
	SetDefault bool
	// HandlerFrom names the option that set Handler, for conflict errors.
	HandlerFrom string
}

// newLogHandler resolves the base handler (the consumer's, or JSON or text
// to the output) and wraps it in srvr's handler.
func newLogHandler(ls *LogSettings) (*logHandler, error) {
	if ls.Handler != nil {
		if err := ls.checkOwnHandler(); err != nil {
			return nil, err
		}

		return &logHandler{base: ls.Handler, maxBytes: ls.maxAttrBytes()}, nil
	}

	var leveler slog.Leveler = slog.LevelInfo
	if ls.LevelVar != nil {
		leveler = ls.LevelVar
	} else if ls.Level != "" {
		var l slog.Level
		if err := l.UnmarshalText([]byte(ls.Level)); err != nil {
			return nil, ConfigError("Level", strconv.Quote(ls.Level), "is not a log/slog level such as debug, info, warn, error or info-4")
		}

		leveler = l
	}

	out := io.Writer(os.Stdout)
	if ls.Output != nil {
		out = ls.Output
	}

	out = &guardedWriter{w: out}
	opts := &slog.HandlerOptions{Level: leveler, AddSource: ls.AddSource}

	var base slog.Handler

	switch strings.ToLower(ls.Format) {
	case "", "json":
		base = slog.NewJSONHandler(out, opts)
	case "text":
		base = slog.NewTextHandler(out, opts)
	default:
		return nil, ConfigError("Format", strconv.Quote(ls.Format), "is not json or text")
	}

	return &logHandler{base: base, maxBytes: ls.maxAttrBytes()}, nil
}

// checkOwnHandler rejects settings that belong to a consumer's handler.
func (ls *LogSettings) checkOwnHandler() error {
	for _, c := range []struct {
		field string
		set   bool
	}{
		{"Level", ls.Level != "" || ls.LevelVar != nil},
		{"Format", ls.Format != ""},
		{"AddSource", ls.AddSource},
		{"Output", ls.Output != nil},
	} {
		if c.set {
			return ConfigError(c.field, "set", "cannot be combined with "+ls.HandlerFrom+": it belongs to that handler")
		}
	}

	return nil
}

// maxAttrBytes returns the effective string attribute cap; negative
// disables it.
func (ls *LogSettings) maxAttrBytes() int {
	return cmp.Or(ls.MaxAttrBytes, defaultMaxAttrBytes)
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

	return slog.New(req.core.log.bind(&req.trace))
}

// logHandler is the handler srvr wraps around the configured one. It adds
// trace_id and span_id as top-level keys to every record written with a
// request context (or by a request logger). Because an attribute added in
// Handle would land inside any open group, it does not forward WithAttrs
// and WithGroup: it records them and replays them, in order, after adding
// the correlation keys.
type logHandler struct {
	base  slog.Handler
	steps []step
	// trace is the trace a request logger is bound to, or nil.
	trace *Trace
	// maxBytes caps string attributes; negative disables the cap.
	maxBytes int
	// truncated is set when a recorded With attribute was cut.
	truncated bool
}

// step is one recorded WithAttrs (attrs) or WithGroup (group) call.
// Attributes are cut when recorded.
type step struct {
	group string
	attrs []slog.Attr
}

// Enabled asks the base handler; a panic there is counted like a failed
// write and the record is skipped.
func (h *logHandler) Enabled(ctx context.Context, l slog.Level) (enabled bool) {
	defer func() {
		if recover() != nil {
			logWriteErrors.Add(1)

			enabled = false
		}
	}()

	return h.base.Enabled(ctx, l)
}

func (h *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}

	t := truncator{maxBytes: h.maxBytes}
	cut := t.attrs(attrs)

	c := h.with(step{attrs: cut})
	c.truncated = c.truncated || t.cut

	return c
}

func (h *logHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h // as log/slog does
	}

	return h.with(step{group: name})
}

func (h *logHandler) with(st step) *logHandler {
	c := *h
	c.steps = append(slices.Clip(h.steps), st)

	return &c
}

// bind returns h bound to tr, for a request logger.
func (h *logHandler) bind(tr *Trace) *logHandler {
	c := *h
	c.trace = tr

	return &c
}

func (h *logHandler) Handle(ctx context.Context, r slog.Record) error {
	h.handle(ctx, r, false)

	return nil
}

// handle writes r; own marks a record srvr built itself, whose message is
// a schema event name and is not cut. An error or a panic in the base
// handler, while the recorded steps are replayed or in Handle, is counted
// instead of passed on, and the record is skipped.
func (h *logHandler) handle(ctx context.Context, r slog.Record, own bool) {
	defer func() {
		if recover() != nil {
			logWriteErrors.Add(1)
		}
	}()

	t := truncator{maxBytes: h.maxBytes}
	r = t.record(r, own)

	var top []slog.Attr
	if tr, ok := h.traceOf(ctx); ok {
		top = append(top, slog.String(keyTraceID, tr.TraceID), slog.String(keySpanID, tr.SpanID))
	}

	if t.cut || h.truncated {
		top = append(top, slog.Bool(keyAttrTruncated, true))
	}

	if err := h.target(top).Handle(ctx, r); err != nil {
		logWriteErrors.Add(1)
	}
}

// target returns the base handler with the top-level attributes and the
// recorded steps replayed after them.
func (h *logHandler) target(top []slog.Attr) slog.Handler {
	target := h.base
	if len(top) > 0 {
		target = target.WithAttrs(top)
	}

	for _, st := range h.steps {
		if st.group != "" {
			target = target.WithGroup(st.group)
		} else {
			target = target.WithAttrs(st.attrs)
		}
	}

	return target
}

// traceOf returns the bound trace, or the trace of ctx.
func (h *logHandler) traceOf(ctx context.Context) (Trace, bool) {
	if h.trace != nil {
		return *h.trace, true
	}

	return TraceFromContext(ctx)
}
