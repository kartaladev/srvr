# srvr

srvr runs Go HTTP servers on the routing engine you choose (net/http, Gin or Fiber v3) with one
lifecycle, one request pipeline and one log schema:

- every error rendered as an RFC 9457 problem through [prblm](https://github.com/kartaladev/prblm),
  never leaking internals in a 5xx;
- structured `log/slog` JSON designed for AI-assisted triage: trace ids on every record, stable event
  names, self-contained error records with redacted error text ([LOGGING.md](LOGGING.md));
- graceful start and shutdown with ordered hooks, bounded timeouts and safe defaults that need no
  configuration.

Status: pre-v1. The public API and the log schema are commitments once v1 is tagged; until then
breaking changes are listed in [CHANGELOG.md](CHANGELOG.md).

## Install

```sh
go get github.com/kartaladev/srvr
```

Go 1.27 or later.

## Quick start

```go
type Hello struct{}

func (Hello) Configure(g *srvr.Group) {
	g.GET("/hello", func(w http.ResponseWriter, _ *http.Request) error {
		_, err := io.WriteString(w, "hello")

		return err
	})
	g.GET("/missing", func(http.ResponseWriter, *http.Request) error {
		return prblm.ErrNotFound // rendered as an RFC 9457 problem
	})
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := srvr.New() // listens on :8080; logs JSON to stdout
	if err != nil {
		return err
	}

	if err := srv.Register(Hello{}); err != nil {
		return err
	}

	// Run serves until SIGINT or SIGTERM, then drains in-flight requests.
	return srv.Run(ctx)
}
```

Every option has a functional form (`srvr.WithAddr`, `srvr.WithLogLevel`, ...) and a plain config
struct with `json`, `yaml` and `env` tags (`srvr.Config`, `srvr.LogConfig`, ...) that any
configuration loader can fill. srvr never reads environment variables or files itself.

## Running under a lifecycle container

`Run(ctx)` is the one call for a plain `main`: it serves until `ctx` is done, `Shutdown` is called or
serving fails, then drains and returns. For manual control, `Start(ctx)` returns once the server is
serving, `Wait()` reports how serving ended and `HealthCheck(ctx)` says whether it still serves:

```go
func runManually(ctx context.Context, srv *srvr.Server) error {
	if err := srv.Start(ctx); err != nil { // returns once serving; ctx bounds startup only
		return err
	}

	<-ctx.Done()

	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()

	if err := srv.Shutdown(stopCtx); err != nil {
		return err
	}

	return srv.Wait() // the serve failure joined with failing shutdown hooks, or nil
}
```

Because `Start`, `Shutdown` and `HealthCheck` have the shapes lifecycle containers detect, a server
registers in one as a starter without an adapter; srvr imports no container. With
[doger](https://github.com/kartaladev/doger) v0.3.0 or later, provide each route configurer, then the
server, which collects every configurer with `InvokeAll`:

```go
root := do.New()
do.Provide(root, NewUsersAPI)  // func(do.Injector) (*UsersAPI, error); *UsersAPI is a RouteConfigurer[*srvr.Group]
do.Provide(root, NewOrdersAPI)
do.Provide(root, func(i do.Injector) (*srvr.Server, error) {
	srv, err := srvr.New()
	if err != nil {
		return nil, err
	}

	confs, err := do.InvokeAll[srvr.RouteConfigurer[*srvr.Group]](i)
	if err != nil {
		return nil, err
	}

	for _, c := range confs {
		if err := srv.Register(c); err != nil {
			return nil, err
		}
	}

	return srv, nil
}, do.AsStarter())

// doger has no start timeout of its own: bound startup with a deadline.
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

if err := root.StartWithContext(ctx); err != nil {
	log.Fatal(err)
}

root.ShutdownOnSignals(syscall.SIGINT, syscall.SIGTERM) // calls srv.Shutdown with the shutdown deadline
```

(`do` is `github.com/kartaladev/doger`.) doger cancels the context it passed to `Start` as soon as
`StartWithContext` returns; srvr ignores that cancellation by design, since the context bounds startup
only. `root.HealthCheck()` reports the server through its `HealthCheck`. Do not register `Run` as a
run-once hook: it blocks until the server stops. For gin and fiber, swap the types: `*ginsrvr.Server`
with `RouteConfigurer[*gin.RouterGroup]`, `*fibersrvr.Server` with `RouteConfigurer[fiber.Router]`.

## Engines

| Engine | Module | Handler shape | Errors reported by |
|--------|--------|---------------|--------------------|
| net/http (Go 1.22+ `ServeMux` patterns) | `github.com/kartaladev/srvr` | `func(http.ResponseWriter, *http.Request) error` | returning the error |
| Gin | [`github.com/kartaladev/srvr/ginsrvr`](ginsrvr) | `gin.HandlerFunc` | `c.Error(err)` |
| Fiber v3 | [`github.com/kartaladev/srvr/fibersrvr`](fibersrvr) | `func(fiber.Ctx) error` | returning the error |

A route configurer is typed by its engine (`RouteConfigurer[*srvr.Group]`,
`RouteConfigurer[*gin.RouterGroup]`, `RouteConfigurer[fiber.Router]`), so a configurer for the wrong
engine does not compile.

## Static files and single-page apps

`srvr.Static(prefix, fsys, opts...)` serves the files of an `fs.FS`, such as a Vite or React build
directory or an `embed.FS`, as a route configurer. `ginsrvr.Static` and `fibersrvr.Static` take the
same arguments and `srvr.StaticOption` values and give the same answers on their engines:

```go
root, err := os.OpenRoot("dist") // refuses symbolic links that leave dist; os.DirFS does not
if err != nil {
	return err
}

if err := srv.Register(srvr.Static("/", root.FS())); err != nil {
	return err
}
```

With no options it serves `GET` and `HEAD` of regular files with their `Content-Type`,
`Content-Length` and `Last-Modified`, answers a directory with its `index.html` (sent with
`Cache-Control: no-cache`), and answers everything else with srvr's problems: `404` for missing
files, dotfiles (`.env`, `.git/...`) and paths that try to leave the file system, `405` for other
methods, and the safe `500` for file system errors. Directories are never listed, nothing is
redirected or compressed, and byte ranges are not served yet. Every other registered route wins over
the static mount, in either registration order, and a static `404` is logged and limited like a
request no route matched.

For a single-page app, `srvr.WithSPAFallback("index.html")` answers client-side routes such as
`/dashboard` or `/users/42/edit` with the app. Only browser navigations get it: `GET` or `HEAD`, an
`Accept` header listing `text/html` (not just `*/*`, which `fetch` sends), and a last path segment
without a `.`, so a missing `app-9999.js` or an API typo still gets a `404` problem. The fallback is
sent with `Cache-Control: no-cache` and `Vary: Accept`. Content-hashed assets can be cached for good
with `srvr.WithStaticCacheControl("public, max-age=31536000, immutable")`; index files and the
fallback keep `no-cache` (`srvr.WithStaticIndexCacheControl` changes it). Every option has a field in
`srvr.StaticConfig` for configuration loaders.

```go
s.Register(srvr.Static("/", site, srvr.WithSPAFallback("index.html")))      // net/http
s.Register(ginsrvr.Static("/", site, srvr.WithSPAFallback("index.html")))   // gin
s.Register(fibersrvr.Static("/", site, srvr.WithSPAFallback("index.html"))) // fiber
```

**Prefer `os.OpenRoot` over `os.DirFS`.** srvr refuses any request path that could leave the file
system (`..`, encoded or not, backslashes, control characters), but symbolic links are the file
system's business: `os.DirFS(dir)` follows a link inside `dir` to anywhere on the disk. The
`fs.FS` of `os.OpenRoot(dir)` (`root.FS()`) refuses a link that escapes the root, and such a request
gets the safe `500` problem and an `ERROR` record. An `embed.FS` has no links and no modification
times, so its files are sent without `Last-Modified`.

Each engine routes as its router does, so a few answers differ, each documented on the engine's
`Static` and pinned by the parity tests: net/http redirects a raw `..` or `//` in the request target
to the cleaned path (`307`), where gin and fiber answer `404`; gin, and fiber with `UnescapePath`,
serve `GET /assets%2Fapp.js` from `Static("/assets")`, where net/http and fiber by default answer
`404`; fiber refuses a consumer `Get("/*")` beside `Static("/")` at `Start`, where the consumer's
catch-all wins on net/http and gin; and fiber opens a file again by name to send it, so a file
removed or made unreadable in between is a `404`, or the safe `500` when it fails at fasthttp's
second open. See [ginsrvr](ginsrvr#static-files-and-single-page-apps)
and [fibersrvr](fibersrvr#static-files-and-single-page-apps).

fiber can keep served files open between requests with an explicit, per-mount opt-in:
`fibersrvr.WithSendFileCache(d)`, or `srvr.StaticConfig.FiberSendFileCache` (`0` is off). It measured
about twice as fast per request on `os.DirFS`, but it loosens the default: a file replaced on disk
may be served stale for up to `d`, and each cached mount starts a fasthttp cache-cleaner goroutine
that outlives `Shutdown` (fiber v3.7.0 never stops it), which a goleak test must ignore. `srvr.Static`
and `ginsrvr.Static` have no file cache and fail `Start` on a non-zero `FiberSendFileCache` rather
than ignore it.

## Custom error rendering

By default every engine answers errors with RFC 9457 problems from prblm. `srvr.WithErrorRenderer(r)`
hands every error answer to your `srvr.ErrorRenderer` instead: handler errors, recovered panics, the
router's `404` and `405`, static misses, oversized bodies, a client that closed the request and fiber's
pre-routing rejections. The renderer gets the error and a `srvr.ErrorRequest` (method, path, matched
route, trace id and request headers; never the query string) and returns a `srvr.ErrorResponse`.
srvr's own errors can be told apart with `errors.Is` and `errors.As`: `srvr.ErrNotFound` (which also
matches `prblm.ErrNotFound`), `*srvr.MethodNotAllowedError` (with `Allow`), `srvr.ErrRequestTooLarge`,
`srvr.ErrHeadersTooLarge` and `srvr.ErrRequestTimeout` (fiber), `srvr.ErrClientClosed` and
`*srvr.PanicError`. It cannot be combined with `srvr.WithProblemOptions` (construction fails).
On a `405` srvr does not add an `Allow` header: the renderer sets it from
`srvr.MethodNotAllowedError.Allow` (found with `errors.As`), as the house renderer below does.

To change a few answers only, wrap `srvr.DefaultErrorRenderer()`, which answers like the default,
with the server's trace id:

```go
var errQuotaExceeded = errors.New("quota exceeded")

var quotaRenderer = srvr.ErrorRendererFunc(func(ctx context.Context, req srvr.ErrorRequest, err error) srvr.ErrorResponse {
	if !errors.Is(err, errQuotaExceeded) {
		return srvr.DefaultErrorRenderer().Render(ctx, req, err)
	}

	return srvr.ErrorResponse{
		Status: http.StatusTooManyRequests,
		Header: http.Header{"Content-Type": {"application/json"}, "Retry-After": {"60"}},
		Body:   []byte(`{"code":"quota_exceeded"}`),
		Title:  "quota exceeded", // for the access record and error hooks only
	}
})

srv, err := srvr.New(srvr.WithErrorRenderer(quotaRenderer))
```

A house format maps each kind of error to a fixed code and never echoes the error's text, which can
carry internals (SQL, file paths, upstream addresses):

```go
var houseRenderer = srvr.ErrorRendererFunc(func(_ context.Context, req srvr.ErrorRequest, err error) srvr.ErrorResponse {
	status, code := http.StatusInternalServerError, "internal"

	var mna *srvr.MethodNotAllowedError

	switch {
	case errors.Is(err, srvr.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.As(err, &mna):
		status, code = http.StatusMethodNotAllowed, "method_not_allowed"
	case errors.Is(err, srvr.ErrRequestTooLarge):
		status, code = http.StatusRequestEntityTooLarge, "too_large"
	case errors.Is(err, srvr.ErrClientClosed):
		status, code = 499, "client_closed"
	}

	h := http.Header{"Content-Type": {"application/json"}}
	if mna != nil {
		h.Set("Allow", strings.Join(mna.Allow, ", "))
	}

	return srvr.ErrorResponse{
		Status: status,
		Header: h,
		Body:   []byte(`{"code":"` + code + `","trace":"` + req.TraceID + `"}`),
	}
})
```

Both are compiled and run by `Example_customErrorRendering` in
[readme_example_test.go](readme_example_test.go). The same renderer works on every engine:
`ginsrvr.New(srvr.WithErrorRenderer(r))`, `fibersrvr.New(srvr.WithErrorRenderer(r))`.

srvr keeps its safety rules around any renderer:

- The renderer is called at most once per request, never after the response started: a handler that
  already wrote keeps its response, and the record says `srvr.response.committed`.
- An error whose `Unwrap` chain is cyclic or panics is made walkable before the renderer sees it.
- A renderer that panics, answers a status outside `400`-`599`, a body over 1 MiB, a body without
  `Content-Type`, or takes longer than 5 seconds is replaced by srvr's fixed safe `500` problem, and
  the record says why in `srvr.renderer.failure`. Its context keeps the request's values but not its
  cancellation.
- `Content-Length` is set from the body; `Transfer-Encoding` and hop-by-hop headers are dropped. A
  renderer header replaces the handler's header of the same name, except `Vary`, which is merged.
- The log records the error chain as without a renderer; `problem.type` and `problem.title` come from
  `ErrorResponse.Type` and `Title` and are left out when empty (see [LOGGING.md](LOGGING.md)).

Engine differences, pinned by the parity tests (`internal/paritytest`):

- fiber never cancels a request's context when the client disconnects, so the renderer gets the
  handler's error, never `srvr.ErrClientClosed`.
- fiber sends nothing before the handler returns: only a started body stream (`SendStreamWriter`)
  counts as committed, and a buffered body is replaced by the renderer's answer.
- net/http and gin reject headers over `MaxHeaderBytes` before srvr runs, with Go's plain-text `431`;
  fiber hands `srvr.ErrHeadersTooLarge` to the renderer.
- gin: a status-only abort (`c.AbortWithStatus(401)`) keeps gin's bare status; the renderer is not
  called, the error hook is. Without a renderer gin answers a closed request with an empty `200`, as
  in v0.3.0; with one it is `499` through the renderer, as on net/http.

## Modules

| Module | Purpose | Dependencies |
|--------|---------|--------------|
| `github.com/kartaladev/srvr` | core and the net/http engine | standard library and prblm only |
| [`ginsrvr`](ginsrvr) | Gin engine | gin, prblm/adapters/ginprblm |
| [`fibersrvr`](fibersrvr) | Fiber v3 engine | fiber, prblm/adapters/fiberprblm, fasthttp |
| [`otelsrvr`](otelsrvr) | OpenTelemetry metrics and tracing | OpenTelemetry API (its tests also require the SDK) |
| `otelsetup` | OpenTelemetry SDK and exporter wiring (Prometheus, OTLP) for applications | OpenTelemetry SDK, OTLP exporters (HTTP and gRPC), Prometheus client |

## More

- [LOGGING.md](LOGGING.md): the log schema, redaction and triage records.
- [docs/PERFORMANCE.md](docs/PERFORMANCE.md): running the benchmarks and profiles, comparing results, and the CI allocation gate.
- [CHANGELOG.md](CHANGELOG.md): changes per release.
- [RELEASING.md](RELEASING.md): how the modules are tagged.

srvr is released under the [MIT License](LICENSE).
