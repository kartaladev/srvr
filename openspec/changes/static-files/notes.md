# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (task group 1)
- **Status:** closed: done 2026-11-19
- **Target:** static-files
- **Context:**
  - 1.1: `git fetch`; `origin/main` is still `72e0efd`, as at planning. Nothing merged or archived since: `followups-hardening` archived and released as v0.2.0 before planning; `scrty-integration` active and unmerged (0/45), applied after this change. Work continues on branch `feat/static-files`.
  - 1.2: carry-overs. Named entries: otel-observability "Unmatched 404 problems differ between net/http and gin/fiber": owner decision 2026-11-19 **defer**; it stays open/unassigned and static misses send `prblm.ErrNotFound`'s problem on every engine (neither widens nor fixes the gap). followups-hardening "RequestState.result keeps deriving 'consumer code ran' from Matched": not adopted; checked by this change because D5 clears the route on a static miss, so `ResultInfo.Err` stays nil as for router answers (tasks 2.8, 4.3, 5.4 record what the rows show). Every other open entry addressed to `unassigned` (core-server, gin-and-fiber-engines, otel-observability, followups-hardening, scrty-integration) is unrelated to serving static files and stays unassigned. No entry is addressed to `static-files`.
  - D2 owner answer (2026-11-19): "I need the correct one with best performance." Recorded as: D2 confirmed. `Ctx.SendFile` is the correct primitive (srvr's resolver stays the only authority: `middleware/static` maps permission errors to `404`, passes misses on and cannot serve the fallback) and is not slower than `middleware/static`, which wraps the same `fasthttp.FS`. The remaining performance lever is fasthttp's open-file cache (`CacheDuration`), which trades freshness (static-files "fallback after Start": a replaced file is served fresh). Task 5.6 now benchmarks `CacheDuration: -1` against fiber's default cache; if the cache gains materially, the numbers and the freshness trade-off go back to the owner before anything changes.
  - 1.3: code facts in design Context rechecked in the merged code: `Group.add`/`joinPattern`, `installRoutes`/`conflicts`, `withRoute`, `answerUnmatched`/`markRouterAnswered` (`routing.go`, `server.go`); gin `newEngine`, `requestStage` (marks every request without `FullPath` router-answered), `configure` (`ginsrvr/server.go`); fiber `configure`, `routeOwners.duplicate`, `requestStage` (route from `c.Matched()`), `routerAnswered` (`fibersrvr/server.go`); `engine.RequestState` (`SetRoute`, `Matched`, `MarkRouterAnswered` without getter), `unmatched`, `result`, `Configure`, `IsNil`, `ConfigError`. Dependencies: gin v1.12.0 `NoRoute` rebuilds the 404 chain (`gin.go:326`); fiber v3.7.0 `SendFile` (`res.go:1262`, `FS`, `CacheDuration`, `Compress`, `ByteRange`, `MaxAge`, `maxSendFileHandlers`). The `engine-parity` MODIFIED block equals the main requirement plus the three static rows. `openspec validate static-files --strict` passes.
  - Refreshed: design D2 (owner answer), task 5.6 (cache comparison). Nothing else needed.
- **Evidence:** commands above; `openspec validate static-files --strict`: "Change 'static-files' is valid".
- **Suggested action:** none.

### Planning 2026-11-19: owner decisions and planning findings
- **Status:** closed: recorded
- **Target:** static-files
- **Context:** owner decisions (2026-11-19): serve static files and SPAs on all three engines with one programming model (`srvr.Static`, `ginsrvr.Static`, `fibersrvr.Static` returning each engine's `RouteConfigurer`); shared `StaticOption`/`StaticConfig` in core; the policy in `engine`; "native file server + shared srvr policy"; safe SPA caching default (`no-cache` on index files and the fallback, nothing else unless configured); compression off; this change goes before `scrty-integration`. The change order in `AGENTS.md` and `openspec/config.yaml` now reads `... followups-hardening -> static-files -> scrty-integration`. Planning findings, all read in source or probed, none yet proven by a repository test:
  - net/http (Go 1.27, scratch probe outside the repository): `PathValue` of `{path...}` is percent-decoded per segment (`..%2Fsecret.txt` -> `../secret.txt`, `a%00.txt` -> `a\x00.txt`); raw `..` and `//` targets get ServeMux's `307` to the cleaned path; `%zz` gets `400` from the HTTP server before any handler; `PUT /api/users` beside `POST /api/users` and `GET /{path...}` gets `405` `Allow: GET, HEAD, POST`.
  - gin v1.12.0: a catch-all cannot share a tree level with other segments (`tree.go:353` panics), hence the no-route dispatcher (design D4).
  - fiber v3.7.0: `middleware/static` resolves paths itself and maps permission errors to `404`; `Ctx.SendFile` with `SendFile.FS` serves one named file from the same `fasthttp.FS`. fasthttp compresses `fs.FS` files only in memory (`fs.go:1973`), generates no `ETag` by default, and sends a file's zero modification time unchanged (H1 below).
  - prblm v0.2.1 keeps a `Cache-Control` already on the response, so static headers are set only after a file opened (design D8).
  - **Refinement needing owner confirmation (task 1.2):** design D2 serves fiber with `Ctx.SendFile` instead of `middleware/static`, and net/http and gin with `http.ServeContent` instead of `http.FileServerFS`, so that srvr's resolver is the only authority on which file answers. The static-middleware design is kept in D2 as the alternative.
- **Evidence:** `openspec validate static-files --strict` passes at planning; probe output reproduced in design Context.
- **Suggested action:** consolidation rechecks the code facts and records the owner's answer on D2 (done: see the consolidation outcome).

### H1: fasthttp answers 304 and a year-1 Last-Modified for files without a modification time
- **Status:** open: unconfirmed hypothesis (rule 2)
- **Target:** static-files (task 5.1)
- **Context:** `fasthttp.FS` takes `lastModified` from `fileInfo.ModTime()` (`fs.go:2121`) and answers `304` when `If-Modified-Since` is not before it (`fs.go:1528`). `embed.FS` files report the zero time, so fiber's `SendFile` would send `Last-Modified: Mon, 01 Jan 0001 00:00:00 GMT` and `304` to any `If-Modified-Since`, where net/http's `ServeContent` skips both for a zero time.
- **Evidence:** unverified: read in fasthttp v1.78.0 source; no test yet.
- **Suggested action:** task 5.1 writes the RED row; fix in fibersrvr (design D10) only if it fails.

### os.Root escape is answered 500, not 404
- **Status:** closed: confirmed by task 2.7 (`TestStatic_Symlinks/os.Root_refuses_the_escape`: `500` safe problem, `ERROR` record with the error; `os.DirFS` row serves `SECRET`); the spec row stands
- **Target:** static-files (task 2.7)
- **Context:** design D6 expects `os.Root.FS()` to refuse a symbolic link out of the root with an error that is not `fs.ErrNotExist`, which the resolver turns into the `500` problem and an `ERROR` record. If Go returns a not-exist error instead, the row becomes `404`.
- **Evidence:** unverified: read in Go's `os` documentation; no test yet.
- **Suggested action:** task 2.7 runs the "Symbolic links" row and corrects the spec row through the owner if the status differs.

### Static catch-alls in the route listing and scrty's endpoint-collision check
- **Status:** open
- **Target:** scrty-integration
- **Context:** `scrty-integration` plans a `srvr.WithRouteCheck` check that fails `Start` on full collisions between scrty endpoints and routes (its notes "Catch-all routes collide with every endpoint by default", design D5/D14). This change lists every static mount (`GET /{path...}`, `GET /*filepath`, `GET /*`). With `Static("/")`, every scrty endpoint path is covered by that pattern, although the scrty chain runs outside the router and answers first.
- **Evidence:** design D4 and static-files "Static route listing"; scrty-integration's planned check not yet merged.
- **Suggested action:** in scrty-integration consolidation, treat static mounts as shadowed (warning) rather than colliding, or exclude listing entries from the static configurer types; add rows for `Static("/")` with scrty endpoints.

### Security headers on static responses
- **Status:** open
- **Target:** scrty-integration
- **Context:** static responses carry no `X-Content-Type-Options: nosniff`, CSP or frame options. srvr leaves security headers to scrty. A front end served by srvr is where they matter most.
- **Evidence:** design Non-Goals.
- **Suggested action:** confirm in scrty-integration that the scrty chain's header middleware runs for static requests on every engine (it runs outside the router, so it should), and add a row.

### Byte ranges and the other preconditions
- **Status:** open
- **Target:** unassigned
- **Context:** this change ignores `Range`, `If-Range`, `If-Match`, `If-None-Match` and `If-Unmodified-Since` on every engine (design D10), so media seeking and resumable downloads get the whole file. Adding them needs `416` and `412` answers as problems on every engine, and a decision on multi-range requests (fasthttp sends the whole file).
- **Evidence:** design D10 and Non-Goals.
- **Suggested action:** a later change adds `WithStaticByteRanges` (or default-on ranges) with parity rows.

### Compression and precompressed files
- **Status:** open
- **Target:** unassigned
- **Context:** compression is off on every engine. fasthttp's `Compress` keeps an in-memory cache for `fs.FS` files (no files written next to sources), but net/http has no equivalent. Serving `app.js.br`/`app.js.gz` next to `app.js` is the common SPA approach.
- **Evidence:** fasthttp `fs.go:1973`; design Defaults.
- **Suggested action:** a later change serves precompressed siblings by `Accept-Encoding`, with `Vary: Accept-Encoding`, identically on every engine.

### Dotfile opt-in, ETag, navigation-rule options, directory redirects, index Cache-Control omission, non-seekable files
- **Status:** open
- **Target:** unassigned
- **Context:** postponed by Gall's Law (design Defaults and D3, D7, D8): an explicitly named opt-in for dotfiles such as `/.well-known/` (today a consumer route wins instead); `ETag` generation (no engine sends one); SPA navigation exclusions or `*/*` acceptance; `/dir` to `/dir/` redirects (relative links); a way to send no `Cache-Control` on index files; serving `fs.File` values that are not `io.Seeker` (`archive/zip`), which get `500` today.
- **Evidence:** design decisions named above.
- **Suggested action:** adopt individually when a consumer needs one; each needs parity rows.

### Slice 2: os.DirFS misses answered 500 (ENOTDIR, ENAMETOOLONG)
- **Status:** closed: confirmed, fixed in slice 2 (task 2.6)
- **Target:** static-files
- **Context:** design D3 classified only `fs.ErrNotExist` and `fs.ErrInvalid` as misses and expected "the file system's own name limit gives not-exist or invalid" (D3 Failure modes). On `os.DirFS`, a name below a regular file (`index.html/x`) fails `Stat` with `ENOTDIR` and a 4096-character name with `ENAMETOOLONG`; neither matches `fs.ErrNotExist`, so both got the `500` problem where static-files "Not found" wants `404`. `engine.missOrError` now also treats `syscall.ENOTDIR` and `syscall.ENAMETOOLONG` as misses (both names exist on Windows too: `GOOS=windows go vet` passes). Unit rows "not a directory" and "name too long" in `TestStatic_Resolve`.
- **Evidence:** RED: `go test -race -run TestStatic_NotFound .`: `--- FAIL: TestStatic_NotFound/DirFS/below_a_file: status 500 Cache-Control "no-store", want 404 no-store (body "{\"title\":\"Internal Server Error\",\"status\":500,\"instance\":\"/assets/index.html/x\",...}")` and `--- FAIL: TestStatic_NotFound/DirFS/4096-character_name: status 500 ...`. The MapFS rows passed. GREEN: both pass after the fix.
- **Suggested action:** none.

### Slice 2: Resolve reads Stat before Open (design D3 kept for FIFOs and devices)
- **Status:** closed: decision recorded
- **Target:** static-files
- **Context:** opening first and reading the open file's `Stat` would save one file system call per request (owner asked for the best performance, D2). It is not done: `os.DirFS` would open a FIFO or device named by the request before learning its type, and opening a FIFO for reading blocks until a writer appears, so one request could hang a handler goroutine. `Stat` first rejects anything that is not a regular file or directory without opening it (D3 rule 4).
- **Evidence:** design D3; not measured.
- **Suggested action:** none; revisit only with a benchmark showing the extra `Stat` matters.

### Slice 2: Faulty file system rows named error.chain for the error's text
- **Status:** closed: spec wording corrected (task 2.7)
- **Target:** static-files
- **Context:** the "Faulty file system" and "Symbolic links" rows said the `http.request.failed` record has "`error.chain` containing `permission denied`". In the merged log schema `error.chain` lists the chain's Go types (`[*fs.PathError *errors.errorString]`) and the text is `exception.message` (`open app.js: permission denied`). The rows now name `exception.message`; the requirement says the record carries the error (`error.chain`, `exception.message`). No key is added and no behaviour changed.
- **Evidence:** first run of `go test -race -run TestStatic_FaultyFS .`: `error.chain [*fs.PathError *errors.errorString] does not contain "permission denied"`; record printed: `error.chain:[*fs.PathError *errors.errorString] ... exception.message:open app.js: permission denied`. `openspec validate static-files --strict` passes after the edit.
- **Suggested action:** none.

### Slice 2: net/http static patterns are registered for every method (owner decision 2026-11-19)
- **Status:** closed: owner decision applied (task 2.9)
- **Target:** static-files
- **Context:** design D4 registered `GET {prefix}/{path...}`. Two proofs from the "Conflicts with other routes" rows: (1) `Mount("/", h)` beside `Static("/")` did not conflict: ServeMux found `GET /{path...}` more specific than the method-less `/`, so the static mount answered every `GET` and the registered route lost; (2) `Mount("/api/", h)` beside `Static("/")` failed `Start`, because each pattern is more specific in one dimension (path or method), so an SPA at `/` could not sit beside any mounted handler or `Handle("", ...)` route. Asked the owner: "GET only (as planned)" or "every method". Decision: **every method**. The mount registers `{prefix}` and `{prefix}/{path...}` method-less, so every other route wins; it answers other methods with the `405` problem, whose `Allow` lists `GET, HEAD` and every method another route serves for the path (probed on the ServeMux, only on that path), keeping the spec's `Allow: GET, HEAD, POST` row. The listing still shows `GET`. Spec "Conflicts with other routes" net/http rows: `GET /{path...}`, `GET /` and `GET /{$}` now start with the route winning; `Mount("/api/")` starts; `Mount("/")` and `Mount("/assets/")` beside `Static("/assets")` conflict. engine-parity: the catch-all exception row is now fiber's (net/http behaves as gin). Design D4 updated.
- **Evidence:** RED: `go test -race -run TestStatic_Conflicts .`: `TestStatic_Conflicts/mount_at_the_root: after 10s: Start did not return` (no conflict); `TestStatic_NoConflict` with GET-only patterns: `Start returned before readiness: srvr: invalid route configuration: "/api/" (srvr_test.configFunc) conflicts with "GET /{path...}" (*srvr.staticConfigurer)`. GREEN: `TestStatic_Conflicts`, `TestStatic_NoConflict`, `TestStatic_Methods` and the whole root suite (`go test -race .`) pass.
- **Suggested action:** none.

### net/http and gin advertise Accept-Ranges while ranges are ignored
- **Status:** open
- **Target:** unassigned (with "Byte ranges and the other preconditions")
- **Context:** `http.ServeContent` always sets `Accept-Ranges: bytes` (Go 1.27 `net/http/fs.go:398`), although srvr strips `Range` before calling it (design D10), so a client is told ranges work and then gets `200` with the whole file. That is allowed (a server may ignore `Range`), but misleading, and fiber with `ByteRange: false` does not send the header, so it is a header difference between engines that no spec row names. Removing it needs a response-writer wrapper on the serving path (an allocation per request) or serving without `ServeContent`.
- **Evidence:** unverified by a test: read in the Go source; no spec row asserts `Accept-Ranges`.
- **Suggested action:** decide together with byte ranges: serving ranges makes the header true; otherwise strip it with a parity row.

### ServeContent writes a plain-text 500 when Seek fails
- **Status:** open
- **Target:** unassigned
- **Context:** `http.ServeContent` seeks to the end and back to learn the size, and answers a failing `Seek` with `http.Error(w, "seeker can't seek", 500)` (plain text, not a problem). srvr checks only that the file implements `io.Seeker` (design D2 failure modes). A file system whose `Seek` fails (a broken network file system, a consumer `fs.File` with a faulty `Seek`) would get a plain-text 500 and an access record without srvr's failure. Not in this change's spec.
- **Evidence:** unverified: read in the Go source (`serveContent`); no test yet.
- **Suggested action:** add a "Faulty file system" row with a failing `Seek` (RED), then seek in the resolver or the engine before `ServeContent` and return the error as the 500 problem, measuring the extra seek.

### Slice 2: /simplify (task 2.12)
- **Status:** closed: done
- **Target:** static-files
- **Context:** four review angles (reuse, simplification, efficiency, altitude) over the slice's diff. Applied: `StaticResult` keeps one `File` field typed `engine.StaticFileContent` (`fs.File` plus `io.Seeker`) instead of `File` and `Content`; `Vary` and `Resolve`'s `accept` parameter removed until the fallback (slice 3) needs them; the default index name is filled in once by `NewStatic`; `missOrError` uses `hasCycle` and a package-level miss list (no allocation per miss); `Resolve`'s two-case switch became two `if`s; the route listing reads a `listMethod` the registration passes (`Group.addListed`) instead of a `static` flag back-patched by index; the ServeMux and method list for the static `405` live in a small `served` value the handler captures, so the configure-phase builder is not kept alive by handlers, the list is built only when a static mount exists, and `allow` copies the request once; `MarkRouterAnswered` clears the route itself, so a static miss calls the existing `markRouterAnswered` (no `staticMiss` copy) and the `RouterAnswered` getter moves to slice 5, where fibersrvr first reads it (Gall's Law); the duplicate-prefix key uses `joinPattern`; tests reuse `configFields` (extended to single `Unwrap`), `selfError`, an inline conflict row type, `file`-only serve rows and one `embed.FS` subtest. Design D3 and D5 updated to the code. Skipped: bypassing `http.ServeContent` to save its two size seeks per served file (it would re-implement `HEAD`, `If-Modified-Since` and sniffing; measured cost about 2 syscalls against `Stat`+`Open`, efficiency review's benchmark: a served file about 97 µs, 474 B, 7 allocs on `os.DirFS`); one combined pass over the request path in `requestName` (nanoseconds); merging `staticServer` into `loggedServer` (different arguments); sharing `faultFS`/`noSeekFile` across the two test packages (needs an exported test-support package).
- **Evidence:** after the changes `go vet`, `golangci-lint run` (0 issues) and `go test -race . ./engine` pass.
- **Suggested action:** none.
