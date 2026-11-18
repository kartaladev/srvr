package srvr_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
)

// observerFunc adapts a function to srvr.Observer.
type observerFunc func(ctx context.Context, info srvr.RequestInfo) (context.Context, func(srvr.ResultInfo))

func (f observerFunc) Begin(ctx context.Context, info srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
	return f(ctx, info)
}

// callLog records calls in order across observers.
type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *callLog) add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.calls = append(l.calls, s)
}

func (l *callLog) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return slices.Clone(l.calls)
}

// named returns an observer that logs name.Begin and name.end.
func named(log *callLog, name string) srvr.Observer {
	return observerFunc(func(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
		log.add(name + ".Begin")

		return ctx, func(srvr.ResultInfo) { log.add(name + ".end") }
	})
}

// TestObserver_Compose covers request-observer "Composition", all rows.
func TestObserver_Compose(t *testing.T) {
	t.Parallel()

	tests := []struct {
		names []string
		order []string
	}{
		{[]string{"A", "B"}, []string{"A.Begin", "B.Begin", "B.end", "A.end"}},
		{[]string{"A"}, []string{"A.Begin", "A.end"}},
	}

	for _, tt := range tests {
		t.Run(tt.order[0], func(t *testing.T) {
			t.Parallel()

			log := &callLog{}
			var opts []srvr.Option
			for _, n := range tt.names {
				opts = append(opts, srvr.WithObserver(named(log, n)))
			}

			base, _ := loggedServer(t, okRoute("/ping"), opts...)
			get(t, base+"/ping")

			if got := log.get(); !slices.Equal(got, tt.order) {
				t.Errorf("call order = %v, want %v", got, tt.order)
			}
		})
	}
}

// TestObserver_ComposeContext checks that each Begin receives the context
// the previous one returned.
func TestObserver_ComposeContext(t *testing.T) {
	t.Parallel()

	type key struct{}

	var seen atomic.Value

	first := observerFunc(func(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
		return context.WithValue(ctx, key{}, "from A"), nil
	})
	second := observerFunc(func(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
		seen.Store(ctx.Value(key{}))

		return ctx, nil
	})

	base, _ := loggedServer(t, okRoute("/ping"), srvr.WithObserver(first), srvr.WithObserver(second))
	get(t, base+"/ping")

	if got := seen.Load(); got != "from A" {
		t.Errorf("B saw %v, want the context A returned", got)
	}
}

// panicky is a pointer observer type, for the typed-nil row.
type panicky struct{}

func (*panicky) Begin(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
	return ctx, nil
}

// TestObserver_FaultsConstruction covers request-observer "Observer
// faults", the nil and typed-nil rows.
func TestObserver_FaultsConstruction(t *testing.T) {
	t.Parallel()

	for name, o := range map[string]srvr.Observer{"nil": nil, "typed-nil": (*panicky)(nil)} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := srvr.New(srvr.WithObserver(o)); !errors.Is(err, srvr.ErrConfig) {
				t.Fatalf("New = %v, want an error wrapping ErrConfig", err)
			}
		})
	}
}

// panickingError is an error whose Error method panics.
type panickingError struct{}

func (panickingError) Error() string { panic("Error() panicked") }

// valuePanics is a context whose Value panics.
type valuePanics struct{ context.Context }

func (valuePanics) Value(any) any { panic("Value panicked") }

// TestObserver_Faults covers request-observer "Observer faults", the
// request rows, plus a returned context whose Value panics and an end
// panic whose value's Error panics.
func TestObserver_Faults(t *testing.T) {
	t.Parallel()

	type ctxKey struct{}

	tests := []struct {
		name     string
		begin    func(ctx context.Context) (context.Context, func(srvr.ResultInfo))
		requests int
		minTime  time.Duration
		kind     string // the expected fault kind, empty for none
	}{
		{"panics in Begin", func(context.Context) (context.Context, func(srvr.ResultInfo)) { panic("boom") }, 1, 0, "begin_panic"},
		{"nil context", func(context.Context) (context.Context, func(srvr.ResultInfo)) { return nil, func(srvr.ResultInfo) {} }, 1, 0, ""},
		{"foreign context", func(context.Context) (context.Context, func(srvr.ResultInfo)) {
			return context.WithValue(context.Background(), ctxKey{}, 1), nil
		}, 1, 0, ""},
		{"nil end", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) { return ctx, nil }, 1, 0, ""},
		{"panics in end", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) {
			return ctx, func(srvr.ResultInfo) { panic("boom") }
		}, 1, 0, "end_panic"},
		{"panics in Begin on 1000 requests", func(context.Context) (context.Context, func(srvr.ResultInfo)) { panic("boom") }, 1000, 0, "begin_panic"},
		{"blocks in Begin", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) {
			time.Sleep(50 * time.Millisecond)

			return ctx, nil
		}, 1, 50 * time.Millisecond, ""},
		{"blocks in end", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) {
			return ctx, func(srvr.ResultInfo) { time.Sleep(50 * time.Millisecond) }
		}, 1, 50 * time.Millisecond, ""},
		{"panics in end with ErrAbortHandler", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) {
			return ctx, func(srvr.ResultInfo) { panic(http.ErrAbortHandler) }
		}, 1, 0, "end_panic"},
		{"returns a context whose Value panics", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) {
			return valuePanics{ctx}, nil
		}, 1, 0, "begin_panic"},
		{"panics in end with an error whose Error panics", func(ctx context.Context) (context.Context, func(srvr.ResultInfo)) {
			return ctx, func(srvr.ResultInfo) { panic(panickingError{}) }
		}, 1, 0, "end_panic"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs := observerFunc(func(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
				return tt.begin(ctx)
			})
			out := &logBuffer{}
			srv := newServer(t, srvr.WithObserver(obs), srvr.WithLogOutput(out))

			var handlerHasTrace atomic.Bool

			mustNil(t, srv.Register(on("", "/ping", func(w http.ResponseWriter, r *http.Request) error {
				_, ok := srvr.TraceFromContext(r.Context())
				handlerHasTrace.Store(ok)
				w.WriteHeader(http.StatusOK)

				return nil
			})))
			base := startServer(t, srv)

			for range tt.requests {
				start := time.Now()
				if got := get(t, base+"/ping").status; got != http.StatusOK {
					t.Fatalf("status = %d, want 200", got)
				}

				if d := time.Since(start); d < tt.minTime {
					t.Fatalf("response after %v, want at least %v", d, tt.minTime)
				}
			}

			if !handlerHasTrace.Load() {
				t.Error("the handler's context lost srvr's request state")
			}

			eventually(t, func() bool { return len(out.find(t, "http.request.completed")) == tt.requests }, "missing access records")

			faults := out.find(t, "srvr.observer.fault")
			switch {
			case tt.kind == "" && len(faults) != 0:
				t.Fatalf("fault records = %v, want none", faults)
			case tt.kind != "" && len(faults) != 1:
				t.Fatalf("fault records = %d, want 1:\n%s", len(faults), out.String())
			case tt.kind != "":
				if f := faults[0]; f["srvr.observer.fault.kind"] != tt.kind || f["level"] != "ERROR" {
					t.Errorf("fault record = %v, want kind %s at ERROR", f, tt.kind)
				}
			}
		})
	}
}

// orderedOutput records whether the access record was written after the
// end function returned.
type orderedOutput struct {
	logBuffer

	endReturned atomic.Bool
	afterEnd    atomic.Bool
}

func (o *orderedOutput) Write(p []byte) (int, error) {
	if o.endReturned.Load() {
		o.afterEnd.Store(true)
	}

	return o.logBuffer.Write(p)
}

// TestObserver_FaultsBlockingEnd covers the blocking end-function row:
// the access record is written after the end function returns.
func TestObserver_FaultsBlockingEnd(t *testing.T) {
	t.Parallel()

	out := &orderedOutput{}
	obs := observerFunc(func(ctx context.Context, _ srvr.RequestInfo) (context.Context, func(srvr.ResultInfo)) {
		return ctx, func(srvr.ResultInfo) {
			time.Sleep(50 * time.Millisecond)
			out.endReturned.Store(true)
		}
	})

	srv := newServer(t, srvr.WithObserver(obs), srvr.WithLogOutput(out))
	mustNil(t, srv.Register(okRoute("/ping")))

	start := time.Now()
	if got := get(t, startServer(t, srv)+"/ping").status; got != http.StatusOK || time.Since(start) < 50*time.Millisecond {
		t.Fatalf("status %d after %v", got, time.Since(start))
	}

	eventually(t, func() bool { return len(out.find(t, "http.request.completed")) == 1 }, "no access record")

	if !out.afterEnd.Load() {
		t.Error("the access record was written before the end function returned")
	}
}
