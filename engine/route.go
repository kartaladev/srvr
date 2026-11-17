package engine

import "fmt"

// RouteError returns an error wrapping [ErrRouteConfig] with the message
// format and args describe.
func RouteError(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrRouteConfig}, args...)...)
}

// Configure runs c.Configure(g) for an engine's configure phase and turns
// a panic into an error wrapping [ErrRouteConfig] that names c's type and
// the panic value, read through ValueText. The routes c registered before
// panicking are left to the engine, whose Start fails.
func Configure[G any](c interface{ Configure(G) }, g G) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = RouteError("configurer %T panicked: %s", c, ValueText(v))
		}
	}()

	c.Configure(g)

	return nil
}
