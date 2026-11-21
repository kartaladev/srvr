package engine

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/kartaladev/prblm"
	"github.com/kartaladev/srvr/internal/errchain"
)

// answerMapper is the prblm mapper srvr adds after the consumer's mappers
// (see [Core.ProblemOptions]): it answers srvr's own errors (design D3)
// with the problems srvr sent before they existed, so the default
// rendering is unchanged. A consumer mapper on them runs first and wins.
var answerMapper prblm.Mapper = prblm.MapperFunc(answerProblem)

// answerProblem returns the problem of srvr's own answer err, or nil for
// any other error, which prblm resolves as usual. ErrNotFound needs no
// case: it matches prblm.ErrNotFound, which prblm's built-ins answer.
func answerProblem(_ context.Context, err error) *prblm.Problem {
	if mna, ok := errors.AsType[*errchain.MethodNotAllowedError](err); ok {
		p := prblm.New(http.StatusMethodNotAllowed)
		if mna != nil && len(mna.Allow) > 0 { // a typed nil lists no method
			p.Header = http.Header{"Allow": {strings.Join(mna.Allow, allowSeparator)}}
		}

		return p
	}

	switch {
	case errors.Is(err, errchain.ErrRequestTooLarge):
		// prblm's built-in answer to *http.MaxBytesError.
		return &prblm.Problem{Status: http.StatusRequestEntityTooLarge, Detail: "The request body is too large."}
	case errors.Is(err, errchain.ErrHeadersTooLarge):
		return prblm.New(http.StatusRequestHeaderFieldsTooLarge)
	case errors.Is(err, errchain.ErrRequestTimeout):
		return prblm.New(http.StatusRequestTimeout)
	default:
		return nil
	}
}

// allowSeparator joins the methods of an Allow header, as ServeMux does.
const allowSeparator = ", "

// MethodNotAllowed returns the error of a 405 answer whose Allow header
// would be allow, a sorted list joined with ", " as ServeMux and static
// mounts build it; an empty allow lists no method.
func MethodNotAllowed(allow string) error {
	var methods []string
	if allow != "" {
		methods = strings.Split(allow, allowSeparator)
	}

	return &errchain.MethodNotAllowedError{Allow: methods}
}
