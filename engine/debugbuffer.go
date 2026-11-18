package engine

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"
)

// DebugBufferConfig configures per-request debug buffering. Consumers use
// it as srvr.DebugBufferConfig, inside LogConfig or with
// srvr.WithDebugBufferConfig; every zero field is the default.
type DebugBufferConfig struct {
	// Enabled turns buffering on. Default off.
	Enabled bool `json:"enabled" yaml:"enabled" env:"ENABLED"`
	// Level is the lowest level buffered, a log/slog level name such as
	// "debug" or "info-4". Default "debug", the level production
	// normally drops.
	Level string `json:"level" yaml:"level" env:"LEVEL"`
	// MaxRecords caps the records one request buffers; the oldest are
	// evicted. Default 100.
	MaxRecords int `json:"max_records" yaml:"max_records" env:"MAX_RECORDS"`
	// MaxBytes caps the estimated bytes one request buffers; the oldest
	// records are evicted. Default 64 KiB.
	MaxBytes int `json:"max_bytes" yaml:"max_bytes" env:"MAX_BYTES"`
	// TotalBytes caps the estimated bytes all requests of the server
	// buffer together; a record that does not fit is dropped. Default
	// 16 MiB.
	TotalBytes int `json:"total_bytes" yaml:"total_bytes" env:"TOTAL_BYTES"`
}

// The default per-request limits: enough records for a request's story,
// in bounded memory.
const (
	defaultBufferRecords = 100
	defaultBufferBytes   = 64 << 10
	defaultBufferTotal   = 16 << 20
)

// bufferPolicy is the resolved debug buffer configuration of a server.
type bufferPolicy struct {
	level       slog.Level
	flushStatus int
	maxRecords  int
	maxBytes    int
	budget      *budget
}

// budget is the bytes all buffers of a server may hold together.
type budget struct {
	limit int64
	used  atomic.Int64
}

// reserve takes n bytes and reports whether they fit; it never lets used
// exceed limit.
func (b *budget) reserve(n int) bool {
	for {
		used := b.used.Load()
		if used+int64(n) > b.limit {
			return false
		}

		if b.used.CompareAndSwap(used, used+int64(n)) {
			return true
		}
	}
}

// release returns n reserved bytes.
func (b *budget) release(n int) { b.used.Add(-int64(n)) }

// newBufferPolicy returns the policy of c, which enables buffering and
// which validateDebugBuffer accepted, with the defaults applied.
func newBufferPolicy(c DebugBufferConfig) *bufferPolicy {
	p := &bufferPolicy{
		level:       slog.LevelDebug,
		flushStatus: http.StatusInternalServerError,
		maxRecords:  cmp.Or(c.MaxRecords, defaultBufferRecords),
		maxBytes:    cmp.Or(c.MaxBytes, defaultBufferBytes),
		budget:      &budget{limit: int64(cmp.Or(c.TotalBytes, defaultBufferTotal))},
	}

	if c.Level != "" {
		p.level, _ = parseLevel("", c.Level) // validated
	}

	return p
}

// validateDebugBuffer checks an enabled debug buffer configuration: no
// negative limit, a budget that fits one request's buffer, and a level
// name not above a static log level.
func validateDebugBuffer(s *Settings) error {
	c := s.Log.DebugBuffer
	if !c.Enabled {
		return nil
	}

	var errs []error

	if c.Level != "" {
		level, err := parseLevel("DebugBuffer.Level", c.Level)
		if logLevel, static := s.Log.staticLevel(); err == nil && static && level > logLevel {
			err = NewConfigError("DebugBuffer.Level", strconv.Quote(c.Level),
				"is above the log level "+logLevel.String()+": it would buffer records that are written anyway")
		}

		errs = append(errs, err)
	}

	errs = append(errs,
		notNegative("DebugBuffer.MaxRecords", c.MaxRecords),
		notNegative("DebugBuffer.MaxBytes", c.MaxBytes),
		notNegative("DebugBuffer.TotalBytes", c.TotalBytes),
	)

	if total, maxBytes := cmp.Or(c.TotalBytes, defaultBufferTotal), cmp.Or(c.MaxBytes, defaultBufferBytes); total >= 0 && total < maxBytes {
		errs = append(errs, NewConfigError("DebugBuffer.TotalBytes", total,
			"is below DebugBuffer.MaxBytes "+strconv.Itoa(maxBytes)+": one request could never fill its buffer"))
	}

	return errors.Join(errs...)
}

// notNegative returns an error naming field when v, a limit whose zero
// means the default, is negative.
func notNegative(field string, v int) error {
	if v < 0 {
		return NewConfigError(field, v, "must not be negative (0 means the default)")
	}

	return nil
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

	mu        sync.Mutex
	sealed    bool
	entries   []entry
	bytes     int
	dropped   int
	truncated bool
}

// add appends e, evicting the oldest entries beyond the limits; a sealed
// buffer takes nothing. A record larger than the byte limit on its own is
// dropped, and so is one the server's budget cannot take, which marks the
// buffer truncated.
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

	// The entries to evict are counted first, so the budget is asked once
	// for the difference: nothing is evicted for a record it refuses.
	evict, freed := 0, 0
	for evict < len(b.entries) && (len(b.entries)-evict >= b.policy.maxRecords || b.bytes-freed+e.size > b.policy.maxBytes) {
		freed += b.entries[evict].size
		evict++
	}

	if grow := e.size - freed; grow > 0 && !b.policy.budget.reserve(grow) {
		b.dropped++
		b.truncated = true

		return
	} else if grow < 0 {
		b.policy.budget.release(-grow)
	}

	clear(b.entries[:evict])
	b.entries = append(b.entries[evict:], e)
	b.bytes += e.size - freed
	b.dropped += evict
}

// open reports whether the buffer still takes records.
func (b *requestBuffer) open() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	return !b.sealed
}

// sealedBuffer is what a request's buffer held when it was sealed.
type sealedBuffer struct {
	entries   []entry
	dropped   int
	truncated bool
}

// summary returns the srvr.debug_buffer.* attributes of the access record.
func (s sealedBuffer) summary() []slog.Attr {
	return []slog.Attr{
		slog.Int(keyBufferFlushed, len(s.entries)),
		slog.Int(keyBufferDropped, s.dropped),
		slog.Bool(keyBufferTruncated, s.truncated),
	}
}

// seal stops the buffer taking records, returns what it held and
// releases its bytes to the server's budget: the caller writes or drops
// the entries next, and nothing else holds them.
func (b *requestBuffer) seal() sealedBuffer {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.sealed = true
	s := sealedBuffer{entries: b.entries, dropped: b.dropped, truncated: b.truncated}
	b.entries = nil
	b.policy.budget.release(b.bytes)
	b.bytes = 0

	return s
}

// shouldFlush reports whether the buffer of req, which sent status, is
// flushed: the request panicked (or was aborted) or failed at or above the
// flush threshold. A returned error fails with the status resolved for
// it, even when the response was committed with another one.
func (p *bufferPolicy) shouldFlush(req *RequestState, status int) bool {
	fail := req.failure
	if req.aborted || (fail != nil && fail.panic != nil) {
		return true
	}

	if fail != nil && fail.problem != nil {
		status = fail.problem.Status
	}

	return status >= p.flushStatus
}

// flushBuffer seals the request's buffer. When the request failed at or
// above the flush threshold or panicked and its access record is written,
// it writes the buffered records in logging order and returns the summary
// attributes for the access record; otherwise it drops them and returns
// nil.
func (c *Core) flushBuffer(req *RequestState, status int, written bool) []slog.Attr {
	if req.buffer == nil {
		return nil
	}

	sealed := req.buffer.seal()
	if !written || !c.log.buffering.shouldFlush(req, status) {
		return nil
	}

	for _, e := range sealed.entries {
		e.handler.write(e.ctx, e.record, e.cut, true)
	}

	return sealed.summary()
}
