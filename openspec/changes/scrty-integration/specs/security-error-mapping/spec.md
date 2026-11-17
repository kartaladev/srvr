# Spec Delta

## Purpose

Defines how refusals and challenges from a scrty security chain become RFC 9457 problems in a srvr server, with the status scrty chose and the same body on every engine, and without leaking credentials, tokens or internal error text.

## ADDED Requirements

### Requirement: Refusals use scrty's status
A scrty refusal reaching a srvr server SHALL be rendered as a problem whose status is `httpsec.StatusForError(err)`. Its `title` SHALL be the standard status text, and it SHALL carry no `detail`. Neither the error text nor any wrapped error's text SHALL appear in the response.

#### Scenario: Refusal statuses
- **Given** a server on `<engine>` wired with a scrty chain that refuses `GET /orders` with `<refusal>`
- **When** a client sends `GET /orders`
- **Then** the response status is `<status>` with content type `application/problem+json`
- **And** the body has `title` `<title>`, has no `detail`, and does not contain `<hidden>`

##### Examples: net/http
| engine | refusal | status | title | hidden |
|--------|---------|--------|-------|--------|
| net/http | `httpsec.ErrAuthenticationRequired` | `401` | `Unauthorized` | `httpsec:` |
| net/http | `ratelimit.ErrThrottled` | `401` | `Unauthorized` | `ratelimit` |
| net/http | `httpsec.ErrCredentialsMissing` | `400` | `Bad Request` | `httpsec:` |
| net/http | `httpsec.ErrMalformedRequest` | `400` | `Bad Request` | `httpsec:` |
| net/http | `httpsec.ErrHostNotAllowed` | `400` | `Bad Request` | `httpsec:` |
| net/http | `httpsec.ErrCrossSiteRequest` | `403` | `Forbidden` | `httpsec:` |
| net/http | `httpsec.ErrRequestTooLarge` | `413` | `Request Entity Too Large` | `httpsec:` |
| net/http | `lockout.ErrAccountLocked` | `429` | `Too Many Requests` | `lockout` |
| net/http | `httpsec.ErrChallengeUnavailable` wrapping `errors.New("redis: i/o timeout")` | `500` | `Internal Server Error` | `redis` |
| net/http | an issuing failure wrapping `errors.New("kms: denied")` | `500` | `Internal Server Error` | `kms` |

##### Examples: every engine
| engine | refusal | status | title | hidden |
|--------|---------|--------|-------|--------|
| gin | `httpsec.ErrAuthenticationRequired` | `401` | `Unauthorized` | `httpsec:` |
| fiber v3 | `httpsec.ErrAuthenticationRequired` | `401` | `Unauthorized` | `httpsec:` |
| gin | `lockout.ErrAccountLocked` | `429` | `Too Many Requests` | `lockout` |
| fiber v3 | `lockout.ErrAccountLocked` | `429` | `Too Many Requests` | `lockout` |
| fiber v3 | `httpsec.ErrChallengeUnavailable` wrapping `errors.New("redis: i/o timeout")` | `500` | `Internal Server Error` | `redis` |

### Requirement: Every engine renders a refusal identically
For the same refusal and request, the net/http, gin and fiber v3 engines SHALL produce the same status, problem members, content type and the headers that matter (`WWW-Authenticate`, `Vary`, `Cache-Control`). Only `trace_id` differs. The one exception is a `404` refusal on gin for a path gin matched no route for, which ginsec commits with an empty body before srvr can render it.

#### Scenario: Engine parity
- **Given** three servers, one per engine, wired with equivalent chains that refuse `GET /orders` with `<refusal>`
- **When** the same request with `Accept` `<accept>` is sent to each
- **Then** the three responses have equal status, content type and body members apart from `trace_id`

##### Examples:
| refusal | accept |
|---------|--------|
| `httpsec.ErrAuthenticationRequired` | `application/json` |
| `httpsec.ErrAuthenticationRequired` | `application/xml` |
| an MFA challenge with methods `totp` and `webauthn` | `application/json` |
| `httpsec.ErrCrossSiteRequest` | `*/*` |

#### Scenario: Unmatched-path 404 on gin
- **Given** a server on `<engine>` wired with a chain enabling SSO login under `/sso/login/{provider}` and no application route under `/sso`
- **When** a client sends `GET /sso/login/unknown` and the chain refuses it with `404`
- **Then** the response status is `404` with content type `<content_type>` and body `<body>`
- **And** one access record with `msg` `http.request.failed` and `level` `WARN` is written

##### Examples:
| engine | content_type | body |
|--------|--------------|------|
| net/http | `application/problem+json` | a problem with `status` `404` |
| fiber v3 | `application/problem+json` | a problem with `status` `404` |
| gin | (none) | (empty), committed by ginsec; an accepted parity exception |

### Requirement: Challenges expose only what a client needs
A `*httpsec.ChallengeError` SHALL render with scrty's status and a `challenge` member naming the kind. An MFA challenge SHALL also have `mfa_methods`, a list of `{name, channel, begins}`. The challenge `Session` SHALL never appear in the problem, and its `Token` SHALL appear only as the opt-in `access_token` member.

#### Scenario: Challenge problems
- **Given** a server wired with a scrty chain that answers a login with a challenge of kind `<kind>` with methods `<methods>`
- **When** the problem is rendered
- **Then** the status is `<status>` and the member `challenge` is `<challenge>`
- **And** the member `mfa_methods` is `<mfa_methods>` and the body contains neither the challenge token nor the session id

##### Examples:
| kind | methods | status | challenge | mfa_methods |
|------|---------|--------|-----------|-------------|
| `policy.ChallengeMFA` | `totp` on `factor.AuthenticatorApp` (no begin), `email` on `factor.Email` (begins) | `401` | `mfa` | `[{"name":"totp","channel":"authenticator-app","begins":false},{"name":"email","channel":"email","begins":true}]` |
| `policy.ChallengeMFA` | (empty) | `401` | `mfa` | `[]` |
| `policy.ChallengeMFA` | (none), raised without a session | `401` | `mfa` | (none) |
| `policy.ChallengePasswordChange` | (none) | `403` | `password_change` | (none) |
| `policy.ChallengeMFAEnrolment` | (none) | `403` | `mfa_enrolment` | (none) |
| `policy.ChallengeAccountRecovery` | (none) | `403` | `account_recovery` | (none) |
| an unknown future kind `100` | (none) | `401` | `unknown` | (none) |

#### Scenario: Challenge members opt-out
- **Given** a bridge configured with `<config>`
- **When** an MFA challenge is rendered
- **Then** the body `<members>`

##### Examples:
| config | members |
|--------|---------|
| (none) | has `challenge` and `mfa_methods` |
| `WithChallengeMembers(false)` | has neither `challenge` nor `mfa_methods` and the status is still `401` |
| `WithConfig(Config{ChallengeMembers: ptr(false)})` | has neither `challenge` nor `mfa_methods` and the status is still `401` |

### Requirement: The challenge token is returned only by opt-in
With `WithChallengeToken()` or `WithConfig(Config{ChallengeToken: true})`, a problem for a `*httpsec.ChallengeError` with a non-empty `Token` SHALL carry it as the `access_token` member and the response SHALL have `Cache-Control: no-store`. Without the opt-in, or with an empty `Token`, the member SHALL be absent. The token SHALL never be logged at any level.

#### Scenario: Challenge token
- **Given** a bridge configured with `<config>`
- **When** a password login is answered with an MFA challenge whose token is `<token>`
- **Then** the status is `401` and the member `access_token` is `<access_token>`
- **And** the header `Cache-Control` is `no-store` and no log line at level `DEBUG` or above contains `tok-123`

##### Examples:
| config | token | access_token |
|--------|-------|--------------|
| (none) | `tok-123` | (none) |
| `WithChallengeToken()` | `tok-123` | `tok-123` |
| `WithConfig(Config{ChallengeToken: true})` | `tok-123` | `tok-123` |
| `WithChallengeToken()` | (empty) | (none) |
| `WithChallengeToken()` and `WithChallengeMembers(false)` | `tok-123` | `tok-123` |

### Requirement: Headers scrty sets are preserved
When the chain sets response headers before refusing, such as `WWW-Authenticate` for Basic authentication or `DPoP-Nonce`, the rendered problem SHALL keep them unchanged alongside prblm's `Vary` and `Cache-Control`.

#### Scenario: Authentication headers kept
- **Given** a server on `<engine>` wired with a chain using `<auth>`
- **When** a client sends `GET /orders` with `<credentials>`
- **Then** the response status is `401` and the header `<header>` is `<value>`

##### Examples:
| engine | auth | credentials | header | value |
|--------|------|-------------|--------|-------|
| net/http | Basic with realm `orders` | `Authorization: Basic Ym9iOndyb25n` (wrong password) | `WWW-Authenticate` | `Basic realm="orders"` |
| gin | Basic with realm `orders` | `Authorization: Basic Ym9iOndyb25n` (wrong password) | `WWW-Authenticate` | `Basic realm="orders"` |
| fiber v3 | Basic with realm `orders` | `Authorization: Basic Ym9iOndyb25n` (wrong password) | `WWW-Authenticate` | `Basic realm="orders"` |
| net/http | Basic with realm `orders` | no `Authorization` header | `WWW-Authenticate` | (none) |
| net/http | Bearer without DPoP | no `Authorization` header | `WWW-Authenticate` | (none) |
| net/http | Basic with realm `orders` | `Authorization: Basic Ym9iOndyb25n` (wrong password) | `Cache-Control` | `no-store` |

### Requirement: Refusals are logged without secrets
A refusal SHALL produce one `http.request.failed` record at `WARN` for 4xx and `ERROR` for 5xx. The record SHALL have `error.type`, `error.chain` and `exception.message`, and SHALL have `srvr.security.refused` `true`. A challenge's token, session id and credentials SHALL NOT appear in any log record.

#### Scenario: Refusal record
- **Given** a server wired with a chain that refuses with `<refusal>` for a request carrying `Authorization: Bearer abc.def`
- **When** the request completes
- **Then** one record with `msg` `http.request.failed` and `level` `<level>` is written with `srvr.security.refused` `true`
- **And** no log line contains `<forbidden>`

##### Examples:
| refusal | level | forbidden |
|---------|-------|-----------|
| `httpsec.ErrAuthenticationRequired` | `WARN` | `abc.def` |
| an MFA challenge with token `tok-123` | `WARN` | `tok-123` |
| an MFA challenge on session `sess-456` | `WARN` | `sess-456` |
| an MFA challenge with token `tok-123` and `WithChallengeToken()` | `WARN` | `tok-123` |
| `httpsec.ErrChallengeUnavailable` | `ERROR` | `abc.def` |

### Requirement: Non-scrty errors keep their own mapping
The security mapper SHALL match an error when `httpsec.StatusForError` gives a status other than `500`, when it wraps a `*httpsec.ChallengeError`, or when it wraps `httpsec.ErrChallengeUnavailable`, `oidcprovider.ErrDependency` or `recovery.ErrDependency`. Every other error SHALL be left to srvr's and the consumer's mappers. A matched error SHALL set `srvr.security.refused` `true` on its record.

#### Scenario: Mapper scope
- **Given** a server wired with security and a handler that returns `<error>`
- **When** the request fails
- **Then** the response status is `<status>`
- **And** the record's `srvr.security.refused` is `<refused>`

##### Examples:
| error | status | refused |
|-------|--------|---------|
| `prblm.ErrNotFound` | `404` | (none) |
| `errors.New("boom")` | `500` | (none) |
| `fmt.Errorf("load: %w", httpsec.ErrAuthenticationRequired)` | `401` | `true` |
| a fiber `*fibersec.RefusalError` wrapping `httpsec.ErrCredentialsMissing` on fiber v3 | `400` | `true` |
| a `*httpsec.ChallengeError` of kind `policy.ChallengeMFA` | `401` | `true` |
| `fmt.Errorf("verify: %w", httpsec.ErrChallengeUnavailable)` | `500` | `true` |
| `oidcprovider.ErrDependency` | `500` | `true` |
| `recovery.ErrDependency` | `500` | `true` |
| an issuing failure from the chain (scrty's unexported `issueFailure`) | `500` | (none) |
