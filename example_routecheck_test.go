package srvr_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/engine"
)

// healthRoutes declares the routes the check below expects.
type healthRoutes struct{}

func (healthRoutes) Configure(g *srvr.Group) {
	g.GET("/healthz", func(w http.ResponseWriter, _ *http.Request) error {
		w.WriteHeader(http.StatusOK)

		return nil
	})
}

// A route check asserts the route table at startup: Start fails before
// binding when a required route is missing.
func ExampleWithRouteCheck() {
	requireHealth := func(_ context.Context, routes engine.Routes) error {
		for _, r := range routes {
			fmt.Printf("%s %s (%s)\n", r.Method, r.Pattern, r.Configurer)
			if r.Method == http.MethodGet && r.Pattern == "/healthz" {
				return nil
			}
		}

		return errors.New("GET /healthz is not registered")
	}

	srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard), srvr.WithRouteCheck(requireHealth))
	if err != nil {
		panic(err)
	}

	if err := srv.Register(healthRoutes{}); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()
	<-srv.Ready()
	cancel()
	fmt.Println(<-done)

	// Output:
	// GET /healthz (srvr_test.healthRoutes)
	// <nil>
}
