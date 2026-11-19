# Spec Delta

## MODIFIED Requirements

### Requirement: Trace ids are unique under concurrency
Generated trace and span ids SHALL come from a cryptographically secure random source seeded only from the operating system's random source, SHALL be valid W3C ids (lowercase hex, 32 and 16 characters, not all zeros) and SHALL be unique across concurrent and sequential requests.

#### Scenario: Concurrent generation
- **Given** a server receiving `<requests>` requests without `traceparent` from `<clients>` concurrent clients
- **When** all requests complete
- **Then** the number of distinct trace ids is `<distinct>` and the number of distinct span ids is `<distinct>`
- **And** every trace id is 32 and every span id 16 lowercase hex characters, none all zeros

##### Examples:
| requests | clients | distinct |
|----------|---------|----------|
| `1000` | `1000` | `1000` |
| `10000` | `64` | `10000` |
| `10000` | `1` | `10000` |
