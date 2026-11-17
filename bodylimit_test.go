package srvr_test

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/kartaladev/srvr"
)

// TestBodyLimit covers error-responses / Scenario: Body limit.
func TestBodyLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		limit  []srvr.Option
		size   int
		status int
	}{
		{nil, 4194304, http.StatusOK},
		{nil, 4194305, http.StatusRequestEntityTooLarge},
		{[]srvr.Option{srvr.WithMaxRequestBodyBytes(10)}, 10, http.StatusOK},
		{[]srvr.Option{srvr.WithMaxRequestBodyBytes(10)}, 11, http.StatusRequestEntityTooLarge},
		{[]srvr.Option{srvr.WithConfig(srvr.Config{MaxRequestBodyBytes: -1})}, 8388608, http.StatusOK},
		{nil, 0, http.StatusOK},
	}

	for i, tt := range tests {
		t.Run(strconv.Itoa(i)+"/"+strconv.Itoa(tt.size), func(t *testing.T) {
			t.Parallel()

			srv := newServer(t, tt.limit...)
			err := srv.Register(configFunc(func(g *srvr.Group) {
				g.POST("/upload", func(_ http.ResponseWriter, r *http.Request) error {
					_, err := io.Copy(io.Discard, r.Body)

					return err
				})
			}))
			if err != nil {
				t.Fatal(err)
			}
			base := startServer(t, srv)

			res := do(t, http.MethodPost, base+"/upload", bytes.NewReader(make([]byte, tt.size)), nil)
			if res.status != tt.status {
				t.Fatalf("status = %d, want %d", res.status, tt.status)
			}
		})
	}
}
