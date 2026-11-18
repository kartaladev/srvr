package engine

import (
	"cmp"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

// LogConfig configures srvr's logger. Consumers use it as srvr.LogConfig
// and pass it with srvr.WithLogConfig; every
// field's zero value is the default. Text fields are case-insensitive and
// parsed when the server is constructed, so a mistake fails New naming the
// field.
type LogConfig struct {
	// Level is the minimum level: a log/slog level name with an optional
	// offset, such as "debug", "WARN" or "info-4". Default "info": every
	// request and lifecycle record srvr writes, without debug noise or its
	// cost in production.
	Level string `json:"level" yaml:"level" env:"LEVEL"`
	// Format is "json" (the default, for log agents) or "text".
	Format string `json:"format" yaml:"format" env:"FORMAT"`
	// AddSource adds the caller's file and line as "source". Default off:
	// least noise.
	AddSource bool `json:"add_source" yaml:"add_source" env:"ADD_SOURCE"`
	// MaxAttrBytes caps every string attribute, error text and message,
	// and every other slog.Any value by its formatted text: one that fits
	// is written unchanged, a larger one is replaced by its text cut.
	// Default 1024 bytes, so one careless value cannot flood the log
	// pipeline; a negative value disables the cap, which loosens a safety
	// default. Error values still reach the handler as their guarded text,
	// a plain string, whatever the cap.
	MaxAttrBytes int `json:"max_attr_bytes" yaml:"max_attr_bytes" env:"MAX_ATTR_BYTES"`
}

// defaultMaxAttrBytes keeps one hostile or careless value from flooding
// the log pipeline.
const defaultMaxAttrBytes = 1024

// LogSettings is the logging part of [Settings], set by srvr's log
// options.
type LogSettings struct {
	LogConfig

	// Output receives the JSON or text records; nil means os.Stdout.
	Output io.Writer
	// LevelVar, when set, replaces LogConfig.Level so the level can change
	// at runtime.
	LevelVar *slog.LevelVar
	// Handler is a consumer's slog.Handler; it replaces Output, Format and
	// the level, and srvr still wraps it.
	Handler slog.Handler
	// SetDefault makes the server's logger slog.Default at construction.
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

		return &logHandler{base: ls.Handler, maxBytes: ls.maxAttrBytes(), cache: &targetCache{}}, nil
	}

	// Every invalid field is reported, Level first.
	var errs []error

	var leveler slog.Leveler = slog.LevelInfo
	if ls.LevelVar != nil {
		leveler = ls.LevelVar
	} else if ls.Level != "" {
		var l slog.Level
		if err := l.UnmarshalText([]byte(ls.Level)); err != nil {
			errs = append(errs, NewConfigError("Level", strconv.Quote(ls.Level),
				"is not a log/slog level such as debug, info, warn, error or info-4"))
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
		errs = append(errs, NewConfigError("Format", strconv.Quote(ls.Format), "is not json or text"))
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return &logHandler{base: base, maxBytes: ls.maxAttrBytes(), cache: &targetCache{}}, nil
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
			return NewConfigError(c.field, "set", "cannot be combined with "+ls.HandlerFrom+": it belongs to that handler")
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
	// cache holds the last target built for this handler's steps, so the
	// records of one request logger reuse it. Copies made by with and
	// bind get a cache of their own.
	cache *targetCache
}

// targetCache is a logHandler's last target and the top attributes it
// was built with; it is replaced whole, so concurrent records never see a
// target paired with another key.
type targetCache struct{ last atomic.Pointer[cachedTarget] }

// targetKey is the top attributes a target is built with: the trace, when
// traced, and the truncation flag.
type targetKey struct {
	traceID, spanID string
	traced, cut     bool
}

// cachedTarget is a target and the key it was built for.
type cachedTarget struct {
	key     targetKey
	handler slog.Handler
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
	c := h.clone()
	c.steps = append(slices.Clip(h.steps), st)

	return c
}

// bind returns h bound to tr, for a request logger.
func (h *logHandler) bind(tr *Trace) *logHandler {
	c := h.clone()
	c.trace = tr

	return c
}

// clone returns a copy of h with a target cache of its own: the cache key
// holds only the top attributes, so a copy whose steps or trace differ
// must never share its parent's cache.
func (h *logHandler) clone() *logHandler {
	c := *h
	c.cache = &targetCache{}

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

	tr, traced := h.traceOf(ctx)
	key := targetKey{traced: traced, cut: t.cut || h.truncated}
	if traced {
		key.traceID, key.spanID = tr.TraceID, tr.SpanID
	}

	if err := h.cachedTarget(key).Handle(ctx, r); err != nil {
		logWriteErrors.Add(1)
	}
}

// cachedTarget returns the target for key's top attributes, reusing the
// last one built when key matches it.
func (h *logHandler) cachedTarget(key targetKey) slog.Handler {
	if h.cache != nil {
		if c := h.cache.last.Load(); c != nil && c.key == key {
			return c.handler
		}
	}

	var top []slog.Attr
	if key.traced {
		top = append(top, slog.String(keyTraceID, key.traceID), slog.String(keySpanID, key.spanID))
	}

	if key.cut {
		top = append(top, slog.Bool(keyAttrTruncated, true))
	}

	target := h.target(top)
	if h.cache != nil {
		h.cache.last.Store(&cachedTarget{key: key, handler: target})
	}

	return target
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

// LogSchemaVersion is the srvr.log.schema value of every record srvr and
// its companion modules (such as otelsetup's otel.export.failed) write.
const LogSchemaVersion = schemaVersion

// The log schema: every event name and attribute key srvr writes. It is a
// public, versioned commitment (see LOGGING.md): adding a key keeps
// schemaVersion; renaming or removing one, or changing its meaning or value
// type, bumps it. TestLogSchemaGolden pins it.
const (
	// schemaVersion is the value of keySchema on every record srvr writes.
	schemaVersion = 1

	eventRequestCompleted = "http.request.completed"
	eventRequestFailed    = "http.request.failed"
	eventStarted          = "server.started"
	eventStopping         = "server.stopping"
	eventStopped          = "server.stopped"
	eventFailed           = "server.failed"
	eventHookFailed       = "server.shutdown_hook.failed"
	eventConfigWarning    = "server.config.warning"
	eventObserverFault    = "srvr.observer.fault"

	keyServerAddress     = "server.address"
	keyDrainDuration     = "srvr.drain.duration_ms"
	keyDrainForced       = "srvr.drain.forced"
	keyHookPhase         = "srvr.shutdown_hook.phase"
	keyHookIndex         = "srvr.shutdown_hook.index"
	keyFunction          = "code.function.name"
	keyExceptionMsg      = "exception.message"
	keyServiceName       = "service.name"
	keyServiceVersion    = "service.version"
	keyVCSRevision       = "vcs.revision"
	keyErrorType         = "error.type"
	keyErrorKind         = "error.kind"
	keyErrorChain        = "error.chain"
	keyErrorTruncated    = "srvr.error.truncated"
	keyFingerprint       = "error.fingerprint"
	keyExceptionType     = "exception.type"
	keyStacktrace        = "exception.stacktrace"
	keyStackTruncated    = "srvr.stacktrace.truncated"
	keyAborted           = "srvr.response.aborted"
	keyFilePath          = "code.file.path"
	keyLineNumber        = "code.line.number"
	keyEnvironment       = "deployment.environment.name"
	keyWarningCode       = "srvr.warning.code"
	keyWarningMessage    = "srvr.warning.message"
	keyObserverFaultKind = "srvr.observer.fault.kind"
	keyObserverType      = "srvr.observer.type"

	keySchema        = "srvr.log.schema"
	keyTraceID       = "trace_id"
	keySpanID        = "span_id"
	keyMethod        = "http.request.method"
	keyRoute         = "http.route"
	keyStatus        = "http.response.status_code"
	keyBodySize      = "http.response.body.size"
	keyDuration      = "duration_ms"
	keyUserAgent     = "user_agent.original"
	keyAttrTruncated = "srvr.attr.truncated"
	keyCommitted     = "srvr.response.committed"
	keyURLPath       = "url.path"
	keyURLQuery      = "url.query"
	keyClientAddress = "client.address"
	keyProblem       = "problem"
	keyProblemStatus = "status"
	keyProblemType   = "type"
	keyProblemTitle  = "title"
)

// WarningCode is the srvr.warning.code of a server.config.warning record:
// a stable snake_case identifier and part of the log schema, so every code
// is declared here.
type WarningCode string

// The warning codes of srvr's engines.
const (
	// WarnGinDebugMode: gin's process-wide mode is debug at construction.
	WarnGinDebugMode WarningCode = "gin_debug_mode"
	// WarnFiberHeaderLimitCapped: a MaxHeaderBytes the consumer set is
	// above the 64 KiB fiber can buffer per connection, so it was capped.
	WarnFiberHeaderLimitCapped WarningCode = "fiber_header_limit_capped"
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

// truncatedSuffix marks a value srvr cut.
const truncatedSuffix = "…[truncated]"

// exceeds reports whether n bytes are more than maxBytes; a negative
// maxBytes disables the limit.
func exceeds(n, maxBytes int) bool { return maxBytes >= 0 && n > maxBytes }

// cutString returns s cut to at most max bytes at a UTF-8 boundary plus
// truncatedSuffix, and whether it cut. A negative max disables cutting.
func cutString(s string, maxBytes int) (string, bool) {
	if !exceeds(len(s), maxBytes) {
		return s, false
	}

	n := maxBytes
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}

	return s[:n] + truncatedSuffix, true
}

// owned is a value srvr bounded itself (an exception message or stack
// with its own, larger limit; error.chain with at most maxChainErrors
// types); the record handler writes the wrapped value as is, never cut by
// MaxAttrBytes.
type owned struct{ v slog.Value }

// ownAttr returns the attribute key holding v as srvr's own value.
func ownAttr(key string, v slog.Value) slog.Attr { return slog.Any(key, owned{v}) }

// truncator cuts the values of one record or step.
type truncator struct {
	maxBytes int
	cut      bool
}

// attr returns a with every string, error text, value of any other kind
// (capAny) and resolved LogValuer cut, recursing into groups.
func (t *truncator) attr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()

	switch v.Kind() { //nolint:exhaustive // only strings, errors and groups carry text to cut.
	case slog.KindString:
		s, cut := cutString(v.String(), t.maxBytes)
		t.cut = t.cut || cut

		return slog.String(a.Key, s)
	case slog.KindGroup:
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(t.attrs(v.Group())...)}
	case slog.KindAny:
		switch x := v.Any().(type) {
		case owned:
			return slog.Attr{Key: a.Key, Value: x.v}
		case error:
			// The handler would format the error again; the text is ready,
			// read guarded against a typed nil and a panicking Error.
			s, cut := cutString(errorText(x), t.maxBytes)
			t.cut = t.cut || cut

			return slog.String(a.Key, s)
		default:
			return t.capAny(a.Key, v)
		}
	}

	return slog.Attr{Key: a.Key, Value: v}
}

// capAny returns v unchanged when its formatted text fits maxBytes (the
// base handler then renders it as usual), and that text cut otherwise. Its
// formatting is guarded: a value whose String panics becomes the
// ValueText placeholder. A negative maxBytes keeps every value.
func (t *truncator) capAny(key string, v slog.Value) slog.Attr {
	if t.maxBytes < 0 {
		return slog.Attr{Key: key, Value: v}
	}

	text, panicked := valueText(v.Any())
	if panicked {
		return slog.String(key, text)
	}

	s, cut := cutString(text, t.maxBytes)
	if !cut {
		return slog.Attr{Key: key, Value: v}
	}

	t.cut = true

	return slog.String(key, s)
}

// attrs returns attrs with their values cut.
func (t *truncator) attrs(attrs []slog.Attr) []slog.Attr {
	out := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		out[i] = t.attr(a)
	}

	return out
}

// record returns r with its message and attributes cut, or r itself when
// nothing needs rewriting. The message of a record srvr writes itself
// (own) is a fixed event name of the log schema and is never cut. A
// negative limit disables cutting, not the rewrite: srvr's own text and
// error values still reach the base handler as plain strings.
func (t *truncator) record(r slog.Record, own bool) slog.Record {
	if !needsRewrite(r, t.maxBytes) && (own || !exceeds(len(r.Message), t.maxBytes)) {
		return r
	}

	msg := r.Message
	if !own {
		var cut bool
		msg, cut = cutString(msg, t.maxBytes)
		t.cut = t.cut || cut
	}

	out := slog.NewRecord(r.Time, r.Level, msg, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(t.attr(a))

		return true
	})

	return out
}

// needsRewrite reports whether an attribute of r may need rewriting. Other
// kinds pass through unchanged.
func needsRewrite(r slog.Record, maxBytes int) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		found = mayNeedRewrite(a.Value, maxBytes)

		return !found
	})

	return found
}

// mayNeedRewrite reports whether v must be rewritten for the base handler:
// a string longer than maxBytes; srvr's own value or an error, always
// unwrapped or handed on as text; any other value of KindAny while a limit
// is set, whose size is only known once formatted; or a group or
// LogValuer, which may hold any of these.
func mayNeedRewrite(v slog.Value, maxBytes int) bool {
	switch v.Kind() {
	case slog.KindString:
		return exceeds(len(v.String()), maxBytes)
	case slog.KindGroup, slog.KindLogValuer:
		return true
	case slog.KindAny:
		switch v.Any().(type) {
		case owned, error:
			return true
		default:
			return maxBytes >= 0
		}
	default:
		return false
	}
}
