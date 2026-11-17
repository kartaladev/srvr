package fibersrvr_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr"
	"github.com/kartaladev/srvr/fibersrvr"
)

// RegistrationHandler contributes the registration routes.
type RegistrationHandler struct{}

// Configure mounts the routes on the root router.
func (h *RegistrationHandler) Configure(r fiber.Router) {
	r.Post("/registrations", h.Register)
}

// Register returns an error, which srvr renders as a problem.
func (h *RegistrationHandler) Register(c fiber.Ctx) error {
	if c.Query("email") == "" {
		return prblm.ErrInvalidInput
	}

	return c.SendStatus(http.StatusCreated)
}

func Example() {
	srv, err := fibersrvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard))
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

	http.DefaultClient.CloseIdleConnections()
	cancel()
	fmt.Println(<-done)

	// Output:
	// 201 text/plain; charset=utf-8
	// 400 application/problem+json
	// <nil>
}
