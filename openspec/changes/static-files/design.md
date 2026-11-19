# Design

## Context

See proposal.md - Why. The code facts this design relies on, read in the merged code and dependency sources at planning (to be rechecked with gopls in consolidation):

- **net/http** (`routing.go`, `server.go`): a configurer gets the root `*Group`; `Group.add` joins prefix and path into a ServeMux pattern (`joinPattern`) and buffers the route with the group's middleware; `installRoutes` names both routes and configurers of a ServeMux conflict; `withRoute` sets `http.route` and the handler location before the route's middleware runs. `answerUnmatched` turns ServeMux's 404/405 into problems and marks them router-answered (`markRouterAnswered`); any other fallback answer, such as ServeMux's redirect to a cleaned path, is replayed. `Mount` already accepts `http.FileServerFS` but strips nothing but the group prefix and keeps FileServer's behaviour (listing, plain-text 404). Probed on Go 1.27 with a scratch ServeMux (`GET /assets/{path...}` and `GET /{path...}`): `PathValue("path")` is percent-decoded per segment (`..%2Fsecret.txt` gives `../secret.txt`, `a%00.txt` gives `a\x00.txt`, `%252e%252e/x` gives `%2e%2e/x`); a raw `..` or `//` gets a `307` to the cleaned path; `/assets` without a pattern of its own gets a `307` to `/assets/`; `%zz` is refused with `400` by the HTTP server before the mux; `PUT /api/users` beside `POST /api/users` and `GET /{path...}` gets `405` with `Allow: GET, HEAD, POST`; `DELETE /nope` gets `405` with `Allow: GET, HEAD`.
- **gin** v1.12.0: a catch-all `/*filepath` conflicts with any other segment at the same level (`tree.go:353` panics), so `/*filepath` cannot sit beside `/api/users`. Unrouted requests run `allNoRoute`, which `combineHandlers` builds from `engine.Handlers` (srvr's `requestStage`, `ginprblm.Middleware`, `guardErrors`, `ginprblm.Recovery`) plus `noRoute`; `NoRoute` rebuilds it (`gin.go:326`). Before NoRoute, `HandleMethodNotAllowed` answers `405` when another method's tree matches (`gin.go:735-750`). ginsrvr's `requestStage` marks every request without `FullPath()` as router-answered; `configure` hands each configurer `&s.engine.RouterGroup`. `RouterGroup.StaticFS` wraps `http.FileServer`.
- **fiber** v3.7.0: routes match in stack order. After a matched route calls `c.Next()` with nothing left, the router returns the identity value `fiber.ErrNotFound` (`router.go:767`). `app.Get` registers `GET` and `HEAD`. `Ctx.SendFile(name, fiber.SendFile{FS: fsys, ...})` serves one named file from an `fs.FS` through `fasthttp.FS` with `GenerateIndexPages: false`, returns a 404 `*fiber.Error` for a miss, and rejects a name with a control byte (`res.go:1262`). `middleware/static` resolves the request path itself (`fspath.Valid`, route prefix, `IndexNames`), turns any open error into `404` and, without `NotFoundHandler`, resets the response and calls `c.Next()`, so a permission error and a missing file look the same (`static.go`, fasthttp `fs.go:1437-1500`). fasthttp compresses an `fs.FS` file into an in-memory cache and writes compressed files to disk only for its own `osFS` (`fs.go:1973`). It generates no `ETag` unless `GenerateETag` is set. It evaluates `If-Modified-Since` against the file's modification time (`fs.go:1528`), and a zero time is sent as-is (`fs.go:2121`). fibersrvr's `requestStage` sets `http.route` from `c.Route()` after the chain whenever `c.Matched()`, and marks router-answered only for the identity `fiber.ErrNotFound`/`ErrMethodNotAllowed` with `!c.Matched()`. `configure` runs configurers in order, then `owners.duplicate` rejects duplicate routes.
- **engine**: `Configure` recovers a configurer panic into `RouteErrorf` naming the configurer; `RequestState` has `SetRoute`, `Matched() = route != ""`, `MarkRouterAnswered` (no getter) and `unmatched` (router-answered, failed, not panicked, 404/405); `IsNil` treats typed-nil pointers, maps, slices and funcs as nil; `ConfigError` unwraps to `ErrConfig`.
- **prblm** v0.2.1: problem responses get `Cache-Control: no-store` and `Vary: Accept`, but a `Cache-Control` already on the response is kept (`respond.go` `MergeInto`).
- **Go**: `os.Root.FS()` exists (Go 1.25+) and refuses paths that escape the root. `os.DirFS` follows symbolic links anywhere. `fs.ValidPath` rejects `.`/`..`/empty elements and leading or trailing slashes, but not backslashes or control characters. `mime.TypeByExtension` reads the system's MIME tables on Unix in addition to Go's built-in ones.

## Test fixture

Unless a row says otherwise, `site` is an `fs.FS` built twice from the same tree, as an `fstest.MapFS` and as an `os.DirFS` of a `t.TempDir()`. Rows that say so use an `embed.FS` from `testdata/`. The tree holds:

- `index.html` (`<html>app</html>`), `app.js` (`console.log(1)`), `style.css`, `logo.svg`, `data.json`
- `notes` (no extension, plain text), `café.txt`, `a/b.txt`
- `docs/index.html`, `docs/guide.html`
- `empty/x/` (directories without an index file)
- `assets/app-3f2a.js`
- `.env`, `.git/config`, `assets/.hidden.js`

With `os.DirFS`, `secret.txt` (`SECRET`) lies next to the served directory, outside it. The parity harness in `internal/paritytest` builds the fixture once and gives every engine case its own server.

## Goals / Non-Goals

**Goals:**
- One resolver in `engine` decides every static answer for all engines, so parity is a property of the code, not of three implementations kept in step.
- No static request bypasses srvr's pipeline: trace context, access record, observers, recovery and problems apply as for routes.
- Static misses behave like unmatched requests for logs, metrics and the unmatched-record limit.

**Non-Goals:**
- Byte ranges, `ETag`, compression, precompressed variants, directory redirects and a configurable navigation rule (proposal, Scope).
- Security headers on static responses (`X-Content-Type-Options`, CSP): `scrty-integration`'s concern.
- Symlink policing inside srvr: confinement belongs to the `fs.FS` (D6).
- Changing `Mount`: it stays the escape hatch for a plain `http.Handler`.

## Slices

1. **Consolidation** (task group 1, not a code slice).
2. **net/http serves files** (`srvr.Static`, the `engine` resolver): files and index files with `Content-Type` and `Last-Modified`, `If-Modified-Since` with the other preconditions and `Range` ignored, `HEAD`, `405` for other methods, `404` problems for missing, hidden and hostile paths, `500` problems for file system errors (including non-seekable files), configuration validation (prefix, file system, options, index names) and the config struct, the route listing, and misses recorded and limited as unmatched. Scenarios: "Serving a file", "Conditional and range requests", "Partial failure while serving", "Configuration sources" (index-name rows), "Rejected static configuration" (all but the fallback and cache rows), "Several invalid settings", "Static configurer lifecycle misuse" (net/http rows), "Not found", "Hostile paths", "Symbolic links", "Faulty file system", "Other methods", "Precedence" and "Conflicts with other routes" (net/http rows without fallback), "Static access records", "Static misses and the unmatched-record limit", "Observers see static requests" and "Consumer middleware around static files" (net/http rows), and "Static route listing" (net/http rows). Hot paths: the net/http static handler and the `engine` resolver. It works on its own: a net/http server serves a build directory safely.
3. **SPA fallback and caching headers on net/http**: `WithSPAFallback`, `WithStaticCacheControl`, `WithStaticIndexCacheControl`, the fallback and index `Cache-Control`, `Vary: Accept`, fallback validation at `Start`. Scenarios: "Fallback decision", "Fallback under a prefix", "Cache headers", the remaining "Configuration sources" and "Rejected static configuration" rows, and the fallback rows of "Precedence". Hot path: the resolver's fallback branch. It works on its own: a net/http server serves a React or Vite app with deep links.
4. **gin**: `ginsrvr.Static` served from gin's no-route chain (D4), with every scenario's gin rows. Hot paths: ginsrvr's static dispatcher and its configure-phase installation.
5. **fiber**: `fibersrvr.Static` installed after all other configurers and served with `Ctx.SendFile` (D2, D4), with every scenario's fiber rows. Hot paths: fibersrvr's static handler, its deferred installation, and the route rule in `requestStage`.
6. **Parity and documented exceptions**: `internal/paritytest` runs every "every engine" row against all three engines from one table, and pins the three new `engine-parity` exceptions. `LOGGING.md` gets static misses and the unmatched limit; the README section is completed. No production change is expected; a parity failure found here is a defect claim (rule 2) and is fixed under a RED row.
7. **Release**: as in `followups-hardening`: prepare, owner stop, tags in RELEASING.md order, restore `replace` lines, GitHub Release, proxy check.

No slice depends on a later one: slice 2's resolver already has the decision type that slice 3 extends with the fallback. Slices 4 and 5 reuse the resolver unchanged. Slice 6 only adds tests and docs.

## Decisions

### D1. One resolver in `engine`; the engines only send bytes
`engine` gets a static mount type built by `engine.NewStatic(prefix, fsys, StaticConfig)`. It validates and copies the configuration and returns the mount or a joined error. Its `Resolve(method, rest, accept)` returns one decision: serve a file (open file, name, size, modification time, `Content-Type`, `Cache-Control`, `Vary`, whether it is the fallback), not found, method not allowed, or failed (with the error). `rest` is the path below the prefix as the engine extracted it, percent-decoded (D3). Each engine maps the decision to its native calls: it serves the file, returns `prblm.ErrNotFound`, returns a `405` problem with `Allow: GET, HEAD`, or returns the error for the `500` problem. Every header and every status therefore comes from one function, which one table-driven test in `engine` covers (rows from every scenario), and the engine tests only check the mapping.

`srvr.StaticConfig`, `srvr.StaticOption` and the five options are aliases or wrappers of `engine` declarations, as for `DebugBufferConfig`. The engine modules take `...srvr.StaticOption`.

**Alternatives:** each engine's own static helper with a thin srvr wrapper. Rejected: three resolvers disagree on dotfiles, index redirects, listing and error mapping (Context), and parity would depend on keeping them in step.

**Failure modes:** an engine extracts `rest` differently (decoding, trailing slash). D3 fixes one extraction per engine, and slice 6 runs every row on every engine. The resolver opens the file and the engine serves it: the open file is closed by the engine on every path, including a panic (deferred close), and fiber closes it before `SendFile` reopens by name (D2). A consumer `fs.FS` that panics is called inside the request's recovery (net/http `Recover`, `ginprblm.Recovery`, `fiberprblm.Recovery`) and, at `Start`, under the configure phase's recover (D9).

### D2. Native byte-serving primitives: `http.ServeContent` and fiber's `Ctx.SendFile`
net/http and gin serve the opened file with `http.ServeContent(w, r', name, modtime, file)`. That is net/http's own content server (type sniffing, `Content-Length`, `If-Modified-Since`, `HEAD`), and gin's `StaticFS` uses the same code underneath. `http.FileServerFS` is not used: it resolves the path again, redirects `/index.html` to `./` (which would break the fallback, served as `index.html`), lists directories without an index, and writes plain-text errors. fiber serves the resolved name with `c.SendFile(name, fiber.SendFile{FS: fsys, CacheDuration: -1})`. That is fiber's file server (`fasthttp.FS`) without index pages, compression or ranges (`Compress` and `ByteRange` false, `MaxAge` 0). `CacheDuration: -1` disables fasthttp's open-file cache, so a file replaced on disk is served fresh as on net/http, and no file handle outlives a request.

This refines the brief's "fiber uses `middleware/static`". The middleware resolves the request path itself, maps a permission error to `404`, passes misses to `c.Next()`, and cannot answer one path with another file (the fallback). With it, srvr's resolver would no longer be the only authority. `SendFile` is the primitive the middleware wraps: the same `fasthttp.FS`, given a name srvr already resolved. The owner confirmed it in consolidation (2026-11-19, notes.md), asking for the correct primitive with the best performance: the cache trade-off below is measured in task 5.6 before any change.

**Alternatives:** `middleware/static` with `Next`, `NotFoundHandler` and `ModifyResponse` hooks (two resolvers; permission errors invisible; fallback impossible without rewriting `c.Path`). `fasthttp.ServeFS` (enables compression and index pages). Copying bytes by hand (loses native conditional handling).

**Failure modes:** a file that is not an `io.Seeker` (`archive/zip` files, for example) cannot go through `ServeContent`. The resolver checks seekability on every engine and fails with an error naming the file's type, giving the `500` problem everywhere ("Faulty file system" row). Reading or seeking fails midway: `ServeContent` has already sent `200` and `Content-Length`, so the response is cut short, and the record says `200` completed ("Partial failure while serving"). `SendFile` returns a 404 `*fiber.Error` when the file vanished after resolution; fibersrvr maps it to the `404` problem as a miss. `SendFile` keeps one handler per distinct config in the app (`maxSendFileHandlers`). srvr builds one `SendFile` value per mount and reuses it, so the cache never grows per request. fasthttp may log through its own logger when an open fails. Resolution happens first, so this happens only in the vanish race; noted as a risk.

### D3. Resolution rules
Per engine, `rest` is: net/http `r.PathValue("path")` (`""` on the exact-prefix pattern); gin `c.Request.URL.Path` without the mount prefix (gin keeps the decoded path); fiber `c.Params("*")`, percent-decoded with `url.PathUnescape`, because fiber leaves reserved escapes encoded. `rest` with a trailing slash names a directory. Then, in order:

1. Method not `GET`/`HEAD`: method not allowed.
2. Malformed escape (fiber only), invalid UTF-8, a backslash, a NUL or other control character, or a name `fs.ValidPath` refuses (`.`, `..`, empty element): not found, never the fallback.
3. Any element starting with `.`: not found, never the fallback. Hidden files look exactly like missing files.
4. `fs.Stat` the name, `"."` for `""`. Not-exist (`errors.Is(err, fs.ErrNotExist)`), invalid (`fs.ErrInvalid`), "not a directory" (`syscall.ENOTDIR`, a name below a file) or "name too long" (`syscall.ENAMETOOLONG`), which `os.DirFS` returns without matching `fs.ErrNotExist` (notes.md, slice 2): a miss. Any other error: failed. A regular file requested with a trailing slash: a miss. A directory: the first index name that is a regular file inside it, else a miss. Anything else (device, socket): a miss.
5. A miss: the fallback if configured and the request is a navigation (D7), else not found.
6. Open the chosen file. An open error is classified as in step 4. Check `io.Seeker`. Compute the headers (D8, D10).

There are no redirects. `/docs` and `/docs/` both serve `docs/index.html`, so no `Location` is ever built from request input (no open-redirect surface). Relative links inside such pages must be root-relative; the godoc says so.

**Alternatives:** redirect `/docs` to `/docs/` as `http.FileServer` does (needs a safe `Location` from request input, and differs per engine); `403` for directories without an index (reveals existence).

**Failure modes:** a 4096-character name gets `404` (the file system's own name limit gives not-exist, invalid or `ENAMETOOLONG`). A name valid on Unix but refused by `os.DirFS` on Windows (`:`) is not-exist or invalid there, so `404`. A path ServeMux or fiber cleans before srvr sees it never reaches the resolver (net/http `307`, fiber removes dot segments). These are documented parity exceptions, and no engine serves outside the file system ("Hostile paths").

### D4. Registered routes win: per-engine installation
- **net/http**: `srvr.Static(...).Configure(g)` registers `{prefix}` (prefixes other than `/`) and `{prefix}/{path...}` for every method on the group it is given, like any route, with that group's middleware (owner decision 2026-11-19, notes.md: GET-only patterns made ServeMux refuse every method-less route, such as `Mount("/api/", h)`, beside `Static("/")`). ServeMux's "most specific pattern wins" puts every other route first in either registration order: a route with a method is more specific than the mount's method-less pattern, and a `Mount` below the prefix is more specific in path. A consumer `GET /{path...}` or `GET /` (ServeMux's catch-all) therefore wins every `GET`; only a method-less route with an equivalent path (`Mount("/")`, `Mount("/assets/")` beside `Static("/assets")`) is a conflict that `installRoutes` names. The mount answers other methods with the `405` problem; its `Allow` is `GET, HEAD` plus every method another route serves for the path, found by asking the ServeMux which pattern matches the request with that method (only on that `405` path), so `PUT /api/users` beside `POST /api/users` gets `GET, HEAD, POST` as from ServeMux itself. The listing shows the mount's routes with method `GET`. Longest prefix: `/docs/{path...}` is more specific than `/{path...}`. A second mount of the same joined prefix is detected by the builder before ServeMux and reported as `Static.Prefix` (D9).
- **gin**: a catch-all cannot share a tree level (Context), so ginsrvr serves static mounts from the no-route chain. `ginsrvr.Server.configure` recognises its own static configurers (an unexported interface implemented by `ginsrvr.Static`'s type), does not call their `Configure`, and after the loop installs one dispatcher with `e.NoRoute`. The dispatcher picks the mount with the longest prefix containing the path, and answers `404` itself otherwise. `NoRoute` rebuilds `allNoRoute` from `engine.Handlers` at that moment, which is the root group's handler list, so srvr's stages and every consumer middleware added to the root group during the configure phase run for static requests. The difference from net/http, where only middleware added before `Static` applies, is documented in the godoc. `NoRoute` is called only when a static mount exists, so a server without one keeps today's no-route chain exactly (followups-hardening's "runs only for matched routes" rows). A request another method's tree matches goes to gin's no-method chain; with a static mount, ginsrvr installs the dispatcher there too with `e.NoMethod` (owner decision 2026-11-19, notes.md): a `GET` or `HEAD` inside a mount is the mount's (status and gin's `Allow` header reset before consumer middleware runs, as net/http answers it), and every other method, or a path outside every mount, keeps gin's `405` (documented exception). One dispatcher serves both chains. With a mount, `requestStage` decides for every request without a route whether a mount answers it (the no-method chain is recognised by gin's preset `405`), records the mount's route and resets the preset `404`/`405` status and `Allow` before consumer middleware runs, so middleware sees a mount's request as a matched route's; the dispatcher answers the requests with a recorded route and marks the others as the router's. `Configure` called directly panics with a message saying to use `Server.Register`; `engine.Configure` turns that into the route error naming the calling configurer.
- **fiber**: routes match in stack order, so fibersrvr's `configure` sets static configurers aside and installs them after every other configurer, longest prefix first: `app.Get(prefix)` and `app.Get(prefix + "/*")`. Then the existing duplicate check runs over all routes, including a consumer `Get("/*")`. Consumer `Use` middleware registered earlier applies. `Configure` called directly panics like gin's, for the same reason: the deferral only works through `Register`.

**Alternatives:** require consumers to register `Static` last (order-dependent, silent mistakes); gin catch-all routes with a documented "no other routes at that level" rule (breaks `/api/...` beside `/`).

**Failure modes:** a consumer gin catch-all (`GET /*any`) or net/http `GET /{path...}` shadows every static `GET`. It is a registered route and wins by design; on fiber the equivalent `Get("/*")` is a duplicate and fails `Start` (documented exception). A fiber sub-app mounted at `/` (`router.Use("/", sub)`) runs before static mounts, because mounts are middleware. Its routes win, which is the intended rule. Two mounts with the same prefix fail `Start` on every engine.

### D5. A static miss is answered as by the router
On not found and method not allowed, the engine's static handler calls `state.SetRoute("")` and `state.MarkRouterAnswered()` before returning the problem. The record then has no `http.route`, observers see an unmatched request, and the unmatched-record limit counts it. A static `404` is the answer the router would give if static files were part of routing, and with `Static("/")` every scanner path lands there, so the limit would otherwise be useless for SPA servers. `MarkRouterAnswered` also clears the route (an answer of the router means no route matched; /simplify of slice 2), so the engines call only it, and `engine.RequestState` gets the getter `RouterAnswered()` in slice 5, where fibersrvr reads it. fibersrvr's `requestStage` then sets the route after the chain only when `c.Matched() && !state.RouterAnswered()`. A served file or fallback sets `http.route` to the mount pattern in the engine's syntax: net/http's `withRoute` already does; gin's `requestStage`, on a server with a mount, records the containing mount's pattern before consumer middleware runs, without marking the request router-answered; the dispatcher marks a miss, a `405` and a path no mount contains (slice 4, notes.md); fiber reads `c.Route().Path`. The static handler is srvr's code, so the location stays unset and no consumer code location is logged.

**Alternatives:** count static misses as consumer answers (keeps `http.route`, never limited). Rejected: path scanners would bypass the limit.

**Failure modes:** consumer middleware that returns `404` before the static handler runs is not marked, so it is never limited ("Static misses and the unmatched-record limit" rows). That matches `followups-hardening`'s rule. A `500` from the file system is not a miss and keeps `http.route`. On gin, slice 4 checked `unmatched` and `result` against the access and observer rows (notes.md).

### D6. Symbolic links are the file system's business
srvr does not inspect links: it cannot do so portably for an arbitrary `fs.FS`, and doing it per request on `fs.ReadLinkFS` costs an `Lstat` per path element. `os.DirFS` follows links anywhere; `os.Root.FS()` refuses an escape with an error that is not not-exist, which D3 turns into the `500` problem and an `ERROR` record an operator sees. The godoc of every `Static` and the README recommend `root, err := os.OpenRoot(dir)` with `root.FS()`, and warn that `os.DirFS` serves whatever its links point to. "Symbolic links" pins both.

**Alternatives:** refuse every symlink (breaks legitimate deploy layouts, partial on `fs.FS`); an option to follow links (nothing to loosen when srvr does not police them).

**Failure modes:** a consumer keeps `os.DirFS` with a hostile writable directory. That is outside srvr's control and documented. The `500` for an `os.Root` escape is a hypothesis read from Go's source (the escape error is not `fs.ErrNotExist`); slice 2's RED row confirms or corrects it.

### D7. SPA navigation rule
The fallback answers a miss when all hold: method `GET` or `HEAD`; `Accept` lists `text/html` with `q` above zero (media ranges split on `,`, parameters on `;`, compared case-insensitively); the last element of `rest` contains no `.`; the path passed rules 2 and 3 of D3. Common practice: `connect-history-api-fallback` (webpack and Vite dev servers) uses `GET`/`HEAD`, an `Accept` of `text/html` or `*/*`, and the dot rule. nginx `try_files $uri /index.html` and static hosts (Netlify, Vercel rewrites) fall back unconditionally. srvr drops `*/*` because `fetch` sends it by default: an API typo would otherwise get `200` HTML. srvr drops the unconditional form because missing hashed assets would get HTML with a JavaScript name. The fallback response is `200` with `Cache-Control` from `IndexCacheControl` and `Vary: Accept`, since the answer depends on `Accept`. With D4, the fallback only answers what no other route matched.

**Alternatives:** `*/*` counts (API typos get HTML); unconditional (assets get HTML); configurable exclusions now (postponed, notes.md).

The fallback is opened first and its type read from the open file (one name lookup fewer per navigation than `Stat` then `Open`; its name is the consumer's, checked at `Start`, never the request's), on every request, so a fallback deleted or replaced after `Start` is seen at once (/simplify of slice 3).

**Failure modes:** a route segment with a dot (`/users/john.doe`) gets `404` (documented limitation, row pinned). A fallback deleted after `Start` gives `404` and a fallback that cannot be opened gives `500` ("fallback after Start" rows); neither crashes.

### D8. Caching headers are set only once a file is served
The resolver computes `Cache-Control`: `IndexCacheControl` (default `no-cache`) for an index file or the fallback, `CacheControl` (default none) for other files. The engine sets it only after the file opened, so a problem never inherits it (prblm keeps a pre-set `Cache-Control`). net/http and gin set it before `ServeContent`, which keeps it on `304`. fiber sets it after `SendFile` returns `200` or `304`, because fasthttp's `NotModified` resets the response. `SendFile.MaxAge` stays 0, so fiber adds none of its own. Values are validated as header values: no CR, LF, NUL or other control character (D9).

**Failure modes:** a consumer value that is syntactically odd but has no control characters is sent as given (srvr does not parse `Cache-Control`). An empty `IndexCacheControl` means the default, so the zero config stays safe. A consumer cannot send no `Cache-Control` on index files, deliberately (notes.md).

### D9. Configuration, validation and error reporting
`engine.StaticConfig{SPAFallback, CacheControl, IndexCacheControl string; IndexNames []string}` with tags `spa_fallback`, `cache_control`, `index_cache_control`, `index_names` (the `json`/`yaml` snake case and `env` upper case of `Config`). `StaticOption` is `func(*StaticConfig)`. `WithStaticConfig(c)` replaces every field; the other options set one. `Static` applies the options in order to a copy and clones `IndexNames`, then builds the mount. Each invalid field becomes `engine.NewConfigError("Static.<Field>", value, reason)`; a nil option becomes `StaticOption` with its index; all are joined. Prefix rules: non-empty, starts with `/`, no empty, `.` or `..` element, none of `{ } * : % ? # \`, no control character, normalised by dropping one trailing slash. The file system must not be nil (`engine.IsNil`). Index names are single valid path elements. `SPAFallback` must be a valid path naming a regular file. That check calls `fs.Stat` under recover during the configure phase, so a panic or an error other than not-exist names `Static.SPAFallback` with the reason. Errors surface at `Start`, wrapped with `RouteErrorf("static %q (%T): %w", prefix, configurer, err)`, so `errors.Is(err, ErrRouteConfig)`, `errors.Is(err, ErrConfig)` and `errors.As(err, **ConfigError)` all hold. net/http appends them to the builder's errors, which are joined with the other route errors. gin and fiber return them from `configure`.

**Alternatives:** validate in `Register` (it takes any configurer and cannot see a file system the consumer fills before `Start`); panic in `Static` (crashes the caller).

**Failure modes:** a file system that blocks in `Stat` blocks `Start`'s configure phase, which no context bounds. Risk, documented. Only the fallback check touches the file system at `Start`.

### D10. Content type and conditional requests
`Content-Type` comes from `mime.TypeByExtension` of the served name (the fallback's own name for the fallback). It is set before `ServeContent` on net/http and gin, which then do not sniff, and after `SendFile` on fiber. Without an extension the engine sniffs: net/http and fasthttp both use `http.DetectContentType`. Only `If-Modified-Since` is honoured. net/http and gin pass `ServeContent` a clone of the request without `Range`, `If-Range`, `If-Match`, `If-None-Match` and `If-Unmodified-Since`, so no `206`, `412` or `416` plain-text answer can occur. For a file without a modification time (`embed.FS`), the zero time makes `ServeContent` skip `Last-Modified` and `If-Modified-Since`. fiber removes `If-Modified-Since` from the request before `SendFile` and `Last-Modified` from the response after it when the time is zero. Reading fasthttp (`fs.go:1528`, `:2121`) suggests it would otherwise send year-1 `Last-Modified` and answer `304` to any later date. That is hypothesis H1, and slice 5's RED row is its proof (rule 2). `Range` is ignored on fiber because `ByteRange` is false.

**Failure modes:** a system MIME table maps an extension differently from Go's built-in table (for example `.js` as `application/javascript`). Both engines read the same table, so parity holds, but a row's expected value could differ by host. Rows use the extensions Go's built-in table defines, and the CI image is the reference (risk). A header the clone drops would have been honoured by a spec-strict server; this is the documented "preconditions ignored" behaviour, and a later change adds them with ranges.

## Defaults

| Behaviour | Default | Why | Override |
|-----------|---------|-----|----------|
| Methods | `GET`, `HEAD`; others `405` | Static files are read-only | none (a consumer route for the path wins) |
| Directory listing | never | Leaks structure | none |
| Dotfiles and dot-directories | `404` | `.env`, `.git` are common leaks | none now; a consumer route (`/.well-known/...`) wins (notes.md) |
| Index names | `index.html` | Common build output | `WithStaticIndexNames`, `StaticConfig.IndexNames` |
| SPA fallback | off | Serving HTML for misses is a choice | `WithSPAFallback`, `StaticConfig.SPAFallback` |
| `Cache-Control` on index files and fallback | `no-cache` | Deploys must reach users at once | `WithStaticIndexCacheControl`, `StaticConfig.IndexCacheControl` |
| `Cache-Control` on other files | none | Only the consumer knows which names are hashed | `WithStaticCacheControl`, `StaticConfig.CacheControl` |
| Compression, ranges, `ETag` | off | Gall's Law; parity | later change |
| Symlinks | as the `fs.FS` does | srvr cannot police an arbitrary `fs.FS` | choose `os.Root.FS()` |

## Hot Paths

- `engine` resolver: method check, decoding and validation, hidden check, `Stat`, index selection, miss classification, navigation check, open, seekability, header computation, and every error branch (not-exist, invalid, permission, other error, panic, non-seekable, fallback missing or failing).
- net/http static handler: request clone with preconditions removed, `ServeContent`, the route-state change on a miss, the `405` problem, close on every path.
- gin dispatcher: longest-prefix choice, `rest` extraction, `SetRoute` on a hit, the `404` and `405` problems, `ServeContent`.
- fiber static handler: `Params("*")` decoding, `SendFile` with the mount's config, the zero-time header fix, the vanish race mapped to `404`, `Cache-Control` and `Content-Type` after `SendFile`; `requestStage`'s new route rule.
- Configure phase: option application, validation and joining, the fallback `Stat` under recover, gin's and fiber's deferred installation, duplicate prefixes, the route listing.

## Risks / Trade-offs

- [The fiber primitive differs from the brief (`SendFile`, not `middleware/static`)] → D2 gives the reasons; consolidation asks the owner to confirm, and the static-middleware design is kept as the recorded alternative.
- [H1: fasthttp's zero-time `Last-Modified`/`304`] → removed by fibersrvr for zero times; proven by slice 5's RED row before the fix.
- [A `Static("/")` server answers `405` instead of `404` for `POST` to unknown paths] → documented in the proposal and CHANGELOG; still unmatched and limited.
- [fasthttp's own logger may print when a file vanishes between resolution and `SendFile`] → rare race; slice 5 checks where fasthttp's logger writes under fibersrvr and records it in notes.md if it reaches stdout or stderr.
- [Disabling fasthttp's file cache costs throughput for hot assets] → parity and freshness first; slice 5 adds a `BenchmarkPipeline` static row and measures fiber with `CacheDuration: -1` against fiber's default cache; a material gain goes back to the owner with the freshness trade-off (owner asked for the best performance, 2026-11-19).
- [`mime.TypeByExtension` depends on host tables] → rows use built-in extensions; noted in the godoc.
- [A blocking `fs.FS` blocks `Start`] → only the fallback check reads at `Start`; documented.
- [`scrty-integration`'s endpoint-collision check will see static catch-alls] → notes.md entry addressed to it.

## Migration Plan

Additive. Consumers who `Mount` `http.FileServerFS` can switch to `srvr.Static`. Their visible differences are `404` problems instead of plain text, hidden dotfiles, no listings and no `/index.html` redirect, listed in the CHANGELOG. Rollback is removing the `Static` registration.

## Open Questions

- Whether gin's dispatcher should also answer `OPTIONS` with an `Allow` header for CORS preflights: the spec answers `405` on every engine, and preflights are expected to be handled by consumer or scrty middleware before routing. Revisit with `scrty-integration` if its CORS handling needs it.
