package engine

import (
	"context"
	"errors"
	"net/http"

	"github.com/kartaladev/prblm"
)

// statusClientClosedRequest is the status srvr answers a request with when
// its handler returns context.Canceled after the client went away: 499
// "Client Closed Request", the convention of nginx, grpc-gateway and
// Google AIP-193. No client reads it; it keeps a disconnect out of the 5xx
// counts, so it neither pages as a server error nor flushes a debug buffer.
const statusClientClosedRequest = 499

// builtinResolver resolves an error with prblm's built-in mappers and
// status hints only, so [clientClosed] can tell whether they would decide.
var builtinResolver = prblm.NewHandler(prblm.WithoutDiscovery())

// clientClosedMapper is the prblm mapper srvr adds after the consumer's
// mappers (see [Core.ProblemOptions]).
var clientClosedMapper prblm.Mapper = prblm.MapperFunc(clientClosed)

// clientClosed maps an error matching context.Canceled to a 499 problem
// when the request context ctx is cancelled too: the client went away (or
// a forced drain closed its connection), so nothing the server sends
// reaches it. It returns nil, leaving the error to prblm, when ctx is
// still live (a server-side cancellation is a bug and stays a 500) or
// ended by its deadline (a server-side timeout, not the client), and
// when prblm's built-in mappers or a prblm.WithStatus hint would answer
// with another status than the 500 fallback, such as a sentinel wrapping
// the cancellation. The consumer's own mappers, scoped mappers and
// describers run before it and always win. A hint of exactly 500 cannot be
// told from the fallback and is answered with 499. The probe always asks
// prblm's default built-ins, so with prblm.WithoutBuiltins a sentinel
// wrapping the cancellation still makes it decline, leaving a 500.
//
// On fiber the request context is never cancelled by a disconnect
// (fasthttp does not detect one), so there a disconnect stays a 500.
func clientClosed(ctx context.Context, err error) *prblm.Problem {
	if ctx == nil || !errors.Is(err, context.Canceled) || !errors.Is(ctx.Err(), context.Canceled) {
		return nil
	}

	// The probe must not see the request's scoped mappers, which already
	// declined, nor run a consumer enricher: it only asks prblm's built-ins.
	if builtinResolver.Resolve(context.Background(), err).Status != http.StatusInternalServerError { //nolint:contextcheck // see above
		return nil
	}

	return &prblm.Problem{Status: statusClientClosedRequest, Title: "Client Closed Request"}
}
