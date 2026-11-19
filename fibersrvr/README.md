# fibersrvr

The Fiber v3 engine for [srvr](../README.md): srvr's lifecycle, request pipeline, RFC 9457 problems
and log schema on a `*fiber.App`.

```sh
go get github.com/kartaladev/srvr/fibersrvr
```

Handlers are `func(fiber.Ctx) error`; srvr renders a returned error as a problem. Configurers
implement `srvr.RouteConfigurer[fiber.Router]`.

```go
type RegistrationHandler struct{}

func (h *RegistrationHandler) Configure(r fiber.Router) {
	r.Post("/registrations", h.Register)
}

func (h *RegistrationHandler) Register(c fiber.Ctx) error {
	if c.Query("email") == "" {
		return prblm.ErrInvalidInput
	}

	return c.SendStatus(http.StatusCreated)
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := fibersrvr.New() // listens on :8080; logs JSON to stdout
	if err != nil {
		return err
	}

	if err := srv.Register(&RegistrationHandler{}); err != nil {
		return err
	}

	return srv.Start(ctx)
}
```

Every `srvr` option applies. `fibersrvr.WithFiberConfig(func(*fiber.Config))` customises the
configuration; srvr keeps its error handler, and prefork is not supported. Fiber buffers at most
64 KiB of headers per connection, so a larger `MaxHeaderBytes` is capped with a warning.

## Static files and single-page apps

`fibersrvr.Static(prefix, fsys, opts...)` serves the files of an `fs.FS` exactly as `srvr.Static`
does on net/http, with the same `srvr.StaticOption` values and defaults (see the
[root README](../README.md#static-files-and-single-page-apps)):

```go
root, err := os.OpenRoot("dist") // refuses symbolic links that leave dist; os.DirFS does not
if err != nil {
	return err
}

if err := srv.Register(fibersrvr.Static("/", root.FS(), srvr.WithSPAFallback("index.html"))); err != nil {
	return err
}
```

Files are sent with fiber's `Ctx.SendFile` by the name srvr's resolver chose, with fiber's open-file
cache off, so a file replaced on disk is served fresh; nothing is compressed and byte ranges are not
served. fiber matches routes in registration order, so the server installs the mounts after every
other configurer, longest prefix first: every registered route wins, in either registration order,
and middleware added with `Use` runs around the mounts even when added after `Static`. A `GET` or
`HEAD` to a path only another method's route serves is the mount's, so a browser's `GET /login`
beside `POST /login` gets the SPA fallback; other methods get fiber's `405` with `Allow` listing
`GET, HEAD` and the path's routes. A consumer route that repeats a mount's pattern, such as
`Get("/*")` beside `Static("/")`, would never be served and fails `Start` as a duplicate. Register
`fibersrvr.Static` with `Server.Register`; a configurer calling its `Configure` fails `Start`.

### Opt-in: fiber's open-file cache

`fibersrvr.WithSendFileCache(d)`, an option of one mount, or the config field
`srvr.StaticConfig.FiberSendFileCache` (`fiber_send_file_cache`, env `FIBER_SEND_FILE_CACHE`; `0`
means off), turns on fiber's open-file cache for that mount: fasthttp keeps each served file open
for `d` after its last use. On `os.DirFS` it measured about twice as fast per request
(`BenchmarkPipeline/static cached` against `static`). It loosens the default, so it is opt-in, and
it costs:

- **Stale files:** a file replaced on disk may be served with its old content for up to `d`. An
  `embed.FS` cannot change, so it is unaffected.
- **A goroutine that outlives `Shutdown`:** each cached mount starts fasthttp's cache cleaner
  (`github.com/valyala/fasthttp.(*inMemoryCacheManager).handleCleanCache`) on its first served
  file. fiber v3.7.0 never sets `fasthttp.FS.CleanStop`, so nothing stops it; a test binary using
  goleak must ignore that function.

```go
fibersrvr.Static("/assets", root.FS(), fibersrvr.WithSendFileCache(time.Minute))
```

`d <= 0` passed to the option and a negative field fail `Start` with a `*srvr.ConfigError` naming
`Static.FiberSendFileCache`; `srvr.Static` and `ginsrvr.Static`, which have no file cache, fail
`Start` on any non-zero value instead of ignoring it.

Released under the [MIT License](LICENSE).
