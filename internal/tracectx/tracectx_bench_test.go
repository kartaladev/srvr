package tracectx_test

import (
	"testing"

	"github.com/kartaladev/srvr/internal/tracectx"
)

// BenchmarkNewTrace measures generating the ids of a new trace, as every
// request without a valid traceparent does: from one goroutine, and from
// GOMAXPROCS goroutines at once ("parallel"), where a shared random
// source would contend.
func BenchmarkNewTrace(b *testing.B) {
	b.Run("serial", func(b *testing.B) {
		b.ReportAllocs()

		for b.Loop() {
			_ = tracectx.New()
		}
	})

	b.Run("parallel", func(b *testing.B) {
		b.ReportAllocs()

		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				_ = tracectx.New()
			}
		})
	})
}
