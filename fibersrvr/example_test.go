package fibersrvr_test

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

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

	go func() { done <- srv.Run(ctx) }()
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

// ExampleStatic serves an embedded single-page app at the root beside an
// API route: the route wins, files are served as they are, and a browser
// navigation to a client-side route gets index.html.
func ExampleStatic() {
	site, err := fs.Sub(embedded, "testdata/static") // //go:embed testdata/static
	if err != nil {
		panic(err)
	}

	srv, err := fibersrvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}

	api := routes(func(r fiber.Router) {
		r.Get("/api/users", func(c fiber.Ctx) error { return c.SendString("users") })
	})
	for _, c := range []srvr.RouteConfigurer[fiber.Router]{fibersrvr.Static("/", site, srvr.WithSPAFallback("index.html")), api} {
		if err := srv.Register(c); err != nil {
			panic(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Run(ctx) }()
	<-srv.Ready()

	for _, path := range []string{"/app.js", "/api/users", "/dashboard", "/missing.js"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv.Addr()+path, nil)
		req.Header.Set("Accept", "text/html")

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}

		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		fmt.Println(strings.TrimSpace(fmt.Sprint(path, " ", res.StatusCode, " ", res.Header.Get("Content-Type"), " ", res.Header.Get("Cache-Control"))))
	}

	http.DefaultClient.CloseIdleConnections()
	cancel()
	fmt.Println("Start returned", <-done)

	// Output:
	// /app.js 200 text/javascript; charset=utf-8
	// /api/users 200 text/plain; charset=utf-8
	// /dashboard 200 text/html; charset=utf-8 no-cache
	// /missing.js 404 application/problem+json no-store
	// Start returned <nil>
}

// ExampleWithSendFileCache turns on fiber's open-file cache for one mount
// of build assets. An embed.FS cannot change, so the cache never serves a
// stale file here; the mount's cache cleaner goroutine outlives Shutdown.
// A duration that is not positive fails Start.
func ExampleWithSendFileCache() {
	site, err := fs.Sub(embedded, "testdata/static")
	if err != nil {
		panic(err)
	}

	srv, err := fibersrvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}

	if err := srv.Register(fibersrvr.Static("/assets", site, fibersrvr.WithSendFileCache(time.Minute))); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Run(ctx) }()
	<-srv.Ready()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv.Addr()+"/assets/app.js", nil)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}

	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()
	fmt.Println(res.StatusCode, res.Header.Get("Content-Type"))

	http.DefaultClient.CloseIdleConnections()
	cancel()
	fmt.Println("Start returned", <-done)

	// Output:
	// 200 text/javascript; charset=utf-8
	// Start returned <nil>
}
