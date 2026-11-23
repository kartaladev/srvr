# Spec Delta

## Purpose

Defines how refusals and challenges from a scrty security chain become error answers in a srvr server: with the default prblm renderer, the RFC 9457 problems of scrty's own `prblmsec` integration, resolved under the server's problem namespace, with the status scrty chose and the same body on every engine; with a consumer's `ErrorRenderer`, scrty's error passed unchanged. No credential, session handle or internal error text leaks, and a challenge's token is sent only by opt-in. In the rows below the server has `ProblemNamespace` `https://errors.example/p`, so a type `security/<name>` is sent as `https://errors.example/p/security/<name>`.

## ADDED Requirements

### Requirement: Refusals render as scrty's problems
With the default renderer, a scrty refusal reaching a srvr server SHALL be rendered by the problem handler of that server with `prblmsec`'s mapper, registered ahead of the consumer's and srvr's own mappers. Its status SHALL be `httpsec.StatusForError(err)` and its type `prblmsec`'s type for it under the server's `ProblemNamespace` (`about:blank` for a recognised `500`). Neither the error text nor any wrapped error's text SHALL appear in the response.

#### Scenario: Refusal statuses
- **Given** a server on `<engine>` wired with a scrty chain that refuses `GET /orders` with `<refusal>`
- **When** a client sends `GET /orders`
- **Then** the response status is `<status>` with content type `application/problem+json`
- **And** the body has `type` `<type>` and `trace_id`, and does not contain `<hidden>`

##### Examples: net/http
| engine | refusal | status | type | hidden |
|--------|---------|--------|------|--------|
| net/http | `httpsec.ErrAuthenticationRequired` | `401` | `https://errors.example/p/security/authentication-required` | `httpsec:` |
| net/http | `ratelimit.ErrThrottled` | `401` | `https://errors.example/p/security/authentication-failed` | `ratelimit` |
| net/http | `httpsec.ErrCredentialsMissing` | `400` | `https://errors.example/p/security/invalid-request` | `httpsec:` |
| net/http | `httpsec.ErrCrossSiteRequest` | `403` | `https://errors.example/p/security/cross-site-request` | `httpsec:` |
| net/http | `httpsec.ErrRequestTooLarge` | `413` | `https://errors.example/p/security/request-too-large` | `httpsec:` |
| net/http | `lockout.ErrAccountLocked` | `429` | `https://errors.example/p/security/account-locked` | `lockout` |
| net/http | `httpsec.ErrChallengeUnavailable` wrapping `errors.New("redis: i/o timeout")` | `500` | `about:blank` | `redis` |
| net/http | an issuing failure wrapping `errors.New("kms: denied")` | `500` | `about:blank` | `kms` |

##### Examples: every engine
| engine | refusal | status | type | hidden |
|--------|---------|--------|------|--------|
| gin | `httpsec.ErrAuthenticationRequired` | `401` | `https://errors.example/p/security/authentication-required` | `httpsec:` |
| fiber v3 | `httpsec.ErrAuthenticationRequired` | `401` | `https://errors.example/p/security/authentication-required` | `httpsec:` |
| gin | `lockout.ErrAccountLocked` | `429` | `https://errors.example/p/security/account-locked` | `lockout` |
| fiber v3 | `lockout.ErrAccountLocked` | `429` | `https://errors.example/p/security/account-locked` | `lockout` |
| fiber v3 | `httpsec.ErrChallengeUnavailable` wrapping `errors.New("redis: i/o timeout")` | `500` | `about:blank` | `redis` |

### Requirement: Every engine renders a refusal identically
For the same refusal and request, the net/http, gin and fiber v3 engines SHALL produce the same status, problem members, content type and the headers that matter (`WWW-Authenticate`, `Vary`, `Cache-Control`). Only `trace_id` differs. This includes a `404` refusal on a path no route matched.

#### Scenario: Engine parity
- **Given** three servers, one per engine, wired with equivalent chains that refuse `GET /orders` with `<refusal>`
- **When** the same request with `Accept` `<accept>` is sent to each
- **Then** the three responses have equal status, content type, headers that matter and body members apart from `trace_id`

##### Examples:
| refusal | accept |
|---------|--------|
| `httpsec.ErrAuthenticationRequired` | `application/json` |
| `httpsec.ErrAuthenticationRequired` | `application/xml` |
| an MFA challenge with methods `totp` and `webauthn` | `application/json` |
| `httpsec.ErrCrossSiteRequest` | `*/*` |

#### Scenario: Unmatched-path 404
- **Given** a server on `<engine>` wired with a chain enabling SSO login under `/sso/login/{provider}` and no application route under `/sso`
- **When** a client sends `GET /sso/login/unknown` and the chain refuses it with `404`
- **Then** the response status is `404` with content type `application/problem+json` and a problem body with `status` `404`
- **And** one access record with `msg` `http.request.failed` and `level` `WARN` is written

##### Examples:
| engine |
|--------|
| net/http |
| gin (`ginsecurity.WithSecurity` mounts `ginsec.Middleware(chain, ginsec.WithRefusalsRendered())`) |
| fiber v3 |

### Requirement: Challenges expose only what a client needs
A `*httpsec.ChallengeError` SHALL render as `prblmsec` renders it: scrty's status, a type by its kind, a `challenge` member from the fixed vocabulary (`mfa`, `password-change`, `mfa-enrolment`, `account-recovery`; none for any other kind) and, for an MFA challenge, `methods`, a list of `{name, channel, begins}`. The challenge `Session` SHALL never appear in the problem, and its `Token` SHALL appear only through the opt-in of "The challenge token is returned only by opt-in".

#### Scenario: Challenge problems
- **Given** a server wired with a scrty chain that answers a login with a challenge of kind `<kind>` with methods `<methods>`
- **When** the problem is rendered
- **Then** the status is `<status>`, the type ends in `<type>` and the member `challenge` is `<challenge>`
- **And** the member `methods` is `<methods_member>` and the body contains neither the challenge token nor the session id

##### Examples:
| kind | methods | status | type | challenge | methods_member |
|------|---------|--------|------|-----------|----------------|
| `policy.ChallengeMFA` | `totp` on `factor.AuthenticatorApp` (no begin), `email` on `factor.Email` (begins) | `401` | `security/mfa-required` | `mfa` | `[{"name":"totp","channel":"authenticator-app","begins":false},{"name":"email","channel":"email","begins":true}]` |
| `policy.ChallengePasswordChange` | (none) | `403` | `security/password-change-required` | `password-change` | (none) |
| `policy.ChallengeMFAEnrolment` | (none) | `403` | `security/mfa-enrolment-required` | `mfa-enrolment` | (none) |
| `policy.ChallengeAccountRecovery` | (none) | `403` | `security/account-recovery-required` | `account-recovery` | (none) |
| an unknown future kind `100` | (none) | `401` | `security/challenge-required` | (none) | (none) |

### Requirement: The challenge token is returned only by opt-in
By default srvr SHALL give `prblmsec` its `WithoutChallengeToken()` option, so no problem carries `access_token` or `token_type`. With `security.WithChallengeToken()` or `WithConfig(Config{ChallengeToken: true})`, a problem for a `*httpsec.ChallengeError` with a non-empty `Token` SHALL carry it as `access_token`, with `token_type` when the challenge names one, and the response SHALL have `Cache-Control: no-store`. The token SHALL never be logged at any level.

#### Scenario: Challenge token
- **Given** security configured with `<config>`
- **When** a password login is answered with an MFA challenge whose token is `<token>`
- **Then** the status is `401`, the member `access_token` is `<access_token>` and `Cache-Control` is `<cache>`
- **And** no log line at level `DEBUG` or above contains `tok-123`

##### Examples:
| config | token | access_token | cache |
|--------|-------|--------------|-------|
| (none) | `tok-123` | (none) | `no-store` (set by scrty for a challenge carrying a token) |
| `WithChallengeToken()` | `tok-123` | `tok-123` | `no-store` |
| `WithConfig(Config{ChallengeToken: true})` | `tok-123` | `tok-123` | `no-store` |
| `WithChallengeToken()` | (empty) | (none) | prblm's default |

### Requirement: Headers scrty sets are preserved
When the chain sets response headers before refusing, such as `WWW-Authenticate` for Basic authentication or `DPoP-Nonce`, the rendered problem SHALL keep them unchanged alongside prblm's `Vary` and `Cache-Control`. srvr SHALL add no `WWW-Authenticate` of its own.

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
An error that `httpsec.LookupStatus` does not recognise SHALL be left to the consumer's and srvr's mappers. A recognised error SHALL set `srvr.security.refused` `true` on its record, whichever renderer answers it.

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
| an issuing failure from the chain (scrty's unexported `issueFailure`) | `500` | `<refused>` as `httpsec.LookupStatus` reports it |

### Requirement: A custom renderer receives scrty's error unchanged
With `srvr.WithErrorRenderer(r)`, a scrty refusal SHALL reach `r` as the error the chain returned, its chain walkable to scrty's error, and the response SHALL be `r`'s answer. A renderer that delegates to `srvr.DefaultErrorRenderer()` SHALL get the same problem the default renderer gives.

#### Scenario: Refusal under a custom renderer
- **Given** a net/http server wired with security and `WithErrorRenderer(<renderer>)`, with a chain refusing `GET /orders` with `httpsec.ErrAuthenticationRequired`
- **When** a client sends `GET /orders`
- **Then** the response is `<answer>` and the record has `srvr.security.refused` `true`

##### Examples:
| renderer | answer |
|----------|--------|
| `R`, answering `httpsec.StatusForError(err)` with body `{"code":"auth"}` when `errors.Is(err, httpsec.ErrAuthenticationRequired)` | `401` `application/vnd.test+json` `{"code":"auth"}` |
| a renderer returning `srvr.DefaultErrorRenderer().Render(ctx, req, err)` | `401` problem of type `https://errors.example/p/security/authentication-required` |
