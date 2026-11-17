package srvr

import "github.com/kartaladev/srvr/engine"

// WithServiceConfig sets every field of [ServiceConfig].
func WithServiceConfig(c ServiceConfig) Option {
	return func(s *engine.Settings) { s.Service = c }
}

// WithServiceName sets service.name.
func WithServiceName(name string) Option {
	return func(s *engine.Settings) { s.Service.Name = name }
}

// WithServiceVersion sets service.version.
func WithServiceVersion(version string) Option {
	return func(s *engine.Settings) { s.Service.Version = version }
}

// WithServiceEnvironment sets deployment.environment.name.
func WithServiceEnvironment(env string) Option {
	return func(s *engine.Settings) { s.Service.Environment = env }
}
