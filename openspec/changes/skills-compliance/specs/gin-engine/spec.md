# Spec Delta

## MODIFIED Requirements

### Requirement: Gin engine customisation cannot remove srvr's safety settings
srvr SHALL set Gin's trusted proxies to none (gin trusts every proxy by default), then run `ginsrvr.WithGinEngine(func(*gin.Engine))` before installing its pipeline, then re-assert `ContextWithFallback`, `HandleMethodNotAllowed`, `RedirectTrailingSlash` off and its recovery. A hook that registers routes or middleware SHALL be rejected, because they would run outside srvr's pipeline.

#### Scenario: Engine hook
- **Given** a `ginsrvr` server whose `WithGinEngine` hook `<hook>`
- **When** the server is constructed, started and handles a request
- **Then** the effective result is `<result>`

##### Examples:
| hook | result |
|------|--------|
| sets `e.ContextWithFallback = false` | `ContextWithFallback` is `true` |
| sets `e.HandleMethodNotAllowed = false` | `DELETE /users` returns `405` |
| sets `e.RedirectTrailingSlash = true` | `GET /users/` returns a `404` problem |
| calls `e.SetTrustedProxies([]string{"10.0.0.0/8"})` | `c.ClientIP()` honours `X-Forwarded-For` from `10.0.0.1` |
| (none) | `c.ClientIP()` ignores `X-Forwarded-For` |
| `nil` | construction returns an error wrapping `srvr.ErrConfig` |
| calls `e.Use(gin.Logger())` | construction returns an error wrapping `srvr.ErrConfig` naming the hook |
| calls `e.GET("/x", h)` | construction returns an error wrapping `srvr.ErrConfig` naming the hook |
| panics | construction returns an error wrapping `srvr.ErrConfig` containing the panic value |
