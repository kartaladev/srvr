package gincycle

import (
	"testing"

	"go.uber.org/goleak"
)

// leakOptions are the goleak options of the gincycle test binary. They
// ignore only cyclicHandler: gin's c.Error never returns for a cyclic
// chain (engine-parity documented difference, observed with gin v1.12.0),
// so the handler goroutine TestParity_GinCyclicError leaves cannot be
// stopped and spins until the binary exits.
var leakOptions = []goleak.Option{goleak.IgnoreAnyFunction(cyclicHandlerName)}

// cyclicHandlerName is cyclicHandler's full function name.
const cyclicHandlerName = "github.com/kartaladev/srvr/internal/paritytest/gincycle.cyclicHandler"

// TestMain fails the test binary when a goroutine is still running after
// its tests passed.
func TestMain(m *testing.M) { goleak.VerifyTestMain(m, leakOptions...) }
