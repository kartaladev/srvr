package engine

import (
	"cmp"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
)

// ServiceConfig names the service in srvr's lifecycle and error records,
// so a failure maps to the deployed code. Consumers use it as
// srvr.ServiceConfig and pass it with srvr.WithServiceConfig. Defaults follow OpenTelemetry: service.name is
// "unknown_service:<executable>", and service.version and vcs.revision
// come from the binary's build information when present.
type ServiceConfig struct {
	// Name is service.name.
	Name string `json:"name" yaml:"name" env:"NAME"`
	// Version is service.version.
	Version string `json:"version" yaml:"version" env:"VERSION"`
	// Environment is deployment.environment.name, omitted when empty.
	Environment string `json:"environment" yaml:"environment" env:"ENVIRONMENT"`
}

// attrs returns the service and build attributes, reading build
// information with readBuild (debug.ReadBuildInfo outside tests).
func (c ServiceConfig) attrs(readBuild func() (*debug.BuildInfo, bool)) []slog.Attr {
	var version, revision string
	if info, ok := readBuild(); ok && info != nil {
		version = info.Main.Version
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" {
				revision = s.Value
			}
		}
	}

	attrs := []slog.Attr{slog.String(keyServiceName, cmp.Or(c.Name, "unknown_service:"+filepath.Base(os.Args[0])))}
	for _, kv := range [...][2]string{
		{keyServiceVersion, cmp.Or(c.Version, version)},
		{keyVCSRevision, revision},
		{keyEnvironment, c.Environment},
	} {
		if kv[1] != "" {
			attrs = append(attrs, slog.String(kv[0], kv[1]))
		}
	}

	return attrs
}
