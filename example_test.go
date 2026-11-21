package srvr_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"

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
	srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard))
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

// API shows groups, prefixes and middleware.
type API struct{}

// Configure adds the routes of API under /api/v1.
func (API) Configure(g *srvr.Group) {
	api := g.Group("/api", header("X-API", "1"))
	v1 := api.Group("/v1")
	v1.Use(header("X-Version", "v1")) // only routes registered from here on

	v1.GET("/users/{id}", func(w http.ResponseWriter, r *http.Request) error {
		_, err := fmt.Fprintf(w, "user %s", r.PathValue("id"))

		return err
	})
}

// header returns middleware setting a response header.
func header(name, value string) srvr.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(name, value)
			next.ServeHTTP(w, r)
		})
	}
}

func ExampleGroup() {
	srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}

	if err := srv.Register(API{}); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Start(ctx) }()
	<-srv.Ready()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv.Addr()+"/api/v1/users/42", nil)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	fmt.Println(res.StatusCode, string(body), res.Header.Get("X-API"), res.Header.Get("X-Version"))

	// Output:
	// 200 user 42 1 v1
}

func ExampleWithErrorHook() {
	srv, err := srvr.New(
		srvr.WithAddr("127.0.0.1:0"),
		srvr.WithLogOutput(io.Discard),
		// Runs after srvr has recorded the error for the access record.
		srvr.WithErrorHook(func(_ context.Context, err error, p *prblm.Problem) {
			fmt.Println("answered", p.Status, "for:", err)
		}),
	)
	if err != nil {
		panic(err)
	}

	if err := srv.Register(Hello{}); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Start(ctx) }()
	<-srv.Ready()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv.Addr()+"/missing", nil)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}

	_, _ = io.Copy(io.Discard, res.Body)
	_ = res.Body.Close()

	// Output:
	// answered 404 for: prblm: not found
}

func ExampleLogConfig() {
	// Any loader can fill the config structs; srvr reads no files or
	// environment variables itself.
	var c srvr.LogConfig
	if err := json.Unmarshal([]byte(`{"level":"debug","format":"text"}`), &c); err != nil {
		panic(err)
	}

	_, err := srvr.New(srvr.WithLogConfig(c), srvr.WithLogOutput(io.Discard))
	fmt.Println("valid:", err == nil)

	_, err = srvr.New(srvr.WithLogConfig(srvr.LogConfig{Level: "loud"}))
	fmt.Println(errors.Is(err, srvr.ErrConfig))

	// Output:
	// valid: true
	// true
}

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
	requireHealth := func(_ context.Context, routes srvr.Routes) error {
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

// Signup logs its progress at DEBUG and fails.
type Signup struct{}

// Configure adds POST /registrations, which fails after two steps.
func (Signup) Configure(g *srvr.Group) {
	g.POST("/registrations", func(_ http.ResponseWriter, r *http.Request) error {
		log := srvr.LoggerFromContext(r.Context())
		log.DebugContext(r.Context(), "step one")
		log.DebugContext(r.Context(), "step two")

		return errors.New("db down")
	})
}

// A failing request writes its DEBUG records, which the INFO level would
// drop, just before its failed record.
func ExampleWithDebugBuffer() {
	var out bytes.Buffer

	srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(&out), srvr.WithDebugBuffer())
	if err != nil {
		panic(err)
	}

	if err := srv.Register(Signup{}); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()
	<-srv.Ready()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+srv.Addr()+"/registrations", nil)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}

	_ = res.Body.Close()

	cancel()
	if err := <-done; err != nil { // the drain waits for the request's records
		panic(err)
	}

	for line := range strings.Lines(out.String()) {
		var rec struct {
			Msg      string `json:"msg"`
			Level    string `json:"level"`
			TraceID  string `json:"trace_id"`
			Buffered bool   `json:"srvr.buffered"`
		}

		if json.Unmarshal([]byte(line), &rec) == nil && rec.TraceID != "" {
			fmt.Println(rec.Level, rec.Msg, "buffered:", rec.Buffered)
		}
	}

	// Output:
	// DEBUG step one buffered: true
	// DEBUG step two buffered: true
	// ERROR http.request.failed buffered: false
}

// Debug buffering configured by a loader, here from JSON, with the rest of
// the log settings: requests failing with 400 or above flush their DEBUG
// records.
func ExampleDebugBufferConfig() {
	var logs srvr.LogConfig
	if err := json.Unmarshal([]byte(`{"level":"info","debug_buffer":{"enabled":true,"flush_status":400,"max_records":50}}`), &logs); err != nil {
		panic(err)
	}

	fmt.Printf("%+v\n", logs.DebugBuffer)

	_, err := srvr.New(srvr.WithLogConfig(logs))
	fmt.Println("New:", err)

	// Output:
	// {Enabled:true Level: FlushStatus:400 MaxRecords:50 MaxBytes:0 TotalBytes:0}
	// New: <nil>
}

// ExampleStatic serves an embedded site under /assets.
func ExampleStatic() {
	site, err := fs.Sub(embedded, "testdata/static") // //go:embed testdata/static
	if err != nil {
		panic(err)
	}

	srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}

	if err := srv.Register(srvr.Static("/assets", site)); err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()
	<-srv.Ready()

	for _, path := range []string{"/assets/app.js", "/assets", "/assets/missing.js"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv.Addr()+path, nil)

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
	// /assets/app.js 200 text/javascript; charset=utf-8
	// /assets 200 text/html; charset=utf-8 no-cache
	// /assets/missing.js 404 application/problem+json no-store
	// Start returned <nil>
}

// ExampleWithSPAFallback serves a single-page app: files as they are, and
// index.html for client-side routes a browser navigates to.
func ExampleWithSPAFallback() {
	site, err := fs.Sub(embedded, "testdata/static")
	if err != nil {
		panic(err)
	}

	srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard))
	if err != nil {
		panic(err)
	}

	err = srv.Register(srvr.Static("/", site,
		srvr.WithSPAFallback("index.html"),
		srvr.WithStaticCacheControl("public, max-age=31536000, immutable"), // hashed build assets
	))
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()
	<-srv.Ready()

	for _, r := range []struct{ path, accept string }{
		{"/app.js", "*/*"},
		{"/dashboard", "text/html"}, // a browser navigation: the app
		{"/dashboard", "*/*"},       // fetch's default: a 404 problem, not HTML
	} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv.Addr()+r.path, nil)
		req.Header.Set("Accept", r.accept)

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}

		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		fmt.Println(r.path, r.accept, res.StatusCode, res.Header.Get("Cache-Control"))
	}

	cancel()
	fmt.Println("Start returned", <-done)

	// Output:
	// /app.js */* 200 public, max-age=31536000, immutable
	// /dashboard text/html 200 no-cache
	// /dashboard */* 404 no-store
	// Start returned <nil>
}

// ExampleWithErrorRenderer answers errors in an application's own JSON
// format, never writing the error's text, and delegates nothing to prblm.
func ExampleWithErrorRenderer() {
	renderer := srvr.ErrorRendererFunc(func(_ context.Context, req srvr.ErrorRequest, err error) srvr.ErrorResponse {
		status, code := http.StatusInternalServerError, "internal"
		if errors.Is(err, srvr.ErrNotFound) {
			status, code = http.StatusNotFound, "not_found"
		}

		return srvr.ErrorResponse{
			Status: status,
			Header: http.Header{"Content-Type": {"application/json"}},
			Body:   []byte(`{"code":"` + code + `","trace":"` + req.TraceID + `"}`),
			Title:  code, // for the access record and error hooks only
		}
	})

	srv, err := srvr.New(srvr.WithAddr("127.0.0.1:0"), srvr.WithLogOutput(io.Discard), srvr.WithErrorRenderer(renderer))
	if err != nil {
		panic(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Start(ctx) }()
	<-srv.Ready()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+srv.Addr()+"/missing", nil)
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	body, _ := io.ReadAll(res.Body)
	fmt.Println(res.StatusCode, res.Header.Get("Content-Type"), string(body))

	// Output:
	// 404 application/json {"code":"not_found","trace":"4bf92f3577b34da6a3ce929d0e0e4736"}
}
