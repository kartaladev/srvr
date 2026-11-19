# Spec Delta

## MODIFIED Requirements

### Requirement: Construction validates configuration
Constructing a server SHALL validate every option and config field and SHALL return one error naming every offending field and its value instead of a server when any is invalid, problem options included. Construction SHALL NOT open a listener. A `*slog.Logger` passed with `WithLogger` whose `Handler()` is nil or a typed nil SHALL fail construction naming `Logger`, since srvr could not write a single record through it.

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
| `WithProblemOptions` and `ShutdownTimeout` | `prblm.WithNamespace("not a uri")` and `-1s` | one error wrapping `srvr.ErrConfig`, matching `errors.As` for each field | `ProblemOptions` and `ShutdownTimeout` |
| `WithProblemOptions` and `LogConfig.Level` | `prblm.WithNamespace("not a uri")` and `loud` | one error wrapping `srvr.ErrConfig`, matching `errors.As` for each field | `ProblemOptions` and `Level` |
| `WithProblemOptions` and `MaxHeaderBytes` | an option that panics with `boom`, and `-1` via `WithMaxHeaderBytes(-1)` | one error wrapping `srvr.ErrConfig`, matching `errors.As` for each field, without panicking | `ProblemOptions` and `MaxHeaderBytes` |
| `WithProblemOptions` | `prblm.WithNamespace("not a uri")` and no other invalid field | an error wrapping `srvr.ErrConfig` | `ProblemOptions` |

#### Scenario: Problem-option errors are joined on every engine
- **Given** engine `<engine>` configured with `WithProblemOptions(prblm.WithNamespace("not a uri"))` and `ShutdownTimeout` `-1s`
- **When** the consumer constructs the server
- **Then** construction returns one error wrapping `srvr.ErrConfig`
- **And** `errors.As` finds a `*srvr.ConfigError` with `Field` `<field>`

##### Examples:
| engine | field |
|--------|-------|
| `net/http` | `ProblemOptions` |
| `net/http` | `ShutdownTimeout` |
| `gin` | `ProblemOptions` |
| `gin` | `ShutdownTimeout` |
| `fiber` | `ProblemOptions` |
| `fiber` | `ShutdownTimeout` |
