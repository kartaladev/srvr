package srvr_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

var errQuota = errors.New("quota exceeded")

// TestErrorExtension covers error-responses / Scenario: Consumer mapper and hook.
func TestErrorExtension(t *testing.T) {
	t.Parallel()

	type seen struct {
		err    error
		status int
	}

	tests := []struct {
		name   string
		opts   func(got *atomic.Pointer[seen]) []srvr.Option
		err    error
		status int
		hook   bool // the hook must have seen err with the response status
	}{
		{
			name: "consumer mapper",
			opts: func(*atomic.Pointer[seen]) []srvr.Option {
				return []srvr.Option{srvr.WithProblemOptions(prblm.WithMappers(prblm.Status(errQuota, http.StatusTooManyRequests)))}
			},
			err: errQuota, status: http.StatusTooManyRequests,
		},
		{
			name: "consumer hook",
			opts: func(got *atomic.Pointer[seen]) []srvr.Option {
				return []srvr.Option{srvr.WithErrorHook(func(_ context.Context, err error, p *prblm.Problem) {
					got.Store(&seen{err: err, status: p.Status})
				})}
			},
			err: prblm.ErrNotFound, status: http.StatusNotFound, hook: true,
		},
		{
			name: "panicking consumer hook",
			opts: func(*atomic.Pointer[seen]) []srvr.Option {
				return []srvr.Option{srvr.WithErrorHook(func(context.Context, error, *prblm.Problem) { panic("hook") })}
			},
			err: prblm.ErrNotFound, status: http.StatusNotFound,
		},
		{
			name: "consumer prblm.OnError does not replace srvr's hook",
			opts: func(got *atomic.Pointer[seen]) []srvr.Option {
				return []srvr.Option{
					srvr.WithProblemOptions(prblm.OnError(func(context.Context, error, *prblm.Problem) { panic("replaced") })),
					srvr.WithErrorHook(func(_ context.Context, err error, p *prblm.Problem) {
						got.Store(&seen{err: err, status: p.Status})
					}),
				}
			},
			err: prblm.ErrNotFound, status: http.StatusNotFound, hook: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got atomic.Pointer[seen]
			srv := newServer(t, tt.opts(&got)...)
			mustNil(t, srv.Register(configFunc(func(g *srvr.Group) {
				g.GET("/x", func(http.ResponseWriter, *http.Request) error { return tt.err })
			})))
			base := startServer(t, srv)

			if res := get(t, base+"/x"); res.status != tt.status {
				t.Fatalf("status = %d, want %d", res.status, tt.status)
			}
			if tt.hook {
				s := got.Load()
				if s == nil || !errors.Is(s.err, tt.err) || s.status != tt.status {
					t.Fatalf("hook saw %+v, want %v with status %d", s, tt.err, tt.status)
				}
			}
		})
	}
}

// TestErrorExtension_Config: invalid error options fail construction.
func TestErrorExtension_Config(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, message string
		opt           srvr.Option
	}{
		{"nil hook", "ErrorHook", srvr.WithErrorHook(nil)},
		{"invalid prblm option", "ProblemOptions", srvr.WithProblemOptions(prblm.WithNamespace("not a uri"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv, err := srvr.New(tt.opt)
			if srv != nil || !errors.Is(err, srvr.ErrConfig) || !strings.Contains(err.Error(), tt.message) {
				t.Fatalf("New = %v, %v; want ErrConfig naming %s", srv, err, tt.message)
			}
		})
	}
}
