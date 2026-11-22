# triage-error-records Specification

## Purpose

Defines the error records srvr writes so that an AI assistant or a human can triage a failed request from its log record alone: what failed, where, what the client saw, which build, and whether it is a known failure.

## Requirements

### Requirement: Failed-request records describe the error
An `http.request.failed` record for a returned error SHALL carry `error.type` (the Go type of the innermost error reached through `Unwrap() error`; an error wrapping several errors is itself the innermost), `exception.message` (the full `Error()` text), and `error.chain` (the Go types from outermost to innermost). These SHALL appear in logs only, never in the response.

#### Scenario: Error description
- **Given** a handler that returns `<error>`
- **When** the request fails
- **Then** the record has `error.type` `<type>`, `exception.message` `<message>` and `error.chain` `<chain>`

##### Examples:
| error | type | message | chain |
|-------|------|---------|-------|
| `errors.New("boom")` | `*errors.errorString` | `boom` | `["*errors.errorString"]` |
| `fmt.Errorf("insert user: %w", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED})` | `syscall.Errno` | `insert user: dial: connection refused` | `["*fmt.wrapError","*net.OpError","syscall.Errno"]` |
| `fmt.Errorf("a: %w", prblm.ErrNotFound)` | `*errors.errorString` | `a: prblm: not found` | `["*fmt.wrapError","*errors.errorString"]` |
| `errors.Join(errors.New("x"), errors.New("y"))` | `*errors.joinError` | `x\ny` | `["*errors.joinError","*errors.errorString","*errors.errorString"]` |

### Requirement: Records include what the client saw
A failed-request record SHALL carry a `problem` group with the `status` actually sent and, when the renderer gave them, its `type` and `title` (prblm, the default, always gives both), or `srvr.response.committed` `true` with no `problem` when the response had already started. When srvr replaced a faulty renderer's answer with its safe `500`, the record SHALL carry `srvr.renderer.failure` naming the fault.

#### Scenario: Problem group
- **Given** a handler that `<behaviour>`
- **When** the request fails
- **Then** the record has `<problem>`

##### Examples:
| behaviour | problem |
|-----------|---------|
| returns `prblm.ErrConflict` | `problem.status` `409`, `problem.type` `about:blank`, `problem.title` `Conflict` |
| returns `errors.New("boom")` | `problem.status` `500`, `problem.title` `Internal Server Error` |
| writes `200` then returns `errors.New("late")` | no `problem` and `srvr.response.committed` `true` |
| writes `200` then panics with `"late"` | no `problem`, `srvr.response.committed` `true`, `error.kind` `panic` and `level` `ERROR` |
| returns `ErrQuota` with `WithErrorRenderer(R)`, `R` answering `429` with `Type` and `Title` empty | `problem.status` `429`, no `problem.type`, no `problem.title`, no `srvr.renderer.failure` |
| returns `ErrQuota` with `WithErrorRenderer(R)`, `R` answering `429` with `Type` `https://errors.example/quota` and `Title` `Quota exceeded` | `problem.status` `429`, `problem.type` `https://errors.example/quota`, `problem.title` `Quota exceeded` |
| returns `ErrQuota` with `WithErrorRenderer(R)`, `R` setting a `Title` of 10 000 bytes with a newline and an ANSI escape | `problem.title` cut to the log's `MaxAttrBytes` with srvr's truncation marker and its control characters escaped, as for every logged value |
| returns `errors.New("boom")` with `WithErrorRenderer(F)`, `F` panicking with `"secret token"` | `problem.status` `500`, `problem.type` `about:blank`, `problem.title` `Internal Server Error`, `srvr.renderer.failure` `panic`, the original error's `error.chain`; the panic value is not logged |

### Requirement: Records name the handler's code location
A failed-request record SHALL carry `code.function.name`, `code.file.path` and `code.line.number` of the route's handler, resolved once at registration, so the failing code is identifiable even when the error carries no stack.

#### Scenario: Handler location
- **Given** a route registered with handler `<handler>`
- **When** the handler returns an error
- **Then** the record has `code.function.name` `<function>`

##### Examples:
| handler | function |
|---------|----------|
| method value `h.Register` on type `RegistrationHandler` in package `main` | `main.RegistrationHandler.Register` |
| a function literal inside `main.routes` | `main.routes.func1` |
| a mounted `http.Handler` of type `*main.Files` | `*main.Files.ServeHTTP` |

### Requirement: Panics are recorded with their stack
A recovered panic SHALL produce an `http.request.failed` record at `ERROR`, also when the response had already started, with `error.kind` `panic`, `error.type` `panic`, `exception.type` the panic value's Go type, `exception.message` from the panic value, and `exception.stacktrace` with the goroutine stack at the panic, capped at 32 KiB.

#### Scenario: Panic record
- **Given** a handler that panics with `<value>`
- **When** the panic is recovered
- **Then** the record has `error.kind` `panic`, `error.type` `panic`, `exception.message` `<message>` and `exception.type` `<type>`
- **And** `exception.stacktrace` contains `<frame>`

##### Examples:
| value | message | type | frame |
|-------|---------|------|-------|
| `"boom"` | `boom` | `string` | the handler's function name |
| `errors.New("db down")` | `db down` | `*errors.errorString` | the handler's function name |
| a nil map write | `assignment to entry in nil map` | `runtime.plainError` | the handler's function name |
| a value whose `String()` panics | `<panic formatting value>` | its Go type | the handler's function name |

#### Scenario: Oversized stack
- **Given** a recovered panic whose captured stack is `<stack>`
- **When** the record is written
- **Then** `exception.stacktrace` is at most 32768 bytes and `srvr.stacktrace.truncated` is `<truncated>`

##### Examples:
| stack | truncated |
|-------|-----------|
| from recursion depth `1` | `false` |
| from recursion depth `10000` (the runtime elides middle frames, about 15 KiB) | `false` |
| exactly 32768 bytes | `false` |
| 32769 bytes | `true` |

### Requirement: Errors are grouped by a stable fingerprint
A failed-request record SHALL carry `error.fingerprint`: 16 lowercase hex characters derived from the route, the HTTP method and the error-type chain (or, for panics, the panic value's type and the top application frame), never from message text. A method other than `GET`, `HEAD`, `POST`, `PUT`, `DELETE`, `CONNECT`, `OPTIONS`, `TRACE`, `PATCH` and `QUERY` SHALL count as `_OTHER`, so a client cannot create unbounded fingerprints.

#### Scenario: Fingerprint stability
- **Given** two failed requests `<first>` and `<second>`
- **When** both records are written
- **Then** their `error.fingerprint` values are `<relation>`

##### Examples:
| first | second | relation |
|-------|--------|----------|
| `GET /users/1` returning `fmt.Errorf("user %d: %w", 1, sql.ErrNoRows)` | `GET /users/2` returning `fmt.Errorf("user %d: %w", 2, sql.ErrNoRows)` | equal |
| `GET /users/1` returning `sql.ErrNoRows` wrapped once | `GET /users/1` returning `sql.ErrNoRows` wrapped twice | different |
| `GET /users/1` returning `errors.New("a")` | `POST /users` returning `errors.New("a")` | different |
| a panic with `"x"` in the same handler | a panic with `"y"` in the same handler | equal |

##### Examples: client-chosen methods
| first | second | relation |
|-------|--------|----------|
| `FOO1 /nope` (unmatched) | `FOO2 /nope` (unmatched) | equal |
| `FOO1 /nope` (unmatched) | `GET /nope` (unmatched) | different |
| `PURGE /files` on a method-less route (`Handle("", "/files", h)`) returning `errors.New("a")` | `BREW /files` on the same route returning `errors.New("a")` | equal |
| `QUERY /search` returning `errors.New("a")` | `POST /search` returning `errors.New("a")` | different |
| `get /nope` (unmatched, lowercase) | `GET /nope` (unmatched) | different |

### Requirement: Error walking is bounded and safe
Walking an error chain SHALL stop at 32 levels and 64 errors total, SHALL mark truncation with `srvr.error.truncated` `true`, SHALL truncate `exception.message` to 8 KiB, and SHALL recover a panicking `Error()` or `Unwrap()` without failing the request or the record.

#### Scenario: Hostile error values
- **Given** a handler that returns `<error>`
- **When** the request fails
- **Then** the response status is `500`
- **And** the record has `<outcome>`

##### Examples:
| error | outcome |
|-------|---------|
| an error wrapped 100 levels deep | `error.chain` of 32 entries and `srvr.error.truncated` `true` |
| an error whose `Unwrap()` returns itself | `error.chain` of at most 32 entries and `srvr.error.truncated` `true` |
| `errors.Join` of 100 errors | `error.chain` of 64 entries and `srvr.error.truncated` `true` |
| an error whose `Error()` panics | `exception.message` `<panic in Error(): main.badErr>` |
| an error whose `Unwrap()` panics | `error.chain` up to that error and `srvr.error.truncated` `true` |
| a typed-nil `*MyErr` returned as `error` | `error.type` `*main.MyErr` and `exception.message` `<nil *main.MyErr>` |
| an error with a 1 MiB message | `exception.message` of 8192 bytes of the message followed by `…[truncated]` |

### Requirement: Error records carry the build that failed
Every failed-request record SHALL carry `service.name`, `service.version` and, when present in build information, `vcs.revision`, so a failure maps to the deployed code.

#### Scenario: Build attributes on errors
- **Given** a server configured with `WithServiceConfig(ServiceConfig{Name: "registration-api", Version: "<version>"})` built at revision `<revision>`
- **When** a request fails
- **Then** the record has `service.version` `<version>` and `vcs.revision` `<logged>`

##### Examples:
| version | revision | logged |
|---------|----------|--------|
| `1.4.2` | `9c1e2ab` | `9c1e2ab` |
| `1.4.2` | (none) | (none) |

### Requirement: Error text can be redacted by the consumer
The consumer SHALL be able to install a redactor applied to `exception.message` and `exception.stacktrace` before writing, on request failure records and on srvr's lifecycle records (server-lifecycle / Lifecycle error text is redacted and limited); a redactor that panics SHALL cause the attribute to be replaced by `[redaction failed]`, never the unredacted text.

#### Scenario: Redacting error text
- **Given** a server configured with `<redactor>`
- **When** a handler returns `errors.New("duplicate key alice@example.com")`
- **Then** the record's `exception.message` is `<message>`

##### Examples:
| redactor | message |
|----------|---------|
| (none) | `duplicate key alice@example.com` |
| `WithErrorRedactor(emailMasker)` | `duplicate key ***@example.com` |
| `WithErrorRedactor(panicky)` | `[redaction failed]` |
| `WithErrorRedactor(nil)` | construction error wrapping `srvr.ErrConfig` |

### Requirement: Secrets in error text are redacted by default
With no redactor configured, srvr SHALL mask, in `exception.message` and `exception.stacktrace`, the value of every URL query parameter and of every `key=value` or `key: value` pair whose key contains `token`, `key`, `secret`, `password`, `passwd`, `authorization`, `cookie`, `session` or `credential` (case-insensitive), replacing it with `***`. `WithErrorRedactor(fn)` replaces this redactor; `WithoutErrorRedaction()` writes the text unredacted.

#### Scenario: Default redaction
- **Given** a server configured with `<config>`
- **When** a handler returns an error whose text is `<text>`
- **Then** the record's `exception.message` is `<message>`

##### Examples: masked by default
| config | text | message |
|--------|------|---------|
| (none) | `charge: Get "http://pay.local/v1/charge?api_key=SECRET&amount=5": dial tcp: refused` | `charge: Get "http://pay.local/v1/charge?api_key=***&amount=***": dial tcp: refused` |
| (none) | `login failed: password=hunter2 user=bob` | `login failed: password=*** user=bob` |
| (none) | `upstream said Authorization: Bearer abc.def` | `upstream said Authorization: ***` |
| (none) | `bad cookie session_id=7f3a; Path=/` | `bad cookie session_id=***; Path=/` |
| (none) | `ACCESS_TOKEN=xyz rejected` | `ACCESS_TOKEN=*** rejected` |
| (none) | `duplicate key alice@example.com` | `duplicate key alice@example.com` |
| (none) | `GET http://a.local/users/42 failed` | `GET http://a.local/users/42 failed` |
| (none) | `query ?=x and ?& are odd` | `query ?=x and ?& are odd` |
| (none) | (empty) | (empty) |

##### Examples: overrides
| config | text | message |
|--------|------|---------|
| `WithErrorRedactor(strings.ToUpper)` | `password=hunter2` | `PASSWORD=HUNTER2` |
| `WithoutErrorRedaction()` | `login failed: password=hunter2` | `login failed: password=hunter2` |
| `WithoutErrorRedaction()`, then `WithErrorRedactor(strings.ToUpper)` | `password=hunter2` | `PASSWORD=HUNTER2` |
| `WithErrorRedactor(strings.ToUpper)`, then `WithoutErrorRedaction()` | `password=hunter2` | `password=hunter2` |

#### Scenario: Default redaction of panics and lifecycle text
- **Given** a server with no redactor configured
- **When** `<event>`
- **Then** `<attribute>` contains `<contained>` and not `<absent>`

##### Examples:
| event | attribute | contained | absent |
|-------|-----------|-----------|--------|
| a handler panics with `"token=abc"` | `exception.message` of `http.request.failed` | `token=***` | `token=abc` |
| a handler panics with an error whose text is `password=p` | `exception.stacktrace` of `http.request.failed` | `password=***` if the stack quotes it | `password=p` |
| a `BeforeDrain` hook returns `errors.New("secret=s")` | `exception.message` of `server.shutdown_hook.failed` | `secret=***` | `secret=s` |
| a route configurer panics with `"api_key=k"` | `exception.message` of `server.failed` | `api_key=***` | `api_key=k` |
