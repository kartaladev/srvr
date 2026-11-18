# otelsrvr

OpenTelemetry HTTP server metrics and tracing for [srvr](../README.md) on every engine (net/http,
Gin, Fiber). It depends only on the OpenTelemetry API: the application installs the SDK and
exporters, for example with `github.com/kartaladev/srvr/otelsetup`.

```sh
go get github.com/kartaladev/srvr/otelsrvr
```

```go
obs, err := otelsrvr.New(otelsrvr.WithTracing()) // global providers by default
if err != nil {
	log.Fatal(err)
}

srv, err := srvr.New(srvr.WithObserver(obs))
if err != nil {
	log.Fatal(err)
}
```

It records `http.server.request.duration` (and, when enabled, active requests and body sizes) with
semantic-convention attributes, and with `WithTracing` one server span per request whose ids replace
srvr's own `trace_id` and `span_id` in logs and problems. Build observers with `New`; a zero
`Observer` records nothing.

Released under the [MIT License](LICENSE).
