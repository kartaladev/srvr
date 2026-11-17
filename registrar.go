package srvr

// RouteConfigurer contributes routes to a server. G is the routing engine's
// group type: *Group for the net/http engine of this package. A configurer
// written for one engine does not compile against another.
type RouteConfigurer[G any] interface {
	// Configure adds routes to group. It runs once per registration, in
	// registration order, when the server starts.
	Configure(group G)
}

// RouteRegistrar accepts route configurers for a server whose engine uses
// the group type G.
type RouteRegistrar[G any] interface {
	// Register adds conf to the configurers run when the server starts.
	Register(conf RouteConfigurer[G]) error
}
