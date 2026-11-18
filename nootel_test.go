package srvr_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestCoreHasNoOTel guards the core module's dependency set: no package of
// the core, test-only or not, may import OpenTelemetry (design, Context).
func TestCoreHasNoOTel(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", "-test", "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}

	for pkg := range strings.Lines(string(out)) {
		if strings.HasPrefix(pkg, "go.opentelemetry.io/") {
			t.Errorf("the core module imports %s", strings.TrimSpace(pkg))
		}
	}
}
