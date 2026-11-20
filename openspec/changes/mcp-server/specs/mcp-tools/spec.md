# Spec Delta

## Purpose

Gives MCP tools srvr's programming model: a configurer registers tools, a tool handler takes typed input and returns typed output or an `error`, and srvr turns every error and panic into a safe tool result and a self-contained triage record.

## ADDED Requirements

### Requirement: Tool configurers register tools
An MCP endpoint SHALL accept tool configurers through `Register` until the server starts and SHALL call each configurer's `Configure` once during `Start`. Every tool a configurer adds SHALL appear in `tools/list` with its name, its description and an input schema inferred from its input type.

#### Scenario: Tools listed after Start
- **Given** a `<engine>` server with an MCP endpoint and a configurer adding `<tools>`
- **When** a client sends `tools/list`
- **Then** the result lists `<listed>`

##### Examples:
| engine | tools | listed |
|--------|-------|--------|
| net/http | `get_order` with input `{ID string}` | `get_order` with input schema type `object` and required property `id` |
| gin | `get_order` with input `{ID string}` | `get_order` with input schema type `object` and required property `id` |
| net/http | `get_order` and `cancel_order` from two configurers | `cancel_order`, `get_order` |
| net/http | (none) | (empty) |
| net/http | `get_order` from a configurer, plus `ping` added through the SDK on the same server | `get_order`, `ping` |

#### Scenario: Broken tool registration fails Start
- **Given** a `<engine>` server with an MCP endpoint and `<registration>`
- **When** `<action>` is called
- **Then** it returns an error wrapping `<sentinel>`
- **And** the error names `<named>`

##### Examples: faulty configurers
| engine | registration | action | sentinel | named |
|--------|--------------|--------|----------|-------|
| net/http | a nil configurer | `Start` | `srvr.ErrRouteConfig` | `ToolConfigurer` |
| net/http | a typed-nil `*OrderTools` configurer | `Start` | `srvr.ErrRouteConfig` | `*OrderTools` |
| gin | a configurer whose `Configure` panics with `"boom"` | `Start` | `srvr.ErrRouteConfig` | `*OrderTools` and `boom` |
| net/http | a configurer adding a tool with a nil handler | `Start` | `srvr.ErrRouteConfig` | `get_order` |
| net/http | two configurers adding `get_order` | `Start` | `srvr.ErrRouteConfig` | `get_order` |
| net/http | a configurer adding a tool named `(empty)` | `Start` | `srvr.ErrRouteConfig` | `Tool.Name` |
| net/http | a configurer adding a tool named `get order` | `Start` | `srvr.ErrRouteConfig` | `Tool.Name` |
| net/http | a configurer adding a tool named with `129` characters | `Start` | `srvr.ErrRouteConfig` | `Tool.Name` |
| net/http | a configurer adding a tool whose input type is `string` | `Start` | `srvr.ErrRouteConfig` | `get_order` |

##### Examples: boundary names accepted
| engine | registration | action | sentinel | named |
|--------|--------------|--------|----------|-------|
| net/http | a configurer adding a tool named with `128` characters of `[A-Za-z0-9_.-]` | `Start` | (none, succeeds) | (none) |

##### Examples: lifecycle misuse
| engine | registration | action | sentinel | named |
|--------|--------------|--------|----------|-------|
| net/http | a configurer registered after `Start` | `Register` | `srvr.ErrServerStarted` | (none) |
| net/http | a configurer registered after `Shutdown` | `Register` | `srvr.ErrServerClosed` | (none) |

### Requirement: Tool results carry the handler's output
A tool handler returning a nil error SHALL produce a tool result with `isError` false, its output as `structuredContent`, and the same output as JSON text content.

#### Scenario: Successful call
- **Given** a `<engine>` server with tool `get_order` returning `<output>` and a nil error
- **When** a client calls `get_order` with `{"id":"7"}`
- **Then** the result has `isError` `false` and `structuredContent` `<structured>`

##### Examples:
| engine | output | structured |
|--------|--------|------------|
| net/http | `Order{ID: "7", Total: 10}` | `{"id":"7","total":10}` |
| gin | `Order{ID: "7", Total: 10}` | `{"id":"7","total":10}` |
| net/http | the zero `Order{}` | `{"id":"","total":0}` |

### Requirement: Invalid tool arguments never reach the handler
Arguments that do not match the tool's input schema SHALL be answered with a JSON-RPC invalid-params error, and the handler SHALL NOT be called.

#### Scenario: Invalid arguments
- **Given** a net/http server with tool `get_order` whose input requires string `id`
- **When** a client calls `get_order` with `<arguments>`
- **Then** the response is a JSON-RPC error with code `<code>`
- **And** the handler was called `<calls>` times

##### Examples:
| arguments | code | calls |
|-----------|------|-------|
| `{}` | `-32602` | `0` |
| `{"id":7}` | `-32602` | `0` |
| `"7"` | `-32602` | `0` |
| (none) | `-32602` | `0` |
| `{"id":"7"}` | (none, success) | `1` |

### Requirement: Tool errors are rendered without internals
An error returned by a tool handler SHALL produce a tool result with `isError` true. Its text SHALL be the title of the problem prblm maps the error to and, for a 4xx problem, its `detail` when set. A 5xx or unmapped error SHALL produce the generic text `Internal Server Error`. The result SHALL carry `trace_id` in `_meta`. Error text SHALL NOT appear.

#### Scenario: Error to tool result
- **Given** a `<engine>` server with tool `get_order` whose handler returns `<error>`
- **When** a client calls `get_order`
- **Then** the result has `isError` `true` and text content `<text>`
- **And** the result does not contain `<hidden>` and `_meta.trace_id` is the request's trace id

##### Examples: mapped
| engine | error | text | hidden |
|--------|-------|------|--------|
| net/http | `prblm.ErrNotFound` | `Not Found` | `prblm:` |
| gin | `prblm.ErrNotFound` | `Not Found` | `prblm:` |
| net/http | `prblm.New(404, prblm.WithDetail("order 7 does not exist"))` | `Not Found: order 7 does not exist` | (none) |
| net/http | `fmt.Errorf("order 7: %w", prblm.ErrConflict)` | `Conflict` | `order 7` |
| net/http | `prblm.WithStatus(errors.New("gone"), 410)` | `Gone` | `gone` |
| net/http | `prblm.ErrInvalidInput` | `Bad Request` | `prblm:` |

##### Examples: unmapped and server-side
| engine | error | text | hidden |
|--------|-------|------|--------|
| net/http | `errors.New("pq: password authentication failed for user app")` | `Internal Server Error` | `pq:` |
| gin | `errors.New("pq: password authentication failed for user app")` | `Internal Server Error` | `pq:` |
| net/http | `prblm.New(500, prblm.WithDetail("db at 10.0.0.5 down"))` | `Internal Server Error` | `10.0.0.5` |
| net/http | `context.DeadlineExceeded` | `Gateway Timeout` | `deadline` |

##### Examples: faulty errors
| engine | error | text | hidden |
|--------|-------|------|--------|
| net/http | a typed-nil `*MyError` returned as `error` | `Internal Server Error` | `nil` |
| net/http | an error whose `Error()` panics | `Internal Server Error` | `panic` |

#### Scenario: Exposing error text is an explicit opt-in
- **Given** a net/http server with tool `get_order` returning `errors.New("order 7 locked by job 12")` and the endpoint configured `<configured_by>`
- **When** a client calls `get_order`
- **Then** the result text is `<text>`

##### Examples:
| configured_by | text |
|---------------|------|
| (default) | `Internal Server Error` |
| `WithToolErrorText()` | `order 7 locked by job 12` |
| config `expose_tool_error_text: true` | `order 7 locked by job 12` |

### Requirement: Tool panics are recovered
A panic in a tool handler SHALL be recovered and answered as a tool result with `isError` true and text `Internal Server Error`. The session and the server SHALL keep serving, and the panic value SHALL NOT reach the client.

#### Scenario: Panicking tool
- **Given** a `<engine>` server with tool `get_order` whose handler panics with `<value>`
- **When** a client calls `get_order` and then `tools/list` in the same session
- **Then** the first result has `isError` `true` and text `Internal Server Error` without `<value>`
- **And** `tools/list` succeeds

##### Examples:
| engine | value |
|--------|-------|
| net/http | `"secret-token-123"` |
| gin | `"secret-token-123"` |
| net/http | `errors.New("db pool exhausted")` |
| net/http | a nil value (`panic(nil)`) |
| net/http | `http.ErrAbortHandler` |

### Requirement: Tool failures are self-contained records
Each failed tool call SHALL produce one `WARN` record for a 4xx-mapped error or one `ERROR` record otherwise. The record's `msg` is `mcp.tool.failed` (`mcp.tool.panic.recovered` with a stack for a panic). It SHALL carry the error chain text and types, the code location, the tool result sent and `error.fingerprint`, computed from the error types and the tool name, never from message text.

#### Scenario: Failed record
- **Given** a net/http server with tool `<tool>` whose handler `<fails>`
- **When** a client calls `<tool>`
- **Then** one record with `msg` `<msg>` and level `<level>` is written
- **And** it carries `gen_ai.tool.name` `<tool>`, `trace_id`, `error.type` and `error.fingerprint`

##### Examples:
| tool | fails | msg | level |
|------|-------|-----|-------|
| `get_order` | returns `prblm.ErrNotFound` | `mcp.tool.failed` | `WARN` |
| `get_order` | returns `errors.New("db down")` | `mcp.tool.failed` | `ERROR` |
| `get_order` | panics with `"boom"` | `mcp.tool.panic.recovered` | `ERROR` |

#### Scenario: Fingerprint stability
- **Given** a net/http server with tools `get_order` and `cancel_order`
- **When** `<first>` and `<second>` fail
- **Then** their `error.fingerprint` values are `<relation>`

##### Examples:
| first | second | relation |
|-------|--------|----------|
| `get_order` returning `fmt.Errorf("order 1: %w", sql.ErrNoRows)` | `get_order` returning `fmt.Errorf("order 2: %w", sql.ErrNoRows)` | equal |
| `get_order` returning `sql.ErrNoRows` | `cancel_order` returning `sql.ErrNoRows` | different |
| `get_order` returning `sql.ErrNoRows` | `get_order` returning `context.Canceled` | different |

### Requirement: Tool handlers get the request context
A tool handler's context SHALL carry the HTTP request's trace context and request-scoped logger, and SHALL be cancelled when the client cancels the call, the session closes or the server's drain deadline passes.

#### Scenario: Context values
- **Given** a `<engine>` server with a tool that reads `<accessor>` from its context
- **When** a client calls it with `traceparent` `00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01`
- **Then** the tool sees `<seen>`

##### Examples:
| engine | accessor | seen |
|--------|----------|------|
| net/http | `srvr.TraceFromContext` | trace id `4bf92f3577b34da6a3ce929d0e0e4736`, ok `true` |
| gin | `srvr.TraceFromContext` | trace id `4bf92f3577b34da6a3ce929d0e0e4736`, ok `true` |
| net/http | `srvr.LoggerFromContext` | a logger whose records carry `trace_id` `4bf92f3577b34da6a3ce929d0e0e4736` |

#### Scenario: Cancellation
- **Given** a net/http server with a tool that blocks until its context is done
- **When** `<event>` happens during the call
- **Then** the tool's context error is `<err>` and its goroutine ends

##### Examples:
| event | err |
|-------|-----|
| the client sends `notifications/cancelled` for the call | `context.Canceled` |
| the client deletes the session | `context.Canceled` |
| `Shutdown` with a `200ms` context expires | `context.Canceled` |
