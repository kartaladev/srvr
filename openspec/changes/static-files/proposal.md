# Proposal

## Why

Services built on srvr often ship a web front end with the API: a React or Vite build directory, from `os.DirFS` or an `embed.FS`. Today a consumer can only `Mount` `http.FileServerFS` on net/http. It lists directories, serves dotfiles, sends plain-text 404s instead of problems, and has no single-page-app fallback. On gin and fiber each team wires the engine's own static helper, and each helper behaves differently. The owner decided on 2026-11-19 to add one safe static-file and SPA programming model for all three engines, applied after `followups-hardening` and before `scrty-integration`, so the security bridge is planned against a server that already serves front ends.

## What Changes

- **New configurers** `srvr.Static(prefix, fsys, opts...)`, `ginsrvr.Static(...)` and `fibersrvr.Static(...)` serve the files of an `fs.FS` under a URL prefix, registered like any route configurer: `s.Register(srvr.Static("/", site, srvr.WithSPAFallback("index.html")))`.
- **Shared configuration in core**: `srvr.StaticOption` options (`WithSPAFallback`, `WithStaticCacheControl`, `WithStaticIndexCacheControl`, `WithStaticIndexNames`, `WithStaticConfig`) and a plain `srvr.StaticConfig` with `json`/`yaml`/`env` tags. Both are aliases of `engine` declarations, like the other shared types.
- **Shared policy in `engine`**: one resolver decides, for every engine, which file answers a request (path decoding and validation, hidden dotfiles, index files, SPA fallback) and which headers it gets (`Content-Type`, `Cache-Control`, `Vary`). The engines only send the bytes with their native file primitives: `http.ServeContent` on net/http and gin, fiber's `Ctx.SendFile` with `SendFile.FS` on fiber (design D2 explains why this differs from the static middleware in the original brief).
- **Answers**: `GET` and `HEAD` only (`405` problem with `Allow: GET, HEAD` otherwise). No directory listing, ever. A missing or hidden file gets srvr's `404` problem through prblm. A file system error other than "not found" gets the safe `500` problem. If-Modified-Since is honoured; Range and the other preconditions are ignored for now.
- **Routing**: other registered routes always win over static files and the fallback, on every engine. The longest static prefix owns a request. A static miss is logged and counted like a request no route matched.
- **Route listing**: each static mount is listed with its configurer, so route checks see it.
- **engine-parity**: the documented differences gain the static rows (see Capabilities).
- **Change order**: AGENTS.md and `openspec/config.yaml` list `... followups-hardening -> static-files -> scrty-integration` (owner decision 2026-11-19).

## Defaults and Customization

- Zero configuration (`srvr.Static("/assets", fsys)`): `GET`/`HEAD` of regular files below the prefix. `index.html` answers a directory. No SPA fallback. No `Cache-Control` on ordinary files. `Cache-Control: no-cache` on index files. Dotfiles and dot-directories answer `404`, as if they did not exist. Directories are never listed. Compression is off. Range requests get the whole file.
- Nothing leaks to clients: a `404` or `500` is prblm's problem with no file name, file system path or error text. The error chain, including the name inside the file system, goes only to the `http.request.failed` record. No new log key is written. No request body, credentials or query string are logged (unchanged access-log rules).
- Opt-in: `WithSPAFallback(file)` serves `file` (`200`, `Cache-Control: no-cache`, `Vary: Accept`) for a `GET`/`HEAD` navigation request that matches no file: its `Accept` lists `text/html` and its last path segment has no `.`. `WithStaticCacheControl(v)` sets `Cache-Control` for non-index files, such as `public, max-age=31536000, immutable` for hashed assets. `WithStaticIndexCacheControl(v)` replaces `no-cache` on index files and the fallback. `WithStaticIndexNames(names...)` replaces `index.html`. `WithStaticConfig(c)` sets every field from a config struct.
- No option loosens a safety default: dotfiles, directory listing, compression and symlink following stay as above. Symlink confinement is the file system's job; the godoc recommends `os.OpenRoot(dir)` with `Root.FS()` over `os.DirFS` (design D6).

## Capabilities

### New Capabilities
- `static-files`: serving an `fs.FS` under a prefix on every engine. It covers configuration and its validation, file resolution and path safety, missing files and file system errors as problems, the SPA fallback, caching headers, methods, precedence of other routes, logging, observation and limiting of static requests, and the route listing.

### Modified Capabilities
- `engine-parity`: "Parity exceptions are explicit" gains the static rows: fiber refuses a consumer catch-all beside a mount and has an opt-in file cache, and net/http answers a path with dot segments with ServeMux's redirect to the cleaned path.

## Scope by Gall's Law

The simplest version that works end to end is `srvr.Static` on net/http. It serves files with the safe defaults, answers misses and file system errors with problems, and logs and counts misses like unmatched requests. The SPA fallback and caching headers come next, still on net/http. Then gin and then fiber each get the same model on the shared `engine` policy. The parity suite and the documentation close the change, and the release follows after owner confirmation.

Deliberately left for later (each recorded in `notes.md`): byte ranges and the preconditions other than If-Modified-Since, precompressed or on-the-fly compressed files, an opt-in for dotfiles such as `/.well-known/` (a consumer route already wins over static), a configurable SPA navigation rule (prefix exclusions, `*/*`), `ETag` generation, `X-Content-Type-Options` and other security headers (`scrty-integration`'s job), and directory redirects.

## Impact

- Compatibility decisions:
  - Public API (additive): `srvr.Static`, `srvr.StaticOption`, `srvr.StaticConfig`, the five `With*` static options; `ginsrvr.Static`, `fibersrvr.Static`; in `engine` (unstable before v1) the static resolver, its config and option types, and a request-state accessor for "answered by the router", which fiber needs (design D4).
  - Defaults: no existing default changes. A server without a static configurer behaves exactly as v0.2.0. With `Static("/")` registered, a request with any method other than `GET`/`HEAD` to a path no other route matches gets `405` with `Allow: GET, HEAD` instead of `404`. That is the router's answer for a path it now serves.
  - Log schema: no key added, renamed or removed; `srvr.log.schema` stays `1`. Static hits carry `http.route` in each engine's syntax. Static misses carry none, as for unmatched requests.
  - Module dependency sets: unchanged. The core stays standard library plus prblm at runtime (`io/fs`, `mime`, `net/http`). ginsrvr uses gin and net/http only. fibersrvr uses fiber's `Ctx.SendFile`, already in `github.com/gofiber/fiber/v3` v3.7.0. No `middleware/static` import is needed.
  - Version: the release slice suggests `v0.3.0` (new API, pre-1.0 minor).
- Code: new `static.go` in the root module, `engine/static.go`, `ginsrvr/static.go`, `fibersrvr/static.go`, configure-phase changes in `ginsrvr` (NoRoute dispatch) and `fibersrvr` (deferred install, route-state rule), `internal/paritytest`, `README.md`, `CHANGELOG.md`, `LOGGING.md` (static misses and the unmatched limit), godoc.
- Builds on, for consolidation to check:
  - `core-server` and `gin-and-fiber-engines`: `Group.add`/`joinPattern` and `installRoutes`' named ServeMux conflicts; `answerUnmatched` replaying ServeMux's redirects; gin's `allNoRoute` chain running srvr's `e.Use` stages and `requestStage` marking requests with no `FullPath` as router-answered; fiber's `configure` loop, `routeOwners` and duplicate check, and `requestStage` setting the route from `c.Matched()` after the chain.
  - `followups-hardening` (v0.2.0): `RequestState.MarkRouterAnswered` and `unmatched` (router-answered, failed, not panicked, 404/405); `WithUnmatchedRecordLimit`; the open entry "Unmatched 404 problems differ between net/http and gin/fiber". Static misses send `prblm.ErrNotFound`'s problem on every engine, so this change neither widens nor fixes that gap.
  - prblm v0.2.1 keeps a `Cache-Control` already set on the response, so static headers must be set only once a file is served.
- Affects `scrty-integration`, applied next: its endpoint-collision route check will see static catch-all patterns in the listing (notes.md entry addressed to it).
