# Draft issue for kartaladev/scrty

**Title:** Integration hooks for host libraries: Bearer challenge, resolved client on refusals, error-handler accessor, ginsec unrouted 404

**Labels:** enhancement

You could split this into four issues. The items are independent, and they are ordered by impact.

---

## Context

`github.com/kartaladev/srvr` is an HTTP server library that wraps scrty v0.1.1 `httpsec` (and `ginsec`, `fibersec`). It renders refusals as RFC 9457 problems through prblm and writes one structured access record per request. The probes below ran against v0.1.1 (tag `38c939c`) with Go 1.27.

## 1. A 401 without credentials carries no `WWW-Authenticate` (affects clients)

RFC 9110 section 15.5.2 requires a server answering 401 to send at least one challenge in `WWW-Authenticate`. RFC 6750 section 3 defines the `Bearer` challenge. scrty sends one only when a failing `Authorization: Basic` header was presented, or when DPoP is enabled. `httpsec/bearer.go` documents this: "It writes no WWW-Authenticate header itself."

Reproduction:

```go
chain, _ := httpsec.New(
	httpsec.EnableBasicAuth(httpsec.BasicAuthDeps{Authenticator: failAuthn{}, Attempts: attempts{}},
		httpsec.WithBasicAuthRealm("orders")),
	httpsec.EnableAuthorization(az{}, authorize.Rule[httpsec.Request]{
		Match: func(httpsec.Request) bool { return true }, Require: authorize.Authenticated()}),
)
h := chain.Middleware()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
// GET /orders, first with no Authorization header, then with a wrong Basic one
```

```
Authorization=""                   -> status 401 WWW-Authenticate=""
Authorization="Basic Ym9iOndyb25n" -> status 401 WWW-Authenticate="Basic realm=\"orders\""
```

**Request:** on every 401 the chain answers, emit the challenges for the schemes it has enabled (`Basic realm=...`, `Bearer`, `DPoP`), whether or not credentials were presented. The option to remove them through `WithErrorHandler` can stay.

A host library cannot fix this safely from outside, because only scrty knows which schemes and realms are enabled.

## 2. The resolved client is invisible on refused requests

scrty resolves the real client (trusted proxies, `X-Forwarded-For`) inside the chain and publishes it with `ClientFromContext` only on the context passed downstream. For a refused request, neither the outer middleware nor the `WithErrorHandler` request sees it.

```go
// outer middleware -> chain -> handler; error handler records its *http.Request
status=200 client in outer ctx=false, in handler ctx=true  (handler ran=true),  in error-handler ctx=false (ran=false)
status=401 client in outer ctx=false, in handler ctx=false (handler ran=false), in error-handler ctx=false (ran=true)
```

Refused requests are when an operator most needs the real client address. Behind a proxy, a host's access log can only see the proxy's address, so srvr omits `client.address` on refusals and sets `srvr.client.unresolved`.

**Request:** either pass the error handler a request whose context carries the resolved client (`ClientFromContext` works there), or add `httpsec.ResolveClient(r *http.Request) (Client, bool)` using the chain's configured trust settings.

## 3. Report whether a chain has an error handler

A host that renders refusals itself depends on the consumer passing its options (srvr's `bridge.ChainOptions()`) to `httpsec.New`. If they don't, refusals come out as scrty's bare responses. srvr can only warn at `Start` when `ChainOptions()` was never called. It cannot detect a consumer who passed their own options but left out the error handler.

**Request:** a read-only accessor, for example `func (c *Chain) HasErrorHandler() bool`.

## 4. ginsec commits an empty 404 on unrouted paths

`ginsec.refuse` (`httpsec/ginsec/middleware.go:154-166`) writes the header immediately for a 404 refusal on a path Gin matched no route for. The doc comment explains why: it keeps Gin's default `404 page not found` body from appearing, so the three adapters answer alike. The cost is that a consumer's error middleware cannot render this case at all.

Reproduction (gin v1.12.0):

```go
func problemRenderer(c *gin.Context) {
	c.Next()
	if len(c.Errors) == 0 || c.Writer.Written() {
		return
	}
	status := c.Writer.Status()
	c.Header("Content-Type", "application/problem+json")
	c.String(status, `{"status":%d,"title":%q}`, status, http.StatusText(status))
}

chain, _ := httpsec.New(httpsec.RegisterInterceptor(
	httpsec.InterceptorFunc(func(*httpsec.Exchange, httpsec.Next) error { return oidc.ErrUnknownProvider }),
	httpsec.OrderAuthorizer))
eng := gin.New()
eng.Use(problemRenderer, ginsec.Middleware(chain))
eng.GET("/routed/:p", func(c *gin.Context) {})
```

```
GET /routed/nope     -> 404 content-type="application/problem+json" body="{\"status\":404,\"title\":\"Not Found\"}"
GET /sso/login/nope  -> 404 content-type="" body=""
```

**Request:** an option to leave that 404 uncommitted when the consumer has an error middleware that renders it, for example `ginsec.WithUnroutedRefusalsUncommitted()`. The default could stay as it is.

## Workarounds in srvr until then

| Item | srvr's workaround |
|---|---|
| 1 | None. srvr passes scrty's response through. |
| 2 | Omits `client.address` on refusals and logs `srvr.client.unresolved: true`. |
| 3 | Warns at `Start` when `ChainOptions()` was never called. |
| 4 | Documents a Gin parity exception. |
