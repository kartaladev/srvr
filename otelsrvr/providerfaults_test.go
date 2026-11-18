package otelsrvr_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// faultyMeter fails creating the instrument named fail, panics creating
// any instrument when panicCreate is set, or returns a duration histogram
// whose Record panics.
type faultyMeter struct {
	noop.Meter

	fail                     string
	panicCreate, panicRecord bool
}

// create returns an error when name is the instrument to fail.
func (m faultyMeter) create(name string) error {
	if m.panicCreate {
		panic("meter panicked")
	}

	if name == m.fail {
		return errors.New("meter refused")
	}

	return nil
}

func (m faultyMeter) Float64Histogram(name string, _ ...metric.Float64HistogramOption) (metric.Float64Histogram, error) {
	if err := m.create(name); err != nil {
		return nil, err
	}

	if m.panicRecord {
		return panickingHistogram{}, nil
	}

	return noop.Float64Histogram{}, nil
}

func (m faultyMeter) Int64UpDownCounter(name string, _ ...metric.Int64UpDownCounterOption) (metric.Int64UpDownCounter, error) {
	return noop.Int64UpDownCounter{}, m.create(name)
}

func (m faultyMeter) Int64Histogram(name string, _ ...metric.Int64HistogramOption) (metric.Int64Histogram, error) {
	return noop.Int64Histogram{}, m.create(name)
}

type panickingHistogram struct{ noop.Float64Histogram }

func (panickingHistogram) Record(context.Context, float64, ...metric.RecordOption) {
	panic("Record panicked")
}

// meterProvider returns m from Meter, or panics when panics is set.
type meterProvider struct {
	noop.MeterProvider

	meter  metric.Meter
	panics bool
}

func (p meterProvider) Meter(string, ...metric.MeterOption) metric.Meter {
	if p.panics {
		panic("Meter panicked")
	}

	return p.meter
}

// TestProviderFaults covers otel-metrics "Provider faults", all rows, plus
// panicking providers and instruments (design D11).
func TestProviderFaults(t *testing.T) {
	t.Parallel()

	construction := map[string]metric.MeterProvider{
		"meter fails to create the histogram":      meterProvider{meter: faultyMeter{fail: "http.server.request.duration"}},
		"meter panics creating the histogram":      meterProvider{meter: faultyMeter{panicCreate: true}},
		"Meter panics":                             meterProvider{panics: true},
		"meter fails to create active_requests":    meterProvider{meter: faultyMeter{fail: "http.server.active_requests"}},
		"meter fails to create request.body.size":  meterProvider{meter: faultyMeter{fail: "http.server.request.body.size"}},
		"meter fails to create response.body.size": meterProvider{meter: faultyMeter{fail: "http.server.response.body.size"}},
	}

	for name, mp := range construction {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			opts := []otelsrvr.Option{otelsrvr.WithMeterProvider(mp), otelsrvr.WithActiveRequests(), otelsrvr.WithBodySizes()}
			if _, err := otelsrvr.New(opts...); !errors.Is(err, otelsrvr.ErrConfig) {
				t.Fatalf("New = %v, want an error wrapping ErrConfig", err)
			}
		})
	}

	shutDown := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()))
	if err := shutDown.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	serving := map[string]metric.MeterProvider{
		"SDK provider already shut down": shutDown,
		"histogram Record panics":        meterProvider{meter: faultyMeter{panicRecord: true}},
	}

	for name, mp := range serving {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			obs, err := otelsrvr.New(otelsrvr.WithMeterProvider(mp))
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			if got := send(t, http.MethodGet, serve(t, usersRoute, srvr.WithObserver(obs))+"/users/1", ""); got != http.StatusOK {
				t.Fatalf("status = %d, want 200", got)
			}
		})
	}
}
