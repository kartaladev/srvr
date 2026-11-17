package srvr_test

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
)

// Hello is a route configurer for the net/http engine.
type Hello struct{}

// Configure adds the routes of Hello.
func (Hello) Configure(g *srvr.Group) {
	g.GET("/hello", func(w http.ResponseWriter, _ *http.Request) error {
		_, err := io.WriteString(w, "hello")

		return err
	})
	g.GET("/missing", func(http.ResponseWriter, *http.Request) error {
		return prblm.ErrNotFound // rendered as an RFC 9457 problem
	})
}

func Example() {
	srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"))
	if err != nil {
		panic(err)
	}

	if err := srv.Register(Hello{}); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()
	<-srv.Ready()

	for _, path := range []string{"/hello", "/missing"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv.Addr()+path, nil)

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}

		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		fmt.Println(path, res.StatusCode, res.Header.Get("Content-Type"))
	}

	cancel()
	fmt.Println("Start returned", <-done)

	// Output:
	// /hello 200 text/plain; charset=utf-8
	// /missing 404 application/problem+json
	// Start returned <nil>
}
