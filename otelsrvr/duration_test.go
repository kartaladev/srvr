package otelsrvr_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const durationName = "http.server.request.duration"

// TestDuration covers otel-metrics "Duration per engine", net/http row.
func TestDuration(t *testing.T) {
	t.Parallel()

	reader := sdkmetric.NewManualReader()
	obs, err := otelsrvr.New(otelsrvr.WithMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	base := serve(t, usersRoute, srvr.WithObserver(obs))
	if got := send(t, http.MethodGet, base+"/users/42", ""); got != http.StatusOK {
		t.Fatalf("status = %d", got)
	}

	rm := collect(t, reader)

	m, scope, _ := find(rm, durationName)
	if m.Unit != "s" {
		t.Errorf("unit = %q, want s", m.Unit)
	}

	if scope.Name != "github.com/kartaladev/srvr/otelsrvr" {
		t.Errorf("scope = %q", scope.Name)
	}

	points := histogram(t, rm, durationName)
	if len(points) != 1 || points[0].Count != 1 {
		t.Fatalf("data points = %+v, want one with count 1", points)
	}

	if route, _ := attr(points[0].Attributes, "http.route"); route != "/users/{id}" {
		t.Errorf("http.route = %q", route)
	}

	if status, _ := attr(points[0].Attributes, "http.response.status_code"); status != "200" {
		t.Errorf("http.response.status_code = %q", status)
	}
}

// TestDurationProvider covers otel-metrics "Provider selection", all rows.
// The global-provider rows replace the global MeterProvider, so the test
// is not parallel and restores it.
func TestDurationProvider(t *testing.T) {
	checkGoroutines(t)

	saved := otel.GetMeterProvider()
	t.Cleanup(func() { otel.SetMeterProvider(saved) })

	t.Run("global provider", func(t *testing.T) {
		reader := sdkmetric.NewManualReader()
		otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))

		obs, err := otelsrvr.New()
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		send(t, http.MethodGet, serve(t, usersRoute, srvr.WithObserver(obs))+"/users/1", "")

		if points := histogram(t, collect(t, reader), durationName); len(points) != 1 {
			t.Fatalf("global provider data points = %d, want 1", len(points))
		}
	})

	t.Run("no-op global provider", func(t *testing.T) {
		otel.SetMeterProvider(noop.NewMeterProvider())

		obs, err := otelsrvr.New()
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		if got := send(t, http.MethodGet, serve(t, usersRoute, srvr.WithObserver(obs))+"/users/1", ""); got != http.StatusOK {
			t.Fatalf("status = %d, want 200", got)
		}
	})

	t.Run("injected provider", func(t *testing.T) {
		global := sdkmetric.NewManualReader()
		otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(global)))

		reader := sdkmetric.NewManualReader()

		obs, err := otelsrvr.New(otelsrvr.WithMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))))
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		send(t, http.MethodGet, serve(t, usersRoute, srvr.WithObserver(obs))+"/users/1", "")

		if points := histogram(t, collect(t, reader), durationName); len(points) != 1 {
			t.Fatalf("injected provider data points = %d, want 1", len(points))
		}

		if _, _, ok := find(collect(t, global), durationName); ok {
			t.Error("the global provider recorded too")
		}
	})

	for _, tt := range []struct {
		name string
		opt  otelsrvr.Option
	}{
		{"nil provider", otelsrvr.WithMeterProvider(nil)},
		{"typed-nil provider", otelsrvr.WithMeterProvider((*sdkmetric.MeterProvider)(nil))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := otelsrvr.New(tt.opt); !errors.Is(err, otelsrvr.ErrConfig) {
				t.Fatalf("New = %v, want an error wrapping ErrConfig", err)
			}
		})
	}
}
