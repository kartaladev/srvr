# Spec Delta

## MODIFIED Requirements

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
| `PURGE /files` on a `Mount` handler returning `errors.New("a")` | `BREW /files` on the same handler returning `errors.New("a")` | equal |
| `QUERY /search` returning `errors.New("a")` | `POST /search` returning `errors.New("a")` | different |
| `get /nope` (unmatched, lowercase) | `GET /nope` (unmatched) | different |

## ADDED Requirements

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
