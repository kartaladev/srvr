# Spec Delta

## MODIFIED Requirements

### Requirement: Failed-request records are never suppressed
An `http.request.failed` record SHALL be written whatever the access log configuration; only the opt-in unmatched-record limit may suppress one (see "Unmatched failed records can be rate limited"). A recovered panic SHALL be logged at `ERROR`, and an error that arrived after the response started at the higher of the level of the status sent and of the status the error would have been answered with.

#### Scenario: Failures while the access log is suppressed
- **Given** a server configured with `<config>`
- **When** a client sends `<request>`
- **Then** an `http.request.failed` record is `<written>`

##### Examples:
| config | request | written |
|--------|---------|---------|
| `AccessLogConfig{SkipPaths: ["/healthz"]}` | `GET /healthz` returning `prblm.ErrNotFound` | written |
| `WithAccessLogDisabled()` | `GET /ping` returning `prblm.ErrNotFound` | written |
| `WithAccessLogDisabled()` | `GET /nope` (no route) | written |
| `WithAccessLogDisabled()` | `GET /late` writing `200` and then panicking | written, at `ERROR` |
| `WithAccessLogDisabled()` | `GET /late` writing `200` and then returning `errors.New("late")` | written, at `ERROR` |
| `WithAccessLogDisabled()` and `WithUnmatchedRecordLimit(1, time.Minute)` | `GET /a` returning `errors.New("boom")`, ten times within a minute | written, ten times |
| `WithUnmatchedRecordLimit(1, time.Minute)` | `GET /a` panicking, ten times within a minute | written, ten times |

#### Scenario: Level of a failure after the response started
- **Given** a net/http route `GET /late` that writes status `<written>` and the body `partial`, then `<fails>`
- **When** a client sends `GET /late`
- **Then** the client receives status `<written>` and a body starting with `partial`
- **And** the `http.request.failed` record has level `<level>`, `srvr.response.committed` `true` and no `problem`

##### Examples:
| written | fails | level |
|---------|-------|-------|
| `200` | returns `errors.New("late")` | `ERROR` |
| `200` | panics with `"late"` | `ERROR` |
| `201` | returns `prblm.ErrConflict` | `WARN` |
| `500` | returns `prblm.ErrConflict` | `ERROR` |

### Requirement: Unmatched failed records can be rate limited
When `AccessLogConfig.UnmatchedLimit` sets `Burst` above zero, the server SHALL write at most `Burst` `http.request.failed` records of requests no route matched per `Interval`, count the rest, and add the count as `srvr.unmatched.suppressed` to the next such record written and to `server.stopped`. Only a 404 or 405 the router answered SHALL count; an error consumer code reported is never limited. The limit is per server and off by default.

#### Scenario: Limiting unmatched failed records
- **Given** a server configured with `<config>`
- **When** `<requests>`
- **Then** `<written>`

##### Examples: within and over the limit
| config | requests | written |
|--------|----------|---------|
| (none) | 100 `GET /nope-<n>` within one second | 100 `http.request.failed` records, none with `srvr.unmatched.suppressed` |
| `WithUnmatchedRecordLimit(10, time.Second)` | exactly 10 `GET /nope-<n>` within one second | 10 records, none with `srvr.unmatched.suppressed` |
| `WithUnmatchedRecordLimit(10, time.Second)` | 11 `GET /nope-<n>` within one second | 10 records |
| `WithUnmatchedRecordLimit(10, time.Second)` | 15 `GET /nope-<n>` within one second, then `GET /nope-x` two seconds later | 11 records; the last has `srvr.unmatched.suppressed` `5` |
| `WithUnmatchedRecordLimit(10, time.Second)` | 15 `DELETE /users/42` with only `GET /users/{id}` registered, within one second | 10 records with status `405` |
| `WithUnmatchedRecordLimit(10, time.Second)` | 1000 `GET /nope-<n>` from 50 concurrent clients within one second | exactly 10 records; with one more request two seconds later, its record has `srvr.unmatched.suppressed` `990` |
| `WithUnmatchedRecordLimit(1, time.Minute)` | 3 `GET /nope-<n>`, then `Shutdown` | 1 record; `server.stopped` has `srvr.unmatched.suppressed` `2` |
| `WithUnmatchedRecordLimit(1, time.Minute)` | 1 `GET /nope`, then `Shutdown` | 1 record; `server.stopped` has no `srvr.unmatched.suppressed` |
| `AccessLogConfig{UnmatchedLimit: UnmatchedLimitConfig{Burst: 1, Interval: time.Minute}}` | 3 `GET /nope-<n>` | 1 record |
| `WithUnmatchedRecordLimit(1, time.Minute)` | 3 `GET /nope-<n>`, the server observed by `otelsrvr` | 1 record; `http.server.request.duration` counts 3 requests |

##### Examples: rejected configuration
| config | written |
|--------|---------|
| `WithUnmatchedRecordLimit(-1, time.Second)` | construction error wrapping `srvr.ErrConfig` naming `UnmatchedLimit.Burst` |
| `WithUnmatchedRecordLimit(10, 0)` | construction error wrapping `srvr.ErrConfig` naming `UnmatchedLimit.Interval` |
| `WithUnmatchedRecordLimit(10, -time.Second)` | construction error wrapping `srvr.ErrConfig` naming `UnmatchedLimit.Interval` |
| `WithUnmatchedRecordLimit(0, 0)` | a server without a limit |

#### Scenario: Failures consumer middleware reports are not limited
- **Given** engine `<engine>` configured with `WithUnmatchedRecordLimit(1, time.Minute)`, only route `GET /x`, and consumer middleware registered with `<registration>` that `<middleware>`
- **When** a client sends `GET /nope-1`, `GET /nope-2` and `GET /nope-3`
- **Then** each response is a `<status>` problem
- **And** `<written>` `http.request.failed` records are written

##### Examples: consumer-reported errors
| engine | registration | middleware | status | written |
|--------|--------------|------------|--------|---------|
| `fiber` | `router.Use(mw)` on the root router | returns `prblm.ErrNotFound` | `404` | `3` |
| `fiber` | `router.Use(mw)` on the root router | returns `fmt.Errorf("gate: %w", fiber.ErrNotFound)` | `404` | `3` |
| `fiber` | `router.Use(mw)` on the root router | panics with `"boom"` (a panic is never limited) | `500` | `3` |

##### Examples: answered by the router
| engine | registration | middleware | status | written |
|--------|--------------|------------|--------|---------|
| `net/http` | `group.Use(mw)` on the root group (it runs only for matched routes) | returns `prblm.ErrNotFound` | `404` | `1` |
| `gin` | `group.Use(mw)` on the root group (it runs only for matched routes) | calls `c.AbortWithError(404, prblm.ErrNotFound)` | `404` | `1` |
| `fiber` | `router.Use(mw)` on the root router | returns `c.Next()` | `404` | `1` |
| `fiber` | `router.Use(mw)` on the root router | returns `fiber.ErrNotFound`, the value fiber's router itself returns | `404` | `1` |
