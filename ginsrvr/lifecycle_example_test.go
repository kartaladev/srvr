package ginsrvr_test

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// pingAPI answers GET /ping with 204.
type pingAPI struct{}

func (pingAPI) Configure(g *gin.RouterGroup) {
	g.GET("/ping", func(c *gin.Context) { c.Status(http.StatusNoContent) })
}

// getPing sends GET /ping to addr and returns the status.
func getPing(ctx context.Context, addr string) int {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/ping", nil)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}

	_ = res.Body.Close()
	http.DefaultClient.CloseIdleConnections()

	return res.StatusCode
}

// Start returns once the server is serving; Shutdown stops it and Wait
// reports how serving ended. This is the shape a lifecycle container runs.
func ExampleServer_Start() {
	srv, err := ginsrvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}

	if err := srv.Register(pingAPI{}); err != nil {
		panic(err)
	}

	ctx := context.Background()
	if err := srv.Start(ctx); err != nil {
		panic(err)
	}

	fmt.Println("health:", srv.HealthCheck(ctx))
	fmt.Println("GET /ping:", getPing(ctx, srv.Addr()))

	if err := srv.Shutdown(ctx); err != nil {
		panic(err)
	}

	fmt.Println("wait:", srv.Wait())
	// Output:
	// health: <nil>
	// GET /ping: 204
	// wait: <nil>
}

// Run serves until its context ends, then drains and returns: the one call
// for a plain main.
func ExampleServer_Run() {
	srv, err := ginsrvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}

	if err := srv.Register(pingAPI{}); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Run(ctx) }()
	<-srv.Ready()

	fmt.Println("GET /ping:", getPing(ctx, srv.Addr()))

	cancel() // in a main, signal.NotifyContext cancels it on SIGINT or SIGTERM
	fmt.Println("run:", <-done)
	// Output:
	// GET /ping: 204
	// run: <nil>
}
