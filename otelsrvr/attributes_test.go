package otelsrvr_test

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// dbError has an ErrorType method, as semconv.ErrorType prefers.
type dbError struct{}

func (dbError) Error() string     { return "db timeout" }
func (dbError) ErrorType() string { return "db_timeout" }

// metered starts a net/http server with otelsrvr on a manual reader and
// conf, and returns its base URL and the reader.
func metered(t *testing.T, conf configFunc, opts ...otelsrvr.Option) (string, *sdkmetric.ManualReader) {
	t.Helper()

	reader := sdkmetric.NewManualReader()

	obs, err := otelsrvr.New(append([]otelsrvr.Option{otelsrvr.WithMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))}, opts...)...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return serve(t, conf, srvr.WithObserver(obs)), reader
}

// TestAttributes covers otel-metrics "Attribute values", normal and
// hostile rows.
func TestAttributes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       string
		outcome    func(w http.ResponseWriter) error // for POST /users
		wantRoute  string                            // empty: no http.route
		wantMethod string                            // expected http.request.method
		errorType  string                            // empty: no error.type
		noStatus   bool
	}{
		{"200", "GET", "/users/42", nil, "/users/{id}", "GET", "", false},
		{"200 other id", "GET", "/users/43", nil, "/users/{id}", "GET", "", false},
		{"ErrConflict", "POST", "/users", func(http.ResponseWriter) error { return prblm.ErrConflict }, "/users", "POST", "*errors.errorString", false},
		{"errors.New", "POST", "/users", func(http.ResponseWriter) error { return errors.New("x") }, "/users", "POST", "*errors.errorString", false},
		{"wrapped OpError", "POST", "/users", func(http.ResponseWriter) error {
			return fmt.Errorf("insert: %w", &net.OpError{Op: "dial", Err: errors.New("refused")})
		}, "/users", "POST", "*net.OpError", false},
		{"ErrorType method", "POST", "/users", func(http.ResponseWriter) error { return fmt.Errorf("insert: %w", dbError{}) }, "/users", "POST", "db_timeout", false},
		{"joined", "POST", "/users", func(http.ResponseWriter) error { return errors.Join(errors.New("x"), errors.New("y")) }, "/users", "POST", "*errors.joinError", false},
		{"joined with ErrorType", "POST", "/users", func(http.ResponseWriter) error { return errors.Join(errors.New("x"), dbError{}) }, "/users", "POST", "db_timeout", false},
		{"panic", "POST", "/users", func(http.ResponseWriter) error { panic("boom") }, "/users", "POST", "panic", false},
		{"ErrAbortHandler", "POST", "/users", func(http.ResponseWriter) error { panic(http.ErrAbortHandler) }, "/users", "POST", "panic", true},
		{"503 written directly", "GET", "/users/42?503", nil, "/users/{id}", "GET", "503", false},
		{"random path", "GET", "/random/a1b2c3", nil, "", "GET", "404", false},
		{"PROPFIND", "PROPFIND", "/users", nil, "", "_OTHER", "405", false},
		{"XYZZY", "XYZZY", "/users/42", nil, "", "_OTHER", "405", false},
		{"lowercase get", "get", "/users/42", nil, "", "_OTHER", "405", false},
		{"QUERY", "QUERY", "/users", nil, "", "QUERY", "405", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, reader := metered(t, configFunc(func(g *srvr.Group) {
				g.GET("/users/{id}", func(w http.ResponseWriter, r *http.Request) error {
					status := http.StatusOK
					if r.URL.RawQuery == "503" {
						status = http.StatusServiceUnavailable
					}

					w.WriteHeader(status)

					return nil
				})
				g.POST("/users", func(w http.ResponseWriter, _ *http.Request) error { return tt.outcome(w) })
			}))

			sendRaw(t, tt.method, base+tt.path)

			points := histogram(t, collect(t, reader), durationName)
			if len(points) != 1 {
				t.Fatalf("data points = %d, want 1", len(points))
			}

			set := points[0].Attributes

			if got, _ := attr(set, "http.request.method"); got != tt.wantMethod {
				t.Errorf("http.request.method = %q, want %q", got, tt.wantMethod)
			}

			if got, ok := attr(set, "http.route"); got != tt.wantRoute || ok != (tt.wantRoute != "") {
				t.Errorf("http.route = %q (present %v), want %q", got, ok, tt.wantRoute)
			}

			if got, ok := attr(set, "error.type"); got != tt.errorType || ok != (tt.errorType != "") {
				t.Errorf("error.type = %q (present %v), want %q", got, ok, tt.errorType)
			}

			if _, ok := attr(set, "http.response.status_code"); ok == tt.noStatus {
				t.Errorf("http.response.status_code present = %v, want %v", ok, !tt.noStatus)
			}

			for _, kv := range set.ToSlice() {
				switch kv.Key {
				case "http.request.method", "http.route", "http.response.status_code", "url.scheme", "error.type":
				default:
					t.Errorf("unexpected attribute %s", kv.Key)
				}
			}
		})
	}
}

// sendRaw sends a request with any method, tolerating an aborted
// connection.
func sendRaw(t *testing.T, method, url string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, http.NoBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	if res, err := clientFor(req.URL.String()).Do(req); err == nil {
		_ = res.Body.Close()
	}
}

// typePanicsError is an error whose ErrorType method panics.
type typePanicsError struct{}

func (typePanicsError) Error() string     { return "x" }
func (typePanicsError) ErrorType() string { panic("ErrorType panicked") }

// unwrapPanicsError is an error whose Unwrap panics.
type unwrapPanicsError struct{}

func (unwrapPanicsError) Error() string { return "x" }
func (unwrapPanicsError) Unwrap() error { panic("Unwrap panicked") }

// TestAttributes_FaultyErrors checks that an error whose methods panic
// still records a data point, with its Go type as error.type (design D11).
func TestAttributes_FaultyErrors(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		err  error
		want string
	}{
		{"ErrorType panics", typePanicsError{}, "otelsrvr_test.typePanicsError"},
		// srvr's chain guard already replaced a chain it cannot walk.
		{"Unwrap panics", fmt.Errorf("wrap: %w", unwrapPanicsError{}), "*engine.unresolvable"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			base, reader := metered(t, configFunc(func(g *srvr.Group) {
				g.POST("/users", func(http.ResponseWriter, *http.Request) error { return tt.err })
			}))
			sendRaw(t, http.MethodPost, base+"/users")

			points := histogram(t, collect(t, reader), durationName)
			if got, _ := attr(points[0].Attributes, "error.type"); got != tt.want {
				t.Errorf("error.type = %q, want %q", got, tt.want)
			}
		})
	}
}
