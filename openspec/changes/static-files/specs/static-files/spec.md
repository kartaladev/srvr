# Spec Delta

## Purpose

Serves the files of an `fs.FS`, such as a single-page-app build directory or an embedded site, under a URL prefix on the net/http, Gin and Fiber v3 engines, with one programming model, safe defaults and the same observable behaviour on every engine. Examples name files of `site`, the test file tree defined in the `static-files` change's design ("Test fixture"); "every engine" means one test case run on `net/http`, `gin` and `fiber`, each with its own `Static`.

## ADDED Requirements

### Requirement: Files of a file system are served under a prefix
A static configurer SHALL answer `GET` and `HEAD` below its prefix with the regular file of that name in its `fs.FS`: status `200`, `Content-Type` from the extension (sniffed without one), `Content-Length`, and `Last-Modified` when the file has a modification time. `HEAD` gets the same headers without a body. A directory is answered by its index file, with or without the trailing slash and without a redirect. No engine sends an `ETag`.

#### Scenario: Serving a file
- **Given** a server on every engine with `Static("<prefix>", site)`
- **When** a client sends `<request>`
- **Then** the response status is `<status>` with `Content-Type` `<type>` and body `<body>`
- **And** the response has no `ETag` and no `Location` header

##### Examples: served
| prefix | request | status | type | body |
|--------|---------|--------|------|------|
| `/assets` | `GET /assets/app.js` | `200` | `text/javascript; charset=utf-8` | `console.log(1)` |
| `/assets` | `HEAD /assets/app.js` | `200` | `text/javascript; charset=utf-8` | (empty), with `Content-Length` `14` |
| `/assets` | `GET /assets/style.css` | `200` | `text/css; charset=utf-8` | the file |
| `/assets` | `GET /assets/logo.svg` | `200` | `image/svg+xml` | the file |
| `/assets` | `GET /assets/data.json` | `200` | `application/json` | the file |
| `/assets` | `GET /assets/notes` | `200` | `text/plain; charset=utf-8` (sniffed) | the file |
| `/assets` | `GET /assets/caf%C3%A9.txt` | `200` | `text/plain; charset=utf-8` | the file `café.txt` |
| `/assets` | `GET /assets/a%2Fb.txt` (an encoded slash decodes to a path inside the file system) | `200` | `text/plain; charset=utf-8` | the file `a/b.txt` |
| `/assets` | `GET /assets/` | `200` | `text/html; charset=utf-8` | `<html>app</html>` |
| `/assets` | `GET /assets` (the prefix without a trailing slash) | `200` | `text/html; charset=utf-8` | `<html>app</html>` |
| `/assets` | `GET /assets/index.html` | `200` | `text/html; charset=utf-8` | `<html>app</html>` (no redirect to `/assets/`) |
| `/assets` | `GET /assets/docs` | `200` | `text/html; charset=utf-8` | `docs/index.html` |
| `/assets` | `GET /assets/docs/` | `200` | `text/html; charset=utf-8` | `docs/index.html` |
| `/assets/` (trailing slash, same as `/assets`) | `GET /assets/app.js` | `200` | `text/javascript; charset=utf-8` | `console.log(1)` |
| `/` | `GET /` | `200` | `text/html; charset=utf-8` | `<html>app</html>` |
| `/` | `GET /docs/guide.html` | `200` | `text/html; charset=utf-8` | the file |
| `/` with `site` an `embed.FS` | `GET /app.js` | `200` | `text/javascript; charset=utf-8` | `console.log(1)`, without `Last-Modified` (embedded files have no modification time) |

#### Scenario: Conditional and range requests
- **Given** a server on every engine with `Static("/assets", site)`, `site` an `<fs>` whose `app.js` was modified at `Wed, 18 Nov 2026 10:00:00 GMT` unless it is an `embed.FS`
- **When** a client sends `GET /assets/app.js` with `<headers>`
- **Then** the response status is `<status>` and `<outcome>`

##### Examples:
| fs | headers | status | outcome |
|----|---------|--------|---------|
| `os.DirFS` | `If-Modified-Since: Wed, 18 Nov 2026 10:00:00 GMT` | `304` | no body; `Last-Modified` `Wed, 18 Nov 2026 10:00:00 GMT` |
| `os.DirFS` | `If-Modified-Since: Tue, 17 Nov 2026 10:00:00 GMT` | `200` | the whole file |
| `os.DirFS` | `If-Modified-Since: not a date` | `200` | the whole file |
| `embed.FS` | `If-Modified-Since: Wed, 18 Nov 2026 10:00:00 GMT` | `200` | the whole file, no `Last-Modified` (a file without a modification time is never "not modified") |
| `os.DirFS` | `Range: bytes=0-3` | `200` | the whole file, no `Content-Range` (byte ranges are not served yet) |
| `os.DirFS` | `Range: bytes=999-` | `200` | the whole file, never `416` |
| `os.DirFS` | `If-Match: "x"` | `200` | the whole file, never `412` |
| `os.DirFS` | `If-Unmodified-Since: Mon, 16 Nov 2026 10:00:00 GMT` | `200` | the whole file, never `412` |
| `os.DirFS` | `If-None-Match: *` | `200` | the whole file (no `ETag` to match) |

#### Scenario: Partial failure while serving
- **Given** a server on engine `<engine>` with `Static("/assets", site)` and a `1 MiB` file `big.bin`
- **When** `<event>`
- **Then** `<outcome>`

##### Examples:
| engine | event | outcome |
|--------|-------|---------|
| `net/http` | the client closes the connection after reading `64 KiB` of `GET /assets/big.bin` | one `http.request.completed` record with status `200`; the server keeps serving |
| `gin` | the client closes the connection after reading `64 KiB` of `GET /assets/big.bin` | one `http.request.completed` record with status `200`; the server keeps serving |
| `fiber` | the client closes the connection after reading `64 KiB` of `GET /assets/big.bin` | one `http.request.completed` record with status `200`; the server keeps serving |
| `net/http` | 50 concurrent clients request `GET /assets/big.bin` and `GET /assets/app.js` | every response is `200` with the whole file; the test passes under `-race` |
| `gin` | 50 concurrent clients request `GET /assets/big.bin` and `GET /assets/app.js` | every response is `200` with the whole file; the test passes under `-race` |
| `fiber` | 50 concurrent clients request `GET /assets/big.bin` and `GET /assets/app.js` | every response is `200` with the whole file; the test passes under `-race` |

### Requirement: Static configuration is programmatic and by config struct
A static configurer SHALL take `StaticOption` values applied in argument order and a plain `StaticConfig` (`json`/`yaml`/`env` tags) through `WithStaticConfig`, a zero field meaning its default. A later option wins over an earlier one. The configurer SHALL copy its configuration when it is created, so a caller changing a slice or struct afterwards changes nothing.

#### Scenario: Configuration sources
- **Given** a server on every engine with `Static("/", site, <options>)`
- **When** a client sends `<request>`
- **Then** `<outcome>`

##### Examples:
| options | request | outcome |
|---------|---------|---------|
| (none) | `GET /app.js` | no `Cache-Control` header |
| `WithStaticCacheControl("public, max-age=31536000, immutable")` | `GET /app.js` | `Cache-Control: public, max-age=31536000, immutable` |
| `WithStaticConfig(StaticConfig{CacheControl: "public, max-age=60"})` | `GET /app.js` | `Cache-Control: public, max-age=60` |
| `WithStaticConfig(StaticConfig{CacheControl: "public, max-age=60"})`, then `WithStaticCacheControl("no-store")` | `GET /app.js` | `Cache-Control: no-store` |
| `WithStaticCacheControl("no-store")`, then `WithStaticConfig(StaticConfig{})` | `GET /app.js` | no `Cache-Control` header (the config struct replaces every field) |
| `WithStaticIndexNames("home.html")` with `home.html` in `site` | `GET /` | `200` with `home.html` and `Cache-Control: no-cache` |
| `WithStaticConfig(StaticConfig{IndexNames: []string{"home.html"}})` with `home.html` in `site` | `GET /` | `200` with `home.html` |
| `WithStaticIndexNames("home.html", "index.html")` without `home.html` in `site` | `GET /` | `200` with `index.html` (first existing name wins) |
| `WithStaticConfig(c)` where `c.IndexNames` is changed to `[]string{"x.html"}` after `Static` returned | `GET /` | `200` with `index.html` |
| a `StaticConfig` decoded from the JSON `{"spa_fallback":"index.html","cache_control":"public, max-age=60","index_cache_control":"no-store","index_names":["index.html"]}` | `GET /dashboard` with `Accept: text/html` | `200` with `index.html` and `Cache-Control: no-store` |

### Requirement: Invalid static configuration fails Start
A static configurer with invalid configuration SHALL make `Start` return, without binding the listener, an error wrapping `srvr.ErrRouteConfig` and a `*srvr.ConfigError` whose `Field` names the setting and whose `Value` holds the offending value, on every engine. Checks that read the file system run under recover, so a file system that panics fails `Start` instead of crashing it.

#### Scenario: Rejected static configuration
- **Given** a server on every engine with `<configurer>` registered
- **When** the server starts
- **Then** `Start` returns an error wrapping `srvr.ErrRouteConfig` whose `*srvr.ConfigError` has `Field` `<field>`, `Ready()` is never closed and a later `Shutdown` returns `nil`

##### Examples:
| configurer | field |
|------------|-------|
| `Static("/", nil)` | `Static.FS` |
| `Static("/", fsys)`, `fsys` a typed-nil `*embed.FS` | `Static.FS` |
| `Static("", site)` | `Static.Prefix` |
| `Static("assets", site)` | `Static.Prefix` |
| `Static("/assets/{name}", site)` | `Static.Prefix` |
| `Static("/assets/*", site)` | `Static.Prefix` |
| `Static("/assets/:id", site)` | `Static.Prefix` |
| `Static("/a/../b", site)` | `Static.Prefix` |
| `Static("/a//b", site)` | `Static.Prefix` |
| `Static("/a\x00", site)` | `Static.Prefix` |
| `Static("/", site, nil)` | `StaticOption` |
| `Static("/", site, WithSPAFallback("missing.html"))` | `Static.SPAFallback` |
| `Static("/", site, WithSPAFallback("docs"))` (a directory) | `Static.SPAFallback` |
| `Static("/", site, WithSPAFallback("../index.html"))` | `Static.SPAFallback` |
| `Static("/", site, WithSPAFallback("/index.html"))` | `Static.SPAFallback` |
| `Static("/", site, WithStaticCacheControl("max-age=60\r\nSet-Cookie: a=b"))` | `Static.CacheControl` |
| `Static("/", site, WithStaticIndexCacheControl("no-cache\x00"))` | `Static.IndexCacheControl` |
| `Static("/", site, WithStaticIndexNames(""))` | `Static.IndexNames` |
| `Static("/", site, WithStaticIndexNames("a/index.html"))` | `Static.IndexNames` |
| `Static("/", site, WithStaticIndexNames(".."))` | `Static.IndexNames` |
| `Static("/", fsys, WithSPAFallback("index.html"))`, `fsys.Open` panics with `"boom"` | `Static.SPAFallback` |
| `Static("/", fsys, WithSPAFallback("index.html"))`, `fsys.Open` returns `fs.ErrPermission` | `Static.SPAFallback` |
| `Static("/assets", site)` registered twice | `Static.Prefix` (the second mount of the same prefix) |
| `Static("/", site)` and `Static("/", other)` | `Static.Prefix` |

#### Scenario: Several invalid settings
- **Given** a server on every engine with `Static("assets", nil, WithStaticCacheControl("a\nb"))` registered
- **When** the server starts
- **Then** `Start` returns one error naming `<fields>`

##### Examples:
| fields |
|--------|
| `Static.Prefix`, `Static.FS` and `Static.CacheControl` |

#### Scenario: Static configurer lifecycle misuse
- **Given** a server on engine `<engine>`
- **When** `<misuse>`
- **Then** `<result>`

##### Examples:
| engine | misuse | result |
|--------|--------|--------|
| `net/http` | `Register(Static("/", site))` after `Start` | an error matching `srvr.ErrServerStarted`; no route is added |
| `gin` | `Register(ginsrvr.Static("/", site))` after `Shutdown` | an error matching `srvr.ErrServerClosed` |
| `fiber` | `Register(fibersrvr.Static("/", site))` after `Start` | an error matching `srvr.ErrServerStarted`; no route is added |
| `gin` | a consumer configurer calls `ginsrvr.Static("/", site).Configure(group)` itself | `Start` returns an error wrapping `srvr.ErrRouteConfig` naming the consumer's configurer type and saying that `ginsrvr.Static` is registered with `Server.Register` |
| `net/http` | a consumer configurer calls `srvr.Static("/files", site).Configure(g.Group("/v1"))` itself | `GET /v1/files/app.js` is `200` (net/http joins the group prefix) |
| `fiber` | a consumer configurer calls `fibersrvr.Static("/", site).Configure(router)` itself | `Start` returns an error wrapping `srvr.ErrRouteConfig` naming the consumer's configurer type and saying that `fibersrvr.Static` is registered with `Server.Register` |

### Requirement: Missing and hidden files are 404 problems
A request below a static prefix that names no regular file, or names a dotfile or anything inside a dot-directory, SHALL get srvr's `404` problem for `prblm.ErrNotFound` on every engine, unless the SPA fallback applies, never a directory listing. A dotfile SHALL look exactly like a missing file. The problem SHALL carry prblm's headers (`Cache-Control: no-store`, `Vary: Accept`), never the static `Cache-Control`.

#### Scenario: Not found
- **Given** a server on every engine with `Static("/assets", site, WithStaticCacheControl("public, max-age=60"))`
- **When** a client sends `<request>`
- **Then** the response is a `404` problem `{"title":"Not Found","status":404,"detail":"The requested resource was not found.","instance":"<instance>","trace_id":"<trace id>"}` with `Cache-Control: no-store`

##### Examples:
| request | instance |
|---------|----------|
| `GET /assets/missing.js` | `/assets/missing.js` |
| `HEAD /assets/missing.js` (headers only) | `/assets/missing.js` |
| `GET /assets/empty/` (a directory without an index file) | `/assets/empty/` |
| `GET /assets/empty/x/` | `/assets/empty/x/` |
| `GET /assets/.env` | `/assets/.env` |
| `GET /assets/.git/config` | `/assets/.git/config` |
| `GET /assets/assets/.hidden.js` | `/assets/assets/.hidden.js` |
| `GET /assets/.git/` | `/assets/.git/` |
| `GET /assets/app.js/` (a file named as a directory) | `/assets/app.js/` |
| `GET /assets/index.html/x` | `/assets/index.html/x` |
| `GET /assets/` with `WithStaticIndexNames("home.html")` and no `home.html` | `/assets/` |
| `GET /assets/<4096 x "a">.js` | `/assets/<4096 x "a">.js` |

### Requirement: Paths never escape the file system
A static configurer SHALL resolve a request only to a name inside its `fs.FS`. A path below the prefix that, once percent-decoded, has a `.` or `..` element, an empty element, a backslash, a NUL or another control character, or is not valid UTF-8, SHALL get the `404` problem, never the SPA fallback, never a file outside the file system and never a `5xx`.

#### Scenario: Hostile paths
- **Given** a server on engine `<engine>` with `Static("/assets", os.DirFS(dir), WithSPAFallback("index.html"))`, `secret.txt` lying next to `dir`
- **When** a client sends `GET <path>` with `Accept: text/html`
- **Then** the response status is `<status>` and its body never contains `SECRET` or `<html>app</html>`

##### Examples:
| engine | path | status |
|--------|------|--------|
| `net/http` | `/assets/%2e%2e/secret.txt` | `404` |
| `gin` | `/assets/%2e%2e/secret.txt` | `404` |
| `fiber` | `/assets/%2e%2e/secret.txt` | `404` |
| `net/http` | `/assets/..%2Fsecret.txt` | `404` |
| `gin` | `/assets/..%2Fsecret.txt` | `404` |
| `fiber` | `/assets/..%2Fsecret.txt` | `404` |
| `net/http` | `/assets/..%5Csecret.txt` | `404` |
| `gin` | `/assets/..%5Csecret.txt` | `404` |
| `fiber` | `/assets/..%5Csecret.txt` | `404` |
| `net/http` | `/assets/a%00.txt` | `404` |
| `gin` | `/assets/a%00.txt` | `404` |
| `fiber` | `/assets/a%00.txt` | `404` |
| `net/http` | `/assets/a%0Ab` | `404` |
| `gin` | `/assets/a%0Ab` | `404` |
| `fiber` | `/assets/a%0Ab` | `404` |
| `net/http` | `/assets/%ff.txt` (invalid UTF-8) | `404` |
| `gin` | `/assets/%ff.txt` | `404` |
| `fiber` | `/assets/%ff.txt` | `404` |
| `fiber` | `/assets/%zz` (malformed escape) | `404` (on net/http and gin, Go's HTTP server refuses this target with `400` before any srvr stage runs, as for every route) |
| `net/http` | `/assets/%252e%252e/secret.txt` (double-encoded, a literal name) | `404` |
| `gin` | `/assets/%252e%252e/secret.txt` | `404` |
| `fiber` | `/assets/%252e%252e/secret.txt` | `404` |
| `gin` | `/assets/../secret.txt` sent as a raw request target | `404` |
| `fiber` | `/assets/../secret.txt` sent as a raw request target | `404` (fiber removes the dot segments, so no route matches `/secret.txt`) |
| `net/http` | `/assets/../secret.txt` sent as a raw request target | `307` with `Location: /secret.txt` (ServeMux redirects to the cleaned path; documented parity exception) |
| `net/http` | `/assets//app.js` | `307` with `Location: /assets/app.js` (ServeMux redirects to the cleaned path; documented parity exception) |
| `gin` | `/assets//app.js` | `404` |
| `fiber` | `/assets//app.js` | `404` |

#### Scenario: Symbolic links
- **Given** a server on every engine with `Static("/assets", <fs>)`, `dir/link.txt` a symbolic link to `secret.txt` outside `dir`
- **When** a client sends `GET /assets/link.txt`
- **Then** the response is `<outcome>`

##### Examples:
| fs | outcome |
|----|---------|
| `root.FS()`, `root` from `os.OpenRoot(dir)` | a `500` problem without `SECRET`, the path or the error text; the `http.request.failed` record has the error's `error.chain` and `exception.message` (the root refuses the escape) |
| `os.DirFS(dir)` | `200` with `SECRET`: `os.DirFS` follows links out of its directory, which the godoc warns about and is why it recommends `os.OpenRoot` |

### Requirement: File system errors are safe 500 problems
When the file system returns an error other than not-exist or invalid name, or panics, while a request is resolved or opened, the static configurer SHALL answer the safe `500` problem with no error text, file name or path. The `http.request.failed` record SHALL carry the error (`error.chain`, `exception.message`). A failure after the response started SHALL leave the response as sent.

#### Scenario: Faulty file system
- **Given** a server on every engine with `Static("/assets", fsys)` where `fsys` `<fault>` for `app.js`
- **When** a client sends `GET /assets/app.js`
- **Then** the response is `<response>`
- **And** one `http.request.failed` record is written with `level` `<level>` and `<record>`

##### Examples:
| fault | response | level | record |
|-------|----------|-------|--------|
| returns an error wrapping `fs.ErrPermission` from `Open` | a `500` problem `{"title":"Internal Server Error","status":500,"instance":"/assets/app.js","trace_id":"…"}` | `ERROR` | `exception.message` containing `permission denied` |
| returns `errors.New("disk on fire")` from `Stat` | a `500` problem without `disk on fire` | `ERROR` | `exception.message` containing `disk on fire` |
| panics with `"boom"` in `Open` | a `500` problem | `ERROR` | `error.kind` `panic` |
| returns a typed-nil `*main.MyErr` as `error` from `Open` | a `500` problem | `ERROR` | `error.type` `*main.MyErr` |
| returns a file that does not implement `io.Seeker` | a `500` problem | `ERROR` | `exception.message` naming the file's Go type and saying it cannot seek |
| returns a file whose `Read` fails after `10` bytes | status `200` with `Content-Length` `14` and a body cut short; the connection is closed | `INFO` | `msg` `http.request.completed` (the failure arrived after the response started) |
| returns `fs.ErrNotExist` wrapped as `fmt.Errorf("lookup: %w", fs.ErrNotExist)` | a `404` problem | `WARN` | no `exception.message` naming the file system's error |
| returns a file that is removed between resolution and serving (`Stat` succeeds, `Open` returns `fs.ErrNotExist`) | a `404` problem | `WARN` | `http.response.status_code` `404` |

### Requirement: The SPA fallback serves navigations that match no file
With `WithSPAFallback(file)`, a `GET` or `HEAD` below the prefix that matches no file, whose `Accept` lists `text/html` with a non-zero quality and whose last path segment has no `.`, SHALL get `file` with status `200`, `Content-Type` from `file`, `Cache-Control: no-cache` and `Vary: Accept`. Every other miss SHALL get the `404` problem. Without the option no fallback is served.

#### Scenario: Fallback decision
- **Given** a server on every engine with `Static("/", site, WithSPAFallback("index.html"))`
- **When** a client sends `<request>` with `Accept` `<accept>`
- **Then** the response is `<response>`

##### Examples: fallback served
| request | accept | response |
|---------|--------|----------|
| `GET /dashboard` | `text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8` | `200` `<html>app</html>` with `Cache-Control: no-cache` and `Vary: Accept` |
| `GET /users/42/edit` | `text/html` | `200` `<html>app</html>` |
| `HEAD /dashboard` | `text/html` | `200` without a body, `Content-Type: text/html; charset=utf-8` |
| `GET /dashboard?tab=2` | `TEXT/HTML` | `200` `<html>app</html>` (media types compare case-insensitively) |
| `GET /dashboard` | `application/json, text/html;q=0.1` | `200` `<html>app</html>` |
| `GET /empty/` (a directory without an index file) | `text/html` | `200` `<html>app</html>` |

##### Examples: no fallback
| request | accept | response |
|---------|--------|----------|
| `GET /dashboard` | (none) | a `404` problem |
| `GET /dashboard` | `*/*` | a `404` problem (`fetch` sends `*/*` by default) |
| `GET /dashboard` | `text/*` | a `404` problem |
| `GET /dashboard` | `application/json` | a `404` problem |
| `GET /dashboard` | `text/html;q=0` | a `404` problem |
| `GET /assets/app-9999.js` | `text/html` | a `404` problem (the last segment has an extension) |
| `GET /users/john.doe` | `text/html` | a `404` problem (the dot rule; documented limitation) |
| `GET /.env` | `text/html` | a `404` problem (hidden files never fall back) |
| `GET /%2e%2e/secret` | `text/html` | a `404` problem (hostile paths never fall back) |
| `POST /dashboard` | `text/html` | a `405` problem with `Allow: GET, HEAD` |
| `GET /dashboard` with no `WithSPAFallback` | `text/html` | a `404` problem |

##### Examples: fallback after Start
| request | accept | response |
|---------|--------|----------|
| `GET /dashboard` after `index.html` was deleted from an `os.DirFS` site | `text/html` | a `404` problem |
| `GET /dashboard` after `index.html` was replaced with `<html>v2</html>` in an `os.DirFS` site | `text/html` | `200` `<html>v2</html>` |
| `GET /dashboard` while the fallback's `Open` returns `fs.ErrPermission` | `text/html` | a `500` problem; an `ERROR` `http.request.failed` record |

#### Scenario: Fallback under a prefix
- **Given** a server on every engine with `Static("/app", site, WithSPAFallback("index.html"))` and a `GET /api/users` route
- **When** a client sends `<request>` with `Accept: text/html`
- **Then** the response is `<response>`

##### Examples:
| request | response |
|---------|----------|
| `GET /app/settings` | `200` `<html>app</html>` |
| `GET /app` | `200` `<html>app</html>` (the index file, not the fallback, with `Cache-Control: no-cache`) |
| `GET /other` | a `404` problem (outside the prefix, answered by the router) |
| `GET /api/users` | the route's answer |
| `GET /api/nope` | a `404` problem (outside the prefix) |

### Requirement: Index files and the fallback are revalidated by default
A served index file and the SPA fallback SHALL carry `Cache-Control: no-cache` unless `WithStaticIndexCacheControl` sets another value. Other files SHALL carry no `Cache-Control` unless `WithStaticCacheControl` sets one. The header SHALL be sent on `200` and `304` file responses only, never on problems.

#### Scenario: Cache headers
- **Given** a server on every engine with `Static("/", site, WithSPAFallback("index.html"), <options>)`
- **When** a client sends `<request>` with `Accept: text/html`
- **Then** the response status is `<status>` with `Cache-Control` `<cache>`

##### Examples:
| options | request | status | cache |
|---------|---------|--------|-------|
| (none) | `GET /` | `200` | `no-cache` |
| (none) | `GET /index.html` | `200` | `no-cache` |
| (none) | `GET /docs/` | `200` | `no-cache` |
| (none) | `GET /dashboard` | `200` | `no-cache` |
| (none) | `GET /app.js` | `200` | (none) |
| (none) | `GET /missing.js` | `404` | `no-store` (prblm's) |
| `WithStaticCacheControl("public, max-age=31536000, immutable")` | `GET /assets/app-3f2a.js` | `200` | `public, max-age=31536000, immutable` |
| `WithStaticCacheControl("public, max-age=31536000, immutable")` | `GET /index.html` | `200` | `no-cache` |
| `WithStaticCacheControl("public, max-age=31536000, immutable")` | `GET /missing.js` | `404` | `no-store` (prblm's) |
| `WithStaticIndexCacheControl("no-store")` | `GET /dashboard` | `200` | `no-store` |
| `WithStaticConfig(StaticConfig{IndexCacheControl: "public, max-age=0, must-revalidate"})` | `GET /` | `200` | `public, max-age=0, must-revalidate` |
| `WithStaticCacheControl("public, max-age=60")`, `site` an `os.DirFS` | `GET /app.js` with `If-Modified-Since` equal to its modification time | `304` | `public, max-age=60` |
| `WithStaticIndexCacheControl("")` | `GET /` | `200` | `no-cache` (empty means the default) |

### Requirement: Only GET and HEAD are served
A request below a static prefix with another method, that no other route matches, SHALL get a `405` problem with `Allow: GET, HEAD` on every engine, counted as answered by the router. A path another route serves keeps that route's answer.

#### Scenario: Other methods
- **Given** a server on engine `<engine>` with `Static("/", site)` and `<routes>`
- **When** a client sends `<request>`
- **Then** the response is `<response>`

##### Examples:
| engine | routes | request | response |
|--------|--------|---------|----------|
| `net/http` | (none) | `POST /app.js` | a `405` problem with `Allow: GET, HEAD` |
| `gin` | (none) | `POST /app.js` | a `405` problem with `Allow: GET, HEAD` |
| `fiber` | (none) | `POST /app.js` | a `405` problem with `Allow: GET, HEAD` |
| `net/http` | (none) | `DELETE /nope` | a `405` problem with `Allow: GET, HEAD` |
| `gin` | (none) | `DELETE /nope` | a `405` problem with `Allow: GET, HEAD` |
| `fiber` | (none) | `DELETE /nope` | a `405` problem with `Allow: GET, HEAD` |
| `net/http` | (none) | `OPTIONS /` | a `405` problem with `Allow: GET, HEAD` |
| `gin` | (none) | `OPTIONS /` | a `405` problem with `Allow: GET, HEAD` |
| `fiber` | (none) | `OPTIONS /` | a `405` problem with `Allow: GET, HEAD` |
| `net/http` | `POST /api/users` | `POST /api/users` | the route's answer |
| `gin` | `POST /api/users` | `POST /api/users` | the route's answer |
| `fiber` | `POST /api/users` | `POST /api/users` | the route's answer |
| `net/http` | `POST /api/users` | `PUT /api/users` | a `405` problem with `Allow: GET, HEAD, POST` |
| `gin` | `POST /api/users` | `PUT /api/users` | a `405` problem with `Allow: POST` (documented parity exception) |
| `fiber` | `POST /api/users` | `PUT /api/users` | a `405` problem with `Allow: GET, HEAD, POST` |

### Requirement: Registered routes win over static files
Any route registered by a configurer SHALL take precedence over a static mount and its SPA fallback for the requests it matches, on every engine and in either registration order. Among static mounts, the one with the longest prefix that contains the request path SHALL own it.

#### Scenario: Precedence
- **Given** a server on every engine with configurers registered in order `<order>`: `Static("/", site, WithSPAFallback("index.html"))`, `Static("/docs", docs)` and routes `GET /api/users`, `GET /app.js`
- **When** a client sends `<request>` with `Accept: text/html`
- **Then** the response is `<response>`

##### Examples:
| order | request | response |
|-------|---------|----------|
| static first, routes last | `GET /api/users` | the route's answer |
| routes first, static last | `GET /api/users` | the route's answer |
| static first, routes last | `GET /app.js` (a file of that name exists too) | the route's answer |
| routes first, static last | `GET /app.js` | the route's answer |
| static first, routes last | `GET /api/nope` | `200` `<html>app</html>` (a navigation the API does not answer falls back) |
| static first, routes last | `GET /api/nope` with `Accept: application/json` | a `404` problem |
| static first, routes last | `GET /docs/guide.html` | the file from `docs` |
| static first, routes last | `GET /docs/missing` | a `404` problem from the `/docs` mount (no fallback: that mount has none) |
| static first, routes last | `GET /style.css` | the file from `site` |

#### Scenario: Conflicts with other routes
- **Given** a server on engine `<engine>` with `Static("/", site)` and a consumer route `<route>`
- **When** the server starts
- **Then** `<result>`

##### Examples:
| engine | route | result |
|--------|-------|--------|
| `net/http` | `GET /{path...}` | the server starts; the consumer's route answers every `GET` and `HEAD` (it is more specific than the mount's every-method pattern) and the static mount answers the other methods with `405` |
| `net/http` | `GET /` (ServeMux's `/` matches every path) | the server starts; the consumer's route answers every `GET` and `HEAD` |
| `net/http` | `GET /{$}` | the server starts; the route answers `GET /` and the static mount `GET /app.js` |
| `net/http` | `Mount("/api/", h)` | the server starts; `h` answers `/api/x` and the static mount `GET /app.js` |
| `fiber` | `Get("/*")` | `Start` returns an error wrapping `srvr.ErrRouteConfig` naming the duplicate route |
| `gin` | `GET /*any` | the server starts; the consumer's catch-all answers every `GET` and the static mount answers only what gin routes to no route (gin's catch-all is a registered route) |
| `net/http` | `Mount("/", h)` | `Start` returns an error wrapping `srvr.ErrRouteConfig` naming both patterns and both configurers |
| `net/http` | `Mount("/assets/", h)` with `Static("/assets", site)` instead of `Static("/", site)` | `Start` returns an error wrapping `srvr.ErrRouteConfig` naming both patterns and both configurers |

### Requirement: Static requests are logged, observed and limited like routed requests
Static requests SHALL pass through the whole pipeline (trace context, access record, observers, recovery, problems). A served file or fallback SHALL be recorded with `http.route` in the engine's syntax. A static `404` or `405` SHALL be recorded like a request no route matched (no `http.route`) and counted by the unmatched-record limit. A failure consumer middleware reports SHALL not count.

#### Scenario: Static access records
- **Given** a server on engine `<engine>` with `Static("/assets", site, WithSPAFallback("index.html"))`
- **When** a client sends `<request>` with `Accept: text/html` and `traceparent` `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01`
- **Then** one access record is written with `msg` `<msg>`, `level` `<level>`, `http.route` `<route>` and `http.response.status_code` `<status>`
- **And** it has `trace_id` `4bf92f3577b34da6a3ce929d0e0e4736` and no file system path

##### Examples:
| engine | request | msg | level | route | status |
|--------|---------|-----|-------|-------|--------|
| `net/http` | `GET /assets/app.js` | `http.request.completed` | `INFO` | `/assets/{path...}` | `200` |
| `gin` | `GET /assets/app.js` | `http.request.completed` | `INFO` | `/assets/*filepath` | `200` |
| `fiber` | `GET /assets/app.js` | `http.request.completed` | `INFO` | `/assets/*` | `200` |
| `net/http` | `GET /assets` | `http.request.completed` | `INFO` | `/assets` | `200` |
| `gin` | `GET /assets` | `http.request.completed` | `INFO` | `/assets` | `200` |
| `fiber` | `GET /assets` | `http.request.completed` | `INFO` | `/assets` | `200` |
| `net/http` | `GET /assets/settings` (fallback) | `http.request.completed` | `INFO` | `/assets/{path...}` | `200` |
| `gin` | `GET /assets/settings` (fallback) | `http.request.completed` | `INFO` | `/assets/*filepath` | `200` |
| `fiber` | `GET /assets/settings` (fallback) | `http.request.completed` | `INFO` | `/assets/*` | `200` |
| `net/http` | `GET /assets/missing.js` | `http.request.failed` | `WARN` | (none) | `404` |
| `gin` | `GET /assets/missing.js` | `http.request.failed` | `WARN` | (none) | `404` |
| `fiber` | `GET /assets/missing.js` | `http.request.failed` | `WARN` | (none) | `404` |
| `net/http` | `POST /assets/app.js` | `http.request.failed` | `WARN` | (none) | `405` |
| `gin` | `POST /assets/app.js` | `http.request.failed` | `WARN` | (none) | `405` |
| `fiber` | `POST /assets/app.js` | `http.request.failed` | `WARN` | (none) | `405` |

#### Scenario: Static misses and the unmatched-record limit
- **Given** a server on engine `<engine>` with `WithUnmatchedRecordLimit(1, time.Minute)`, `Static("/", site)` and `<middleware>`
- **When** a client sends `GET /nope-1.js`, `GET /nope-2.js` and `GET /nope-3.js`
- **Then** each response is a `<status>` problem and `<written>` access records with that status are written

##### Examples:
| engine | middleware | status | written |
|--------|------------|--------|---------|
| `net/http` | (none) | `404` | `1` |
| `gin` | (none) | `404` | `1` |
| `fiber` | (none) | `404` | `1` |
| `fiber` | `router.Use(mw)` on the root router, `mw` returning `c.Next()` | `404` | `1` |
| `fiber` | `router.Use(mw)` on the root router, `mw` returning `prblm.ErrNotFound` without calling `c.Next()` | `404` | `3` |
| `net/http` | a root-group middleware registered before the static configurer, writing the `404` problem of `prblm.ErrNotFound` itself without calling next (net/http middleware cannot return an error) | `404` | `3` |
| `gin` | (none), with the requests sent as `PUT` | `405` | `1` |

#### Scenario: Observers see static requests
- **Given** a server on engine `<engine>` observed by `otelsrvr` with `Static("/assets", site)`
- **When** a client sends `GET /assets/app.js` and `GET /assets/missing.js`
- **Then** `http.server.request.duration` counts `2` requests: one with `http.route` `<route>` and status `200`, one without `http.route` and status `404`

##### Examples:
| engine | route |
|--------|-------|
| `net/http` | `/assets/{path...}` |
| `gin` | `/assets/*filepath` |
| `fiber` | `/assets/*` |

#### Scenario: Consumer middleware around static files
- **Given** a server on engine `<engine>` whose root group or router has consumer middleware `<middleware>` registered before `Static("/assets", site)`
- **When** a client sends `GET /assets/app.js`
- **Then** the response is `<response>` and exactly one access record is written

##### Examples:
| engine | middleware | response |
|--------|------------|----------|
| `net/http` | returns `prblm.ErrUnauthenticated` without calling next | a `401` problem |
| `gin` | calls `c.AbortWithError(401, prblm.ErrUnauthenticated)` | a `401` problem |
| `fiber` | returns `prblm.ErrUnauthenticated` without calling `c.Next()` | a `401` problem |
| `net/http` | panics with `"boom"` | a `500` problem; the record has `error.kind` `panic` |
| `gin` | panics with `"boom"` | a `500` problem; the record has `error.kind` `panic` |
| `fiber` | panics with `"boom"` | a `500` problem; the record has `error.kind` `panic` |
| `net/http` | sets header `X-Mw: 1` and calls next | `200` with the file and `X-Mw: 1` |
| `gin` | sets header `X-Mw: 1` and calls `c.Next()` | `200` with the file and `X-Mw: 1` |
| `fiber` | sets header `X-Mw: 1` and returns `c.Next()` | `200` with the file and `X-Mw: 1` |

### Requirement: Static mounts are listed with their configurer
Every engine SHALL list each static mount in `engine.Routes` with `Method` `GET`, its pattern in the engine's syntax and the static configurer's Go type, so route checks see it. A prefix other than `/` SHALL also be listed on its own.

#### Scenario: Static route listing
- **Given** a server on engine `<engine>` with `<configurer>` registered
- **When** the server starts with a route check that records its input
- **Then** the check receives exactly `<listed>`

##### Examples:
| engine | configurer | listed |
|--------|------------|--------|
| `net/http` | `srvr.Static("/", site)` | `{GET /{path...} *srvr.staticConfigurer}` |
| `gin` | `ginsrvr.Static("/", site)` | `{GET /*filepath *ginsrvr.staticConfigurer}` |
| `fiber` | `fibersrvr.Static("/", site)` | `{GET /* *fibersrvr.staticConfigurer}` |
| `net/http` | `srvr.Static("/assets", site)` | `{GET /assets *srvr.staticConfigurer}`, `{GET /assets/{path...} *srvr.staticConfigurer}` |
| `gin` | `ginsrvr.Static("/assets", site)` | `{GET /assets *ginsrvr.staticConfigurer}`, `{GET /assets/*filepath *ginsrvr.staticConfigurer}` |
| `fiber` | `fibersrvr.Static("/assets", site)` | `{GET /assets *fibersrvr.staticConfigurer}`, `{GET /assets/* *fibersrvr.staticConfigurer}` |
