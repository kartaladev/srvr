package srvr

import (
	"runtime/debug"

	"github.com/kartaladev/srvr/internal/engine"
)

// WithBuildInfo makes the server read build information with read instead
// of debug.ReadBuildInfo, for tests of the build attributes.
func WithBuildInfo(read func() (*debug.BuildInfo, bool)) Option {
	return func(s *engine.Settings) { s.ReadBuild = read }
}
