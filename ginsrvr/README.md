# ginsrvr

The Gin engine for [srvr](../README.md): srvr's lifecycle, request pipeline, RFC 9457 problems and
log schema on a `*gin.Engine`.

```sh
go get github.com/kartaladev/srvr/ginsrvr
```

Handlers stay plain `gin.HandlerFunc` and report errors with `c.Error(err)`; srvr renders each as a
problem. Configurers implement `srvr.RouteConfigurer[*gin.RouterGroup]`.

```go
type RegistrationHandler struct{}

func (h *RegistrationHandler) Configure(g *gin.RouterGroup) {
	g.POST("/registrations", h.Register)
}

func (h *RegistrationHandler) Register(c *gin.Context) {
	if c.Query("email") == "" {
		_ = c.Error(prblm.ErrInvalidInput)

		return
	}

	c.Status(http.StatusCreated)
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := ginsrvr.New() // listens on :8080; logs JSON to stdout
	if err != nil {
		return err
	}

	if err := srv.Register(&RegistrationHandler{}); err != nil {
		return err
	}

	return srv.Start(ctx)
}
```

Every `srvr` option applies. `ginsrvr.WithGinEngine(func(*gin.Engine))` customises the engine;
srvr trusts no proxy unless the hook calls `SetTrustedProxies`, and afterwards re-asserts
`ContextWithFallback`, `HandleMethodNotAllowed`, `RedirectTrailingSlash` off and its recovery; a hook
that registers routes or middleware fails construction. Do not
pass an error whose chain cycles to `c.Error`: gin walks it before srvr sees it.

Released under the [MIT License](LICENSE).
