# Spec Delta

## MODIFIED Requirements

### Requirement: Records include what the client saw
A failed-request record SHALL carry a `problem` group with the `status` actually sent and, when the renderer gave them, its `type` and `title` (prblm, the default, always gives both), or `srvr.response.committed` `true` with no `problem` when the response had already started. When srvr replaced a faulty renderer's answer with its safe `500`, the record SHALL carry `srvr.renderer.failure` naming the fault.

#### Scenario: Problem group
- **Given** a handler that `<behaviour>`
- **When** the request fails
- **Then** the record has `<problem>`

##### Examples:
| behaviour | problem |
|-----------|---------|
| returns `prblm.ErrConflict` | `problem.status` `409`, `problem.type` `about:blank`, `problem.title` `Conflict` |
| returns `errors.New("boom")` | `problem.status` `500`, `problem.title` `Internal Server Error` |
| writes `200` then returns `errors.New("late")` | no `problem` and `srvr.response.committed` `true` |
| writes `200` then panics with `"late"` | no `problem`, `srvr.response.committed` `true`, `error.kind` `panic` and `level` `ERROR` |
| returns `ErrQuota` with `WithErrorRenderer(R)`, `R` answering `429` with `Type` and `Title` empty | `problem.status` `429`, no `problem.type`, no `problem.title`, no `srvr.renderer.failure` |
| returns `ErrQuota` with `WithErrorRenderer(R)`, `R` answering `429` with `Type` `https://errors.example/quota` and `Title` `Quota exceeded` | `problem.status` `429`, `problem.type` `https://errors.example/quota`, `problem.title` `Quota exceeded` |
| returns `ErrQuota` with `WithErrorRenderer(R)`, `R` setting a `Title` of 10 000 bytes with a newline and an ANSI escape | `problem.title` cut to the log's `MaxAttrBytes` with srvr's truncation marker and its control characters escaped, as for every logged value |
| returns `errors.New("boom")` with `WithErrorRenderer(F)`, `F` panicking with `"secret token"` | `problem.status` `500`, `problem.type` `about:blank`, `problem.title` `Internal Server Error`, `srvr.renderer.failure` `panic`, the original error's `error.chain`; the panic value is not logged |
