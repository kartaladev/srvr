# Spec Delta

## MODIFIED Requirements

### Requirement: The engine-support API is marked as such
The core SHALL keep the shared pieces in packages under its `internal/` path, importable by srvr's own modules (core, ginsrvr, fibersrvr, otelsrvr, otelsetup, internal/paritytest) and refused by the Go toolchain for any module outside `github.com/kartaladev/srvr/`. The core module's dependency set SHALL remain the standard library plus prblm.

#### Scenario: Core dependencies unchanged
- **Given** the core module after this change
- **When** `go list -deps ./...` runs in the core module with `GOWORK=off` (test files excluded, so test-only modules such as goleak do not count)
- **Then** the third-party modules its packages depend on are `<modules>`

##### Examples:
| modules |
|---------|
| `github.com/kartaladev/prblm` only |

#### Scenario: Only srvr's modules import the shared pieces
- **Given** a module `<module>` whose package imports `<import>`
- **When** `go build ./...` runs in it with `GOWORK=off` and srvr's modules replaced by the working tree
- **Then** the build `<result>`

##### Examples: srvr's own modules
| module | import | result |
|--------|--------|--------|
| `github.com/kartaladev/srvr/ginsrvr` | `github.com/kartaladev/srvr/internal/engine` | succeeds |
| `github.com/kartaladev/srvr/fibersrvr` | `github.com/kartaladev/srvr/internal/engine` | succeeds |
| `github.com/kartaladev/srvr/otelsrvr` | `github.com/kartaladev/srvr/internal/engine` | succeeds |
| `github.com/kartaladev/srvr/otelsetup` | `github.com/kartaladev/srvr/internal/engine` | succeeds |
| `github.com/kartaladev/srvr/internal/paritytest` | `github.com/kartaladev/srvr/internal/engine` | succeeds |

##### Examples: outside modules
| module | import | result |
|--------|--------|--------|
| `example.com/consumer` | `github.com/kartaladev/srvr/internal/engine` | fails with `use of internal package github.com/kartaladev/srvr/internal/engine not allowed` |
| `example.com/consumer` | an extracted package, `github.com/kartaladev/srvr/internal/errchain` | fails with `use of internal package github.com/kartaladev/srvr/internal/errchain not allowed` |
| `example.com/consumer` | `github.com/kartaladev/srvr/engine` | fails: the package no longer exists (`no required module provides package` or `does not contain package`) |
| `example.com/consumer` | `github.com/kartaladev/srvr` only, using `srvr.Config`, `srvr.ErrConfig` and `srvr.New` | succeeds |
| `github.com/kartaladev/srvrx` (a sibling path sharing the prefix, not a nested module) | `github.com/kartaladev/srvr/internal/engine` | fails with `use of internal package ... not allowed` |

### Requirement: srvr re-exports the shared types
The public types every engine shares and the sentinel errors SHALL be declared in srvr's internal engine-support packages and re-exported by `srvr` as aliases, so code written against `srvr` compiles unchanged, `errors.Is` against `srvr` sentinels matches errors created inside srvr, and `errors.As` into a `*srvr.ConfigError` reads the offending field and value.

#### Scenario: Re-exported names
- **Given** the `srvr` name `<name>`
- **When** the consumer compares its type with the internal declaration `<declaration>`
- **Then** both are the same type

##### Examples:
| name | declaration |
|------|-------------|
| `srvr.Config` | the internal `Config` |
| `srvr.LogConfig` | the internal `LogConfig` |
| `srvr.AccessLogConfig` | the internal `AccessLogConfig` |
| `srvr.TraceConfig` | the internal `TraceConfig` |
| `srvr.ServiceConfig` | the internal `ServiceConfig` |
| `srvr.Trace` | the internal `Trace` |
| `srvr.ShutdownPhase` | the internal `ShutdownPhase` |
| `srvr.ErrorHook` | the internal `ErrorHook` |
| `srvr.Option` | the internal `Option` |
| `srvr.ConfigError` | the internal `ConfigError` |
| `srvr.RouteCheck` | the internal `RouteCheck` |
| `srvr.Routes` | the internal `Routes` |
| `srvr.Route` | the internal `Route` |

#### Scenario: srvr names are the engine declarations
- **Given** the error `<err>` produced by `<source>`
- **When** the consumer calls `errors.Is(err, <sentinel>)`
- **Then** the result is `true`

##### Examples:
| err | source | sentinel |
|-----|--------|----------|
| an invalid log level | the internal `ResolveConfig` | `srvr.ErrConfig` |
| an invalid log level | `srvr.New` | the internal `ErrConfig` |
| an invalid log level | `ginsrvr.New` | `srvr.ErrConfig` |
| an invalid log level | `fibersrvr.New` | `srvr.ErrConfig` |
| `Start` on a running server | the internal `Lifecycle` | `srvr.ErrServerStarted` |
| `Start` on a stopped server | the internal `Lifecycle` | `srvr.ErrServerClosed` |

#### Scenario: Configuration errors name their field
- **Given** construction with `<config>`
- **When** the consumer calls `errors.As(err, &ce)` with `ce` a `*srvr.ConfigError`
- **Then** it returns `<found>` with `ce.Field` `<field>`

##### Examples:
| config | found | field |
|--------|-------|-------|
| `LogConfig{Level: "loud"}` | `true` | `Level` |
| `WithLogOutput(nil)` | `true` | `Output` |
| `WithRouteCheck(nil)` | `true` | `RouteCheck` |
| two invalid fields `LogConfig{Level: "loud", Format: "xml"}` | `true` | `Level` (the first; both are named in `err.Error()`) |
| (none) | `false` (no error) | (none) |
