# Spec Delta

## MODIFIED Requirements

### Requirement: Gin middleware order is preserved around srvr's pipeline
srvr's pipeline (trace context, access record, recovery, problem rendering) SHALL run outside every consumer middleware, so a consumer middleware that aborts or writes still produces a problem for an error and exactly one access record.

#### Scenario: Consumer middleware behaviour
- **Given** a Gin group middleware that `<behaviour>` before route `GET /x`
- **When** a client sends `GET /x`
- **Then** the response status is `<status>`
- **And** exactly one access record is written with `msg` `<msg>`

##### Examples:
| behaviour | status | msg |
|-----------|--------|-----|
| calls `c.Next()` | `200` | `http.request.completed` |
| calls `c.AbortWithStatus(401)` | `401` | `http.request.completed` |
| calls `c.AbortWithError(401, errAuth)` | `401` | `http.request.failed` |
| panics | `500` | `http.request.failed` |
| writes `200` then calls `c.Error(errors.New("late"))` | `200` | `http.request.failed` |
| calls `c.AbortWithStatus(429)` | `429` | `http.request.completed` |
| calls `c.AbortWithStatus(503)` | `503` | `http.request.completed` |
| sets 401 and aborts without writing a body (`c.Status(401)`, then `c.Abort()`) | `401` | `http.request.completed` |
| calls `c.Error(errAuth)` and then `c.AbortWithStatus(401)` | `401` | `http.request.failed` |
| calls `c.AbortWithError(503, errors.New("db down"))` | `503` | `http.request.failed` |
