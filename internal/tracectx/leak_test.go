package tracectx_test

import (
	"testing"

	"go.uber.org/goleak"
)

// leakOptions are the goleak options of the tracectx package's test
// binary. The package has no deliberate long-lived goroutine, so it
// ignores nothing.
var leakOptions []goleak.Option

// TestMain fails the test binary when a goroutine is still running after
// its tests passed.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m, leakOptions...) }
