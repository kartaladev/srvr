package errchain

import (
	"testing"

	"go.uber.org/goleak"
)

// leakOptions are the goleak options of the errchain package's test
// binary. The package starts no goroutine, so it ignores nothing.
var leakOptions []goleak.Option

// TestMain fails the test binary when a goroutine is still running after
// its tests passed.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m, leakOptions...) }
