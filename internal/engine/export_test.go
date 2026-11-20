package engine

import (
	"log/slog"
	"net/http"
	"runtime/debug"
)

// HTTPServerOf returns the http.Server the net/http runner builds for h
// with the limits of c.
func HTTPServerOf(c Config, h http.Handler) *http.Server { return c.httpServer(h) }

// ServiceAttrs returns the service attributes of c, reading build
// information with read.
func ServiceAttrs(c *Core, read func() (*debug.BuildInfo, bool)) []slog.Attr {
	return c.settings.Service.attrs(read)
}
