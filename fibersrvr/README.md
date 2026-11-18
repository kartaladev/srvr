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

Released under the [MIT License](LICENSE).
