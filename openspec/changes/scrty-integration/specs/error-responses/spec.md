# Spec Delta

## ADDED Requirements

### Requirement: srvr owns the problem namespace
A server SHALL take its problem namespace from `Config.ProblemNamespace` or `WithProblemNamespace(uri)` and apply it to its prblm handler before the consumer's `WithProblemOptions`, with every engine and with `WithErrorRenderer` (for `DefaultErrorRenderer`). The default SHALL be empty, changing no answer. An invalid namespace SHALL fail construction with an error wrapping `srvr.ErrConfig` naming `ProblemNamespace` and the value.

#### Scenario: Problem namespace
- **Given** a server on `<engine>` built with `<options>`
- **When** it is constructed and a handler returns an error of relative problem type `demo/quota`
- **Then** `<outcome>`

##### Examples:
| engine | options | outcome |
|--------|---------|---------|
| net/http | (none) | the problem's `type` is `demo/quota` (unchanged from v0.4.0) |
| net/http | `WithProblemNamespace("https://errors.example/p")` | the problem's `type` is `https://errors.example/p/demo/quota` |
| gin | `WithConfig(Config{ProblemNamespace: "/p"})` | the problem's `type` is `/p/demo/quota` |
| fiber v3 | `WithProblemNamespace("https://errors.example/p")` | the problem's `type` is `https://errors.example/p/demo/quota` |
| net/http | `WithProblemNamespace("https://errors.example/p")`, `WithProblemOptions(prblm.WithNamespace("https://other.example"))` | the problem's `type` is `https://other.example/demo/quota` (the consumer's prblm option is applied later) |
| net/http | `WithProblemNamespace("not a uri")` | construction returns an error wrapping `srvr.ErrConfig` naming `ProblemNamespace` and `not a uri` |
| net/http | `WithProblemNamespace("https://errors.example/p")`, `WithErrorRenderer(R)` | a server |

### Requirement: Middleware renders errors through the server
`srvr.WriteError(w, r, err)` SHALL answer `err` through the server handling `r` (its renderer, or its prblm handler, with every safety rule of error rendering) and return `true`. Outside a srvr request it SHALL write nothing and return `false`.

#### Scenario: WriteError
- **Given** `<where>`
- **When** middleware calls `srvr.WriteError(w, r, prblm.ErrConflict)`
- **Then** it returns `<result>` and the response is `<response>`

##### Examples:
| where | result | response |
|-------|--------|----------|
| a net/http srvr server | `true` | a `409` problem with `trace_id`, and one `http.request.failed` record |
| a net/http srvr server with `WithErrorRenderer(R)` | `true` | `R`'s `409` answer |
| a plain `http.Server` | `false` | nothing written by srvr |
| a net/http srvr server, after the handler wrote `200` | `true` | the `200` already sent; the failure recorded as committed |
