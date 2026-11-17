package srvr

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"
)

// TestServiceAttributes covers structured-logging / Scenario: Service
// attributes, with an injected build-info reader.
func TestServiceAttributes(t *testing.T) {
	t.Parallel()

	exe := "unknown_service:" + filepath.Base(os.Args[0])
	tagged := func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v1.4.2"}, Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "9c1e2ab"}}}, true
	}
	devel := func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true }
	none := func() (*debug.BuildInfo, bool) { return nil, false }

	tests := []struct {
		name                  string
		opts                  []Option
		build                 func() (*debug.BuildInfo, bool)
		wantName, wantVersion string
		wantRevision, wantEnv string
	}{
		{"config", []Option{WithServiceConfig(ServiceConfig{Name: "registration-api", Version: "1.4.2"})}, tagged, "registration-api", "1.4.2", "9c1e2ab", ""},
		{"tagged build", nil, tagged, exe, "v1.4.2", "9c1e2ab", ""},
		{"go run", nil, devel, exe, "(devel)", "", ""},
		{"no build info", nil, none, exe, "", "", ""},
		{"config then WithServiceName", []Option{WithServiceConfig(ServiceConfig{Name: "registration-api"}), WithServiceName("orders")}, tagged, "orders", "v1.4.2", "9c1e2ab", ""},
		{"WithServiceName then empty config", []Option{WithServiceName("orders"), WithServiceConfig(ServiceConfig{})}, tagged, exe, "v1.4.2", "9c1e2ab", ""},
		{"version and environment options", []Option{WithServiceVersion("2.0.0"), WithServiceEnvironment("staging")}, tagged, exe, "2.0.0", "9c1e2ab", "staging"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s, err := newSettings(tt.opts)
			if err != nil {
				t.Fatal(err)
			}

			got := map[string]string{}
			for _, a := range s.service.attrs(tt.build) {
				got[a.Key] = a.Value.String()
			}

			want := map[string]string{"service.name": tt.wantName}
			for k, v := range map[string]string{"service.version": tt.wantVersion, "vcs.revision": tt.wantRevision, "deployment.environment.name": tt.wantEnv} {
				if v != "" {
					want[k] = v
				}
			}
			if len(got) != len(want) {
				t.Fatalf("attrs = %v, want %v", got, want)
			}
			for k, v := range want {
				if got[k] != v {
					t.Fatalf("attrs = %v, want %v", got, want)
				}
			}
		})
	}
}
