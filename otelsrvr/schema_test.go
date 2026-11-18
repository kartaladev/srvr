package otelsrvr_test

import (
	"errors"
	"net/http"
	"slices"
	"testing"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	"go.opentelemetry.io/otel/semconv/v1.43.0/httpconv"
)

// TestMetricSchemaGolden pins the names, units and attribute keys of every
// instrument against semantic conventions v1.43 (httpconv), so a semconv
// upgrade that renames one fails here.
func TestMetricSchemaGolden(t *testing.T) {
	t.Parallel()

	failed := []string{"error.type", "http.request.method", "http.response.status_code", "http.route", "url.scheme"}
	want := map[string]struct {
		unit string
		keys []string
	}{
		httpconv.ServerRequestDuration{}.Name():  {httpconv.ServerRequestDuration{}.Unit(), failed},
		httpconv.ServerActiveRequests{}.Name():   {httpconv.ServerActiveRequests{}.Unit(), []string{"http.request.method", "url.scheme"}},
		httpconv.ServerRequestBodySize{}.Name():  {httpconv.ServerRequestBodySize{}.Unit(), failed},
		httpconv.ServerResponseBodySize{}.Name(): {httpconv.ServerResponseBodySize{}.Unit(), failed},
	}

	base, reader := metered(t, configFunc(func(g *srvr.Group) {
		g.POST("/users", func(http.ResponseWriter, *http.Request) error { return errors.New("boom") })
	}), otelsrvr.WithActiveRequests(), otelsrvr.WithBodySizes())
	sendRaw(t, http.MethodPost, base+"/users")

	rm := collect(t, reader)
	if got := names(rm); len(got) != len(want) {
		t.Fatalf("instruments = %v", got)
	}

	for name, w := range want {
		m, scope, ok := find(rm, name)
		if !ok {
			t.Errorf("no %s", name)

			continue
		}

		if m.Unit != w.unit || scope.Name != otelsrvr.ScopeName {
			t.Errorf("%s unit %q scope %q, want %q, %q", name, m.Unit, scope.Name, w.unit, otelsrvr.ScopeName)
		}

		if got := attributeKeys(t, m); !slices.Equal(got, w.keys) {
			t.Errorf("%s attribute keys = %v, want %v", name, got, w.keys)
		}
	}
}
