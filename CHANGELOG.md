# Changelog

All notable changes to the srvr modules are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); the modules follow semantic versioning
once tagged.

## [Unreleased]

### Added

- `srvr.WithoutErrorRedaction()` writes error text unredacted, an explicit loosening of the new
  default redaction.
- `engine.RedactSecrets`, srvr's default error redactor, and `engine.MethodLabel`.

### Changed

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

- The sampled flag of an incoming `traceparent` is read from the flags byte's hex value; flags `0a`,
  `0b`, `0e`, `0f`, `fe` and `ff` were read wrongly.
- `WithAccessLogConfig` copies `SkipPaths`, so changing the caller's slice after `New` no longer
  changes which records are written or races with requests.
