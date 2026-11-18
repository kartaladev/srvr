package otelsrvr_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestNoSDKDependency guards the module's dependency set: the non-test
// packages import the OpenTelemetry API only, never the SDK or an
// exporter. go.opentelemetry.io/auto/sdk is imported by the API's own
// global package and is not the SDK.
func TestNoSDKDependency(t *testing.T) {
	out, err := exec.CommandContext(t.Context(), "go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}

	for pkg := range strings.Lines(string(out)) {
		pkg = strings.TrimSpace(pkg)
		if strings.HasPrefix(pkg, "go.opentelemetry.io/otel/sdk") || strings.HasPrefix(pkg, "go.opentelemetry.io/otel/exporters") {
			t.Errorf("otelsrvr imports %s", pkg)
		}
	}
}
