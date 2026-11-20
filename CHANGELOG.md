# Changelog

All notable changes to the srvr modules are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the modules follow semantic versioning
once tagged.

## [Unreleased]

### Changed

- **Breaking (API, `engine`, unstable before v1):** the package `github.com/kartaladev/srvr/engine`
  moved to `github.com/kartaladev/srvr/internal/engine` and can no longer be imported from outside
  srvr's modules. Use the `srvr` names instead (`srvr.Config`, `srvr.ErrConfig`, `srvr.ConfigError`,
  `srvr.Option`, ...); they are unchanged. Upgrade every srvr module (`srvr`, `ginsrvr`,
  `fibersrvr`, `otelsrvr`, `otelsetup`) to the same release: an older engine module imports the
  removed path.
- The shared pipeline is split by responsibility under `internal/`: `internal/errchain` (srvr's
  error values and safe error inspection) and `internal/static` (static file resolution) beside
  `internal/engine`. Behaviour, defaults, the log schema (`srvr.log.schema` `1`) and every module's
  dependency set are unchanged.

## [v0.3.0] - 2026-11-20

### Added

- `srvr.Static(prefix, fsys, opts...)` serves the files of an `fs.FS` (a build directory, an
  `embed.FS`) under a prefix on the net/http engine, with `srvr.StaticConfig`, `srvr.StaticOption`,
  `srvr.WithStaticIndexNames` and `srvr.WithStaticConfig`. Safe defaults: `GET` and `HEAD` only,
  `index.html` for directories, no directory listing, dotfiles and escaping paths answered `404`,
  file system errors answered with the safe `500` problem, only `If-Modified-Since` honoured (no
  ranges, `ETag` or compression). Registered routes win over static files; static `404`s are
  logged and limited like unmatched requests. Compared with `Mount` of `http.FileServerFS`, a
  consumer switching gets `404` problems instead of plain text, hidden dotfiles, no listings and no
  `/index.html` redirect.
- `srvr.WithSPAFallback(file)` serves a single-page app's `index.html` for browser navigations
  (`GET`/`HEAD`, `Accept` listing `text/html`, no `.` in the last path segment) that match no file,
  with `Cache-Control: no-cache` and `Vary: Accept`. `srvr.WithStaticCacheControl` sets
  `Cache-Control` on files (none by default), `srvr.WithStaticIndexCacheControl` on index files and
  the fallback (`no-cache` by default). A fallback that is not a regular file, or a header value with
  a control character, fails `Start`.
- `ginsrvr.Static(prefix, fsys, opts...)` serves static files and single-page apps on the gin engine
  with the same `srvr.StaticOption` values, defaults and answers as `srvr.Static`. Mounts are served
  from gin's no-route and no-method chains (a gin catch-all cannot sit beside other routes), so
  registered routes win in either order, a `GET` or `HEAD` to a path only another method's route
  serves gets the file or the SPA fallback as on net/http, and root-group middleware runs around the
  mounts even when added after `Static`; once a server has a mount, that middleware also runs for
  the requests gin answers with its own `404` or `405`. Any other method that another method's route
  matches gets gin's `405`, whose `Allow` inside a mount also lists `GET` and `HEAD`, as net/http and
  fiber send it (`Allow: GET, HEAD, POST`). A mount replaces a `NoRoute` handler set by a
  `ginsrvr.WithGinEngine` hook. gin routes on the decoded path, so an encoded slash at the end of
  the prefix (`GET /assets%2Fapp.js`) serves `app.js`, where net/http and fiber answer `404`
  (documented engine-parity exception).
- `fibersrvr.Static(prefix, fsys, opts...)` serves static files and single-page apps on the fiber
  engine with the same `srvr.StaticOption` values, defaults and answers as `srvr.Static`. Files are
  sent with fiber's `Ctx.SendFile` with its open-file cache off (a file replaced on disk is served
  fresh), no compression and no byte ranges. Mounts are installed after every other configurer,
  longest prefix first, so registered routes win in either order, middleware added with `Use` runs
  around the mounts even when added after `Static`, and a `GET` or `HEAD` to a path only another
  method's route serves gets the file or the SPA fallback. A consumer route that repeats a mount's
  pattern, such as `Get("/*")` beside `Static("/")`, fails `Start` as a duplicate (net/http and gin
  let the consumer's catch-all win). With fiber's `UnescapePath`, `GET /assets%2Fapp.js` serves
  `app.js` as on gin. `SendFile` opens the file again by name, so a file removed or made unreadable
  between srvr's check and that open is a `404` (a `500` if it fails at fasthttp's second open),
  where net/http and gin serve the file they opened (documented engine-parity exceptions).
- `fibersrvr.WithSendFileCache(d)` and `srvr.StaticConfig.FiberSendFileCache`
  (`fiber_send_file_cache`) turn on fiber's open-file cache for one static mount (opt-in; off by
  default). About twice as fast per request on `os.DirFS`, but a file replaced on disk may be served
  stale for up to `d` (an `embed.FS` is unaffected), and each cached mount starts fasthttp's cache
  cleaner goroutine, which outlives `Shutdown` because fiber v3.7.0 never sets
  `fasthttp.FS.CleanStop`. `d <= 0` in the option and a negative field fail `Start`; `srvr.Static`
  and `ginsrvr.Static` fail `Start` on any non-zero value, since they have no file cache.
- `fibersrvr` documents a further engine-parity difference: fiber normalises the request path
  before routing (`%2e%2e` decoded, dot segments removed), so a problem's `instance` and the route
  reached can differ from net/http and gin, and path rules in a proxy that forwards `%2e%2e`
  unchanged can be bypassed. See the package documentation. No behaviour change.
- `engine.StaticSettings`, `engine.RejectSendFileCache`, `engine.Static.SendFileCache`,
  `engine.Static.FS`, `engine.Static.Patterns`, `engine.StaticResult.Failure`,
  `engine.StaticPrefixes.Claim` and `Len`, and `engine.MarkRouterAnswered` (package `engine`,
  unstable before v1): what a `StaticOption` builds, with the errors an engine module's option
  collects for a refused argument; the last option of an engine without an open-file cache, which
  refuses a positive `FiberSendFileCache`; the mount's cache duration, file system and route
  patterns; the error a 404, 405 or failed resolution answers with, marking the router's answer;
  the duplicate-prefix rule for a key an engine chooses; and the router's-answer mark of a
  request context. Once a request is marked as the router's answer, `RequestState.SetRoute` and
  `SetLocation` do nothing, so a static miss records no `http.route` on any engine.
- The static-file policy every engine shares (package `engine`, unstable before v1):
  `engine.StaticConfig` and `engine.StaticOption` (aliased as `srvr.StaticConfig` and
  `srvr.StaticOption`); `engine.NewStatic`, building a mount (`*engine.Static`) with its `Prefix`,
  `Check` and `Resolve` methods; `engine.CheckStatic`, the configuration check of a mount's
  construction; `engine.StaticPrefixes`, which adds the mounts of one server and refuses a prefix
  served twice; `engine.StaticResult`, `engine.StaticAnswer` (`StaticFile`, `StaticNotFound`,
  `StaticMethodNotAllowed`, `StaticFailed`) and `engine.StaticFileContent`, the decision `Resolve`
  returns for an engine to answer.
- `engine.ServeStaticFile` (package `engine`, unstable before v1) sends a static mount's file with
  `http.ServeContent`, shared by the net/http and gin engines.
- `engine.RequestState.MarkRouterAnswered` (package `engine`, unstable before v1) also clears the
  route an earlier stage recorded: an answer of the router means no route matched.

### Changed

- Requires `github.com/kartaladev/prblm` v0.3.0 (was v0.2.1) in every module, `ginsrvr` requires
  `prblm/adapters/ginprblm` v0.3.0 (was v0.2.1) and `fibersrvr` requires `prblm/adapters/fiberprblm`
  v0.3.0 (was v0.2.0). What a consumer sees:
  - An error whose `Unwrap`, `Is` or `As` method panics, or whose chain is more than 1,000 entries
    deep or wide, gets the safe 500 problem at once, even when a mapper would have matched it; before,
    another resolution step could still map it, and a chain too deep for srvr's cycle guard was walked
    without a bound.
  - `errors.Join(prblm.WithStatus(err, 418), prblm.ErrNotFound)` answers 418 (was 404): the first of
    a status hint and a sentinel in `errors.Is` order decides.
  - A `Cache-Control` or `Vary` header a handler set under a non-canonical key (`w.Header()["cache-control"]`)
    is merged as that header: the problem response carries one line of it, not two.
  - A typed nil in an error chain is skipped: a typed-nil `*prblm.Received` gives 500, not 502.
  - Error hooks (`srvr.WithErrorHook`, prblm hooks) receive a note wrapping `prblm.ErrUnwalkableError`
    before an error prblm cannot walk, instead of `*prblm.PanicError` reports for a panicking method;
    ginprblm's `Recovery` reports the `*prblm.PanicError` itself rather than a wrapper.

### Fixed

- The access record and observers of a request whose error prblm cannot walk describe that error,
  not prblm's note about it: with prblm v0.3.0 the record of a chain over 1,000 entries named
  `*prblm.unwalkableNoteError`, and that of an error whose `Is` or `As` method panics was recorded as a
  panic of the method. srvr's hook no longer calls `errors.Is` on the reported error to recognise
  prblm's notes, so an error whose `Is` method panics is recorded too. Notes are matched by identity
  only: a consumer error whose own `Is` method claims to be a prblm note is recorded, not dropped.
- fibersrvr: a handler panic whose value's `Unwrap` method panics is recorded as a panic
  (`error.kind` `panic`, `exception.type` of the value, stack trace) like on net/http and gin, not as
  a returned error; the response stays the safe 500 problem.

## [v0.2.0] - 2026-11-19

### Changed

- Requires `github.com/kartaladev/prblm` v0.2.1 (was v0.2.0) in every module, and `ginsrvr` requires
  `prblm/adapters/ginprblm` v0.2.1: an error whose `As` or `Is` method panics, or a panic whose value
  is such an error, now gets the safe 500 problem instead of escaping prblm.
- `otelsrvr.ConfigError.Cause` and `otelsetup.ConfigError.Cause` now keep a recovered panic value
  that is an error: a meter provider, tracer provider or propagator that panics with an error in
  `otelsrvr.New`, and the error `http.ServeMux` panics with for a rejected `Prometheus.Path` in
  `otelsetup.New`. `errors.Is`/`errors.As` reach it; `Cause` was nil before. The text is unchanged.
  A panic value that is not an error, a typed nil, or an error whose chain cycles or whose `Unwrap`
  panics still leaves `Cause` nil.
- With the debug buffer on, a request's buffered records are held in a ring: a request that buffers
  a few records allocates about 17% fewer bytes, and one past `MaxRecords` (evicting) about 47% fewer.
  What is written, and its order, are unchanged. Performance only.
- Generated trace and span ids are drawn from a pooled batch of `crypto/rand` bytes, one read per 32
  traces, instead of one `crypto/rand` read per request. On macOS that read was a system call taking
  about 40% of a request's CPU, and a request now costs about 12% less (47% less under parallel
  load). On Linux (`getrandom` vDSO) the change is within noise. Ids stay cryptographically random
  from `crypto/rand`. The trade-off: up to 768 bytes of future ids per pooled batch stay in process
  memory, and ids repeat when a process or VM snapshot is restored more than once (see
  `docs/PERFORMANCE.md`).

### Fixed

- Construction (`srvr.New`, `ginsrvr.New`, `fibersrvr.New`) reports a rejected `WithProblemOptions`
  option in the same joined error as the other invalid fields; it was reported only when every other
  field was valid. `errors.As` finds a `*srvr.ConfigError` with `Field` `ProblemOptions` among them.
- With `WithUnmatchedRecordLimit` (or `AccessLogConfig.UnmatchedLimit`), only the `404` and `405`
  records the router answered are limited. On fiber, a `404` or `405` that consumer middleware
  reported for a path no route matched (for example `prblm.ErrNotFound`, or a wrapped
  `fiber.ErrNotFound`) was suppressed too; it is now always written. net/http and gin group
  middleware never run for unmatched paths, so they were not affected. Each engine marks the router's
  answer with the new `engine.RequestState.MarkRouterAnswered`.

## [v0.1.0] - 2026-11-19

### Added

- Per-request debug buffering, off by default: `srvr.WithDebugBuffer(opts...)` with `BufferLevel`,
  `FlushAtStatus`, `MaxRecords`, `MaxBytes` and `TotalBytes`, or `srvr.WithDebugBufferConfig` /
  `LogConfig.DebugBuffer`. Below-level records logged with a request context are written, marked
  `srvr.buffered`, just before the access record of a request that fails with `500` or above or
  panics; the access record gains `srvr.debug_buffer.flushed`, `dropped` and `truncated`. Additive
  keys: `srvr.log.schema` stays `1`. See `LOGGING.md`.
- `srvr.WithoutErrorRedaction()` writes error text unredacted, an explicit loosening of the new
  default redaction.
- `engine.RedactSecrets`, srvr's default error redactor, and `engine.MethodLabel`.
- `srvr.ConfigError` (alias of the new type `*engine.ConfigError{Field, Value, Reason}`): every
  construction error is one, so `errors.As` reads the offending field; it still matches
  `errors.Is(err, srvr.ErrConfig)` and its text is unchanged.
- `otelsrvr.ConfigError` and `otelsetup.ConfigError` (`Field`, `Value`, `Reason`): every
  configuration error of their `New` is one, joined per invalid field, so `errors.As` reads the
  field; they still match `errors.Is(err, ErrConfig)` and their text is unchanged. The cause of an
  `OTLP.Endpoint`, `Prometheus.Addr` or instrument-creation failure is kept in `Cause` and stays
  reachable with `errors.Is`/`errors.As` (for example `*url.Error` or `*net.AddrError`).
- `srvr.RouteCheck`, `srvr.Routes` and `srvr.Route` aliases: a route check needs no `engine` import.
  `WithRouteCheck` takes `srvr.RouteCheck` (the same type).
- `engine.Core.AccessSuppressed`, for an engine to skip copying what only a suppressed access
  record reports.
- `srvr.WithUnmatchedRecordLimit(burst, interval)` / `AccessLogConfig.UnmatchedLimit`
  (`srvr.UnmatchedLimitConfig{Burst, Interval}`), off by default and a named loosening: at most
  `burst` `http.request.failed` records of requests no route matched (404/405) per `interval` per
  server; the rest are counted and reported as `srvr.unmatched.suppressed` on the next such record
  written and on `server.stopped`. Handler errors, panics and 5xx records are never limited and
  observers see every request. A negative `Burst`, or a positive one without a positive `Interval`,
  fails construction. Additive key: `srvr.log.schema` stays `1`.
- `srvr.WithoutAccessLogUserAgent()` / `AccessLogConfig.OmitUserAgent` leave `user_agent.original`
  out of access records; it is still written by default.
- Benchmarks for every hot path, `make bench` and `make profile` (results under the ignored
  `bench/`), `docs/PERFORMANCE.md`, and a CI job that fails a pull request whose benchmarks
  allocate more than 10% more (`allocs/op` or `B/op`) than `main`; timings are reported only.

### Changed

- Faster, measured with `benchstat` (`docs/PERFORMANCE.md`): `engine.RedactSecrets` skips its
  secret-pair pattern when no key word can occur (a 32 KiB stack 4.2 ms -> 0.13 ms, so a recovered
  panic costs about 99 µs instead of 555 µs); every `LoggerFromContext` call for one request
  returns the same logger (0 allocations after the first, was 11 per call); a `slog.Any` value
  under the attribute cap is measured without building its text (-15% B/op); fiber copies nothing
  out of its buffers for a suppressed access record (-6 allocations). Output is unchanged.

- Requires `github.com/kartaladev/prblm` v0.2.0 (was v0.1.0) in every module. `ginsrvr` and
  `fibersrvr` now import prblm's adapters from their new module paths,
  `github.com/kartaladev/prblm/adapters/ginprblm` and `.../adapters/fiberprblm` (v0.2.0); the old
  `prblm/ginprblm` and `prblm/fiberprblm` modules are no longer required. No srvr API or response
  change.
- **Breaking (API, pre-v1):** `srvr.LogWriteErrors()` is renamed `srvr.LogRecordsLost()`, and
  `engine.LogWriteErrors()` `engine.LogRecordsLost()`, without an alias: the counter counts every
  record lost to an output or consumer-handler error or panic, not only failed writes. Its meaning
  and value are unchanged.
- **Breaking (API, pre-v1):** `ginsrvr.WithEngine` is renamed `ginsrvr.WithGinEngine`; construction
  errors about the hook name `WithGinEngine`.
- **Breaking (API, `engine`, unstable before v1):** `engine.ConfigError(field, value, reason)` is
  replaced by `engine.NewConfigError` returning `*engine.ConfigError`; `engine.RouteError` is
  renamed `engine.RouteErrorf`; `engine.ForEngine` is renamed `engine.Only`; `Core.Settings()` is
  replaced by `Core.MaxHeaderBytesSet()`, the only value an engine read; `RequestState.Route` and
  `RequestState.Location` are read with `Route()` and `Location()` and set with `SetRoute` and
  `SetLocation`.
- Construction returns one error joining every invalid field, `LogConfig.Level` and `Format`
  included: an invalid log field was reported only once every other field was valid, and only the
  first of `Level` and `Format` was; with `WithLogHandler` or `WithLogger`, every conflicting log
  setting is named, not only the first. Each part is a `*srvr.ConfigError`.
- A handler error matching `context.Canceled` returned after the client went away (the request
  context is cancelled) is answered with a `499` "Client Closed Request" problem instead of `500`,
  by a prblm mapper srvr adds after the consumer's, so its access record is `http.request.failed`
  at `WARN` with `problem.status` `499` and a debug buffer is not flushed. A cancellation while the
  request context is live, or past its deadline, stays `500`; the consumer's prblm mappers, prblm's built-ins and
  `prblm.WithStatus` hints still win. On fiber a disconnect is not detected and stays `500`.
  `srvr.log.schema` stays `1` (a value, not a key).
- A consumer's `prblm.OnError` passed through `WithProblemOptions` now runs: srvr adds its own error
  hook with prblm v0.2.0's `prblm.AddErrorHook` instead of `prblm.OnError`, so it no longer
  replaces the consumer's hook, and no prblm option can replace or remove srvr's. The consumer's
  `OnError` runs first, then any `prblm.AddErrorHook`, then srvr's hook and `WithErrorHook`; a
  panicking hook does not stop the others. `WithErrorHook` is unchanged.

- **Breaking (default log content):** with no redactor configured, `exception.message` and
  `exception.stacktrace` now mask URL query values and the values of secret-looking `key=value`,
  `key: value` and `"key":"value"` pairs (`token`, `key`, `secret`, `password`, `passwd`,
  `authorization`, `cookie`, `session`, `credential`). `WithErrorRedactor(fn)` still replaces the
  redactor. `srvr.log.schema` stays `1`.
- **Breaking (default log content):** a `slog.Any` value whose formatted text exceeds `MaxAttrBytes`
  is replaced by that text cut, with `srvr.attr.truncated`; smaller values render unchanged.
- `error.fingerprint` hashes the method as `_OTHER` when it is outside the semantic-convention set,
  so client-chosen methods cannot create unbounded error groups. `otelsrvr` uses the same set.

- **Breaking (behaviour):** `Shutdown` called while `Start` is still configuring, running route
  checks or binding now cancels the context route checks receive, and `Start` returns an error
  wrapping `ErrServerClosed` (it returned `nil` when the interruption landed during configure).

### Fixed

- A route check returning an error whose chain cycles, or whose `Unwrap` or `As` panics, no longer
  hangs or crashes `Start`; `Start`'s and `Shutdown`'s errors hide such a chain from `errors.Is`.
- A route check or shutdown hook calling `runtime.Goexit` (for example `t.FailNow`) is reported as
  failed instead of hanging `Start` or `Shutdown`.
- The serve goroutine is named `serveOn` and joined after the stop, at most until the drain
  deadline plus 100ms.
- A `WithListener` listener whose `Close` does not unblock `Accept` no longer hangs `Shutdown` on
  any engine (net/http, gin, fiber): it returns once the drain deadline passes, instead of a
  further second later.
- `otelsrvr.New(nil)` and `otelsetup.New(ctx, nil)` fail with their module's `ErrConfig` naming the
  option's position, instead of panicking (`otelsrvr`) or skipping it silently (`otelsetup`).
- A zero `otelsrvr.Observer` (not built by `New`) records nothing and writes no
  `srvr.observer.fault` record.
- `otelsetup` copies `OTLP.Headers` in `New`; the trace exporter read the caller's map on every
  export, so changing it after `New` changed, and raced with, the exports.
- `otelsetup`'s `Shutdown` closes the metrics listener's remaining connections when its deadline
  passes and returns after the listener's serving goroutine has.
- `otel.export.failed`'s `exception.message` passes srvr's default redactor.

### Security

- `golang.org/x/net` v0.63.0 in `otelsetup` and `ginsrvr` (GO-2026-6603, -6610, -6611, -6612, -6617,
  -6860, -6866) and `golang.org/x/crypto` v0.59.0 in `ginsrvr`, `fibersrvr` and
  `internal/paritytest` (GO-2026-5932).

- The sampled flag of an incoming `traceparent` is read from the flags byte's hex value; flags `0a`,
  `0b`, `0e`, `0f`, `fe` and `ff` were read wrongly.
- `WithAccessLogConfig` copies `SkipPaths`, so changing the caller's slice after `New` no longer
  changes which records are written or races with requests.
