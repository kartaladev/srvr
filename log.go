package srvr

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

// LogConfig configures srvr's logger. Pass it with [WithLogConfig]; every
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
	// a safety default.
	MaxAttrBytes int `json:"max_attr_bytes" yaml:"max_attr_bytes" env:"MAX_ATTR_BYTES"`
}

// defaultMaxAttrBytes keeps one hostile or careless value from flooding
// the log pipeline.
const defaultMaxAttrBytes = 1024

// logSettings is the logging part of settings.
type logSettings struct {
	LogConfig

	output     io.Writer
	levelVar   *slog.LevelVar
	handler    slog.Handler
	setDefault bool
	// handlerFrom names the option that set handler, for conflict errors.
	handlerFrom string
}

// WithLogConfig sets every field of [LogConfig], replacing what earlier
// level options set.
func WithLogConfig(c LogConfig) Option {
	return func(s *settings) {
		s.log.LogConfig = c
		s.log.levelVar = nil
	}
}

// WithLogLevel sets the minimum log level (default INFO).
func WithLogLevel(l slog.Level) Option {
	return func(s *settings) {
		s.log.Level = l.String()
		s.log.levelVar = nil
	}
}

// WithLogLevelVar makes the minimum log level follow lv, so it can change
// at runtime. lv must not be nil.
func WithLogLevelVar(lv *slog.LevelVar) Option {
	return func(s *settings) {
		if lv == nil {
			s.errs = append(s.errs, configError("LevelVar", "<nil>", "is nil"))

			return
		}

		s.log.levelVar = lv
	}
}

// WithLogFormat sets the log format, "json" (default) or "text".
func WithLogFormat(format string) Option {
	return func(s *settings) { s.log.Format = format }
}

// WithLogAddSource adds the caller's file and line to every record a
// consumer writes; srvr's own records never carry it.
func WithLogAddSource() Option {
	return func(s *settings) { s.log.AddSource = true }
}

// WithLogMaxAttrBytes caps string attributes at n bytes (default 1024); a
// negative n disables the cap, which loosens a safety default.
func WithLogMaxAttrBytes(n int) Option {
	return func(s *settings) { s.log.MaxAttrBytes = n }
}

// WithLogOutput sets where the server's log lines go. The default is
// standard output, where a log agent ships them. w must not be nil.
func WithLogOutput(w io.Writer) Option {
	return func(s *settings) {
		if isNil(w) {
			s.errs = append(s.errs, configError("Output", "<nil>", "is nil"))

			return
		}

		s.log.output = w
	}
}

// WithLogHandler makes srvr write through h instead of its own JSON or
// text handler. srvr still adds trace correlation, truncation and failure
// containment around it. It cannot be combined with level, format, source
// or output settings, which belong to h. h must not be nil.
func WithLogHandler(h slog.Handler) Option {
	return func(s *settings) {
		if isNil(h) {
			s.errs = append(s.errs, configError("Handler", "<nil>", "is nil"))

			return
		}

		s.log.handler, s.log.handlerFrom = h, "WithLogHandler"
	}
}

// WithLogger is WithLogHandler(l.Handler()). l must not be nil.
func WithLogger(l *slog.Logger) Option {
	return func(s *settings) {
		if l == nil {
			s.errs = append(s.errs, configError("Logger", "<nil>", "is nil"))

			return
		}

		s.log.handler, s.log.handlerFrom = l.Handler(), "WithLogger"
	}
}

// WithSetDefaultLogger makes the server's logger the process default
// (slog.SetDefault) when the server is constructed, so slog.Default()
// records written with a request context carry its trace correlation. It
// is opt-in because it changes global state.
func WithSetDefaultLogger() Option {
	return func(s *settings) { s.log.setDefault = true }
}

// newLogHandler resolves the base handler (the consumer's, or JSON or text
// to the output) and wraps it in srvr's handler.
func newLogHandler(ls *logSettings) (*logHandler, error) {
	if ls.handler != nil {
		if err := ls.checkOwnHandler(); err != nil {
			return nil, err
		}

		return &logHandler{base: ls.handler, maxBytes: ls.maxAttrBytes()}, nil
	}

	var leveler slog.Leveler = slog.LevelInfo
	if ls.levelVar != nil {
		leveler = ls.levelVar
	} else if ls.Level != "" {
		var l slog.Level
		if err := l.UnmarshalText([]byte(ls.Level)); err != nil {
			return nil, configError("Level", strconv.Quote(ls.Level), "is not a log/slog level such as debug, info, warn, error or info-4")
		}

		leveler = l
	}

	out := io.Writer(os.Stdout)
	if ls.output != nil {
		out = ls.output
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
		return nil, configError("Format", strconv.Quote(ls.Format), "is not json or text")
	}

	return &logHandler{base: base, maxBytes: ls.maxAttrBytes()}, nil
}

// checkOwnHandler rejects settings that belong to a consumer's handler.
func (ls *logSettings) checkOwnHandler() error {
	for _, c := range []struct {
		field string
		set   bool
	}{
		{"Level", ls.Level != "" || ls.levelVar != nil},
		{"Format", ls.Format != ""},
		{"AddSource", ls.AddSource},
		{"Output", ls.output != nil},
	} {
		if c.set {
			return configError(c.field, "set", "cannot be combined with "+ls.handlerFrom+": it belongs to that handler")
		}
	}

	return nil
}

// maxAttrBytes returns the effective string attribute cap; negative
// disables it.
func (ls *logSettings) maxAttrBytes() int {
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

	return slog.New(req.server.log.bind(&req.trace))
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

func (h *logHandler) Enabled(ctx context.Context, l slog.Level) bool {
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
// a schema event name and is not cut.
func (h *logHandler) handle(ctx context.Context, r slog.Record, own bool) {
	t := truncator{maxBytes: h.maxBytes}
	r = t.record(r, own)

	var top []slog.Attr
	if tr, ok := h.traceOf(ctx); ok {
		top = append(top, slog.String(keyTraceID, tr.TraceID), slog.String(keySpanID, tr.SpanID))
	}

	if t.cut || h.truncated {
		top = append(top, slog.Bool(keyAttrTruncated, true))
	}

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

	handleGuarded(ctx, target, r)
}

// traceOf returns the bound trace, or the trace of ctx.
func (h *logHandler) traceOf(ctx context.Context) (Trace, bool) {
	if h.trace != nil {
		return *h.trace, true
	}

	return TraceFromContext(ctx)
}
