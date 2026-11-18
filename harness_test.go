package srvr_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptrace"
	"runtime"
	"sync/atomic"
	"testing"
)

// TestHarness_CleanupKeepsOtherClients pins the test harness against the
// one-off TestPanicRecovery failure ("http: CloseIdleConnections called"):
// a started server's cleanup must not close connections a parallel test
// uses for its own server.
func TestHarness_CleanupKeepsOtherClients(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, base, otherBase string)
	}{
		{"an idle connection survives another server's cleanup", idleSurvives},
		{"requests in flight survive another server's cleanups", inFlightSurvive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newServer(t)
			mustNil(t, srv.Register(okRoute("/ok")))
			base := startServer(t, srv)

			tt.run(t, base, startServer(t, newServer(t)))
		})
	}
}

// getOK sends GET base/ok with base's client and reports whether it
// reused an idle connection.
func getOK(base string) (reused bool, err error) {
	trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}

	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(context.Background(), trace), http.MethodGet, base+"/ok", http.NoBody)
	if err != nil {
		return false, err
	}

	res, err := clientFor(base).Do(req)
	if err != nil {
		return false, err
	}

	_, _ = io.Copy(io.Discard, res.Body)

	return reused, res.Body.Close()
}

// idleSurvives: the connection left idle by one request is reused by the
// next, although another server's cleanup ran in between.
func idleSurvives(t *testing.T, base, otherBase string) {
	t.Helper()

	if _, err := getOK(base); err != nil {
		t.Fatal(err)
	}

	clientFor(otherBase).CloseIdleConnections() // what its cleanup does

	reused, err := getOK(base)
	if err != nil {
		t.Fatal(err)
	}

	if !reused {
		t.Error("another server's cleanup closed this server's idle connection")
	}
}

// inFlightSurvive: requests sent while another server's cleanup runs in a
// loop never fail (the scratch reproduction; probabilistic before the fix,
// where about 1 run in 10 failed).
func inFlightSurvive(t *testing.T, base, otherBase string) {
	t.Helper()

	if testing.Short() {
		t.Skip("8000 requests; idleSurvives is the deterministic guard")
	}

	var stop atomic.Bool

	done := make(chan struct{})

	go func() {
		defer close(done)

		for !stop.Load() {
			clientFor(otherBase).CloseIdleConnections()
			runtime.Gosched() // leave the parallel tests their share
		}
	}()

	defer func() { stop.Store(true); <-done }()

	const workers, requests = 8, 1000

	errs := make(chan error, workers)

	for range workers {
		go func() {
			for range requests {
				if _, err := getOK(base); err != nil {
					errs <- err

					return
				}
			}
			errs <- nil
		}()
	}

	for range workers {
		if err := <-errs; err != nil {
			t.Fatalf("GET /ok: %v", err)
		}
	}
}
