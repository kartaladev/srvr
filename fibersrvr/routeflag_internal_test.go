package fibersrvr

import (
	"reflect"
	"testing"

	"github.com/gofiber/fiber/v3"
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
// (Route.group, Group.app, App.stack), so a fiber upgrade that renames or
// retypes them fails here instead of silently dropping mounted routes.
func TestMountFields(t *testing.T) {
	t.Parallel()

	for _, f := range []struct {
		owner reflect.Type
		name  string
		want  reflect.Type
	}{
		{reflect.TypeFor[fiber.Route](), "group", reflect.TypeFor[*fiber.Group]()},
		{reflect.TypeFor[fiber.Group](), "app", reflect.TypeFor[*fiber.App]()},
		{reflect.TypeFor[fiber.App](), "stack", reflect.TypeFor[[][]*fiber.Route]()},
	} {
		if got, ok := f.owner.FieldByName(f.name); !ok || got.Type != f.want {
			t.Errorf("%s has no field %s of type %s", f.owner, f.name, f.want)
		}
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
		o.walk(reflect.ValueOf(a), "/root", "T", map[uintptr]bool{})

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
		o.walk(reflect.Value{}, "/", "T", map[uintptr]bool{})
		o.walk(reflect.ValueOf((*fiber.App)(nil)), "/", "T", map[uintptr]bool{})

		if subApp(reflect.Value{}).IsValid() || len(o.routes) != 0 {
			t.Fatalf("routes %v", o.routes)
		}
	})
}
