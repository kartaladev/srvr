package otelsrvr_test

import (
	"log"

	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/otelsrvr"
)

// Example_quickStart mirrors README.md; it has no Output comment, so go
// test compiles it without running it.
func Example_quickStart() {
	obs, err := otelsrvr.New(otelsrvr.WithTracing()) // global providers by default
	if err != nil {
		log.Fatal(err)
	}

	srv, err := srvr.New(srvr.WithObserver(obs))
	if err != nil {
		log.Fatal(err)
	}

	_ = srv // register configurers and Start as usual
}
