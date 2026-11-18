# trace-context Specification

## Purpose

Defines how each request gets a W3C trace context - continued from a valid incoming `traceparent` or newly generated - so logs and problems correlate without requiring OpenTelemetry.

## Requirements

### Requirement: A valid incoming traceparent is continued
When a request carries exactly one valid `traceparent` header (W3C Trace Context level 1), the server SHALL use its trace id, SHALL record its parent id as the parent span id, and SHALL generate a new span id for the request.

#### Scenario: Valid traceparent
- **Given** a request with `traceparent` `<traceparent>`
- **When** the server handles it
- **Then** the request's trace id is `<trace_id>`
- **And** its parent span id is `<parent>` and its span id is a new 16-character lowercase hex value not equal to `<parent>`

##### Examples:
| traceparent | trace_id | parent |
|-------------|----------|--------|
| `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01` | `4bf92f3577b34da6a3ce929d0e0e4736` | `00f067aa0ba902b7` |
| `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-00` | `4bf92f3577b34da6a3ce929d0e0e4736` | `00f067aa0ba902b7` |
| `01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-future` | `4bf92f3577b34da6a3ce929d0e0e4736` | `00f067aa0ba902b7` |

### Requirement: An invalid or absent traceparent starts a new trace
When `traceparent` is absent, malformed, uses version `ff`, has an all-zero trace or parent id, or appears more than once, the server SHALL generate a new random trace id and span id and SHALL NOT echo the rejected value anywhere.

#### Scenario: New trace
- **Given** a request with `traceparent` `<traceparent>`
- **When** the server handles it
- **Then** the request's trace id is a new 32-character lowercase hex value that is not all zeros
- **And** no log record or response contains `<traceparent>`

##### Examples: absent or malformed
| traceparent |
|-------------|
| (none) |
| (empty) |
| `garbage` |
| `00-4BF92F3577B34DA6A3CE929D0E0E4736-00F067AA0BA902B7-01` |
| `00-4bf92f3577b34da6a3ce929d0e0e473-00f067aa0ba902b7-01` |
| `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-extra` |
| `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-zz` |

##### Examples: invalid values
| traceparent |
|-------------|
| `ff-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01` |
| `00-00000000000000000000000000000000-00f067aa0ba902b7-01` |
| `00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01` |
| two `traceparent` headers with different valid values |
| a `traceparent` of 4096 bytes |

### Requirement: Incoming trace context can be ignored
With the `IgnoreIncomingTraceContext` opt-in, the server SHALL start a new trace for every request regardless of `traceparent`.

#### Scenario: Ignoring incoming context
- **Given** a server configured with `<config>`
- **When** a request arrives with `traceparent` `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01`
- **Then** the request's trace id `<outcome>`

##### Examples:
| config | outcome |
|--------|---------|
| (none) | is `4bf92f3577b34da6a3ce929d0e0e4736` |
| `WithIgnoreIncomingTraceContext()` | is a new value |
| `WithTraceConfig(TraceConfig{IgnoreIncoming: true})` | is a new value |

### Requirement: Handlers can read the trace context
Handlers and middleware SHALL be able to read the request's trace id and span id from the request context through an exported accessor, and the accessor SHALL report absence for a context the server did not create.

#### Scenario: Reading the trace context
- **Given** `<context>`
- **When** a handler calls `srvr.TraceFromContext(ctx)`
- **Then** it returns `<result>`

##### Examples:
| context | result |
|---------|--------|
| a request context handled by the server | the request's trace id and span id and `true` |
| `context.Background()` | zero values and `false` |
| a `nil` context | zero values and `false` |

### Requirement: Trace ids are unique under concurrency
Generated trace and span ids SHALL come from a cryptographically secure random source and SHALL be unique across concurrent requests.

#### Scenario: Concurrent generation
- **Given** a server receiving `<requests>` concurrent requests without `traceparent`
- **When** all requests complete
- **Then** the number of distinct trace ids is `<distinct>`

##### Examples:
| requests | distinct |
|----------|----------|
| `1000` | `1000` |

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
