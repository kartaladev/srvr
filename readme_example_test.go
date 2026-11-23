package srvr_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kartaladev/srvr"
)

// Example_quickStart mirrors the quick start in README.md: main there is
// this function, run is runQuickStart. It has no Output comment, so go
// test compiles it without running it.
func Example_quickStart() {
	if err := runQuickStart(); err != nil {
		log.Fatal(err)
	}
}

// runQuickStart is run in README.md.
func runQuickStart() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := srvr.New() // listens on :8080; logs JSON to stdout
	if err != nil {
		return err
	}

	if err := srv.Register(Hello{}); err != nil {
		return err
	}

	// Run serves until SIGINT or SIGTERM, then drains in-flight requests.
	return srv.Run(ctx)
}

// errQuotaExceeded is the application error of the README's renderers.
var errQuotaExceeded = errors.New("quota exceeded")

// quotaRenderer mirrors "Custom error rendering" in README.md: it answers
// errQuotaExceeded itself and hands every other error to prblm, so srvr's
// own answers and the server's problems stay as they are.
var quotaRenderer = srvr.ErrorRendererFunc(func(ctx context.Context, req srvr.ErrorRequest, err error) srvr.ErrorResponse {
	if !errors.Is(err, errQuotaExceeded) {
		return srvr.DefaultErrorRenderer().Render(ctx, req, err)
	}

	return srvr.ErrorResponse{
		Status: http.StatusTooManyRequests,
		Header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {"60"}},
		Body:   []byte(`{"code":"quota_exceeded"}`),
		Title:  "quota exceeded", // for the access record and error hooks only
	}
})

// houseRenderer mirrors README.md's house-format renderer: a fixed code
// per kind of error, the trace id, and never the error's text.
var houseRenderer = srvr.ErrorRendererFunc(func(_ context.Context, req srvr.ErrorRequest, err error) srvr.ErrorResponse {
	status, code := http.StatusInternalServerError, "internal"

	var mna *srvr.MethodNotAllowedError

	switch {
	case errors.Is(err, srvr.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.As(err, &mna):
		status, code = http.StatusMethodNotAllowed, "method_not_allowed"
	case errors.Is(err, srvr.ErrRequestTooLarge):
		status, code = http.StatusRequestEntityTooLarge, "too_large"
	case errors.Is(err, srvr.ErrClientClosed):
		status, code = 499, "client_closed"
	}

	h := http.Header{"Content-Type": {"application/json"}}
	if mna != nil {
		h.Set("Allow", strings.Join(mna.Allow, ", "))
	}

	return srvr.ErrorResponse{
		Status: status,
		Header: h,
		Body:   []byte(`{"code":"` + code + `","trace":"` + req.TraceID + `"}`),
	}
})

// quotaRoute serves GET /quota, which fails with errQuotaExceeded.
type quotaRoute struct{}

func (quotaRoute) Configure(g *srvr.Group) {
	g.GET("/quota", func(http.ResponseWriter, *http.Request) error { return errQuotaExceeded })
}

// Example_customErrorRendering serves README.md's two renderers and
// prints their answers.
func Example_customErrorRendering() {
	for _, r := range []srvr.ErrorRenderer{quotaRenderer, houseRenderer} {
		srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard), srvr.WithErrorRenderer(r))
		if err != nil {
			log.Fatal(err)
		}

		if err := srv.Register(quotaRoute{}); err != nil {
			log.Fatal(err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- srv.Run(ctx) }()
		<-srv.Ready()

		for _, path := range []string{"/quota", "/missing"} {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv.Addr()+path, nil)
			req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

			res, err := http.DefaultClient.Do(req)
			if err != nil {
				log.Fatal(err)
			}
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			fmt.Println(res.StatusCode, res.Header.Get("Content-Type"), strings.TrimSpace(string(body)))
		}

		cancel()

		if err := <-done; err != nil {
			log.Fatal(err)
		}
	}

	// Output:
	// 429 application/json {"code":"quota_exceeded"}
	// 404 application/problem+json {"title":"Not Found","status":404,"detail":"The requested resource was not found.","instance":"/missing","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}
	// 500 application/json {"code":"internal","trace":"4bf92f3577b34da6a3ce929d0e0e4736"}
	// 404 application/json {"code":"not_found","trace":"4bf92f3577b34da6a3ce929d0e0e4736"}
}
