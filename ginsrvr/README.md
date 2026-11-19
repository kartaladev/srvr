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

## Static files and single-page apps

`ginsrvr.Static(prefix, fsys, opts...)` serves the files of an `fs.FS` exactly as `srvr.Static` does
on net/http, with the same `srvr.StaticOption` values and defaults (see the
[root README](../README.md#static-files-and-single-page-apps)):

```go
root, err := os.OpenRoot("dist") // refuses symbolic links that leave dist; os.DirFS does not
if err != nil {
	return err
}

if err := srv.Register(ginsrvr.Static("/", root.FS(), srvr.WithSPAFallback("index.html"))); err != nil {
	return err
}
```

A gin catch-all route cannot sit beside other routes at the same level, so the server serves its
mounts from gin's no-route chain instead of registering routes: every registered route wins, in
either registration order, and the longest mount prefix containing the path answers. A `GET` or
`HEAD` to a path only another method's route serves is the mount's, so a browser's `GET /login`
beside `POST /login` gets the SPA fallback; any other method keeps gin's `405` (`PUT /api/users`
beside `POST /api/users` gets `Allow: POST`; net/http and fiber add `GET, HEAD`). Root-group
middleware (`RouterGroup.Use`) runs around the mounts even when a configurer adds it after `Static`,
and, once a mount exists, also around requests gin answers with its own `404` or `405`. Register `ginsrvr.Static` with `Server.Register`; a
configurer calling its `Configure` fails `Start`. A mount replaces any `NoRoute` handler a
`WithGinEngine` hook set.

Released under the [MIT License](LICENSE).
