package ginsrvr_test

import (
	"context"
	"fmt"
	"io"
	"io/fs"
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

// UsersHandler contributes the users API.
type UsersHandler struct{}

// Configure mounts the routes on the root group.
func (h *UsersHandler) Configure(g *gin.RouterGroup) {
	g.GET("/api/users", func(c *gin.Context) { c.String(http.StatusOK, "users") })
}

// ExampleStatic serves an embedded single-page app at the root beside an
// API route: the route wins, files are served as they are, and a browser
// navigation to a client-side route gets index.html.
func ExampleStatic() {
	site, err := fs.Sub(embedded, "testdata/static") // //go:embed testdata/static
	if err != nil {
		panic(err)
	}

	srv, err := ginsrvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}

	api := routes(func(g *gin.RouterGroup) {
		g.GET("/api/users", func(c *gin.Context) { c.String(http.StatusOK, "users") })
	})
	for _, c := range []srvr.RouteConfigurer[*gin.RouterGroup]{ginsrvr.Static("/", site, srvr.WithSPAFallback("index.html")), api} {
		if err := srv.Register(c); err != nil {
			panic(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()
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

	cancel()
	fmt.Println("Start returned", <-done)

	// Output:
	// /app.js 200 text/javascript; charset=utf-8
	// /api/users 200 text/plain; charset=utf-8
	// /dashboard 200 text/html; charset=utf-8 no-cache
	// /missing.js 404 application/problem+json no-store
	// Start returned <nil>
}
