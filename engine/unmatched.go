package engine

import (
	"net/http"
	"sync/atomic"
	"time"
)

// UnmatchedLimitConfig limits the http.request.failed records of requests
// no route matched (404 and 405 answered by the router), which a path
// scanner can otherwise multiply at will. Consumers use it as
// srvr.UnmatchedLimitConfig, set as AccessLogConfig.UnmatchedLimit or with
// srvr.WithUnmatchedRecordLimit.
//
// The zero value is no limit, the default: failed records are the triage
// record and are never dropped unless the consumer chooses to, so setting
// a limit loosens a triage default. With a limit, the first Burst
// unmatched records of every Interval are written and the rest are
// counted; the count is added as srvr.unmatched.suppressed to the next
// unmatched record written and to server.stopped. Handler errors, panics
// and 5xx records are never limited, and observers still see every
// request.
type UnmatchedLimitConfig struct {
	// Burst is the number of unmatched records written per Interval. 0, the
	// default, means no limit; a negative value fails construction.
	Burst int `json:"burst" yaml:"burst" env:"BURST"`
	// Interval is the length of the window Burst applies to. It must be
	// above zero when Burst is; it is ignored when Burst is 0.
	Interval time.Duration `json:"interval" yaml:"interval" env:"INTERVAL"`
}

// validateUnmatchedLimit checks the unmatched-record limit: no negative
// Burst, and an Interval above zero for a positive one.
func validateUnmatchedLimit(s *Settings) error {
	c := s.Access.UnmatchedLimit

	switch {
	case c.Burst < 0:
		return NewConfigError("UnmatchedLimit.Burst", c.Burst, "must not be negative (0 means no limit)")
	case c.Burst > 0 && c.Interval <= 0:
		return NewConfigError("UnmatchedLimit.Interval", c.Interval, "must be above zero when Burst is set")
	default:
		return nil
	}
}

// unmatchedLimiter is the fixed-window limit of a server's unmatched
// records (design D7), in atomics only: the window is replaced, never
// changed, so its start and count stay consistent without a lock.
type unmatchedLimiter struct {
	burst    int64
	interval time.Duration
	now      func() time.Time
	// window is the current window, nil before the first unmatched record.
	window atomic.Pointer[limitWindow]
	// suppressed counts the records suppressed since the last report.
	suppressed atomic.Int64
}

// limitWindow is one window: when it started and how many records it saw.
type limitWindow struct {
	start time.Time
	count atomic.Int64
}

// newUnmatchedLimiter returns the limiter of c, or nil for no limit.
func newUnmatchedLimiter(c UnmatchedLimitConfig, now func() time.Time) *unmatchedLimiter {
	if c.Burst == 0 {
		return nil
	}

	return &unmatchedLimiter{burst: int64(c.Burst), interval: c.Interval, now: now}
}

// admit reports whether the next unmatched record is written and, when it
// is, the count to report on it; without a limit (nil) every one is. Within a window the first burst records
// are admitted and the rest counted; a record arriving after the window
// ended starts a new one and reports the count, which no other admitted
// record can have taken, as a window admits nothing after its first
// suppression.
func (l *unmatchedLimiter) admit() (bool, int64) {
	if l == nil {
		return true, 0
	}

	now := l.now()

	for {
		w := l.window.Load()
		if w == nil || now.Sub(w.start) >= l.interval {
			next := &limitWindow{start: now}
			next.count.Store(1)

			if !l.window.CompareAndSwap(w, next) {
				continue // another request started the window first
			}

			return true, l.suppressed.Swap(0)
		}

		// A full window is only read, so a flood costs one contended
		// write per record, the suppressed count's.
		if w.count.Load() < l.burst && w.count.Add(1) <= l.burst {
			return true, 0
		}

		l.suppressed.Add(1)

		return false, 0
	}
}

// report returns and resets the count not reported yet, for
// server.stopped; 0 without a limit.
func (l *unmatchedLimiter) report() int64 {
	if l == nil {
		return 0
	}

	return l.suppressed.Swap(0)
}

// unmatched reports whether the failed record of the request, ending with
// status, is the record of a request no route matched: a 404 or 405 the
// router answered, not an aborted request, a panic or a rejection.
func (r *RequestState) unmatched(status int) bool {
	return !r.Matched() && !r.aborted && r.Failed() && !r.Panicked() &&
		(status == http.StatusNotFound || status == http.StatusMethodNotAllowed)
}
