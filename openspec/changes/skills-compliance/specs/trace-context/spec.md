# Spec Delta

## ADDED Requirements

### Requirement: The sampled flag is read from the flags value
The sampled flag of an accepted `traceparent` SHALL be the lowest bit of its trace-flags field read as a hexadecimal byte, for every valid flags value from `00` to `ff`, and SHALL be reported by `srvr.TraceFromContext` and to observers.

#### Scenario: Sampled flag
- **Given** a request with `traceparent` `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-<flags>`
- **When** a handler reads `srvr.TraceFromContext(ctx)`
- **Then** `Sampled` is `<sampled>`

##### Examples:
| flags | sampled |
|-------|---------|
| `00` | `false` |
| `01` | `true` |
| `02` | `false` |
| `09` | `true` |
| `0a` | `false` |
| `0b` | `true` |
| `0e` | `false` |
| `0f` | `true` |
| `fe` | `false` |
| `ff` | `true` |

##### Examples: rejected flags start a new trace
| flags | sampled |
|-------|---------|
| `0B` | `false` (new trace) |
| `0g` | `false` (new trace) |
| `1` | `false` (new trace) |
