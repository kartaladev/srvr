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

### Fixed

- The sampled flag of an incoming `traceparent` is read from the flags byte's hex value; flags `0a`,
  `0b`, `0e`, `0f`, `fe` and `ff` were read wrongly.
- `WithAccessLogConfig` copies `SkipPaths`, so changing the caller's slice after `New` no longer
  changes which records are written or races with requests.
