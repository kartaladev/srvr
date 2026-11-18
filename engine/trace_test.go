package engine_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kartaladev/srvr/engine"
)

// TestStateFrom covers engine-support / Scenario: No request state outside
// a request.
func TestStateFrom(t *testing.T) {
	t.Parallel()

	core, err := engine.ResolveConfig([]engine.Option{})
	if err != nil {
		t.Fatal(err)
	}

	var inside *engine.RequestState
	var insideOK bool

	core.StartTrace(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		inside, insideOK = engine.StateFrom(r.Context())
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	tests := []struct {
		name   string
		state  *engine.RequestState
		ok     bool
		wantOK bool
	}{
		{"context.Background()", nil, false, false},
		{"nil", nil, false, false},
		{"a request context inside srvr's pipeline", inside, insideOK, true},
	}

	// Before an access stage records the response, the status is net/http's
	// default.
	if insideOK && (inside.Status() != http.StatusOK || inside.Matched() || inside.Failed()) {
		t.Fatalf("fresh state status %d matched %v failed %v", inside.Status(), inside.Matched(), inside.Failed())
	}

	bg, bgOK := engine.StateFrom(context.Background()) //nolint:usetesting // context.Background() is the row under test
	nilState, nilOK := engine.StateFrom(nil)           //nolint:staticcheck // a nil context is the row under test.
	tests[0].state, tests[0].ok = bg, bgOK
	tests[1].state, tests[1].ok = nilState, nilOK

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.ok != tt.wantOK || (tt.state != nil) != tt.wantOK {
				t.Fatalf("StateFrom = %v, %v; want state %v", tt.state, tt.ok, tt.wantOK)
			}
		})
	}
}
