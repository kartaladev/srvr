# Spec Delta

## MODIFIED Requirements

### Requirement: Consumer faults are contained on every engine
Every engine SHALL contain faulty consumer code exactly as the net/http engine does, reusing the core's guards: consumer values are formatted without panicking, cyclic error chains reaching srvr get the safe `500` problem, failing log handlers and listeners never affect responses or the lifecycle, and no srvr lock is held while consumer code runs, so it may call the server's own methods.

#### Scenario: Consumer faults on every engine
- **Given** engine `<engine>` with `<fault>`
- **When** the server is constructed, started and, where the fault is in a handler, serves `GET /items/7`
- **Then** `<result>`

##### Examples:
| engine | fault | result |
|--------|-------|--------|
| `gin` | a configurer that panics with a value whose `Error()` panics with itself, `main.selfPanic` | `Start` returns an error wrapping `srvr.ErrRouteConfig` containing `<panic in Error(): main.selfPanic>`; a later `Shutdown` returns `nil` |
| `fiber` | a configurer that panics with a value whose `Error()` panics with itself, `main.selfPanic` | `Start` returns an error wrapping `srvr.ErrRouteConfig` containing `<panic in Error(): main.selfPanic>`; a later `Shutdown` returns `nil` |
| `gin` | a `WithGinEngine` hook that panics with a value whose `Error()` panics with itself | construction returns an error wrapping `srvr.ErrConfig` containing `<panic in Error(): main.selfPanic>` |
| `gin` | a handler calling `c.Error(e)`, `e` a typed-nil `*main.MyErr` returned as `error` | a `500` problem; the record has `error.type` `*main.MyErr` and `exception.message` `<nil *main.MyErr>` |
| `fiber` | a handler returning `e`, a typed-nil `*main.MyErr` returned as `error` | a `500` problem; the record has `error.type` `*main.MyErr` and `exception.message` `<nil *main.MyErr>` |
| `gin` | a handler calling `c.Error(e)`, `e`'s `Error()` panicking with a value whose `Error()` panics | a `500` problem; the record has `exception.message` `<panic in Error(): T>`, `T` its Go type |
| `fiber` | a handler returning an error whose `Error()` panics with a value whose `Error()` panics | a `500` problem; the record has `exception.message` `<panic in Error(): T>`, `T` its Go type |
| `fiber` | a handler returning an error whose `Unwrap()` returns itself | a `500` problem within `1s`; the record has an `error.chain` of at most 32 entries and `srvr.error.truncated` `true` |
| `gin` | a handler that panics with a value whose `Error()` panics with itself | a `500` problem; the record has `error.kind` `panic` and `exception.message` `<panic in Error(): main.selfPanic>` |
| `fiber` | a handler that panics with a value whose `Error()` panics with itself | a `500` problem; the record has `error.kind` `panic` and `exception.message` `<panic in Error(): main.selfPanic>` |
| `gin` | `WithLogHandler(h)` where `h.Enabled` panics | status `200`, the record is skipped and `srvr.LogRecordsLost()` increases |
| `fiber` | `WithLogHandler(h)` where `h.WithAttrs` panics | status `200`, the record is skipped and `srvr.LogRecordsLost()` increases |
| `gin` | gin in debug mode and `WithLogHandler(h)` where `h.Handle` panics | construction succeeds; the `gin_debug_mode` warning is skipped and `srvr.LogRecordsLost()` increases |
| `gin` | `WithErrorRedactor(fn)` where `fn` panics and a handler calls `c.Error(errors.New("boom"))` | a `500` problem; the record has `exception.message` `[redaction failed]` |
| `fiber` | `WithListener(ln)` where `ln.Accept` panics | `Start` returns an error wrapping `srvr.ErrConfig` naming `Listener` and the process keeps running |
| `gin` | `WithListener(ln)` where `ln.Close` panics | `Shutdown` returns `nil`, `server.stopped` has no `srvr.drain.forced`, and a later `Shutdown` returns `nil` |
| `fiber` | `WithListener(ln)` where `ln.Close` returns `errors.New("close failed")` | `Shutdown` returns `nil`, `server.stopped` has no `srvr.drain.forced`, and a later `Shutdown` returns `nil` |
| `fiber` | `WithListener(ln)` where `ln.Addr()` returns `nil` | `Start` returns an error wrapping `srvr.ErrConfig` naming `Listener`; `ln` is closed |
| `gin` | a configurer whose `Configure` calls `srv.Addr()` and `srv.Register(c2)` | `Addr()` returns `""`, `Register` returns an error matching `srvr.ErrServerStarted`, and the server becomes ready |
| `fiber` | a configurer whose `Configure` calls `srv.Addr()` and `srv.Register(c2)` | `Addr()` returns `""`, `Register` returns an error matching `srvr.ErrServerStarted`, and the server becomes ready |
| `gin` | a route check that calls `srv.Addr()` and returns an error whose `Error()` panics with itself | `Start` returns an error wrapping `srvr.ErrRouteConfig` containing `<panic in Error(): main.selfPanic>`, without deadlock |
