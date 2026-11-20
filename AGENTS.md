# srvr

Go library (`github.com/kartaladev/srvr`, Go 1.27) for running HTTP servers on a consumer-chosen routing engine - net/http, Gin or Fiber v3 - with RFC 9457 errors through `github.com/kartaladev/prblm`, optional security through `github.com/kartaladev/scrty`, and slog logs designed for AI-assisted triage. Planning lives in `openspec/` (schema `gherkin-driven`, project context in `openspec/config.yaml`).

## Repository layout

Multi-module repository; `go.work` is for development only.

- `github.com/kartaladev/srvr` - core and the net/http engine. Depends at runtime only on the standard library and prblm; test-only dependencies such as goleak are allowed.
- `srvr/engine` - public package (unstable before v1) holding the shared pipeline pieces the engine modules use, because separate modules cannot import core `internal/`.
- `srvr/ginsrvr` (gin), `srvr/fibersrvr` (fiber v3) - one engine module each.
- `srvr/otelsrvr` - opt-in metrics and tracing (OpenTelemetry API only); `srvr/otelsetup` - opt-in SDK and exporter wiring.
- `srvr/security` - opt-in scrty bridge.

Handlers report errors in each engine's native way and srvr renders every one through prblm: net/http handlers return `error` (`func(w, r) error`), fiber handlers return `error` (`func(fiber.Ctx) error`), gin handlers stay plain `gin.HandlerFunc` and report through `c.Error`.

## Project rules

These rules are mandatory for every task in this repository: planning, implementation and review.

### 1. TDD: RED, GREEN, REFACTOR, repeat

For every behaviour, in small steps:
1. **RED:** write a failing test first (table-driven, one case per spec Examples row). Run it and confirm it fails for the expected reason - not a compile error elsewhere, not a wrong assertion.
2. **GREEN:** write the minimal code that makes it pass. Run the tests.
3. **REFACTOR:** with tests green, name the smell and apply a refactoring from Martin Fowler's *Refactoring* catalog in small behaviour-preserving steps, running the tests after each step.
4. **REPEAT** with the next behaviour.

Never write production code without a failing test that demands it. Never refactor on red.

There is no coverage percentage threshold, but every hot path must be tested and the coverage checked, not assumed: `make cover` from the root (workspace mode, `-coverpkg=github.com/kartaladev/srvr/...`, so the shared `engine` package counts the engine modules' tests; it prints the engine total), or per module `go test -coverpkg=./... -coverprofile=c.out ./... && go tool cover -func=c.out`. Hot paths: the request pipeline in each engine (trace context, access log, observer, recovery, error rendering), route registration and pattern joining, `Start` and `Shutdown`, and log record construction including error-chain walking and redaction.

Every module's tests run under goleak: each test package has a `TestMain` calling `goleak.VerifyTestMain` (a guard in `internal/paritytest` enforces it), and a goroutine that outlives its test is excused only by a named, commented ignore of one function in that package's `leakOptions`. A new goroutine started by srvr needs a test proving it ends when its owner stops.

### 2. Prove every defect claim with a failing test

A suspected error or defect - in this code, a dependency or a framework, found while implementing, reviewing, debugging or planning - is a hypothesis until reproduced:
- Write a test that fails because of the defect and run it. The failure output is the proof.
- Report the claim with the test name and location, the command and the failing output.
- If the test passes, the suspicion is not confirmed: drop it or say it is unconfirmed.
- If it cannot be automated (external infrastructure), label it unverified with the reason.
- The reproducing test stays in the suite as the RED step of the fix.

### 3. Defensive programming (Murphy's Law)

Anything that can go wrong will go wrong. In planning (specs and tasks), implementation and review, enumerate what can fail and make sure each relevant case is specified, handled and tested:
- nil, zero, empty and typed-nil values (configurers, handlers, middleware, options, contexts, loggers)
- malformed or hostile input (traceparent headers, paths, oversized bodies and headers, control characters in logged values)
- boundary values (exactly at a limit, one past it)
- partial failure (response already started, client disconnected, context cancelled, drain deadline exceeded, log output failing)
- concurrency (concurrent Start/Shutdown, concurrent requests, shared state; everything `-race` clean)
- faulty consumer code (configurers, handlers, middleware, hooks, observers, log handlers that error, panic or block)
- framework quirks (gin committing headers early, fiber's pooled and never-cancelling context, ServeMux plain-text 404/405)
- security and privacy (no internals in responses; no credentials, bodies or PII in logs by default)

Reviews ask "what happens when this fails?" for every new branch and reject untested failure paths.

### 4. Gall's Law: evolve from a simple system that works

A complex system that works evolved from a simple system that worked:
- Plan in thin, end-to-end working slices. The first slice is the simplest system that works end to end; each later slice adds one capability to a system that already works.
- Every slice ends working, tested and releasable. No slice depends on code that only becomes useful later.
- Prefer postponing a feature to building it ahead of need.
- In review, reject designs that only work once several unfinished parts come together. If a slice cannot be made to work, return to the last working state and simplify.

### 5. Language skills

Before any Go task (coding, review, debugging, setup), load the `golang-how-to` skill from the `cc-skills-golang` plugin first; it routes to the other Go skills the task needs. Apply the relevant ones throughout, in particular: code style, naming, error handling, safety, security, testing, documentation, design patterns, modernize, refactoring, lint and troubleshooting. If the work touches Dart/Flutter or Next.js/React, load those skills as well.

### 6. Code navigation with gopls

Use the gopls language server (LSP, from the `gopls-lsp` plugin) for semantic questions instead of grep: go to definition, find references before changing any definition, hover for types and docs, workspace symbols, and diagnostics on every changed file after each edit. Use rename through the language server for renames.

### 7. Library design: safe opinionated defaults, flexible configuration

- Zero configuration must produce correct, safe behaviour. Every default is a deliberate choice, documented in godoc with its reason.
- Every behaviour is configurable both programmatically (functional options) and by configuration (plain config structs with `json`/`yaml`/`env` tags that any loader can fill). srvr never reads environment variables or files itself.
- Invalid configuration fails construction with an error naming the field and value; it never silently falls back.
- An option that loosens a safety or privacy default must be explicit and clearly named.
- Public API, defaults, each module's dependency set and the log schema are commitments. The log schema carries `srvr.log.schema`: adding a key keeps the version; renaming or removing a key, or changing its meaning or type, bumps it. The core module depends at runtime only on the standard library and prblm: no non-test package may import anything else. Test-only dependencies, such as `go.uber.org/goleak`, are allowed.

### 8. Conventional commits, one per task group

Use [Conventional Commits](https://www.conventionalcommits.org/) (`feat(core): ...`, `fix(ginsrvr): ...`, `test(...)`, `docs(...)`, `refactor(...)`, `chore(...)`). Commit once per completed, meaningful task group, never per task: each slice gets one commit (module scaffolding included in the first slice that needs it), and the consolidation group commits its plan refresh as `docs(openspec): consolidate <change> against main` when it changed anything.

### 9. /simplify before every commit

When a task group is complete and verified, run `/simplify` on that group's changes, apply what holds up, re-run the tests, then commit.

## OpenSpec workflow

- Changes are planned in order: `core-server` -> `gin-and-fiber-engines` -> `otel-observability` -> `goroutine-leak-checks` -> `triage-debug-buffer` -> `followups-hardening` -> `static-files` -> `scrty-integration` (owner decisions 2026-11-19: `triage-debug-buffer`, `followups-hardening` and `static-files` are applied before `scrty-integration`).
- Task group 1 of every change is consolidation: sync with the main branch, decide earlier changes' `notes.md` carry-overs, refresh the change's artifacts against merged specs and code, and record the outcome. Do not skip it, even when the change looks current.
- While applying, write every follow-up that does not belong to the current change - especially problems not tightly related to it - to the change's `notes.md` (status, target change, context, evidence, suggested action). Never fix such problems silently and never drop them.
- Each spec scenario maps to one table-driven test, and each Examples row to one test case.

## Verification commands

`make check` from the root runs every check (see the Makefile) in every module with `GOWORK=off`; `make <target> MODULES=ginsrvr` (or `make <target>` inside a module) runs one module. By hand, from the directory of every module a slice touches - `./...` never crosses into a nested module (`go help packages`) - with `GOWORK=off` for modules that will be released:

```sh
go vet ./...
golangci-lint run ./...
go test -race ./...
go mod tidy -diff
govulncheck ./...
go test -coverpkg=./... -coverprofile=c.out ./... && go tool cover -func=c.out
```

`make cover` gives the cross-module coverage of the shared packages.

Fiber tests use `app.Test`; net/http and gin tests use `httptest`.
