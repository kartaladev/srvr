package engine_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/internal/engine"
)

// TestCollapseMethods: a pattern one configurer registered for the whole
// method set is listed once with Method ""; anything less stays as is.
func TestCollapseMethods(t *testing.T) {
	t.Parallel()

	all := []string{"GET", "POST"}

	tests := []struct {
		name    string
		methods []string
		in      engine.Routes
		want    engine.Routes
	}{
		{"every method", all, engine.Routes{{"GET", "/x", "A"}, {"POST", "/x", "A"}}, engine.Routes{{"", "/x", "A"}}},
		{"some methods", all, engine.Routes{{"GET", "/x", "A"}}, engine.Routes{{"GET", "/x", "A"}}},
		{"two configurers", all, engine.Routes{{"GET", "/x", "A"}, {"POST", "/x", "B"}}, engine.Routes{{"GET", "/x", "A"}, {"POST", "/x", "B"}}},
		{"a method outside the set", all, engine.Routes{{"GET", "/x", "A"}, {"POST", "/x", "A"}, {"PATCH", "/x", "A"}}, engine.Routes{{"", "/x", "A"}, {"PATCH", "/x", "A"}}},
		{"empty set", nil, engine.Routes{{"GET", "/x", "A"}}, engine.Routes{{"GET", "/x", "A"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := engine.CollapseMethods(tt.in, tt.methods); !slices.Equal(got, tt.want) {
				t.Fatalf("CollapseMethods = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCheckRoutes covers a check's error, panic and its context ending.
func TestCheckRoutes(t *testing.T) {
	t.Parallel()

	// release ends the check srvr abandons in "context ends" once every
	// subtest is done, so the test leaves no goroutine behind.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })

	tests := []struct {
		name  string
		check func(context.Context, engine.Routes) error
		ctx   time.Duration // 0: no deadline
		want  func(error) bool
	}{
		{"error", func(context.Context, engine.Routes) error { return errors.New("collides") }, 0, func(err error) bool {
			return errors.Is(err, srvr.ErrRouteConfig) && strings.Contains(err.Error(), "collides")
		}},
		{"panic", func(context.Context, engine.Routes) error { panic("boom") }, 0, func(err error) bool {
			return errors.Is(err, srvr.ErrRouteConfig) && strings.Contains(err.Error(), "boom")
		}},
		{"context ends", func(context.Context, engine.Routes) error { <-release; return nil }, 20 * time.Millisecond, func(err error) bool {
			return errors.Is(err, context.DeadlineExceeded)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			core, err := engine.ResolveConfig([]engine.Option{srvr.WithRouteCheck(tt.check)})
			if err != nil {
				t.Fatal(err)
			}

			ctx := t.Context()
			if tt.ctx > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tt.ctx)
				defer cancel()
			}

			if err := core.CheckRoutes(ctx, engine.Routes{{Method: "GET", Pattern: "/x"}}); !tt.want(err) {
				t.Fatalf("CheckRoutes = %v", err)
			}
		})
	}
}
