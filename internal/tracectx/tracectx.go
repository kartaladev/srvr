// Package tracectx reads and generates W3C Trace Context (level 1) ids
// without OpenTelemetry.
package tracectx

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
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

// rawLen is the number of random bytes of a trace's two ids.
const rawLen = (traceIDLen + spanIDLen) / 2

// New returns a new trace with random trace and span ids, both never all
// zeros, drawn from a batch of crypto/rand bytes (see idBatch).
// crypto/rand.Read never fails on supported platforms; on failure the
// program crashes (Go 1.24+) rather than generate predictable ids.
func New() Trace {
	var raw [rawLen]byte

	var ids [traceIDLen + spanIDLen]byte

	b := idBatches.Get().(*idBatch) //nolint:forcetypeassert // the pool holds only *idBatch
	defer idBatches.Put(b)

	for {
		b.next(&raw)
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

// batchTraces is the number of traces' ids one crypto/rand read yields.
// One read per request was a third of the request pipeline's CPU on darwin,
// where crypto/rand is a system call (followups-hardening design D7).
const batchTraces = 32

// idBatch holds crypto/rand bytes for the next traces' ids. It is used by
// one goroutine at a time (idBatches), so no bytes are handed out twice,
// and the bytes it hands out are cleared, so it holds only future ids. The
// zero value is an empty batch.
//
// Security trade-off, accepted by the owner (design D7): up to
// batchTraces*rawLen (768) bytes of future ids per pooled batch stay in
// process memory, so a heap disclosure (a core dump, a memory-read bug)
// reveals the next ids drawn from that batch; and a process or VM snapshot
// restored twice repeats the ids of the batches it copied, where reading
// crypto/rand per trace gets the kernel's reseed. Trace ids are not
// secrets (srvr writes them to responses and logs): W3C Trace Context
// asks only that they be random and unique. The source stays crypto/rand.
type idBatch struct {
	buf  [batchTraces * rawLen]byte
	left int // bytes of buf not handed out yet, at its start
}

// idBatches pools batches; one the GC drops takes its unused ids with it,
// and a new one starts empty.
var idBatches = sync.Pool{New: func() any { return new(idBatch) }}

// next fills raw with the batch's next bytes, refilling the batch from
// crypto/rand when it is used up, and clears the bytes it handed out.
func (b *idBatch) next(raw *[rawLen]byte) {
	if b.left == 0 {
		_, _ = rand.Read(b.buf[:])
		b.left = len(b.buf)
	}

	b.left -= rawLen
	drawn := b.buf[b.left : b.left+rawLen]
	copy(raw[:], drawn)
	clear(drawn)
}
