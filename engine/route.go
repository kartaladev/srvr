package engine

import (
	"context"
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
	return c.checkRoutes(ctx, ctx, routes)
}

// checkRoutes is CheckRoutes with each check receiving checkCtx while the
// wait for it ends only with waitCtx: Start's interruption by Shutdown
// cancels checkCtx, and a check that ignores it keeps Start waiting until
// it returns or Start's own context ends. No further check runs once
// checkCtx is done.
func (c *Core) checkRoutes(waitCtx, checkCtx context.Context, routes Routes) error {
	for i, check := range c.settings.RouteChecks {
		if err := checkCtx.Err(); err != nil {
			return err
		}

		if err := runCheck(waitCtx, checkCtx, i, check, slices.Clone(routes)); err != nil {
			return err
		}
	}

	return nil
}

// runCheck runs check i under runGuarded with checkCtx, waiting for it
// until waitCtx ends.
func runCheck(waitCtx, checkCtx context.Context, i int, check RouteCheck, routes Routes) error {
	err, abandoned := runGuarded(waitCtx, func(context.Context) error { return check(checkCtx, routes) })

	// runGuarded returns its own *panicError unwrapped, so a type
	// assertion finds it without walking the check's error chain, which
	// may cycle or panic in Unwrap or As.
	pe, panicked := err.(*panicError) //nolint:errorlint // runGuarded's own error, never wrapped: a chain walk is what must be avoided.

	switch {
	case abandoned:
		return waitCtx.Err()
	case panicked:
		return RouteError("route check %d panicked: %s", i, pe.value)
	case err != nil:
		return fmt.Errorf("%w: route check %d: %w", ErrRouteConfig, i, guard(err))
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
