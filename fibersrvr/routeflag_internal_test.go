package fibersrvr

import (
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/srvr"
)

// TestRouteFlagFields pins the unexported fiber.Route fields declared
// reads, so a fiber upgrade that renames them fails here instead of
// turning middleware into duplicate routes.
func TestRouteFlagFields(t *testing.T) {
	t.Parallel()

	rt := reflect.TypeFor[fiber.Route]()
	for _, name := range []string{"use", "mount", "autoHead"} {
		if f, ok := rt.FieldByName(name); !ok || f.Type.Kind() != reflect.Bool {
			t.Errorf("fiber.Route has no bool field %q", name)
		}
	}
}

// TestMountFields pins the unexported fiber fields the sub-app walk reads
// (Route.group, Group.app, App.stack), so srvr's own fiber upgrade that
// renames or retypes them fails here; a consumer's upgrade fails Start
// through the same check (TestMountFieldCheck).
func TestMountFields(t *testing.T) {
	t.Parallel()

	for _, f := range mountFields {
		if err := f.check(); err != nil {
			t.Error(err)
		}
	}
}

// TestModuleVersion: the fiber version named in errors comes from the
// build information, following a replacement, and is "unknown" when the
// module or its version is absent.
func TestModuleVersion(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		deps []*debug.Module
		want string
	}{
		{"no deps", nil, "unknown"},
		{"nil entry", []*debug.Module{nil}, "unknown"},
		{"other module", []*debug.Module{{Path: "example.com/x", Version: "v1.0.0"}}, "unknown"},
		{"fiber", []*debug.Module{{Path: fiberModule, Version: "v3.7.0"}}, "v3.7.0"},
		{"replaced", []*debug.Module{{Path: fiberModule, Version: "v3.7.0", Replace: &debug.Module{Path: "example.com/fork", Version: "v3.7.1-fork"}}}, "v3.7.1-fork"},
		{"local replacement without version", []*debug.Module{{Path: fiberModule, Version: "v3.7.0", Replace: &debug.Module{Path: "../fiber"}}}, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := moduleVersion(tc.deps, fiberModule); got != tc.want {
				t.Fatalf("moduleVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestWalkDefensive: the sub-app walk stops at a mount cycle and at a
// value that is not an app, instead of recursing forever or panicking.
func TestWalkDefensive(t *testing.T) {
	t.Parallel()

	ok := func(c fiber.Ctx) error { return c.SendStatus(200) }

	t.Run("mount cycle", func(t *testing.T) {
		t.Parallel()

		a, b := fiber.New(), fiber.New()
		a.Get("/x", ok)
		b.Get("/y", ok)
		a.Use("/b", b)
		b.Use("/a", a)

		var o routeOwners
		if err := o.walk(reflect.ValueOf(a), "/root", "T", map[uintptr]bool{}); err != nil {
			t.Fatal(err)
		}

		got := map[string]bool{}
		for _, r := range o.routes {
			got[r.Method+" "+r.Pattern] = true
		}

		if len(o.routes) != 2 || !got["GET /root/x"] || !got["GET /root/b/y"] {
			t.Fatalf("routes %v", o.routes)
		}
	})

	t.Run("not an app", func(t *testing.T) {
		t.Parallel()

		var o routeOwners
		for _, v := range []reflect.Value{{}, reflect.ValueOf((*fiber.App)(nil))} {
			if err := o.walk(v, "/", "T", map[uintptr]bool{}); err != nil {
				t.Fatal(err)
			}
		}

		if len(o.routes) != 0 {
			t.Fatalf("routes %v", o.routes)
		}

		if app, err := subApp(reflect.ValueOf((*fiber.Group)(nil))); app.IsValid() || err != nil {
			t.Fatalf("subApp(nil group) = %v, %v; want the zero Value and nil", app, err)
		}
	})
}

// TestWalkUnreadable: a fiber whose App.stack is renamed or retyped, as a
// consumer's fiber upgrade could make it, fails the walk with an error
// wrapping srvr.ErrRouteConfig naming the field, instead of listing
// nothing or panicking (engine-support / Scenario: Fiber mount internals
// unreadable).
func TestWalkUnreadable(t *testing.T) {
	t.Parallel()

	type renamedApp struct{ trees [][]*fiber.Route }

	type retypedApp struct{ stack []*fiber.Route }

	type valueStackApp struct{ stack [][]fiber.Route }

	route := &fiber.Route{Method: fiber.MethodGet, Path: "/x"}

	for _, tc := range []struct {
		name string
		app  any
		want string
	}{
		{"stack renamed", &renamedApp{trees: [][]*fiber.Route{{route}}}, "fiber.App.stack is missing"},
		{"stack retyped to a flat slice", &retypedApp{stack: []*fiber.Route{route}}, "fiber.App.stack has type []*fiber.Route"},
		{"stack holding route values", &valueStackApp{stack: [][]fiber.Route{{*route}}}, "fiber.App.stack has type [][]fiber.Route"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var o routeOwners

			err := noPanic(t, func() error { return o.walk(reflect.ValueOf(tc.app), "/api", "T", map[uintptr]bool{}) })

			if !errors.Is(err, srvr.ErrRouteConfig) || !strings.Contains(fmt.Sprint(err), tc.want) || !strings.Contains(fmt.Sprint(err), "fiber ") {
				t.Fatalf("walk = %v, want an error wrapping srvr.ErrRouteConfig containing %q", err, tc.want)
			}

			if len(o.routes) != 0 {
				t.Fatalf("routes %v", o.routes)
			}
		})
	}
}

// TestReadInternals: reading fiber's internals returns the reader's
// error, and a panic while reading, a fiber change the type checks missed,
// becomes an error wrapping srvr.ErrRouteConfig instead of crashing Start.
func TestReadInternals(t *testing.T) {
	t.Parallel()

	failed := errors.New("failed")

	for _, tc := range []struct {
		name    string
		read    func() error
		wantErr error
		want    string
	}{
		{"no error", func() error { return nil }, nil, ""},
		{"error", func() error { return failed }, failed, "failed"},
		{"panic", func() error { panic("reflect: call of reflect.Value.Len on ptr Value") }, srvr.ErrRouteConfig, "fiber " + fiberVersion() + ": cannot list mounted sub-app routes: reading fiber's internals panicked: reflect: call of reflect.Value.Len on ptr Value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := noPanic(t, func() error { return readInternals(tc.read) })

			if !errors.Is(err, tc.wantErr) || (err != nil && !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("readInternals = %v, want %v containing %q", err, tc.wantErr, tc.want)
			}
		})
	}
}

// TestSubAppUnreadable: a mount route whose group field is missing or of
// another type fails with an error wrapping srvr.ErrRouteConfig naming
// fiber.Route.group.
func TestSubAppUnreadable(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		group reflect.Value
		want  string
	}{
		{"missing", reflect.Value{}, "field fiber.Route.group is missing"},
		{"retyped", reflect.ValueOf(&fiber.App{}), "field fiber.Route.group has type *fiber.App"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := subApp(tc.group); !errors.Is(err, srvr.ErrRouteConfig) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("subApp = %v, want an error wrapping srvr.ErrRouteConfig containing %q", err, tc.want)
			}
		})
	}
}

// TestMountFieldCheck: with a mounted sub-app, fiber fields that are
// renamed or retyped fail before the walk with an error wrapping
// srvr.ErrRouteConfig naming the field and the fiber version; with no
// mount nothing is checked (engine-support / Scenario: Fiber mount
// internals unreadable).
func TestMountFieldCheck(t *testing.T) {
	t.Parallel()

	// Only their types are inspected.
	type renamedRoute struct{ grp *fiber.Group }   //nolint:unused // stand-in type
	type retypedApp struct{ stack []*fiber.Route } //nolint:unused // stand-in type

	renamed := []mountField{{reflect.TypeFor[renamedRoute](), "group", reflect.TypeFor[*fiber.Group]()}, groupApp, appStack}
	retyped := []mountField{routeGroup, groupApp, {reflect.TypeFor[retypedApp](), "stack", reflect.TypeFor[[][]*fiber.Route]()}}

	for _, tc := range []struct {
		name   string
		mount  bool
		fields []mountField
		want   string // "" for no error
	}{
		{"renamed field", true, renamed, "field fibersrvr.renamedRoute.group is missing"},
		{"retyped field", true, retyped, "field fibersrvr.retypedApp.stack has type []*fiber.Route, want [][]*fiber.Route"},
		{"fields as pinned", true, mountFields, ""},
		{"renamed field, no mount", false, renamed, ""},
		{"retyped field, no mount", false, retyped, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			app := fiber.New()
			sub := fiber.New()
			sub.Get("/users", func(c fiber.Ctx) error { return c.SendStatus(200) })

			var o routeOwners
			o.current = "T"

			before := len(firstStack(app))
			if tc.mount {
				app.Use("/api", sub)
			}

			o.recordMounts(app, before)

			err := o.addMounted(tc.fields)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("addMounted = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, srvr.ErrRouteConfig) || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "fiber "+fiberVersion()+":") {
				t.Fatalf("addMounted = %v, want an error wrapping srvr.ErrRouteConfig containing %q and the fiber version", err, tc.want)
			}

			if len(o.routes) != 0 {
				t.Fatalf("routes %v listed before the check failed", o.routes)
			}
		})
	}
}

// noPanic returns fn's error and fails the test when fn panics.
func noPanic(t *testing.T, fn func() error) (err error) {
	t.Helper()

	defer func() {
		if v := recover(); v != nil {
			t.Fatalf("panicked: %v", v)
		}
	}()

	return fn()
}
