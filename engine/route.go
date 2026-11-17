package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

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

// Route is one route a configurer declared: its method ("" for a route
// served for every method), its pattern in the engine's own syntax and the
// Go type of the configurer that registered it.
type Route struct {
	Method     string
	Pattern    string
	Configurer string
}

// Routes lists the routes of a server after its configure phase, without
// middleware and the routes an engine adds implicitly.
type Routes []Route

// RouteCheck checks the route listing before the server binds its
// listener; an error fails Start. Consumers add one with
// srvr.WithRouteCheck.
type RouteCheck func(ctx context.Context, routes Routes) error

// CheckRoutes runs the server's route checks in order, each with ctx and
// its own copy of routes, and returns the first failure: an error or panic
// as an error wrapping ErrRouteConfig, or ctx's error when ctx ends first,
// abandoning a check that ignores it.
func (c *Core) CheckRoutes(ctx context.Context, routes Routes) error {
	for i, check := range c.settings.RouteChecks {
		if err := runCheck(ctx, i, check, slices.Clone(routes)); err != nil {
			return err
		}
	}

	return nil
}

// runCheck runs one check with runGuarded.
func runCheck(ctx context.Context, i int, check RouteCheck, routes Routes) error {
	err, abandoned := runGuarded(ctx, func(ctx context.Context) error { return check(ctx, routes) })

	var pe *panicError

	switch {
	case abandoned:
		return ctx.Err()
	case errors.As(err, &pe):
		return RouteError("route check %d panicked: %s", i, pe.value)
	case err != nil:
		return fmt.Errorf("%w: route check %d: %w", ErrRouteConfig, i, &guardedError{err})
	default:
		return nil
	}
}

// CollapseMethods returns routes with every pattern that one configurer
// registered for all of methods (an engine's any-method set, such as gin's
// Any or fiber's All) listed once with Method "", keeping the order of the
// first such route.
func CollapseMethods(routes Routes, methods []string) Routes {
	type key struct{ pattern, configurer string }

	inSet := map[key]map[string]bool{}
	for _, r := range routes {
		if !slices.Contains(methods, r.Method) {
			continue
		}

		k := key{r.Pattern, r.Configurer}
		if inSet[k] == nil {
			inSet[k] = map[string]bool{}
		}

		inSet[k][r.Method] = true
	}

	out := make(Routes, 0, len(routes))
	listed := map[key]bool{}

	for _, r := range routes {
		k := key{r.Pattern, r.Configurer}
		if len(methods) == 0 || len(inSet[k]) != len(methods) || !slices.Contains(methods, r.Method) {
			out = append(out, r)

			continue
		}

		if !listed[k] {
			listed[k] = true
			out = append(out, Route{Pattern: r.Pattern, Configurer: r.Configurer})
		}
	}

	return out
}
