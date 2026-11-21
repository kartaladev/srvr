package engine

import (
	"context"

	"github.com/kartaladev/srvr/internal/errchain"
	"github.com/kartaladev/srvr/internal/static"
)

// StaticFailure returns the error an engine's handler answers res with, for
// every answer but static.StaticFile, which the engine serves itself: for
// static.StaticNotFound errchain.ErrNotFound and for static.StaticMethodNotAllowed the 405
// error of [MethodNotAllowed] with Allow header allow, both after marking the request of ctx
// as answered by the router (static-files design D5), so neither has a
// route; for static.StaticFailed the file system's error.
func StaticFailure(ctx context.Context, res static.Result, allow string) error {
	switch res.Answer {
	case static.NotFound:
		MarkRouterAnswered(ctx)

		return errchain.ErrNotFound
	case static.MethodNotAllowed:
		MarkRouterAnswered(ctx)

		return MethodNotAllowed(allow)
	default:
		return res.Err
	}
}
