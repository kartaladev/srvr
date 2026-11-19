# Spec Delta

## MODIFIED Requirements

### Requirement: Parity exceptions are explicit
Where an engine cannot match, the difference SHALL be listed here, documented in the engine's godoc, and covered by a test that pins the engine's actual behaviour.

#### Scenario: Documented differences
- **Given** engine `<engine>`
- **When** `<situation>`
- **Then** the engine's behaviour is `<behaviour>`

##### Examples:
| engine | situation | behaviour |
|--------|-----------|-----------|
| `gin` | a handler writes a success body and later calls `c.Error` | the success response is kept; the record has `srvr.response.committed` `true` |
| `fiber` | a handler sets a buffered body and then returns an error | the body is replaced by the problem (Fiber buffers responses); `srvr.response.committed` is `false` |
| `fiber` | a streaming body has started and an error follows | the stream is kept; the record has `srvr.response.committed` `true` |
| `fiber` | a handler returns `c.SendStream(bytes.NewReader([]byte("hello")))`, a stream without a declared length | the client gets `hello`; `http.response.body.size` is `0` (fiber logs a stream's declared `Content-Length`, and this stream has none; net/http and gin log the bytes written) |
| `net/http` | a client disconnects before the error is rendered | the problem write is attempted and dropped |
| `gin` | a client disconnects before the error is rendered | nothing is written |
| `fiber` | a client disconnects mid-request | the handler is not notified (fasthttp does not detect it) |
| `fiber` | a client disconnects and the handler then returns `fmt.Errorf("query: %w", context.Canceled)` | a `500` problem and an `ERROR` record, where net/http and gin answer `499` at `WARN`: the request context is not cancelled by the disconnect, so srvr cannot tell it from a server-side cancellation |
| `gin` | `HEAD /users` where only `GET /users` is registered | a `405` problem with `Allow` `GET` (gin does not route `HEAD` to `GET` handlers) |
| `gin` | wrong method where `GET` and `POST` are registered | `Allow` is `GET, POST` without `HEAD`, because gin does not route `HEAD` to `GET` handlers; net/http and fiber send `GET, HEAD, POST` |
| `fiber` | a `4194305`-byte body is sent to a handler that never reads it | a `413` problem before the handler runs (fasthttp reads the whole body before routing); the record has no `http.route`, while net/http and gin record the route because their limit trips when the handler reads |
| `fiber` | a client sends method `FOO` to a registered path | a plain-text `501` `Not Implemented`, no problem and no access record (fiber answers before any middleware; accepted exception) |
| `fiber` | `MaxHeaderBytes` is above `65536` and a request carries `70000` bytes of headers | a `431` problem, because fiber's header buffer is capped at `65536` bytes per connection; net/http accepts it |
| `fiber` | a handler panics with `http.ErrAbortHandler` | a `500` problem and an `ERROR` record with `error.kind` `panic`; no aborted connection and no `srvr.response.aborted` (fasthttp has no abort sentinel and does not recover a re-raised panic) |
| `gin` | a handler calls `c.Error(e)` where `e.Unwrap()` returns `e` | the request does not complete: gin's `c.Error` calls `errors.As` on `e` before srvr sees it and never returns, so no response and no access record are written (net/http and fiber answer a `500` problem); `ginsrvr` godoc warns against it (owner decision 2026-11-18, documented only) |
| `fiber` | `ReadHeaderTimeout` is set | it is not applied (fasthttp has no header-only timeout); `ReadTimeout` bounds reading the headers and body together |
| `gin` | `Static("/", site)` and a `POST /api/users` route, and a client sends `PUT /api/users` (any method other than `GET` and `HEAD`) | a `405` problem with `Allow: POST`: gin serves static files from its no-route and no-method chains and answers methods other than `GET` and `HEAD` there with its own `405`; net/http and fiber send `Allow: GET, HEAD, POST`. A `GET` or `HEAD` there is the static mount's on every engine |
| `fiber` | a consumer `Get("/*")` route and `Static("/", site)` | `Start` fails with an error wrapping `srvr.ErrRouteConfig` naming the duplicate route, because fiber matches routes in registration order and cannot let the earlier catch-all win; on net/http (`GET /{path...}`) and gin (`GET /*any`) both start and the consumer's catch-all answers every `GET` |
| `net/http` | `Static("/assets", site)` and a client sends `GET /assets/../secret.txt` or `GET /assets//app.js` as a raw request target | a `307` redirect to the cleaned path (`/secret.txt`, `/assets/app.js`), ServeMux's own answer before any route runs; gin answers `404` and fiber routes the path with its dot segments removed, and no engine serves a file outside the file system |
| `fiber` | a static mount's `StaticConfig.FiberSendFileCache` is positive (`fibersrvr.WithSendFileCache(d)`, opt-in) | fiber's open-file cache serves a file replaced on disk stale for up to `d`, and the mount's cache cleaner goroutine outlives `Shutdown`; net/http and gin have no file cache and fail `Start` with a `*srvr.ConfigError` naming `Static.FiberSendFileCache` (static-files: Rejected file cache configuration) |
