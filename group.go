package srvr

import "net/http"

// HandlerFunc handles a request and reports failure by returning an error,
// which the server renders as an RFC 9457 problem.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// route is a route buffered by a Group until the server installs it.
type route struct {
	method  string
	pattern string
	handler HandlerFunc
}

// Group registers routes on the net/http engine. A configurer receives one
// in Configure. Paths use the patterns of [http.ServeMux] without the
// method, which the registering method supplies; handlers return errors,
// which the server renders as problems.
type Group struct {
	routes *[]route
}

// Handle registers h for method and path, for example
// g.Handle(http.MethodGet, "/items/{id}", h).
func (g *Group) Handle(method, path string, h HandlerFunc) {
	*g.routes = append(*g.routes, route{method: method, pattern: path, handler: h})
}

// GET registers h for GET requests to path.
func (g *Group) GET(path string, h HandlerFunc) { g.Handle(http.MethodGet, path, h) }

// POST registers h for POST requests to path.
func (g *Group) POST(path string, h HandlerFunc) { g.Handle(http.MethodPost, path, h) }

// PUT registers h for PUT requests to path.
func (g *Group) PUT(path string, h HandlerFunc) { g.Handle(http.MethodPut, path, h) }

// PATCH registers h for PATCH requests to path.
func (g *Group) PATCH(path string, h HandlerFunc) { g.Handle(http.MethodPatch, path, h) }

// DELETE registers h for DELETE requests to path.
func (g *Group) DELETE(path string, h HandlerFunc) { g.Handle(http.MethodDelete, path, h) }
