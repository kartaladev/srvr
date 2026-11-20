# Spec Delta

## Purpose

Serves a Model Context Protocol server over the Streamable HTTP transport on a srvr engine, with srvr's safe defaults for sessions, protection, error rendering, logging and shutdown, registered through the same route configurer model as every other route.

## ADDED Requirements

### Requirement: MCP endpoint registers as a route configurer
The MCP mount SHALL be a route configurer for each supported engine (net/http, gin), registered with the server's `Register`. It SHALL route `GET`, `POST` and `DELETE` at its path and list them in the server's route listing, attributed to the mount. A broken mount SHALL fail `Start` with an error wrapping `ErrRouteConfig`.

#### Scenario: Mount on an engine
- **Given** a `<engine>` server with an MCP mount at `<path>` registered before `Start`
- **When** the server starts
- **Then** `Start` signals readiness
- **And** the route listing holds `<routes>`

##### Examples: accepted
| engine | path | routes |
|--------|------|--------|
| net/http | `/mcp` | `GET /mcp`, `POST /mcp`, `DELETE /mcp` |
| gin | `/mcp` | `GET /mcp`, `POST /mcp`, `DELETE /mcp` |
| net/http | `/api/mcp/` | `GET /api/mcp`, `POST /api/mcp`, `DELETE /api/mcp` |

#### Scenario: Broken mount fails Start
- **Given** a `<engine>` server with `<mount>`
- **When** `<action>` is called
- **Then** it returns an error wrapping `<sentinel>`
- **And** the error names `<named>`

##### Examples: rejected configuration
| engine | mount | action | sentinel | named |
|--------|-------|--------|----------|-------|
| net/http | an MCP mount at `/mcp` with a nil SDK server | `Start` | `srvr.ErrRouteConfig` | `MCP.Server` |
| gin | an MCP mount at `/mcp` with a nil SDK server | `Start` | `srvr.ErrRouteConfig` | `MCP.Server` |
| net/http | an MCP mount at `(empty)` path | `Start` | `srvr.ErrRouteConfig` | `MCP.Path` |
| net/http | an MCP mount at `mcp` (no leading slash) | `Start` | `srvr.ErrRouteConfig` | `MCP.Path` |
| net/http | an MCP mount at `/mcp/{id}` | `Start` | `srvr.ErrRouteConfig` | `MCP.Path` |
| net/http | two MCP mounts at `/mcp` | `Start` | `srvr.ErrRouteConfig` | `/mcp` |
| net/http | one MCP endpoint mounted at `/a` and at `/b` | `Start` | `srvr.ErrRouteConfig` | `/b` |
| net/http | an MCP mount with `WithMaxSessions(-1)` and `WithSessionIdleTimeout(-1s)` | `Start` | `srvr.ErrRouteConfig` | `MCP.MaxSessions` and `MCP.SessionIdleTimeout` |
| net/http | an MCP mount at `/mcp` and a route `POST /mcp` | `Start` | `srvr.ErrRouteConfig` | `/mcp` |
| gin | an MCP mount at `/mcp` and a route `POST /mcp` | `Start` | `srvr.ErrRouteConfig` | `/mcp` |
| net/http | a nil option passed to the MCP mount | `Start` | `srvr.ErrRouteConfig` | `MCPOption` |

##### Examples: lifecycle misuse
| engine | mount | action | sentinel | named |
|--------|-------|--------|----------|-------|
| net/http | an MCP mount at `/mcp` registered after `Start` | `Register` | `srvr.ErrServerStarted` | (none) |
| gin | an MCP mount at `/mcp` registered after `Shutdown` | `Register` | `srvr.ErrServerClosed` | (none) |

### Requirement: Sessions are stateful by default
With no session configuration, a successful `initialize` SHALL return an `Mcp-Session-Id` header. Later requests carrying that id SHALL be served in that session. A non-initialize request without the id SHALL get a `400` problem; one with an unknown or closed id SHALL get a `404` problem.

#### Scenario: Session id round trip
- **Given** a `<engine>` server with a default MCP mount at `/mcp`
- **When** a client sends `POST /mcp` with `<message>` and session header `<session>`
- **Then** the response status is `<status>`
- **And** the response `Mcp-Session-Id` header is `<returned>`

##### Examples: accepted
| engine | message | session | status | returned |
|--------|---------|---------|--------|----------|
| net/http | `initialize` | (none) | `200` | a new non-empty id |
| gin | `initialize` | (none) | `200` | a new non-empty id |
| net/http | `tools/list` | the id from `initialize` | `200` | (none) |
| gin | `tools/list` | the id from `initialize` | `200` | (none) |

##### Examples: rejected
| engine | message | session | status | returned |
|--------|---------|---------|--------|----------|
| net/http | `tools/list` | (none) | `400` | (none) |
| net/http | `tools/list` | `does-not-exist` | `404` | (none) |
| gin | `tools/list` | `does-not-exist` | `404` | (none) |
| net/http | `tools/list` | the id of a session closed by `DELETE /mcp` | `404` | (none) |
| net/http | `tools/list` | (empty) | `400` | (none) |

#### Scenario: Client ends its session
- **Given** a `<engine>` server with a default MCP mount at `/mcp` and an open session
- **When** the client sends `DELETE /mcp` with `<session>`
- **Then** the response status is `<status>`

##### Examples:
| engine | session | status |
|--------|---------|--------|
| net/http | the open session's id | `204` |
| gin | the open session's id | `204` |
| net/http | `does-not-exist` | `404` |
| net/http | (none) | `400` |

### Requirement: Stateless mode is an explicit opt-out
With stateless mode set, through an option or the config struct, the mount SHALL NOT issue or read `Mcp-Session-Id`, SHALL serve each `POST` on its own, and SHALL answer `GET` and `DELETE` with a `405` problem whose `Allow` header is `POST`.

#### Scenario: Stateless mount
- **Given** a `<engine>` server with an MCP mount at `/mcp` configured stateless by `<configured_by>`
- **When** a client sends `<request>`
- **Then** the response status is `<status>`
- **And** the `Allow` header is `<allow>` and the `Mcp-Session-Id` header is `<returned>`

##### Examples:
| engine | configured_by | request | status | allow | returned |
|--------|---------------|---------|--------|-------|----------|
| net/http | `WithStateless()` | `POST /mcp` with `initialize` | `200` | (none) | (none) |
| gin | `WithStateless()` | `POST /mcp` with `initialize` | `200` | (none) | (none) |
| net/http | config `stateless: true` | `POST /mcp` with `tools/list` and no session header | `200` | (none) | (none) |
| net/http | `WithStateless()` | `GET /mcp` | `405` | `POST` | (none) |
| gin | `WithStateless()` | `DELETE /mcp` | `405` | `POST` | (none) |

### Requirement: Concurrent sessions are capped
By default the mount SHALL allow at most `1000` open sessions. An `initialize` beyond the cap SHALL get a `503` problem and SHALL NOT create a session; existing sessions SHALL be unaffected, and closing one SHALL admit a new one. The cap SHALL be configurable; a negative value SHALL fail `Start`, and removing the cap SHALL need an explicitly named opt-in.

#### Scenario: Session cap
- **Given** a net/http server with an MCP mount whose session cap is set by `<configured_by>` and `<open>` open sessions
- **When** a client sends `initialize`
- **Then** the response status is `<status>`

##### Examples: default and boundary
| configured_by | open | status |
|---------------|------|--------|
| (default) | `999` | `200` |
| (default) | `1000` | `503` |
| `WithMaxSessions(2)` | `1` | `200` |
| `WithMaxSessions(2)` | `2` | `503` |
| config `max_sessions: 2` | `2` | `503` |
| `WithMaxSessions(2)` | `2`, then one closed by `DELETE` | `200` |
| `WithUnlimitedSessions()` | `5000` | `200` |
| config `max_sessions: 0` | `1000` | `503` |

#### Scenario: Concurrent initialize at the cap
- **Given** a net/http server with `WithMaxSessions(<cap>)` and no open session
- **When** `<clients>` clients send `initialize` at the same time
- **Then** exactly `<accepted>` get `200` and the rest get `503`
- **And** the run is clean under `-race`

##### Examples:
| cap | clients | accepted |
|-----|---------|----------|
| `10` | `50` | `10` |
| `1` | `2` | `1` |

#### Scenario: Invalid cap
- **Given** an MCP mount configured with `<configured_by>`
- **When** the server starts
- **Then** `Start` returns an error wrapping `srvr.ErrRouteConfig` naming `<named>`

##### Examples:
| configured_by | named |
|---------------|-------|
| `WithMaxSessions(-1)` | `MCP.MaxSessions` |
| config `max_sessions: -5` | `MCP.MaxSessions` |

### Requirement: Idle sessions expire
By default a session that receives no request for `30m` SHALL be closed, and a later request with its id SHALL get a `404` problem. The timeout SHALL be configurable; a negative value SHALL fail `Start`, and disabling expiry SHALL need an explicitly named opt-in.

#### Scenario: Idle expiry
- **Given** a net/http server with an MCP mount whose idle timeout is set by `<configured_by>` and a session idle for `<idle>`
- **When** the client sends `tools/list` with that session's id
- **Then** the response status is `<status>`

##### Examples:
| configured_by | idle | status |
|---------------|------|--------|
| (default) | `29m` | `200` |
| (default) | `31m` | `404` |
| `WithSessionIdleTimeout(100ms)` | `50ms` | `200` |
| `WithSessionIdleTimeout(100ms)` | `200ms` | `404` |
| config `session_idle_timeout: 100ms` | `200ms` | `404` |
| `WithoutSessionIdleTimeout()` | `2h` | `200` |

##### Examples: rejected
| configured_by | idle | status |
|---------------|------|--------|
| `WithSessionIdleTimeout(-1s)` | (none) | `Start` fails naming `MCP.SessionIdleTimeout` |

### Requirement: Dead clients are detected by keepalive
For protocol versions that support keepalive, the mount SHALL ping each session every `30s` by default and SHALL close a session after `3` consecutive unanswered pings. Both values SHALL be configurable; a negative value SHALL fail `Start`.

#### Scenario: Keepalive
- **Given** a net/http server with an MCP mount with `<configured_by>` and a session whose client `<client>`
- **When** `<elapsed>` passes
- **Then** the session is `<state>`

##### Examples:
| configured_by | client | elapsed | state |
|---------------|--------|---------|-------|
| `WithKeepAlive(50ms, 3)` | answers pings | `500ms` | open |
| `WithKeepAlive(50ms, 3)` | never answers pings | `500ms` | closed |
| config `keepalive: 50ms`, `keepalive_failures: 3` | never answers pings | `500ms` | closed |
| `WithKeepAlive(50ms, 3)` | answers 2 of 3 pings | `500ms` | open |

##### Examples: rejected
| configured_by | client | elapsed | state |
|---------------|--------|---------|-------|
| `WithKeepAlive(-1s, 3)` | (none) | (none) | `Start` fails naming `MCP.KeepAlive` |
| `WithKeepAlive(30s, -1)` | (none) | (none) | `Start` fails naming `MCP.KeepAliveFailures` |

### Requirement: Event streams outlive the write timeout
An MCP response sent as `text/event-stream`, either the `GET` stream or a streamed `POST` response, SHALL NOT be cut by the server's `WriteTimeout`; it SHALL end when its request or session ends. An `application/json` response SHALL stay bounded by `WriteTimeout`.

#### Scenario: Stream outlives WriteTimeout
- **Given** a `<engine>` server with `WriteTimeout` `1s` and an open session with a `<stream>`
- **When** the server sends a notification `<after>` after the stream started
- **Then** the client receives `<received>`

##### Examples:
| engine | stream | after | received |
|--------|--------|-------|----------|
| net/http | `GET /mcp` event stream | `2s` | the notification |
| gin | `GET /mcp` event stream | `2s` | the notification |
| net/http | streamed `tools/call` response from a tool running `2s` | `2s` | the tool result |
| gin | streamed `tools/call` response from a tool running `2s` | `2s` | the tool result |

#### Scenario: Client leaves the stream
- **Given** a `<engine>` server with an open `GET /mcp` event stream
- **When** the client closes the connection
- **Then** the stream's server goroutines end and the session stays `<state>`

##### Examples:
| engine | state |
|--------|-------|
| net/http | open until it expires or is deleted |
| gin | open until it expires or is deleted |

### Requirement: Shutdown closes sessions before the drain
When the server stops, through `Shutdown` or `Start`'s context, the mount SHALL close every open session before in-flight requests are drained. Open event streams SHALL end, a request already being answered SHALL finish within the drain, and every goroutine the mount started SHALL have ended when `Shutdown` returns.

#### Scenario: Stop with open sessions
- **Given** a `<engine>` server with `<open>` open sessions, `<streams>` open `GET /mcp` streams and `ShutdownTimeout` `5s`
- **When** the server stops through `<stop>`
- **Then** it returns `<result>` within `1s`
- **And** goleak reports no leaked goroutine

##### Examples:
| engine | open | streams | stop | result |
|--------|------|---------|------|--------|
| net/http | `3` | `3` | `Shutdown` | nil |
| gin | `3` | `3` | `Shutdown` | nil |
| net/http | `3` | `0` | `Start` context cancelled | nil from `Start` |
| net/http | `0` | `0` | `Shutdown` | nil |
| net/http | `1` | `1` | `Shutdown` before `Start` | nil |

#### Scenario: Request in flight at shutdown
- **Given** a net/http server whose tool `<tool>` is running for a session
- **When** `Shutdown` is called with a context of `<budget>`
- **Then** the client receives `<received>`

##### Examples:
| tool | budget | received |
|------|--------|----------|
| a tool that returns after `100ms` | `5s` | the tool result |
| a tool that ignores its context and blocks | `200ms` | a closed connection, and `Shutdown` returns `context.DeadlineExceeded` |

### Requirement: Transport refusals are problems
A request the mount refuses before the protocol starts SHALL be answered as an RFC 9457 problem through the server's prblm options, with `trace_id`, never the transport's plain text. JSON-RPC responses within the protocol SHALL keep MCP's JSON-RPC error format.

#### Scenario: Refusal before the protocol
- **Given** a `<engine>` server with a default MCP mount at `/mcp`
- **When** a client sends `<request>`
- **Then** the response status is `<status>` with content type `<content_type>`
- **And** the `Allow` header is `<allow>` and the body contains no transport error text

##### Examples:
| engine | request | status | content_type | allow |
|--------|---------|--------|--------------|-------|
| net/http | `PUT /mcp` | `405` | `application/problem+json` | `DELETE, GET, POST` |
| gin | `PATCH /mcp` | `405` | `application/problem+json` | `DELETE, GET, POST` |
| net/http | `POST /mcp` with `Content-Type: text/plain` | `415` | `application/problem+json` | (none) |
| net/http | `POST /mcp` with `Accept: text/html` | `406` | `application/problem+json` | (none) |
| gin | `POST /mcp` with `Accept: text/html` | `406` | `application/problem+json` | (none) |
| net/http | `POST /mcp` with body `{"jsonrpc":` (malformed JSON) | `400` | `application/json` (JSON-RPC parse error `-32700`) | (none) |
| net/http | `POST /mcp` with `tools/call` for an unknown tool | `200` | `application/json` (JSON-RPC error) | (none) |

### Requirement: Request bodies use the server's limit
The mount SHALL limit request bodies to the server's `MaxRequestBodyBytes`, and SHALL answer a larger body with a `413` problem without creating a session or calling a tool.

#### Scenario: Body limit
- **Given** a `<engine>` server with `MaxRequestBodyBytes` `<limit>` and a default MCP mount
- **When** a client sends `POST /mcp` with a `tools/call` body of `<size>` bytes
- **Then** the response status is `<status>`

##### Examples:
| engine | limit | size | status |
|--------|-------|------|--------|
| net/http | (default) | `4194304` | `200` |
| net/http | (default) | `4194305` | `413` |
| gin | `1024` | `1025` | `413` |
| net/http | `1024` | `1024` | `200` |

### Requirement: Cross-origin and rebinding protection are on by default
The mount SHALL refuse with a `403` problem a cross-origin browser request and a request reaching a loopback address with a non-loopback `Host`. Turning either protection off SHALL need its own explicitly named opt-in.

#### Scenario: Protection
- **Given** a `<engine>` server listening on `127.0.0.1` with an MCP mount configured `<configured_by>`
- **When** a client sends `POST /mcp` `initialize` with `<headers>`
- **Then** the response status is `<status>`

##### Examples: default
| engine | configured_by | headers | status |
|--------|---------------|---------|--------|
| net/http | (default) | `Host: 127.0.0.1:8080` | `200` |
| net/http | (default) | `Host: evil.example` | `403` |
| gin | (default) | `Host: evil.example` | `403` |
| net/http | (default) | `Sec-Fetch-Site: cross-site`, `Origin: https://evil.example` | `403` |
| gin | (default) | `Sec-Fetch-Site: cross-site`, `Origin: https://evil.example` | `403` |
| net/http | (default) | `Sec-Fetch-Site: same-origin` | `200` |
| net/http | (default) | no `Origin` and no `Sec-Fetch-Site` (non-browser client) | `200` |

##### Examples: opt-out
| engine | configured_by | headers | status |
|--------|---------------|---------|--------|
| net/http | `WithoutRebindingProtection()` | `Host: evil.example` | `200` |
| net/http | config `disable_rebinding_protection: true` | `Host: evil.example` | `200` |
| net/http | `WithTrustedOrigins("https://app.example")` | `Sec-Fetch-Site: cross-site`, `Origin: https://app.example` | `200` |
| net/http | `WithoutCrossOriginProtection()` | `Sec-Fetch-Site: cross-site`, `Origin: https://evil.example` | `200` |

### Requirement: Triage records name the MCP call
The access record and any failed record of an MCP request SHALL carry `mcp.method.name`, `jsonrpc.request.id`, `mcp.protocol.version`, `gen_ai.tool.name` for `tools/call`, and `srvr.mcp.session.ref`, a non-reversible reference to the session id. A key with no value SHALL be absent. The raw session id SHALL appear only with an explicitly named opt-in.

#### Scenario: MCP attributes on the access record
- **Given** a `<engine>` server with an MCP mount configured `<configured_by>` and an open session
- **When** a client sends `POST /mcp` with `<message>`
- **Then** the access record carries `<present>`
- **And** it does not carry `<absent>`

##### Examples:
| engine | configured_by | message | present | absent |
|--------|---------------|---------|---------|--------|
| net/http | (default) | `tools/call` `get_order`, id `7` | `mcp.method.name` `tools/call`, `gen_ai.tool.name` `get_order`, `jsonrpc.request.id` `7`, `srvr.mcp.session.ref` | `mcp.session.id` |
| gin | (default) | `tools/call` `get_order`, id `"a"` | `mcp.method.name` `tools/call`, `gen_ai.tool.name` `get_order`, `jsonrpc.request.id` `a`, `srvr.mcp.session.ref` | `mcp.session.id` |
| net/http | (default) | `tools/list`, id `1` | `mcp.method.name` `tools/list`, `jsonrpc.request.id` `1` | `gen_ai.tool.name` |
| net/http | (default) | `notifications/initialized` | `mcp.method.name` `notifications/initialized` | `jsonrpc.request.id` |
| net/http | (default) | a JSON-RPC response from the client | `srvr.mcp.session.ref` | `mcp.method.name` |
| net/http | (default) | a malformed body | (only the standard access keys) | `mcp.method.name` |
| net/http | `WithSessionIDInLogs()` | `tools/list`, id `1` | `mcp.session.id`, `srvr.mcp.session.ref` | (none) |
| net/http | config `log_session_id: true` | `tools/list`, id `1` | `mcp.session.id` | (none) |

#### Scenario: Tool traffic is never logged
- **Given** a net/http server with an MCP mount at log level `DEBUG`
- **When** a client calls a tool with arguments `<arguments>` and the tool returns `<result>`
- **Then** no record contains `<hidden>`

##### Examples:
| arguments | result | hidden |
|-----------|--------|--------|
| `{"card":"4111111111111111"}` | `{"ok":true}` | `4111111111111111` |
| `{"q":"x"}` | `{"secret":"s3cr3t"}` | `s3cr3t` |
| `{"name":"line1\nline2"}` | `{}` | `line1\nline2` |

#### Scenario: Hostile values in logged attributes
- **Given** a net/http server with an MCP mount
- **When** a client sends `tools/call` with `<field>` set to `<value>`
- **Then** the record's `<key>` is `<logged>` on one output line

##### Examples:
| field | value | key | logged |
|-------|-------|-----|--------|
| tool name | `get\norder` | `gen_ai.tool.name` | `get\norder` JSON-escaped |
| request id | a string of `10000` characters | `jsonrpc.request.id` | cut to the server's `MaxAttrBytes` |
| method | `tools/call` with a `{"x":1}` object as id | `jsonrpc.request.id` | absent (invalid id type) |

### Requirement: Endpoint is protected through the security bridge
When the server has the scrty bridge wired, the MCP endpoint SHALL be behind the chain like every route. A refused request SHALL get the bridge's problem with any `WWW-Authenticate` header scrty set, SHALL NOT create a session, and the endpoint SHALL take part in the bridge's startup collision check.

#### Scenario: Authenticated endpoint
- **Given** a `<engine>` server with the scrty bridge requiring a bearer token on `/mcp` and a default MCP mount
- **When** a client sends `POST /mcp` `initialize` with `<authorization>`
- **Then** the response status is `<status>`
- **And** the open session count is `<sessions>`

##### Examples:
| engine | authorization | status | sessions |
|--------|---------------|--------|----------|
| net/http | a valid token | `200` | `1` |
| gin | a valid token | `200` | `1` |
| net/http | (none) | `401` | `0` |
| gin | an expired token | `401` | `0` |

#### Scenario: Collision with a scrty endpoint
- **Given** a net/http server with the scrty bridge whose built-in endpoint is `<endpoint>`
- **And** an MCP mount at `<path>`
- **When** the server starts
- **Then** `Start` `<outcome>`

##### Examples:
| endpoint | path | outcome |
|----------|------|---------|
| `POST /auth/login` | `/mcp` | succeeds |
| `POST /mcp` | `/mcp` | fails with an error naming `/mcp` |
