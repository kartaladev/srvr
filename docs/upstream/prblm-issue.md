# Draft issue for kartaladev/prblm

**Title:** Composable `OnError` hooks and an exported sentinel for ginprblm status-only aborts

**Labels:** enhancement

---

## Context

`github.com/kartaladev/srvr` is an HTTP server library that renders every error through prblm on net/http, Gin and Fiber. srvr writes one structured access record per request, so it has to see every error that prblm renders. Two parts of prblm v0.1.0 make this harder than it needs to be.

## 1. `OnError` holds a single hook

`prblm.OnError` replaces the hook rather than adding to it (`options.go:62-66`):

```go
func OnError(hook func(ctx context.Context, err error, p *Problem)) Option {
	return func(h *Handler) {
		h.onError = hook
	}
}
```

When a library and its consumer both pass `OnError`, the last one silently wins. srvr works around this by owning the hook and forwarding to a consumer hook given through its own `srvr.WithErrorHook`. Every library that wraps prblm would have to do the same.

**Request:** any of these would remove the workaround:
- make repeated `OnError` options compose, called in registration order; or
- add `AddOnError(hook)` and keep `OnError` as "replace"; or
- document that `OnError` replaces, so wrappers know to own it.

The first option changes behaviour for anyone who relies on "last wins". The second is purely additive.

## 2. ginprblm's status-only error is unexported

For `c.AbortWithStatus(401)` with no `c.Error`, ginprblm renders a problem and calls the hook with `errStatusOnly` (`ginprblm/middleware.go:131-134`). It is unexported, so a hook cannot tell this case apart from a real handler error with `errors.Is`.

Reproduction (gin v1.12.0, prblm v0.1.0):

```go
func TestHookOnStatusOnly(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	e.Use(ginprblm.Middleware(prblm.OnError(func(ctx context.Context, err error, p *prblm.Problem) {
		fmt.Printf("OnError called: err=%q type=%T\n", err, err)
	})))
	e.GET("/x", func(c *gin.Context) { c.AbortWithStatus(401) })
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	fmt.Println("status", w.Code, w.Body.String())
}
```

```
OnError called: err="ginprblm: response aborted with an error status and no error" type=*prblm.statusError
status 401 {"title":"Unauthorized","status":401,"instance":"/x"}
```

srvr wants to log a status-only abort as an ordinary response, and a handler error as a failure. Today it has to classify from `c.Errors` instead of from the hook.

**Request:** export the sentinel, for example `var ErrStatusOnly = errors.New(...)`, so `errors.Is(err, ginprblm.ErrStatusOnly)` works. This is additive.

## Workarounds in srvr until then

- srvr owns `OnError` and exposes `srvr.WithErrorHook`.
- srvr classifies Gin failures from `c.Errors` and recovered panics.

Both can be removed once these land.
