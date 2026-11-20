# Spec Delta

## Purpose

Lets a tool work with the MCP session it runs in: find the session from its context, keep per-session state that is released when the session ends, send progress and log notifications, and make bounded, capability-checked requests back to the client.

## ADDED Requirements

### Requirement: A tool finds its session through its context
`SessionFromContext(ctx)` SHALL return the session of a tool call in a stateful endpoint with ok `true`, with an id that is the same for every call in one session and different between sessions. It SHALL return ok `false`, without panicking, for a stateless endpoint, for any context that srvr's MCP endpoint did not create, and for a nil context.

#### Scenario: Session lookup
- **Given** a `<engine>` server with an MCP endpoint configured `<mode>`
- **When** `SessionFromContext` is called with `<ctx>`
- **Then** ok is `<ok>`

##### Examples:
| engine | mode | ctx | ok |
|--------|------|-----|----|
| net/http | (default, stateful) | a tool call's context | `true` |
| gin | (default, stateful) | a tool call's context | `true` |
| net/http | `WithStateless()` | a tool call's context | `false` |
| net/http | (default, stateful) | a plain `GET /items` handler's request context | `false` |
| net/http | (default, stateful) | `context.Background()` | `false` |
| net/http | (default, stateful) | a nil context | `false` |

#### Scenario: Session identity
- **Given** a net/http server with a stateful MCP endpoint and a tool that reports its session's id
- **When** `<calls>`
- **Then** the reported ids are `<relation>`

##### Examples:
| calls | relation |
|-------|----------|
| one client calls the tool twice in one session | equal |
| two clients each call the tool in their own session | different |
| one client calls the tool, deletes the session, initializes again and calls it | different |

### Requirement: Per-session state is isolated and released on close
A session SHALL hold values set by tools, visible only to calls in that session, safe for concurrent use. When the session closes, by `DELETE`, idle expiry, failed keepalive or server stop, its values SHALL be dropped and its close hooks SHALL run once each, under `recover`, with a `5s` budget that does not block shutdown past its deadline.

#### Scenario: State isolation
- **Given** a net/http server with a tool that sets `<key>` to `<value>` and a tool that reads `<key>`
- **When** `<calls>`
- **Then** the read returns `<read>`

##### Examples:
| key | value | calls | read |
|-----|-------|-------|------|
| `cart` | `[1,2]` | set and read in the same session | `[1,2]` |
| `cart` | `[1,2]` | set in session A, read in session B | nothing (ok `false`) |
| `cart` | `[1,2]` | read before any set | nothing (ok `false`) |
| `cart` | `[1,2]` | `100` concurrent set and read calls in one session | a value that was set, clean under `-race` |

#### Scenario: Release on close
- **Given** a net/http server with a session holding a value and a close hook `<hook>`
- **When** the session closes by `<close>`
- **Then** the hook ran `<runs>` times and `<outcome>`

##### Examples: close paths
| hook | close | runs | outcome |
|------|-------|------|---------|
| that records its call | `DELETE /mcp` | `1` | the value is gone |
| that records its call | idle expiry | `1` | the value is gone |
| that records its call | failed keepalive | `1` | the value is gone |
| that records its call | `Shutdown` | `1` | `Shutdown` returns nil |
| that records its call | `DELETE /mcp` sent twice concurrently | `1` | the second `DELETE` gets `404` |

##### Examples: faulty hooks
| hook | close | runs | outcome |
|------|-------|------|---------|
| that panics with `"boom"` | `DELETE /mcp` | `1` | one `ERROR` record `mcp.session.close_hook.failed`; later hooks still run |
| that returns `errors.New("flush failed")` | `Shutdown` | `1` | one `ERROR` record `mcp.session.close_hook.failed`; `Shutdown` returns nil |
| that blocks forever | `Shutdown` with a `1s` context | `1` | `Shutdown` returns within `1s`; one `ERROR` record `mcp.session.close_hook.failed` |
| a nil function | `DELETE /mcp` | `0` | registering it returns an error naming `OnClose` |

#### Scenario: Using a closed session
- **Given** a tool that kept its session after the session closed
- **When** it calls `<call>`
- **Then** it gets `<result>` without panicking

##### Examples:
| call | result |
|------|--------|
| `Set("k", 1)` | an error wrapping `mcpsrvr.ErrSessionClosed` |
| `Value("k")` | nothing (ok `false`) |
| `OnClose(fn)` | an error wrapping `mcpsrvr.ErrSessionClosed`; `fn` never runs |

### Requirement: Tools report progress and log to the client
A tool SHALL be able to send `notifications/progress` for its call when the client gave a `progressToken`, and `notifications/message` at or above the level the client set. Sending SHALL never fail the call: without a token or below the level it is skipped, and to a closed session it returns an error wrapping `ErrSessionClosed`.

#### Scenario: Progress
- **Given** a `<engine>` server with a tool that reports progress `<progress>` of `<total>` with message `<message>`
- **When** a client calls it with `progressToken` `<token>`
- **Then** the client receives `<received>`
- **And** the tool result is still delivered

##### Examples:
| engine | progress | total | message | token | received |
|--------|----------|-------|---------|-------|----------|
| net/http | `1` | `2` | `checking stock` | `"t1"` | `notifications/progress` with `progressToken` `"t1"`, `progress` `1`, `total` `2`, `message` `checking stock` |
| gin | `1` | `2` | `checking stock` | `"t1"` | `notifications/progress` with `progressToken` `"t1"`, `progress` `1`, `total` `2`, `message` `checking stock` |
| net/http | `1` | (none) | (none) | `7` | `notifications/progress` with `progressToken` `7`, `progress` `1` |
| net/http | `1` | `2` | `x` | (none) | nothing, and the call returns no error |

#### Scenario: Log messages to the client
- **Given** a net/http server whose client set logging level `<client_level>`
- **When** a tool sends a log message at `<level>`
- **Then** the client receives `<received>`

##### Examples:
| client_level | level | received |
|--------------|-------|----------|
| `info` | `warning` | `notifications/message` with `level` `warning` |
| `info` | `info` | `notifications/message` with `level` `info` |
| `info` | `debug` | nothing |
| (not set) | `info` | nothing |

#### Scenario: Notifying a gone client
- **Given** a tool whose session `<state>`
- **When** the tool sends progress
- **Then** it gets `<result>` and the tool result is `<tool_result>`

##### Examples:
| state | result | tool_result |
|-------|--------|-------------|
| was closed by `DELETE /mcp` during the call | an error wrapping `mcpsrvr.ErrSessionClosed` | not delivered (the session is gone) |
| has a client whose connection dropped during the call | a return within `1s`, without panicking | the tool finishes and its goroutine ends |

### Requirement: Tools make bounded requests to the client
A tool SHALL be able to request elicitation, sampling and the root list from its client. A request SHALL fail without sending when the client did not declare the capability, SHALL fail after a `60s` default timeout, and SHALL end when the tool's context ends. The timeout SHALL be configurable; a negative value SHALL fail `Start`.

#### Scenario: Client request outcomes
- **Given** a `<engine>` server on protocol `2025-11-25` with a client that declared `<capabilities>` and answers `<answer>`
- **And** the endpoint configured `<configured_by>`
- **When** a tool sends `<request>`
- **Then** the tool gets `<result>`

##### Examples: answered
| engine | capabilities | answer | configured_by | request | result |
|--------|--------------|--------|---------------|---------|--------|
| net/http | `elicitation` | `accept` with `{"refund":"card"}` | (default) | `Elicit` "Refund to card?" | action `accept`, content `{"refund":"card"}` |
| gin | `elicitation` | `accept` with `{"refund":"card"}` | (default) | `Elicit` "Refund to card?" | action `accept`, content `{"refund":"card"}` |
| net/http | `elicitation` | `decline` | (default) | `Elicit` "Refund to card?" | action `decline`, no error |
| net/http | `sampling` | a text message `"ok"` | (default) | `CreateMessage` | a result with text `"ok"` |
| net/http | `roots` | roots `file:///repo` | (default) | `ListRoots` | roots `file:///repo` |

##### Examples: refused or failed
| engine | capabilities | answer | configured_by | request | result |
|--------|--------------|--------|---------------|---------|--------|
| net/http | (none) | (never asked) | (default) | `Elicit` | an error wrapping `mcpsrvr.ErrClientCapability` naming `elicitation`; nothing sent |
| net/http | `elicitation` | (none) | (default) | `CreateMessage` | an error wrapping `mcpsrvr.ErrClientCapability` naming `sampling`; nothing sent |
| net/http | `elicitation` | never | `WithClientRequestTimeout(100ms)` | `Elicit` | after `100ms`, an error wrapping `mcpsrvr.ErrClientTimeout` and `context.DeadlineExceeded` |
| net/http | `elicitation` | never | config `client_request_timeout: 100ms` | `Elicit` | after `100ms`, an error wrapping `mcpsrvr.ErrClientTimeout` |
| net/http | `elicitation` | never | (default) | `Elicit`, then the client cancels the tool call | an error wrapping `context.Canceled` |
| net/http | `elicitation` | never | (default) | `Elicit`, then `Shutdown` | an error wrapping `context.Canceled`; `Shutdown` returns within its deadline |
| net/http | `elicitation` | JSON-RPC error `-32600` | (default) | `Elicit` | an error carrying code `-32600` |
| net/http | `elicitation` | a malformed result | (default) | `Elicit` | an error; the tool does not panic |
| net/http | (stateless) | (never asked) | `WithStateless()` | `Elicit` | an error wrapping `mcpsrvr.ErrNoSession`; nothing sent |

##### Examples: invalid configuration
| engine | capabilities | answer | configured_by | request | result |
|--------|--------------|--------|---------------|---------|--------|
| net/http | (none) | (none) | `WithClientRequestTimeout(-1s)` | (none) | `Start` fails naming `MCP.ClientRequestTimeout` |

#### Scenario: Client request errors returned by a tool
- **Given** a net/http server with a tool that returns the error from `<request>`
- **When** a client calls the tool
- **Then** the tool result has `isError` `true` and text `<text>`

##### Examples:
| request | text |
|---------|------|
| an `Elicit` that timed out | `Gateway Timeout` |
| an `Elicit` refused for a missing capability | `Internal Server Error` |

### Requirement: Client answers are never logged
Elicitation answers, sampling messages and results, and root lists SHALL NOT appear in any record at any level. A failed client request SHALL be logged by method and outcome only.

#### Scenario: Privacy of client answers
- **Given** a net/http server at log level `DEBUG` with a tool that sends `<request>` and gets `<answer>`
- **When** the call finishes
- **Then** no record contains `<hidden>`

##### Examples:
| request | answer | hidden |
|---------|--------|--------|
| `Elicit` "Card number?" | `{"card":"4111111111111111"}` | `4111111111111111` |
| `CreateMessage` with prompt `"summarise patient 42"` | text `"diagnosis: x"` | `patient 42` |
| `ListRoots` | roots `file:///home/alice/secret` | `/home/alice` |
