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

	// Start serves until SIGINT or SIGTERM, then drains in-flight requests.
	return srv.Start(ctx)
}
```

Every option has a functional form (`srvr.WithAddr`, `srvr.WithLogLevel`, ...) and a plain config
struct with `json`, `yaml` and `env` tags (`srvr.Config`, `srvr.LogConfig`, ...) that any
configuration loader can fill. srvr never reads environment variables or files itself.

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
directory or an `embed.FS`, as a route configurer:

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
