package engine

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"unsafe"
)

// DebugBufferConfig configures per-request debug buffering. Consumers use
// it as srvr.DebugBufferConfig, inside LogConfig or with
// srvr.WithDebugBufferConfig; every zero field is the default.
type DebugBufferConfig struct {
	// Enabled turns buffering on. Default off.
	Enabled bool `json:"enabled" yaml:"enabled" env:"ENABLED"`
}

// The default per-request limits: enough records for a request's story,
// in bounded memory.
const (
	defaultBufferRecords = 100
	defaultBufferBytes   = 64 << 10
)

// bufferPolicy is the resolved debug buffer configuration of a server.
type bufferPolicy struct {
	level       slog.Level
	flushStatus int
	maxRecords  int
	maxBytes    int
}

// newBufferPolicy returns the policy of c, or nil when buffering is off.
func newBufferPolicy(c DebugBufferConfig) *bufferPolicy {
	if !c.Enabled {
		return nil
	}

	return &bufferPolicy{
		level:       slog.LevelDebug,
		flushStatus: http.StatusInternalServerError,
		maxRecords:  defaultBufferRecords,
		maxBytes:    defaultBufferBytes,
	}
}

// entry is one buffered record: the handler it was logged through (with
// its recorded steps), the context it was logged with, the record, cut
// when it was buffered, and its estimated size.
type entry struct {
	handler *logHandler
	ctx     context.Context //nolint:containedctx // replayed with the record, as slog's Handle receives it.
	record  slog.Record
	cut     bool
	size    int
}

// entryOverhead is the memory an entry holds besides its text: without it,
// many tiny records would count as a few bytes each.
const entryOverhead = int(unsafe.Sizeof(entry{}))

// wordSize is the size estimate of a value of fixed size.
const wordSize = 8

// newEntry returns the entry of r, already cut, with its size estimated
// from the message and the text of its attributes.
func newEntry(ctx context.Context, h *logHandler, r slog.Record, cut bool) entry {
	size := entryOverhead + len(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		size += attrSize(a)

		return true
	})

	return entry{handler: h, ctx: ctx, record: r, cut: cut, size: size}
}

// attrSize estimates the bytes a's key and value hold: strings by length,
// groups by their members, any other value by its formatted text, and
// fixed-size kinds as one machine word.
func attrSize(a slog.Attr) int {
	size := len(a.Key)

	switch v := a.Value; v.Kind() {
	case slog.KindString:
		size += len(v.String())
	case slog.KindGroup:
		for _, m := range v.Group() {
			size += attrSize(m)
		}
	case slog.KindAny, slog.KindLogValuer:
		text, _ := valueText(v.Any())
		size += len(text)
	default: // a number, time, duration or bool
		size += wordSize
	}

	return size
}

// requestBuffer holds the below-level records of one request until its
// outcome is known, within the policy's limits: the oldest records are
// evicted to make room and counted as dropped.
type requestBuffer struct {
	policy *bufferPolicy

	mu      sync.Mutex
	sealed  bool
	entries []entry
	bytes   int
	dropped int
}

// add appends e, evicting the oldest entries beyond the limits; a sealed
// buffer takes nothing. A record larger than the byte limit on its own is
// dropped.
func (b *requestBuffer) add(e entry) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.sealed {
		return
	}

	if e.size > b.policy.maxBytes {
		b.dropped++

		return
	}

	evict := 0
	for evict < len(b.entries) && (len(b.entries)-evict >= b.policy.maxRecords || b.bytes+e.size > b.policy.maxBytes) {
		b.bytes -= b.entries[evict].size
		evict++
	}

	if evict > 0 {
		clear(b.entries[:evict])
		b.entries = b.entries[evict:]
		b.dropped += evict
	}

	b.entries = append(b.entries, e)
	b.bytes += e.size
}

// open reports whether the buffer still takes records.
func (b *requestBuffer) open() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	return !b.sealed
}

// seal stops the buffer taking records and returns what it holds.
func (b *requestBuffer) seal() []entry {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.sealed = true
	entries := b.entries
	b.entries = nil

	return entries
}

// shouldFlush reports whether the buffer of req, which sent status, is
// flushed: the request panicked (or was aborted) or failed at or above the
// flush threshold.
func (p *bufferPolicy) shouldFlush(req *RequestState, status int) bool {
	panicked := req.aborted || (req.failure != nil && req.failure.panic != nil)

	return panicked || failureStatus(status, req.failure) >= p.flushStatus
}

// failureStatus returns the status a request failed with for the flush
// decision: the status resolved for a returned error, even when the
// response was committed with another one, otherwise the status sent.
func failureStatus(status int, fail *failure) int {
	if fail != nil && fail.problem != nil {
		return fail.problem.Status
	}

	return status
}

// flushBuffer seals the request's buffer and, when the request failed at
// or above the flush threshold or panicked and its access record is
// written, writes the buffered records in logging order.
func (c *Core) flushBuffer(req *RequestState, status int, written bool) {
	if req.buffer == nil {
		return
	}

	entries := req.buffer.seal()
	if !written || !c.log.buffering.shouldFlush(req, status) {
		return
	}

	for _, e := range entries {
		e.handler.write(e.ctx, e.record, e.cut, true)
	}
}
