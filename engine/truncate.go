package engine

import (
	"log/slog"
	"unicode/utf8"
)

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

// ownText is a string srvr sized itself (an exception message or stack
// with its own, larger limit); the record handler writes it as is.
type ownText string

// truncator cuts the values of one record or step.
type truncator struct {
	maxBytes int
	cut      bool
}

// attr returns a with every string, error text and resolved LogValuer
// cut, recursing into groups.
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
		case ownText:
			return slog.String(a.Key, string(x))
		case error:
			// The handler would format the error again; the text is ready,
			// read guarded against a typed nil and a panicking Error.
			s, cut := cutString(errorText(x), t.maxBytes)
			t.cut = t.cut || cut

			return slog.String(a.Key, s)
		}
	}

	return slog.Attr{Key: a.Key, Value: v}
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
// a string longer than maxBytes; srvr's own text or an error, always handed
// on as a string; or a group or LogValuer, which may hold any of these.
func mayNeedRewrite(v slog.Value, maxBytes int) bool {
	switch v.Kind() {
	case slog.KindString:
		return exceeds(len(v.String()), maxBytes)
	case slog.KindGroup, slog.KindLogValuer:
		return true
	case slog.KindAny:
		_, isOwn := v.Any().(ownText)
		_, isErr := v.Any().(error)

		return isOwn || isErr
	default:
		return false
	}
}
