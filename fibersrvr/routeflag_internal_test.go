package fibersrvr

import (
	"reflect"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// TestRouteFlagFields pins the unexported fiber.Route fields routeFlag
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
