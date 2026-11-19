package srvr_test

import (
	"reflect"
	"testing"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

// TestReexportedNames covers engine-support / Scenario: Re-exported names,
// all rows: each srvr name is the engine-support declaration itself.
func TestReexportedNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		got, declaration reflect.Type
	}{
		{"srvr.Config", reflect.TypeFor[srvr.Config](), reflect.TypeFor[engine.Config]()},
		{"srvr.LogConfig", reflect.TypeFor[srvr.LogConfig](), reflect.TypeFor[engine.LogConfig]()},
		{"srvr.AccessLogConfig", reflect.TypeFor[srvr.AccessLogConfig](), reflect.TypeFor[engine.AccessLogConfig]()},
		{"srvr.TraceConfig", reflect.TypeFor[srvr.TraceConfig](), reflect.TypeFor[engine.TraceConfig]()},
		{"srvr.ServiceConfig", reflect.TypeFor[srvr.ServiceConfig](), reflect.TypeFor[engine.ServiceConfig]()},
		{"srvr.Trace", reflect.TypeFor[srvr.Trace](), reflect.TypeFor[engine.Trace]()},
		{"srvr.ShutdownPhase", reflect.TypeFor[srvr.ShutdownPhase](), reflect.TypeFor[engine.ShutdownPhase]()},
		{"srvr.ErrorHook", reflect.TypeFor[srvr.ErrorHook](), reflect.TypeFor[engine.ErrorHook]()},
		{"srvr.Option", reflect.TypeFor[srvr.Option](), reflect.TypeFor[engine.Option]()},
		{"srvr.ConfigError", reflect.TypeFor[srvr.ConfigError](), reflect.TypeFor[engine.ConfigError]()},
		{"srvr.RouteCheck", reflect.TypeFor[srvr.RouteCheck](), reflect.TypeFor[engine.RouteCheck]()},
		{"srvr.Routes", reflect.TypeFor[srvr.Routes](), reflect.TypeFor[engine.Routes]()},
		{"srvr.Route", reflect.TypeFor[srvr.Route](), reflect.TypeFor[engine.Route]()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.got != tt.declaration {
				t.Errorf("%s is %v, want the engine-support declaration %v", tt.name, tt.got, tt.declaration)
			}
		})
	}
}
