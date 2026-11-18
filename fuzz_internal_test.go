package srvr

import (
	"strings"
	"testing"
)

// FuzzJoinPattern covers test-quality / Scenario: Fuzzed properties,
// row FuzzJoinPattern: joining never panics, a valid path joins to a
// pattern starting with "/" with no "//", and an invalid one is rejected.
// The prefix is one a Group accepts: empty or starting with "/", with its
// slashes collapsed as Group stores it.
func FuzzJoinPattern(f *testing.F) {
	for _, seed := range [][2]string{
		{"", ""},
		{"", "/users"},
		{"/api", "/users"},
		{"/api/", "/users"},
		{"/api", ""},
		{"/api", "/"},
		{"//a//b", "//c//"},
		{"/api", "users"},
		{"/files", "/{path...}"},
		{"/a", "/{$}"},
		{"/", "//"},
		{"/x", "/\x00"},
	} {
		f.Add(seed[0], seed[1])
	}

	f.Fuzz(func(t *testing.T, prefix, path string) {
		if !isPath(prefix) {
			t.Skip("a Group rejects this prefix")
		}

		joined, err := joinPattern(collapseSlashes(prefix), path)
		if !isPath(path) {
			if err == nil {
				t.Fatalf("joinPattern(%q, %q) = %q, want an error for a path without a leading slash", prefix, path, joined)
			}

			return
		}

		if err != nil {
			t.Fatalf("joinPattern(%q, %q) = %v, want a pattern", prefix, path, err)
		}
		if !strings.HasPrefix(joined, "/") || strings.Contains(joined, "//") {
			t.Fatalf("joinPattern(%q, %q) = %q, want a leading slash and no //", prefix, path, joined)
		}
	})
}
