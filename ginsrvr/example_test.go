package ginsrvr_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/ginsrvr"
)

// RegistrationHandler contributes the registration routes.
type RegistrationHandler struct{}

// Configure mounts the routes on the root group.
func (h *RegistrationHandler) Configure(g *gin.RouterGroup) {
	g.POST("/registrations", h.Register)
}

// Register reports a failure with c.Error; srvr renders it as a problem.
func (h *RegistrationHandler) Register(c *gin.Context) {
	if c.Query("email") == "" {
		_ = c.Error(prblm.ErrInvalidInput)

		return
	}

	c.Status(http.StatusCreated)
}

func Example() {
	srv, err := ginsrvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}

	if err := srv.Register(&RegistrationHandler{}); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()
	<-srv.Ready()

	for _, url := range []string{"/registrations?email=a@example.com", "/registrations"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+srv.Addr()+url, nil)

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}
		_ = res.Body.Close()

		fmt.Println(strings.TrimSpace(fmt.Sprint(res.StatusCode, " ", res.Header.Get("Content-Type"))))
	}

	cancel()
	fmt.Println(<-done)

	// Output:
	// 201
	// 400 application/problem+json
	// <nil>
}
