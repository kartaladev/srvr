package fibersrvr

import (
	"testing"

	"github.com/gofiber/fiber/v3"
)

// TestMatchKey pins matchKey to the cases of fiber v3.7.0's
// normalizeRoutePattern (path.go), plus parameter names dropped.
func TestMatchKey(t *testing.T) {
	t.Parallel()

	var (
		lax    = fiber.Config{}
		cased  = fiber.Config{CaseSensitive: true}
		strict = fiber.Config{StrictRouting: true}
	)

	tests := []struct {
		path string
		cfg  fiber.Config
		want string
	}{
		{"", lax, "/"},
		{"users", lax, "/users"},
		{"/", lax, "/"},
		{"//", lax, "/"},
		{"/Users/", lax, "/users"},
		{"/Users/", cased, "/Users"},
		{"/Users/", strict, "/users/"},
		{"/users/:id", lax, "/users/:"},
		{"/users/:Name/x", lax, "/users/:/x"},
		{"/users/:id<int>", lax, "/users/:<int>"},
		{`/a\:b`, lax, "/a:b"},
		{"/ÄB", lax, "/Äb"}, // ASCII letters only, as fiber
	}

	for _, tt := range tests {
		if got := matchKey(tt.path, &tt.cfg); got != tt.want {
			t.Errorf("matchKey(%q, CaseSensitive %v, StrictRouting %v) = %q, want %q", tt.path, tt.cfg.CaseSensitive, tt.cfg.StrictRouting, got, tt.want)
		}
	}
}

// TestGroupPath pins groupPath to fiber's getGroupPath (helpers.go).
func TestGroupPath(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ prefix, path, want string }{
		{"/api", "", "/api"},
		{"/api", "users", "/api/users"},
		{"/api/", "/users", "/api/users"},
		{"/", "/users", "/users"},
		{"/api", "/", "/api/"},
	} {
		if got := groupPath(tt.prefix, tt.path); got != tt.want {
			t.Errorf("groupPath(%q, %q) = %q, want %q", tt.prefix, tt.path, got, tt.want)
		}
	}
}
