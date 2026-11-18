# Changelog

All notable changes to the srvr modules are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the modules follow semantic versioning
once tagged.

## [Unreleased]

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
- `srvr.RouteCheck`, `srvr.Routes` and `srvr.Route` aliases: a route check needs no `engine` import.
  `WithRouteCheck` takes `srvr.RouteCheck` (the same type).

### Changed

- **Breaking (API, pre-v1):** `ginsrvr.WithEngine` is renamed `ginsrvr.WithGinEngine`; construction
  errors about the hook name `WithGinEngine`.
- **Breaking (API, `engine`, unstable before v1):** `engine.ConfigError(field, value, reason)` is
  replaced by `engine.NewConfigError` returning `*engine.ConfigError`; `engine.RouteError` is
  renamed `engine.RouteErrorf`; `engine.ForEngine` is renamed `engine.Only`; `Core.Settings()` is
  replaced by `Core.MaxHeaderBytesSet()`, the only value an engine read; `RequestState.Route` and
  `RequestState.Location` are read with `Route()` and `Location()` and set with `SetRoute` and
  `SetLocation`.
- An invalid `LogConfig.Level` and `Format` are both reported, `Level` first; only the first was.
- A handler error matching `context.Canceled` returned after the client went away (the request
  context is cancelled) is answered with a `499` "Client Closed Request" problem instead of `500`,
  by a prblm mapper srvr adds after the consumer's, so its access record is `http.request.failed`
  at `WARN` with `problem.status` `499` and a debug buffer is not flushed. A cancellation while the
  request context is live, or past its deadline, stays `500`; the consumer's prblm mappers, prblm's built-ins and
  `prblm.WithStatus` hints still win. On fiber a disconnect is not detected and stays `500`.
  `srvr.log.schema` stays `1` (a value, not a key).

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
- The serve goroutine is named `serveOn` and joined after the stop, for at most 1s.
- A `WithListener` listener whose `Close` does not unblock `Accept` no longer hangs `Shutdown` on
  net/http and gin: the drain ends at its deadline.
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
