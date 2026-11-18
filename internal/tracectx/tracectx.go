// Package tracectx reads and generates W3C Trace Context (level 1) ids
// without OpenTelemetry.
package tracectx

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

// Field lengths of a traceparent header, in hex characters.
const (
	versionLen = 2
	traceIDLen = 32
	spanIDLen  = 16
	flagsLen   = 2
	// headerLen is the length of a version 00 header:
	// version "-" trace-id "-" parent-id "-" flags.
	headerLen = versionLen + 1 + traceIDLen + 1 + spanIDLen + 1 + flagsLen
	// maxHeaderLen caps what is parsed, so a hostile header costs nothing.
	maxHeaderLen = 512
)

// Trace is the trace context of one request.
type Trace struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
	Sampled      bool
}

// FromHeaders returns the trace context for a request whose traceparent
// header values are values: the incoming trace continued with a new span
// when there is exactly one valid value, a new trace otherwise.
func FromHeaders(values []string) Trace {
	if len(values) == 1 {
		if tr, ok := parse(values[0]); ok {
			tr.SpanID = New().SpanID

			return tr
		}
	}

	return New()
}

// New returns a new trace with random trace and span ids, both never all
// zeros, from one read of crypto/rand. crypto/rand.Read never fails on
// supported platforms; on failure the program crashes (Go 1.24+).
func New() Trace {
	var raw [(traceIDLen + spanIDLen) / 2]byte

	var ids [traceIDLen + spanIDLen]byte
	for {
		_, _ = rand.Read(raw[:])
		hex.Encode(ids[:], raw[:])

		s := string(ids[:])
		if tid, sid := s[:traceIDLen], s[traceIDLen:]; !isZero(tid) && !isZero(sid) {
			return Trace{TraceID: tid, SpanID: sid}
		}
	}
}

// parse parses a traceparent value strictly: lowercase hex, a known
// version layout, no all-zero ids. A future version may append fields
// after a "-"; version 00 may not.
func parse(h string) (Trace, bool) {
	if len(h) < headerLen || len(h) > maxHeaderLen {
		return Trace{}, false
	}

	version := h[:versionLen]
	if !isHex(version) || version == "ff" {
		return Trace{}, false
	}

	if len(h) > headerLen && (version == "00" || h[headerLen] != '-') {
		return Trace{}, false
	}

	if h[2] != '-' || h[35] != '-' || h[52] != '-' {
		return Trace{}, false
	}

	traceID, parentID, flags := h[3:35], h[36:52], h[53:55]
	if !Valid(traceID, parentID) || !isHex(flags) {
		return Trace{}, false
	}

	// The sampled flag is bit 0 of the flags byte, so the hex digits are
	// decoded first: the ASCII codes of 'a'-'f' have the opposite parity of
	// the values they stand for. isHex already accepted both digits.
	var b [1]byte
	_, _ = hex.Decode(b[:], []byte(flags))

	return Trace{TraceID: traceID, ParentSpanID: parentID, Sampled: b[0]&1 == 1}, true
}

// isHex reports whether s is lowercase hex; callers pass fixed-length
// fields.
func isHex(s string) bool {
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}

func isZero(s string) bool { return strings.Trim(s, "0") == "" }

// Valid reports whether traceID and spanID are well-formed W3C ids: 32 and
// 16 lowercase hex characters, neither all zeros.
func Valid(traceID, spanID string) bool {
	return len(traceID) == traceIDLen && len(spanID) == spanIDLen &&
		isHex(traceID) && isHex(spanID) && !isZero(traceID) && !isZero(spanID)
}
