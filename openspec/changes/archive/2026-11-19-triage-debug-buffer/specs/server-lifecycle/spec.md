# Spec Delta

## MODIFIED Requirements

### Requirement: Construction validates configuration
Constructing a server SHALL validate every option and config field and SHALL return one error naming every offending field and its value instead of a server when any is invalid, the log fields (`LogConfig.Level`, `Format`) included. Construction SHALL NOT open a listener. A `*slog.Logger` passed with `WithLogger` whose `Handler()` is nil or a typed nil SHALL fail construction naming `Logger`: srvr could not write a single record through it, and silently dropping every record is not a safe default.

#### Scenario: Valid and invalid configuration
- **Given** a server configured with `<field>` set to `<value>`
- **When** the consumer constructs the server
- **Then** construction returns `<outcome>`
- **And** the error message contains `<message>`

##### Examples: accepted
| field | value | outcome | message |
|-------|-------|---------|---------|
| (none) | (none) | a server | (none) |
| `Addr` | `127.0.0.1:0` | a server | (none) |
| `ReadHeaderTimeout` | `-1s` | a server | (none) |
| `MaxRequestBodyBytes` | `-1` | a server | (none) |

##### Examples: rejected
| field | value | outcome | message |
|-------|-------|---------|---------|
| `Addr` | `localhost:99999` | an error wrapping `srvr.ErrConfig` | `Addr` |
| `Addr` | `not an address` | an error wrapping `srvr.ErrConfig` | `Addr` |
| `ShutdownTimeout` | `-1s` | an error wrapping `srvr.ErrConfig` | `ShutdownTimeout` |
| `MaxHeaderBytes` | `0` via `WithMaxHeaderBytes(0)` | an error wrapping `srvr.ErrConfig` | `MaxHeaderBytes` |
| `WithListener` | `nil` | an error wrapping `srvr.ErrConfig` | `Listener` |
| `WithListener` | a typed-nil `*net.TCPListener` | an error wrapping `srvr.ErrConfig` | `Listener` |
| `WithListener` and `Addr` | a typed-nil `*net.TCPListener` and `:8080` | an error wrapping `srvr.ErrConfig`, without panicking | `Listener` |
| `WithListener` and `Addr` | a listener whose `Addr()` panics, and `:8080` | an error wrapping `srvr.ErrConfig`, without panicking | `Listener` |
| a nil `Option` | `srvr.New(nil)` | an error wrapping `srvr.ErrConfig` | `Option` |
| `WithListener` and `Addr` | both set | an error wrapping `srvr.ErrConfig` | `Listener` |
| `WithShutdownHook` | `srvr.BeforeDrain` with a nil function | an error wrapping `srvr.ErrConfig` | `ShutdownHook` |
| `WithShutdownHook` | phase `srvr.ShutdownPhase(9)` | an error wrapping `srvr.ErrConfig` | `ShutdownPhase` |
| `WithLogger` | a zero `&slog.Logger{}`, whose `Handler()` is nil | an error wrapping `srvr.ErrConfig` | `Logger` |
| `WithLogger` | `slog.New(h)` where `h` is a typed-nil `slog.Handler` | an error wrapping `srvr.ErrConfig` | `Logger` |
| `LogConfig.Level` and `MaxHeaderBytes` | `loud`, and `-1` via `WithMaxHeaderBytes(-1)` | one error wrapping `srvr.ErrConfig`, matching `errors.As` for each field | `Level` and `MaxHeaderBytes` |
| `LogConfig.Format` and `ShutdownTimeout` | `xml` and `-1s` | one error wrapping `srvr.ErrConfig`, matching `errors.As` for each field | `Format` and `ShutdownTimeout` |
| `LogConfig.Level` and `LogConfig.Format` | `loud` and `xml` | one error wrapping `srvr.ErrConfig` | `Level` and `Format` |

## ADDED Requirements

### Requirement: A passed listener whose Close does not unblock Accept never blocks the stop
On every engine, when the listener passed with `WithListener` returns from `Close` without unblocking a pending `Accept`, `Shutdown` SHALL still return once its drain deadline passes, and a later `Shutdown` SHALL return `nil`.

#### Scenario: Stuck listener on every engine
- **Given** engine `<engine>` serving on a passed listener whose `Close` returns `nil` but leaves `Accept` blocked, and `ShutdownTimeout` `200ms`
- **When** `Shutdown` is called with a context without deadline
- **Then** `Shutdown` returns within `1s`
- **And** a later `Shutdown` returns `nil`

##### Examples:
| engine |
|--------|
| `net/http` |
| `gin` |
| `fiber` |
