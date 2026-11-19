# Tasks

## 1. Consolidation: refresh plan against merged changes

- [x] 1.1 Sync with the main branch (`git fetch`, rebase or merge `origin/main` into the working branch) and list every change merged or archived since this change was planned on 2026-11-19 (`openspec list --json`, `openspec/changes/archive/`, `git log origin/main`); verify they are named in the completion note (at planning: `main` at `72e0efd`, `followups-hardening` archived and released as v0.2.0, `scrty-integration` active and unmerged, applied after this change)
- [x] 1.2 Read every `notes.md` carry-over (archived changes and `scrty-integration`) and decide each open entry addressed to `static-files` or `unassigned`; verify none is left undecided. To decide here: `otel-observability` "Unmatched 404 problems differ between net/http and gin/fiber" (static misses send `prblm.ErrNotFound` on every engine and neither widen nor fix the gap; defer unless the owner picks the canonical body now), `followups-hardening` "RequestState.result keeps deriving 'consumer code ran' from Matched" (touched by D5: check that static misses, which clear the route, leave `ResultInfo.Err` as for router answers). Ask the owner to confirm design D2 (fiber `Ctx.SendFile` instead of `middleware/static`) and record the answer
- [x] 1.3 Refresh this change's proposal, specs, design and tasks against the main specs and merged code: recheck with gopls the code facts in design Context (`Group.add`, `installRoutes`, `answerUnmatched`, gin `requestStage`/`configure`/`newEngine`, fiber `configure`/`requestStage`/`routerAnswered`, `RequestState`, `engine.Configure`); re-read gin's catch-all conflict and `NoRoute`, and fiber's `SendFile` and router `next` for the versions in `go.mod`; re-diff the `engine-parity` MODIFIED block against `openspec/specs/engine-parity/spec.md`; verify `openspec validate static-files --strict` passes
- [x] 1.4 Record the outcome of 1.1-1.3 at the top of this change's `notes.md` (replace the placeholder entry); verify the file lists what was refreshed and adopted, or states that nothing was needed
- [x] 1.5 Commit the consolidation as `docs(openspec): consolidate static-files against main`, containing only `openspec/` files; skip it when 1.1-1.4 changed nothing; verify with `git log -1 --stat`

## 2. Slice: net/http serves files from an fs.FS with safe defaults

- [x] 2.1 Load `golang-how-to` (and through it testing, safety, security, error handling, naming, documentation); confirm the root module needs no new dependency (`io/fs`, `mime`, `net/http`, `net/url`, `unicode/utf8` only); verify `go list -deps ./ | grep -v '^[a-z]*$'` shows no new non-standard, non-prblm import after the slice
- [x] 2.2 Static configuration is validated and reported at `Start` (static-files / Scenario: Rejected static configuration, every row except `SPAFallback`, `CacheControl` and `IndexCacheControl`; Scenario: Several invalid settings, with the prefix and FS fields only); verify `go test -race -run 'TestNewStatic|TestStatic_Config' ./engine .` passes
  - RED: `TestNewStatic_Config` (`engine/static_internal_test.go`), one case per row, asserting `errors.As` to `*ConfigError` with the row's `Field` and every field of the joined error; `TestStatic_ConfigErrors` (root, `static_test.go`) through `Start` for the net/http rows, asserting `ErrRouteConfig`, `ErrConfig`, `Ready()` never closed and a later `Shutdown` returning `nil`. Expected failure: undefined `engine.NewStatic` / `srvr.Static` (a compile failure in the new test file only; confirm the rest of the package still builds)
  - GREEN: design D9: `engine.StaticConfig`, `StaticOption`, `NewStatic` (prefix rules, `IsNil` FS, index names, nil options, joined errors); `srvr.Static` and the aliases; `Static.Configure` appends `RouteErrorf("static %q (%T): %w", ...)` to the builder; duplicate joined prefixes are detected in the builder
  - REFACTOR: one validation function per field (Extract Function), named for the field; then `/simplify`
- [x] 2.3 Options and the config struct apply in order and are copied (static-files / Scenario: Configuration sources, the index-name rows and the "changed after `Static` returned" row); verify `go test -race -run TestStatic_ConfigSources .` passes
  - RED: `TestStatic_ConfigSources`, net/http rows; fails until index names are honoured
  - GREEN: `WithStaticIndexNames`, `WithStaticConfig`, clone of `IndexNames`
  - REFACTOR: none expected beyond `/simplify`
- [x] 2.4 The resolver decides every answer (static-files / Scenarios: Serving a file, Not found, Hostile paths, Faulty file system, Other methods; decision level, no HTTP); verify `go test -race -run TestStatic_Resolve ./engine` passes
  - RED: `TestStatic_Resolve` (`engine/static_internal_test.go`), one case per row of those scenarios expressed as `(method, rest)` → decision (kind, name, `Content-Type`, `Cache-Control`, `Vary`, error kind), including the fault rows with a fake `fs.FS` that returns `fs.ErrPermission`, a plain error from `Stat`, a typed-nil error, a non-seeker file, a wrapped `fs.ErrNotExist`, and panics; plus a `FuzzStaticResolve` target asserting that a served name is always `fs.ValidPath`, has no hidden element, and is never served for a `rest` containing `..` after decoding
  - GREEN: design D3 rules 1-6 without the fallback (step 5 always "not found"), D10's content type
  - REFACTOR: look for the conditional ladder smell in the rule order (Decompose Conditional, one predicate per rule); then `/simplify`
- [x] 2.5 net/http serves the decision (static-files / Scenario: Serving a file, net/http rows; Scenario: Conditional and range requests, net/http rows); verify `go test -race -run 'TestStatic_Serve|TestStatic_Conditional' .` passes
  - RED: `TestStatic_Serve` and `TestStatic_Conditional` (`httptest`, `fstest.MapFS`, `os.DirFS` with a fixed `os.Chtimes` time, and an `embed.FS` from `testdata/static/`), one case per row
  - GREEN: `Static.Configure` registers `GET {prefix}` and `GET {prefix}/{path...}` with a handler that resolves, clones the request without `Range`, `If-Range`, `If-Match`, `If-None-Match`, `If-Unmodified-Since`, sets `Content-Type` and calls `http.ServeContent`; closes the file on every path
  - REFACTOR: Extract Function for the precondition-stripping clone; then `/simplify`
- [x] 2.6 Misses, hidden files, hostile paths and other methods are problems (static-files / Scenarios: Not found, Hostile paths, Other methods, net/http rows); verify `go test -race -run 'TestStatic_NotFound|TestStatic_Hostile|TestStatic_Methods' .` passes
  - RED: one test per scenario; the hostile rows build raw request targets with `http.ReadRequest` so no client cleans them, and assert the body never contains `SECRET` or `<html>app</html>`; the `307` rows pin ServeMux's own redirect
  - GREEN: map "not found" to `prblm.ErrNotFound` and "method not allowed" to the `405` problem with `Allow: GET, HEAD` (only reachable through a direct `Configure` on a group whose mux routes other methods here; ServeMux answers the rest)
  - REFACTOR: none expected beyond `/simplify`
- [x] 2.7 File system errors are safe `500` problems (static-files / Scenario: Faulty file system, net/http; Scenario: Symbolic links, net/http); verify `go test -race -run 'TestStatic_FaultyFS|TestStatic_Symlinks' .` passes
  - RED: fake `fs.FS` rows plus a `t.TempDir()` symlink (skipped with a reason where the OS cannot create symlinks); assert the problem body has no error text or path and the record's `error.chain`; record in `notes.md` whether the `os.Root` escape row gives `500` (design D6 hypothesis)
  - GREEN: return the resolver's error from the handler; the existing pipeline renders and logs it
  - REFACTOR: none expected beyond `/simplify`
- [x] 2.8 Static misses are answered as by the router (static-files / Scenario: Static access records, Static misses and the unmatched-record limit, Observers see static requests, Consumer middleware around static files; net/http rows); verify `go test -race -run 'TestStatic_Access|TestStatic_Unmatched|TestStatic_Middleware' .` and the net/http row of the observer test pass
  - RED: the net/http rows, with the fake clock of `TestUnmatchedLimit` and a recording observer; expected failure before GREEN: the miss record has `http.route` `/assets/{path...}` and `3` records are written instead of `1`
  - GREEN: design D5: `SetRoute("")` and `MarkRouterAnswered()` on a miss; add `RequestState.RouterAnswered()` with its own unit row in `engine`
  - REFACTOR: compare with `markRouterAnswered` in `server.go` and share one helper if the shapes match (Extract Function); then `/simplify`
- [x] 2.9 Precedence, conflicts, lifecycle misuse and listing on net/http (static-files / Scenario: Precedence (rows without the fallback), Conflicts with other routes, Static configurer lifecycle misuse, Static route listing, Partial failure while serving; net/http rows); verify `go test -race -run 'TestStatic_Precedence|TestStatic_Conflicts|TestStatic_Lifecycle|TestStatic_Routes|TestStatic_Partial' .` passes
  - RED: one test per scenario; the client-disconnect row uses a raw connection closed after `64 KiB`; the concurrency row uses 50 goroutines under `-race`
  - GREEN: none expected beyond 2.2-2.8 (ServeMux precedence, `installRoutes` conflicts); fix only what a row proves
  - REFACTOR: none expected beyond `/simplify`
- [x] 2.10 Documentation: godoc on `Static`, the options and `StaticConfig` (each default and why, the `os.OpenRoot` recommendation, root-relative links, no redirects, preconditions ignored), an `ExampleStatic` with `embed.FS`, a README "Static files and single-page apps" section (net/http), and a `CHANGELOG.md` `## [Unreleased]` "Added" entry; verify `go test -run Example .` and `go vet ./...` pass and `go doc . Static` shows the defaults
- [x] 2.11 Verify the slice end to end: `make check MODULES=". internal/paritytest"` passes (`GOWORK=off`); `go test -coverpkg=./... -coverprofile=c.out ./... && go tool cover -func=c.out` shows every function of `engine/static.go` and `static.go` covered, including each error branch listed in design Hot Paths; goleak passes; `gopls` diagnostics report nothing on changed files
- [x] 2.12 Run `/simplify` on the slice's changes, apply what holds up, re-run the tests, then commit `feat(core): serve static files from an fs.FS`; verify with `git log -1 --stat`

## 3. Slice: SPA fallback and caching headers on net/http

- [ ] 3.1 Fallback and cache settings are validated (static-files / Scenario: Rejected static configuration, the `SPAFallback`, `CacheControl` and `IndexCacheControl` rows; Scenario: Several invalid settings, the full row); verify `go test -race -run 'TestNewStatic_Config|TestStatic_ConfigErrors' ./engine .` passes
  - RED: add the rows; the panicking and `fs.ErrPermission` `Open` rows use a fake `fs.FS`; expected failure: options undefined, then no error for a missing fallback
  - GREEN: `WithSPAFallback`, `WithStaticCacheControl`, `WithStaticIndexCacheControl`; header-value validation; the fallback `fs.Stat` under recover (design D9)
  - REFACTOR: share the header-value check between both cache fields (Extract Function); then `/simplify`
- [ ] 3.2 The navigation rule (static-files / Scenario: Fallback decision, every row; decision level); verify `go test -race -run TestStatic_Resolve ./engine` passes
  - RED: add the fallback rows to `TestStatic_Resolve`, plus `TestAcceptsHTML` unit rows for media-range parsing (case, `q=0`, `q=0.1`, `*/*`, `text/*`, empty, malformed `;q=x`)
  - GREEN: design D7 in step 5 of D3, with `Vary: Accept`
  - REFACTOR: none expected beyond `/simplify`
- [ ] 3.3 Cache headers on files, index files and the fallback (static-files / Scenario: Cache headers, every row; Scenario: Configuration sources, remaining rows; net/http); verify `go test -race -run 'TestStatic_CacheHeaders|TestStatic_ConfigSources' .` passes
  - RED: the rows, including the `404` rows asserting `Cache-Control: no-store` (prblm's) and the `304` row
  - GREEN: design D8: set `Cache-Control` only after the file opened, before `ServeContent`
  - REFACTOR: none expected beyond `/simplify`
- [ ] 3.4 Fallback over HTTP (static-files / Scenarios: Fallback decision, Fallback under a prefix, Precedence (fallback rows); net/http rows, including the deleted, replaced and unreadable fallback after `Start`); verify `go test -race -run 'TestStatic_Fallback|TestStatic_Precedence' .` passes
  - RED: the rows; expected failure before GREEN: `404` problems where `200` is expected
  - GREEN: none beyond 3.2 and 3.3 expected; fix only what a row proves
  - REFACTOR: none expected beyond `/simplify`
- [ ] 3.5 Documentation: godoc of the three options (the navigation rule, why `*/*` does not count, the dot rule, `Vary: Accept`, the `no-cache` default and why), `ExampleWithSPAFallback`, the README section's SPA part, `CHANGELOG.md` entry; verify `go test -run Example .` passes
- [ ] 3.6 Verify the slice end to end: `make check MODULES=". internal/paritytest"` passes; `go tool cover -func` shows the fallback, header and validation branches covered; `gopls` diagnostics clean
- [ ] 3.7 Run `/simplify`, apply what holds up, re-run the tests, then commit `feat(core): add the SPA fallback and static caching headers`; verify with `git log -1 --stat`

## 4. Slice: gin serves static files from its no-route chain

- [ ] 4.1 `ginsrvr.Static` validation and lifecycle (static-files / Scenarios: Rejected static configuration, Several invalid settings, Static configurer lifecycle misuse; gin rows); verify `go test -race -run 'TestStatic_Config|TestStatic_Lifecycle' ./...` in `ginsrvr` passes
  - RED: `ginsrvr/static_test.go` rows; the direct-`Configure` row expects the `Server.Register` message
  - GREEN: design D4 (gin): `ginsrvr.Static` returns a configurer holding the `engine` mount and its error; `configure` recognises it (unexported interface), returns its error, and after the loop installs one dispatcher with `e.NoRoute`; `Configure` panics with the guidance message
  - REFACTOR: none expected beyond `/simplify`
- [ ] 4.2 gin serves, misses and refuses like net/http (static-files / Scenarios: Serving a file, Conditional and range requests, Not found, Hostile paths, Faulty file system, Symbolic links, Other methods, Fallback decision, Fallback under a prefix, Cache headers, Configuration sources; gin rows); verify `go test -race -run 'TestStatic_' ./...` in `ginsrvr` passes
  - RED: the gin rows with `httptest`; the `PUT /api/users` row pins gin's `Allow: POST` (documented exception)
  - GREEN: the dispatcher: longest prefix, `rest` from `c.Request.URL.Path`, the resolver, `ServeContent` on `c.Writer` with the stripped clone, `c.Error(prblm.ErrNotFound)` and abort for a miss, the `405` problem, `SetRoute` on a hit
  - REFACTOR: compare the request-clone and header-setting code with the root module's and move the shared part to `engine` if it is engine-independent (Move Function); then `/simplify`
- [ ] 4.3 Precedence, conflicts, records, the limit, observers, middleware and listing on gin (static-files / Scenarios: Precedence, Conflicts with other routes, Static access records, Static misses and the unmatched-record limit, Observers see static requests, Consumer middleware around static files, Static route listing, Partial failure while serving; gin rows); verify `go test -race -run 'TestStatic_' ./...` in `ginsrvr` passes
  - RED: the gin rows; check `RequestState.result`'s `Err` for a static miss against the request-observer rows (design D5 failure modes) and record the outcome in `notes.md`
  - GREEN: synthesise the listing entries (`GET {prefix}`, `GET {prefix}/*filepath`) attributed to the static configurer's type, placed after the other routes; `NoRoute` is called after the configure loop, so its rebuilt chain includes the root group's consumer middleware (design D4); add a row proving a server without a static mount keeps the followups-hardening unmatched rows unchanged (no consumer middleware in the no-route chain)
  - REFACTOR: none expected beyond `/simplify`
- [ ] 4.4 Documentation: `ginsrvr` godoc (`Static`, why no-route, the gin catch-all exception), README engine table, `CHANGELOG.md` entry; verify `go test -run Example ./...` in `ginsrvr` passes
- [ ] 4.5 Verify the slice end to end: `make check MODULES=ginsrvr` passes; `make cover` shows the dispatcher, its installation and the shared `engine` static code covered from ginsrvr's tests; `gopls` diagnostics clean
- [ ] 4.6 Run `/simplify`, apply what holds up, re-run the tests, then commit `feat(ginsrvr): serve static files and single-page apps`; verify with `git log -1 --stat`

## 5. Slice: fiber serves static files with Ctx.SendFile

- [ ] 5.1 Prove or drop H1 (design D10): fasthttp sends year-1 `Last-Modified` and answers `304` for an `embed.FS` file; verify `go test -race -run TestStatic_Conditional ./...` in `fibersrvr` passes after GREEN
  - RED: the `embed.FS` row of "Conditional and range requests" on fiber, with `SendFile` and no srvr header fix; record the failing output in `notes.md` (expected: `status 304, want 200` and a `Last-Modified: Mon, 01 Jan 0001 00:00:00 GMT`). If it passes, record "not confirmed", keep the row, and skip the fix in 5.3
  - GREEN: in 5.3
  - REFACTOR: none
- [ ] 5.2 `fibersrvr.Static` validation, lifecycle and deferred installation (static-files / Scenarios: Rejected static configuration, Several invalid settings, Static configurer lifecycle misuse, Conflicts with other routes; fiber rows); verify `go test -race -run 'TestStatic_Config|TestStatic_Lifecycle|TestStatic_Conflicts' ./...` in `fibersrvr` passes
  - RED: `fibersrvr/static_test.go` rows with `app.Test`
  - GREEN: design D4 (fiber): `configure` sets static configurers aside, installs them after the loop longest prefix first (`app.Get(prefix)`, `app.Get(prefix+"/*")`) with their owner recorded, before `owners.duplicate`; `Configure` panics with the guidance message
  - REFACTOR: none expected beyond `/simplify`
- [ ] 5.3 fiber serves, misses and refuses like net/http (static-files / every serving, miss, hostile, fault, symlink, method, fallback, cache and configuration scenario; fiber rows); verify `go test -race -run 'TestStatic_' ./...` in `fibersrvr` passes
  - RED: the fiber rows with `app.Test`; raw targets for the hostile rows
  - GREEN: the handler: `rest` from `c.Params("*")` with `url.PathUnescape`, the resolver, `c.SendFile(name, mount's SendFile{FS, CacheDuration: -1})`, the zero-time fix (H1), `Content-Type` and `Cache-Control` after `SendFile`, `SendFile`'s 404 mapped to a miss, `prblm.ErrNotFound` and the `405` problem returned for srvr's renderer
  - REFACTOR: none expected beyond `/simplify`
- [ ] 5.4 Records, limit, observers, middleware and listing on fiber (static-files / Scenarios: Static access records, Static misses and the unmatched-record limit, Observers see static requests, Consumer middleware around static files, Static route listing, Precedence, Partial failure while serving; fiber rows); verify `go test -race -run 'TestStatic_' ./...` in `fibersrvr` passes
  - RED: the fiber rows; expected failure before GREEN: the miss record has `http.route` `/*` because `requestStage` sets it from `c.Matched()`
  - GREEN: design D5: `requestStage` sets the route only when `c.Matched() && !state.RouterAnswered()`
  - REFACTOR: none expected beyond `/simplify`
- [ ] 5.5 Check where fasthttp's logger writes when a file vanishes between resolution and `SendFile` under fibersrvr (design Risks); verify by a test row or a recorded probe in `notes.md`, with a follow-up entry if it reaches stdout or stderr
- [ ] 5.6 Add a `BenchmarkPipeline` static row on every engine (`GET /assets/app.js`); on fiber also compare `CacheDuration: -1` with fiber's default cache (`benchstat`, 10 runs) and record the numbers in `notes.md`, stopping for the owner if the cache gains 10% or more `ns/op` (owner decision 2026-11-19, D2); verify `go test -run '^$' -bench 'Pipeline/static' -benchmem -benchtime=1x ./...` passes in each touched module
- [ ] 5.7 Documentation: `fibersrvr` godoc (`Static`, `SendFile` with `CacheDuration: -1`, no compression), README engine table, `CHANGELOG.md` entry; verify `go test -run Example ./...` in `fibersrvr` passes
- [ ] 5.8 Verify the slice end to end: `make check MODULES=fibersrvr` passes; `make cover` shows the fiber handler, the deferred installation and `requestStage`'s new branch covered; `gopls` diagnostics clean
- [ ] 5.9 Run `/simplify`, apply what holds up, re-run the tests, then commit `feat(fibersrvr): serve static files and single-page apps`; verify with `git log -1 --stat`

## 6. Slice: static parity and documented exceptions

- [ ] 6.1 One parity table for every "every engine" row (static-files, all scenarios run on every engine); verify `go test -race -run TestStaticParity .` in `internal/paritytest` passes
  - RED: `TestStaticParity` (`internal/paritytest/static_test.go`) builds the fixture once and runs every row on the three engines from one table, comparing status, the headers named by the spec, the body, and the access record's key set against net/http's; any difference is a defect claim (rule 2): record the failing output in `notes.md` and fix it under that row
  - GREEN: fixes only where a row fails
  - REFACTOR: reuse `engines` and the harness helpers (no new helpers unless three call sites repeat)
- [ ] 6.2 The three new documented differences (engine-parity / Scenario: Documented differences, the static rows); verify `go test -race -run TestParityExceptions .` (the existing test of this scenario, extended; name it in the task note) passes
  - RED: the three rows pin each engine's actual behaviour
  - GREEN: godoc of `ginsrvr.Static` and `srvr.Static` names its exception
  - REFACTOR: none
- [ ] 6.3 `LOGGING.md`: static hits carry `http.route`, misses are unmatched and counted by the limit, no new key (`srvr.log.schema` stays `1`); README section finished with all three engines and the `os.OpenRoot` advice; verify `go test -run 'TestLogSchemaDocumented|Example' .` passes and the golden-record test is unchanged
- [ ] 6.4 Verify the change end to end: `make check` passes in every module; `make cover` shows `engine/static.go` covered from the engine modules' tests too; `openspec validate static-files --strict` passes; `gopls` diagnostics clean
- [ ] 6.5 Run `/simplify`, apply what holds up, re-run the tests, then commit `test(paritytest): pin static-file parity across engines`; verify with `git log -1 --stat`

## 7. Slice: release

- [ ] 7.1 Precondition: this change is reviewed and merged to `main`; verify `git log origin/main --oneline -1` contains the merge
- [ ] 7.2 Prepare: move the `CHANGELOG.md` `## [Unreleased]` entries under a version heading (version left as a placeholder), run `make check` and `make cover` on `main`, and dry-run the release guard and the `RELEASING.md` steps without tagging; verify every step passes
- [ ] 7.3 STOP for the owner: present the proposed version (`v0.3.0`: new public API in every engine module), the changelog section and the dry-run results; wait for explicit confirmation of the version and of pushing tags; record the decision in `notes.md`; do not continue without it
- [ ] 7.4 Release in `RELEASING.md` order: tag the core module and push; in each released module (`ginsrvr`, `fibersrvr`, `otelsrvr`, then `otelsetup`) require that core version, drop the `replace`, run its checks with `GOWORK=off`, commit, tag `<module>/<version>` and push; verify the CI `release` job passes for every tag
- [ ] 7.5 Restore the development `replace` lines on `main` in one commit that also runs `go mod tidy` in `internal/paritytest`; verify `make check` passes and `main` CI is green
- [ ] 7.6 Publish the GitHub Release page (RELEASING.md step 6) with the modules table and the version's changelog section; verify with `gh release view <version>`
- [ ] 7.7 Verify through the module proxy (`GOPROXY=https://proxy.golang.org GOFLAGS=-mod=mod go list -m` for every released module) and build a scratch consumer outside the repository that serves an `embed.FS` with each engine's `Static`; record the result in `notes.md`

## Workflow follow-up

- Re-read `notes.md` and make sure every open item has a target or is `unassigned`.
- Archive the change after review; verify `openspec validate --specs --strict` passes for all main specs after the archive sync.
