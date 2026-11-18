package srvr_test

import (
	"context"
	"log"
	"os"
	"os/signal"
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

	// Start serves until SIGINT or SIGTERM, then drains in-flight requests.
	return srv.Start(ctx)
}
