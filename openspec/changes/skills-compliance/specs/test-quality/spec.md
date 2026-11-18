# Spec Delta

## ADDED Requirements

### Requirement: Hostile inputs are fuzzed
Every parser or transform of untrusted input SHALL have a fuzz test with seeds covering its edge cases and a property it must keep, run on its seed corpus by every `go test`: `traceparent` parsing, route pattern joining, attribute cutting and the log line written for a hostile value.

#### Scenario: Fuzzed properties
- **Given** the fuzz test `<fuzz>`
- **When** `go test -run <fuzz>` runs its seeds and `go test -fuzz <fuzz> -fuzztime 30s` explores
- **Then** no input violates `<property>`

##### Examples:
| fuzz | property |
|------|----------|
| `FuzzTraceparent` | an accepted header keeps its trace id and parent id, its sampled flag equals the flags byte's low bit, and a rejected one yields a fresh valid trace |
| `FuzzJoinPattern` | no panic, the pattern starts with `/` and contains no `//` outside the host part |
| `FuzzCutString` | the result is at most the limit plus the suffix, is a prefix of the input plus the suffix, and is valid UTF-8 when the input is |
| `FuzzLogLine` | logging any string as a message and an attribute writes exactly one line that decodes as JSON back to the cut value |

### Requirement: Test hygiene is enforced by the linter
`golangci-lint run` in every module SHALL fail on a test helper without `t.Helper()`, a parallel test whose subtests are not parallel, an unchecked type assertion, `context.Background()` where `t.Context()` applies, and a core non-test import outside the standard library and prblm.

#### Scenario: Lint findings fail the run
- **Given** a module with `<code>`
- **When** `golangci-lint run ./...` runs with the repository's configuration
- **Then** it reports `<linter>`

##### Examples:
| code | linter |
|------|--------|
| a test helper taking `t *testing.T` that does not call `t.Helper()` | `thelper` |
| a `t.Parallel()` test whose `t.Run` subtests do not call `t.Parallel()` | `tparallel` |
| `v := x.(T)` in a test | `forcetypeassert` |
| `context.Background()` inside a test function body | `usetesting` |
| a core non-test file importing `go.opentelemetry.io/otel` | `depguard` |
| none of these | no finding |
