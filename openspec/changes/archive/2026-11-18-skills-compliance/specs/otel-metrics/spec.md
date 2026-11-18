# Spec Delta

## ADDED Requirements

### Requirement: Invalid otelsrvr options fail construction
`otelsrvr.New` SHALL return an error wrapping `otelsrvr.ErrConfig` that names the option's position for a nil option, instead of panicking, and SHALL keep validating the other options.

#### Scenario: Nil option
- **Given** the options `<options>`
- **When** `otelsrvr.New` is called
- **Then** it returns `<result>`

##### Examples:
| options | result |
|---------|--------|
| `nil` | an error wrapping `otelsrvr.ErrConfig` naming `Option` `0`, and no panic |
| `WithTracing()`, `nil` | an error wrapping `otelsrvr.ErrConfig` naming `Option` `1` |
| `nil`, `WithDurationBuckets()` with no boundaries | an error wrapping `otelsrvr.ErrConfig` naming `Option` `0` and `DurationBuckets` |
| (none) | an observer and `nil` |

### Requirement: A zero Observer records nothing without faults
An `otelsrvr.Observer` not built by `otelsrvr.New` (its zero value) SHALL be a no-op observer: requests are answered unchanged, nothing is recorded, and no `srvr.observer.fault` record is written. Its godoc SHALL say to build observers with `New`.

#### Scenario: Zero observer
- **Given** a server on engine `<engine>` configured with `WithObserver(&otelsrvr.Observer{})`
- **When** a client sends `GET /users/42` answered `<status>`
- **Then** the response is `<status>` and the log has `<faults>` `srvr.observer.fault` records

##### Examples:
| engine | status | faults |
|--------|--------|--------|
| `net/http` | `200` | `0` |
| `gin` | `500` | `0` |
| `fiber` | `404` | `0` |
