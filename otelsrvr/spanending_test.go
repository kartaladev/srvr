package otelsrvr_test

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
)

// TestSpanEnding covers otel-tracing "Span ending", all rows.
func TestSpanEnding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		handler  srvr.HandlerFunc
		cancel   bool // the client gives up after the handler started
		shutdown bool // Shutdown with a short deadline while it runs
	}{
		{"writes 200", func(w http.ResponseWriter, _ *http.Request) error { w.WriteHeader(http.StatusOK); return nil }, false, false},
		{"panics", func(http.ResponseWriter, *http.Request) error { panic("boom") }, false, false},
		{"panics with ErrAbortHandler", func(http.ResponseWriter, *http.Request) error { panic(http.ErrAbortHandler) }, false, false},
		{"blocks until the client disconnects", blockUntilDone, true, false},
		{"blocks until Shutdown forces the connection closed", blockUntilDone, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tp, rec := recorder()

			obs, err := otelsrvr.New(otelsrvr.WithTracing(), otelsrvr.WithTracerProvider(tp))
			if err != nil {
				t.Fatal(err)
			}

			srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard), srvr.WithObserver(obs), srvr.WithShutdownTimeout(50*time.Millisecond))
			if err != nil {
				t.Fatal(err)
			}

			started := make(chan struct{}, 1)
			if err := srv.Register(configFunc(func(g *srvr.Group) {
				g.GET("/x", func(w http.ResponseWriter, r *http.Request) error {
					started <- struct{}{}

					return tt.handler(w, r)
				})
			})); err != nil {
				t.Fatal(err)
			}

			done := make(chan error, 1)
			go func() { done <- srv.Start(context.Background()) }()
			<-srv.Ready()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv.Addr()+"/x", http.NoBody)
			client := &http.Client{Transport: &http.Transport{}}
			sent := make(chan struct{})

			go func() {
				defer close(sent)

				if res, err := client.Do(req); err == nil {
					_ = res.Body.Close()
				}
			}()

			<-started

			switch {
			case tt.cancel:
				cancel()
			case tt.shutdown:
				_ = srv.Shutdown(context.Background())
			}

			<-sent

			deadline := time.Now().Add(5 * time.Second)
			for len(rec.Ended()) == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}

			_ = srv.Shutdown(context.Background())

			if err := <-done; err != nil {
				t.Logf("Start = %v", err)
			}

			if got := len(rec.Ended()); got != 1 {
				t.Errorf("ended spans = %d, want 1", got)
			}
		})
	}
}

// blockUntilDone blocks until the request context ends.
func blockUntilDone(_ http.ResponseWriter, r *http.Request) error {
	<-r.Context().Done()

	return r.Context().Err()
}
