# Spec Delta

## ADDED Requirements

### Requirement: Values of any kind are capped
An attribute holding a value of any other kind (a map, slice, struct, `[]byte` or `fmt.Stringer` passed with `slog.Any`, directly, in a group or through `With`) SHALL be written unchanged when its formatted text fits `MaxAttrBytes`, and otherwise SHALL be replaced by that text cut like a string, setting top-level `srvr.attr.truncated` `true`. A value whose formatting panics SHALL be replaced by `<panic formatting value>`.

#### Scenario: Capping slog.Any values
- **Given** a server configured with `<config>`
- **When** a handler logs `INFO` through the request logger with `<attribute>` holding `<value>`
- **Then** the logged value is `<logged>`
- **And** `srvr.attr.truncated` is `<flag>`

##### Examples: within the cap
| config | attribute | value | logged | flag |
|--------|-----------|-------|--------|------|
| (none) | `slog.Any("hdr", h)` | an `http.Header` `{"X-A": ["1"]}` | the JSON object `{"X-A":["1"]}` | (none) |
| (none) | `slog.Any("ids", s)` | the `[]int` `[1, 2, 3]` | the JSON array `[1,2,3]` | (none) |
| (none) | `slog.Any("p", v)` | a struct whose formatted text is exactly 1024 bytes | the value as the handler renders it | (none) |

##### Examples: over the cap
| config | attribute | value | logged | flag |
|--------|-----------|-------|--------|------|
| (none) | `slog.Any("hdr", h)` | an `http.Header` with one 5000-byte value | the first 1024 bytes of its formatted text followed by `…[truncated]` | `true` |
| (none) | `slog.Any("list", s)` | a `[]string` of 50 strings of 100 bytes | the first 1024 bytes of its formatted text followed by `…[truncated]` | `true` |
| (none) | `slog.Any("p", v)` inside `WithGroup("db")` | a struct whose formatted text is 1025 bytes | `db.p` of the first 1024 bytes followed by `…[truncated]` | `true` (top-level) |
| (none) | `slog.Any("p", v)` added with `l.With(...)` | a struct whose formatted text is 2000 bytes | the first 1024 bytes followed by `…[truncated]` | `true` |
| `LogConfig{MaxAttrBytes: 16}` | `slog.Any("ids", s)` | a `[]int` whose formatted text is 20 bytes | the first 16 bytes followed by `…[truncated]` | `true` |
| `LogConfig{MaxAttrBytes: -1}` | `slog.Any("hdr", h)` | an `http.Header` with one 5000-byte value | the value as the handler renders it | (none) |

##### Examples: faulty values
| config | attribute | value | logged | flag |
|--------|-----------|-------|--------|------|
| (none) | `slog.Any("v", s)` | a `fmt.Stringer` whose `String` panics | `<panic formatting value>` | (none) |
| (none) | `slog.Any("v", p)` | a typed-nil pointer to a struct | the value as the handler renders it | (none) |
| (none) | `slog.Any("v", nil)` | `nil` | the value as the handler renders it | (none) |

### Requirement: Access log configuration is copied at construction
The skip paths a consumer passes, through `WithAccessLogConfig` or `WithAccessLogSkipPaths`, SHALL be copied when `New` applies the options, so changing the caller's slice afterwards never changes which records are written and never races with requests.

#### Scenario: Caller mutates skip paths after New
- **Given** a server built with `<option>` holding the slice `s` with `/healthz`
- **When** the caller sets `s[0]` to `/other` after `New` and a client sends `GET /healthz` answered `200`
- **Then** the `http.request.completed` record for `/healthz` is `<written>`

##### Examples:
| option | written |
|--------|---------|
| `WithAccessLogConfig(AccessLogConfig{SkipPaths: s})` | not written |
| `WithAccessLogSkipPaths(s...)` | not written |
| `WithAccessLogConfig(AccessLogConfig{SkipPaths: s})`, with the mutation running concurrently with 100 requests | not written for any, and `go test -race` reports no race |
