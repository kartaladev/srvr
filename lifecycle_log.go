package srvr

import (
	"context"
	"log/slog"
	"reflect"
	"runtime"
	"time"
)

// logEvent writes one of srvr's own records, outside any request:
// schema version, the event's attributes, then the service attributes.
func (s *Server) logEvent(ctx context.Context, level slog.Level, event string, attrs ...slog.Attr) {
	if !s.log.Enabled(ctx, level) {
		return
	}

	r := newOwnRecord(level, event)
	r.AddAttrs(attrs...)
	r.AddAttrs(s.serviceAttrs...)
	s.log.handle(ctx, r, true)
}

// newOwnRecord starts one of srvr's own records: no source (PC 0) and the
// schema version first.
func newOwnRecord(level slog.Level, event string) slog.Record {
	r := slog.NewRecord(time.Now(), level, event, 0)
	r.AddAttrs(slog.Int(keySchema, schemaVersion))

	return r
}

// millis returns d in milliseconds, for the *_ms keys of the log schema.
func millis(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// funcName returns the qualified name of fn, or "" when unknown.
func funcName(fn any) string {
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func || v.IsNil() {
		return ""
	}

	if f := runtime.FuncForPC(v.Pointer()); f != nil {
		return f.Name()
	}

	return ""
}
