# Spec Delta

## MODIFIED Requirements

### Requirement: Deliberate long-lived goroutines are listed with a reason
A goroutine that outlives its test on purpose SHALL be excluded only by an ignore naming one function, defined once per module next to a comment giving the reason. Each ignore SHALL still be needed and SHALL NOT hide a server that was never stopped.

#### Scenario: Each ignore is needed and narrow
- **Given** module `<module>` whose leak options ignore `<function>` because `<reason>`
- **When** the test makes that goroutine run and checks for goroutines without and then with the module's leak options
- **Then** without the options the goroutine is reported, and with them it is `<with options>`

##### Examples: ignores
| module | function | reason | with options |
|--------|----------|--------|--------------|
| `github.com/kartaladev/srvr/fibersrvr` | `github.com/valyala/fasthttp.updateServerDate.func1` | fasthttp refreshes its `Date` header once per process and never stops | not reported |
| `github.com/kartaladev/srvr/fibersrvr` | `github.com/valyala/fasthttp.(*workerPool).Start.func2` | fasthttp's idle-worker cleaner sleeps up to `10s` (`MaxIdleWorkerDuration`'s default) before it sees `Stop`, and the package's `Example` runs a server with that default, which it cannot shorten | not reported |
| `github.com/kartaladev/srvr/fibersrvr` | `github.com/valyala/fasthttp.(*inMemoryCacheManager).handleCleanCache` | the open-file cache cleaner of a static mount with `fibersrvr.WithSendFileCache`: fiber v3.7.0 never sets `fasthttp.FS.CleanStop` (`res.go:1308`), so nothing can stop it after `Shutdown` (static-files, owner decision 2026-11-19) | not reported |
| `github.com/kartaladev/srvr/internal/paritytest` | `github.com/valyala/fasthttp.updateServerDate.func1` | as in `fibersrvr` | not reported |
| `github.com/kartaladev/srvr/internal/paritytest/gincycle` | the package's named cyclic-error handler | gin's `c.Error` never returns for a cyclic chain, so the handler goroutine cannot be stopped (engine-parity documented difference) | not reported |

##### Examples: not hidden
| module | function | reason | with options |
|--------|----------|--------|--------------|
| `github.com/kartaladev/srvr/fibersrvr` | a fiber server started and never shut down | its serve and worker goroutines are not deliberate | reported, naming srvr's serve goroutine |
| `github.com/kartaladev/srvr/internal/paritytest` | a gin handler blocked on a channel never closed | only the `gincycle` binary ignores its handler | reported, naming the blocked handler |
| `github.com/kartaladev/srvr/internal/paritytest` | fasthttp's idle-worker cleaner of a fiber server left at the default `MaxIdleWorkerDuration`, after `Shutdown` | the parity binary does not ignore the cleaner: its harness shortens the duration so the cleaner ends | reported, naming `github.com/valyala/fasthttp.(*workerPool).Start.func2` |
