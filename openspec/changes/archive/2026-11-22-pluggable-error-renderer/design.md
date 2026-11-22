# Design

## Context

See proposal.md, Why. Current state at v0.3.0 plus `engine-internal-split` (unreleased; `main` `78c8165`), rechecked in the code on 2026-11-21. Package `engine` is now `internal/engine`; srvr's error values live in `internal/errchain`; import order `tracectx` < `errchain` < `static` < `engine` is pinned by `TestImportDirection` (`internal/paritytest/layout_test.go`):

- `engine.Core` (`internal/engine/core.go`) holds one `*prblm.Handler` (`Problems()`), built by `newProblems` from `ProblemOptions()`: the consumer's options, then `clientClosedMapper` (499), `traceIDEnricher`, and `prblm.AddErrorHook(c.onError)`. `onError` calls `recordFailure(ctx, err, *prblm.Problem)` and then the consumer's `ErrorHook`, recovering a panic in it.
- `recordFailure` keeps a `failure{err, panic *prblm.PanicError, problem *prblm.Problem, committed}`; it drops prblm's notes (`prblmNotes`) (`internal/engine/access.go`). `RecordRejection` builds a `prblm.Problem` for fiber's pre-routing 413/431/408. `addFailure` writes `problem.status`, `problem.type`, `problem.title`.
- net/http (`server.go`): `stages` = `LogAccess(LimitBody(Problems().Recover(answerUnmatched(mux))))`; handlers go through `wrapHandler` = `Problems().Wrap` over `GuardChain(h(w, r))`; `answerUnmatched` writes `prblm.ErrNotFound` and `prblm.New(405, WithHeader("Allow", ...))`. The 413 is prblm's built-in mapping of `*http.MaxBytesError`. net/http's own `431` happens before srvr runs and is not rendered by srvr.
- Static mounts (`engine.StaticFailure` in `internal/engine/staticfailure.go`, mapping an `internal/static` `Result`) return `prblm.ErrNotFound` and `prblm.New(405, ...)` from the handler, so they reach whatever renders handler errors on each engine. fiber's mount also returns `prblm.ErrNotFound` directly through `notFound` (`fibersrvr/static.go`: path outside the mount, file vanished before `SendFile`).
- gin (`ginsrvr/server.go` `newEngine`): `requestStage`, `ginprblm.Middleware(opts...)`, `guardErrors`, `ginprblm.Recovery(opts...)`, with an extra hook `clearStatusOnly` that undoes the failure of a `c.AbortWithStatus` (`ginprblm.ErrStatusOnly`).
- fiber (`fibersrvr/server.go`): `s.problems = fiberprblm.ErrorHandler(core.ProblemOptions()...)`, `renderError` = `s.problems(c, GuardChain(err))`, `fiberprblm.Recovery()` in `Use`, and `requestStage` calling `RecordRejection` for a status fasthttp set before routing, then `renderError` for the chain's error.
- `GuardChain` (`internal/engine/triage.go`, built on `internal/errchain`'s chain walking) replaces a cyclic chain by `*unresolvable`; `panicOf` finds a `*prblm.PanicError` in a chain. `otelsrvr` reads `*prblm.PanicError` from `ResultInfo.Err`.
- `srvr.ErrorHook` is `engine.ErrorHook func(ctx, err error, p *prblm.Problem)` (`internal/engine/core.go`).
- LOGGING.md: schema `1`; `problem` group with `status`, `type`, `title`.

## Goals / Non-Goals

**Goals:**
- One srvr-owned render path in `internal/engine` that every engine calls when a custom renderer is set, with all safety rules in one place.
- Zero-config output byte-for-byte unchanged, proven by golden parity rows taken at the base commit.
- srvr's own answers expressed as srvr errors, so a renderer never needs prblm to recognise them.

**Non-Goals:**
- Removing prblm from the core module or from ginsrvr/fibersrvr.
- Rendering net/http's own pre-routing `431` or gin's bare status-only aborts (unchanged).
- A configurable body limit or render deadline (fixed now, see Defaults).
- Content negotiation helpers for custom renderers.

## Slices

1. **Slice 2: types and default renderer, no behaviour change** (core, net/http): `ErrorRenderer`, `ErrorRequest`, `ErrorResponse`, the typed errors (D3) sent by `answerUnmatched`, `engine.StaticFailure`, fiber's static `notFound` and the 499 mapper, and a default renderer wrapping today's prblm handler. Benchmark baseline first. Satisfies error-rendering "prblm is the default renderer and its output is unchanged" (net/http rows). Hot paths: `answerUnmatched`, `engine.StaticFailure`, `clientClosed`, the default mapping of each typed error.
2. **Slice 3: `WithErrorRenderer` on net/http**: option and validation, `engine.Render` with every safety rule, the net/http pipeline switching to it when set; the hook receives a problem synthesised from the response (D8 interim). Satisfies error-rendering "custom renderer", "validated", "safety" scenarios on net/http, and triage-error-records rows for custom renderers. Hot paths: `engine.Render`, `writeResponse`, the recovery stage, `recordFailure` with optional type/title.
3. **Slice 4: gin**: srvr's own gin error middleware and recovery when a renderer is set; ginprblm unchanged otherwise. Gin rows of the same scenarios.
4. **Slice 5: fiber**: `renderError`, recovery and `RecordRejection` through `engine.Render` when set. Fiber rows.
5. **Slice 6: hook signature and log schema** (after the owner decides D8 and D9): `ErrorHook` takes an `ErrorReport` value; LOGGING.md and the golden-record test. error-responses MODIFIED rows.
6. **Slice 7: parity, docs, end-to-end**: paritytest golden rows for both renderers, README, LOGGING.md, CHANGELOG, godoc, final benchmark comparison, `make check`, `make cover`.
7. **Slice 8: release** after owner confirmation.

Each slice works alone: slice 2 ships the typed errors with the default renderer only; slice 3 makes the option work end to end on the engine that is in the core module; gin and fiber reject `WithErrorRenderer` until their slice adds it (slice 3 makes `EngineOptions`-style validation return a `ConfigError` "not supported by this engine yet" for gin and fiber, removed in slices 4 and 5), so no slice ships an option that silently does nothing.

## Decisions

### D1: The interface and its value types live in `internal/engine`, aliased in `srvr`

```go
type ErrorRenderer interface {
    Render(ctx context.Context, req ErrorRequest, err error) ErrorResponse
}
type ErrorRequest struct {
    Method, Path, Route, TraceID string
    Header HeaderReader // same reader as observers; Get returns a copy
}
type ErrorResponse struct {
    Status int
    Header http.Header
    Body   []byte
    Type, Title string // for logs and hooks only, never written by srvr
}
```

Like every shared type, they are declared in `internal/engine` beside the prblm handler (the nested engine modules may import it; modules outside `github.com/kartaladev/srvr/` cannot) and aliased in `srvr`. The typed errors of D3 are declared in `internal/errchain` beside srvr's other error values and aliased in `srvr`; no new internal package is planned (one would have to be added to `internalOrder` and the leak-walk list in `internal/paritytest`). An `ErrorRendererFunc` adapter is provided.

`ErrorRequest` is new rather than the observer's `RequestInfo`: `RequestInfo.Path` is empty unless the access log includes paths (a privacy switch for logs), while a renderer needs the path for a response (`instance`). The query string is never passed. The route is passed for renderers that key answers by route.

Alternatives: (a) `Render(w http.ResponseWriter, r *http.Request, err error)` — rejected: fiber has no `http.ResponseWriter`, and srvr could not enforce status, size or commit rules on a writer it hands out; (b) returning `*prblm.Problem` — rejected: that is today's mapper extension and keeps the prblm format; (c) reusing `RequestInfo` — rejected (path privacy, above); (d) a generic `ErrorResponse.Value any` serialised by srvr — rejected: srvr would pick the encoding.

Failure modes: a nil `Header` (treated as empty); a renderer mutating `Header` after return (srvr copies it before writing); `Type`/`Title` hostile (logged through the existing capping and escaping; never written to the response by srvr); the renderer retaining `req.Header` (its `Get` returns copies; on fiber the reader is valid only during `Render`, documented, as for observers).

### D2: Two paths: prblm's adapters for the default, `engine.Render` for a custom renderer

Without `WithErrorRenderer`, every engine keeps today's code (`Problems().Wrap/Recover/Write`, `ginprblm.Middleware/Recovery`, `fiberprblm.ErrorHandler/Recovery`). The default renderer value exists (`engine.DefaultRenderer`, used by `engine.Render` only when a consumer wraps it, see D7) but the pipeline does not route through it.

Alternatives: (a) one path for everyone, the default renderer producing an `ErrorResponse` from `prblm.Handler.Resolve` + encoding — rejected for now: ginprblm and fiberprblm carry engine-specific behaviour (gin's status-only aborts and early-committed headers, fiber's body streams and its `Content-Type` handling) that would have to be re-implemented, risking the byte-for-byte guarantee and adding a buffer copy on every error; (b) adapters only inside the default renderer — that is (a). Recorded in notes.md as a later simplification once parity rows prove one path can match.

Failure modes: the two paths drift → the parity suite runs every Examples row of error-rendering on both paths and on every engine; a renderer set on one engine path but not another → slices 4 and 5 reject the option until supported.

### D3: srvr's own answers are srvr errors; the default renderer maps them to today's problems

| Answer | Error the renderer receives | Matches as well | Default problem (unchanged) |
|---|---|---|---|
| router or static 404 | `srvr.ErrNotFound` | `prblm.ErrNotFound` (wrapped) | prblm's 404 |
| router or static 405 | `*srvr.MethodNotAllowedError{Allow []string}` | — | `405` with `Allow` (sorted, joined `", "`) |
| body over the limit | `srvr.ErrRequestTooLarge` wrapping the engine's error | `*http.MaxBytesError` (net/http, gin) | prblm's 413 |
| fiber header overflow | `srvr.ErrHeadersTooLarge` | — | `431` as `RecordRejection` builds today |
| fiber read timeout | `srvr.ErrRequestTimeout` | — | `408` as today |
| client closed | `srvr.ErrClientClosed` wrapping the handler's error | `context.Canceled` | 499 `Client Closed Request` |
| recovered panic | `*srvr.PanicError` = alias of `*prblm.PanicError` | — | safe 500 |

`ErrNotFound` wraps `prblm.ErrNotFound` so a consumer's prblm mapper on `prblm.ErrNotFound` keeps working on the default path. `PanicError` is an alias, not a new struct, because ginprblm/fiberprblm and prblm's own recovery produce `*prblm.PanicError` and `otelsrvr` and `panicOf` look for it; a second type would split every lookup. The 499 classification stays where it is (`clientClosed`), and on the custom path srvr runs the same predicate and wraps the error before calling the renderer.

Alternatives: passing prblm's values (`prblm.ErrNotFound`) — rejected: a custom renderer would have to import prblm; making `MethodNotAllowedError` a sentinel plus a header — rejected: the renderer needs the methods.

Failure modes: a handler returning `srvr.ErrNotFound` itself (rendered as 404, documented: it is a public sentinel); `Allow` empty (gin's 405 without methods cannot happen; still renders `Allow` absent rather than empty); `ErrClientClosed` on fiber never fires (fiber's context is never cancelled, existing parity exception).

### D4: `engine.Render` enforces the safety rules

Order: (1) if the response is committed, record the failure and return without calling the renderer; (2) `GuardChain(err)`, plus the walk guard prblm v0.3.0 applies to an `Unwrap` that panics (srvr reuses `findInChain`'s bounded walk to produce a stand-in, the same as `unresolvable`); (3) call the renderer with `context.WithoutCancel(reqCtx)` bounded by a 5-second deadline, inside a `recover`; (4) validate: status `400`-`599`, body ≤ 1 MiB, `Content-Type` present when the body is non-empty, returned before the deadline; (5) on any fault, the safe 500 (D5) and `srvr.renderer.failure`; (6) record the failure with an `ErrorReport` and run hooks; (7) write.

The renderer runs synchronously, in the request goroutine. Alternative: a goroutine with a timeout so a blocked renderer cannot hold the request — rejected: the abandoned goroutine outlives its owner (AGENTS.md rule 1, goleak) and keeps the request's memory; a blocked renderer is the same failure as a blocked handler, which `WriteTimeout` and the shutdown drain already bound. The deadline is checked after return, so a renderer that ignores its context still delays the response; godoc says so.

Failure modes, each a spec row: panic (including `panic(nil)`), status 0/negative/1xx-3xx/600+, body exactly at and one past 1 MiB, missing `Content-Type`, late return, nil header, hop-by-hop or `Content-Length` headers (dropped), concurrent renders (`-race`), a hook that panics (contained as today).

### D5: The safe 500 is fixed and does not depend on the renderer

`application/problem+json`, `{"type":"about:blank","title":"Internal Server Error","status":500,"trace_id":"..."}`, the same bytes prblm sends for an unmapped error with srvr's enricher, produced by srvr's builtin prblm handler. It never calls the consumer's renderer again. Alternative: retry the renderer with a generic error — rejected (a renderer that panicked once may panic again; no recursion).

### D6: Writing the response keeps prblm v0.3.0's header rules

srvr copies the renderer's headers onto the response: `Cache-Control` replaces one the handler set (one line), `Vary` values are merged without duplicates, other headers are set (replacing), `Content-Length` and `Transfer-Encoding`, `Connection` and other hop-by-hop headers are dropped. Per engine: net/http and gin write through the `http.ResponseWriter` (gin: through `c.Writer`, after checking `c.Writer.Written()`); fiber sets the fasthttp response and resets a body stream not yet started. Failure modes: gin committing headers early (checked with `Written()`); the client gone during write (write error ignored, as today, record unchanged).

### D7: `WithErrorRenderer` validation and the default renderer value

`WithErrorRenderer(r)` stores `r`; `IsNil(r)` adds `NewConfigError("ErrorRenderer", "<nil>", "is nil")`. `ResolveConfig` adds `NewConfigError("ErrorRenderer", "<type>", "cannot be combined with ProblemOptions")` when both are set (even an empty `WithProblemOptions()` call marks the field set, so the conflict is never silent). No config-struct field: a renderer is code, not configuration; the godoc of `Config` says so. `srvr.DefaultErrorRenderer()` returns the prblm renderer as an `ErrorRenderer` so a consumer can wrap it (decorate one error type, delegate the rest); passing it to `WithErrorRenderer` uses the custom path with prblm output (parity-tested, not byte-identical on gin/fiber edge cases such as body streams — documented).

### D8: OWNER DECISION — the new `ErrorHook` signature

Options:
- **A (recommended)**: `func(ctx context.Context, err error, sent ErrorReport)` with `ErrorReport{Status int; Type, Title string}`; the zero report (`Status` 0) marks a note about an answer not sent (panic after commit), as a nil `p` does today. Engine-neutral, carries what the log carries, cheap.
- B: `func(ctx, err error, resp ErrorResponse)` — exposes the body to hooks (privacy: a hook might log it) and costs a copy.
- C: `func(ctx, err error, status int)` — loses type/title that today's hooks read.
- D: keep `*prblm.Problem` and synthesise one for custom renderers — no break, but keeps prblm in the public hook and lies about the format.

Until the owner decides, slices 3-5 use D internally (the existing hook receives a synthesised `*prblm.Problem{Status, Type, Title}`), so they ship without the breaking change; slice 6 applies the decision.

### D9: OWNER DECISION — `srvr.log.schema` version

`problem.type` and `problem.title` become omitted for a custom renderer that gives none; `srvr.renderer.failure` is added. **Recommendation: keep `1`.** With zero configuration every record is unchanged; the keys keep their meaning and type; omission happens only after a consumer opts into a renderer; adding `srvr.renderer.failure` is an addition. The rule "removing a key bumps" could be read to cover a key that is sometimes absent; the owner decides. If the owner bumps to `2`, slice 6 changes the constant, the golden-record test and LOGGING.md's migration note.

### D10: Benchmarks against the base commit (owner addition 2026-11-20)

`BenchmarkPipeline` in the root module, ginsrvr and fibersrvr gains rows `handler_error`, `not_found`, `method_not_allowed`, `panic` beside the existing `success` (driven the way each engine's existing `BenchmarkPipeline` drives requests). Each row runs with the default renderer and with a trivial custom renderer (a fixed 20-byte JSON body). Method: a git worktree at the change's base commit, `go test -run '^$' -bench Pipeline -benchmem -count=1` alternated base/branch 10 times on the same machine (interleaved, as static-files task 5.6), then `benchstat base.txt branch.txt`. Thresholds: default renderer `ns/op` over +5% with `p < 0.05` on an error row, or any extra `allocs/op` on `success`, stops for the owner. Runs are local (GitHub CI is disabled) and only after the owner confirms no other benchmark session is running on the machine. Numbers go to notes.md: baseline in slice 2, re-runs after slices 3, 4, 5, final comparison in slice 7.

## Defaults

- Renderer: prblm, unchanged (the format consumers already rely on).
- Render deadline: 5 seconds, fixed (long enough for any in-memory renderer; a renderer doing I/O is a misuse the deadline exposes). Not configurable in this change; notes.md follow-up.
- Body limit: 1 MiB, fixed (an error body larger than that is a bug or an attack amplifier). Not configurable; follow-up.
- Status range: `400`-`599`, not configurable (an error answer with 2xx/3xx would hide failures from clients and metrics).
- `ErrorRequest` carries the path but never the query; srvr writes no renderer field to logs except `Type`/`Title` (capped, escaped) and the fault kind.

## Hot Paths

- `engine.Render`: committed check, guard, recover, deadline, validation, safe 500, record, hooks, write — every branch has a spec row.
- `answerUnmatched` (404, 405, replay), `engine.StaticFailure` and fiber's static `notFound`, `clientClosed` and its wrapping, `RecordRejection` on fiber.
- Per engine: net/http `wrapHandler` and recovery, gin error middleware and recovery (status-only aborts unchanged), fiber `renderError`, recovery and rejections.
- `recordFailure`/`addFailure` with optional type and title and `srvr.renderer.failure`; redaction unchanged.
- Construction: `WithErrorRenderer` validation and the `ProblemOptions` conflict on every engine.

## Risks / Trade-offs

- [Two render paths drift] → parity suite runs every row on both; notes.md records the later unification.
- [A custom renderer leaks error text] → godoc: srvr's no-internals guarantee covers prblm and the safe 500 only; README example renders a generic message.
- [Synchronous renderer can block a request] → documented; bounded by `WriteTimeout` and the drain; the deadline turns a late answer into the safe 500.
- [`ErrorHook` break hits consumers] → pre-v1 minor bump, CHANGELOG migration note, owner decision D8.
- [The custom path costs an extra copy] → benchmarks (D10) record it; only consumers who opt in pay.
- [scrty-integration was planned against prblm] → notes.md entry addressed to it; owner confirms the change order (task 1.6).

## Migration Plan

Consumers without a renderer change nothing except `ErrorHook` (slice 6): replace `p *prblm.Problem` with the new type and read `Status`/`Type`/`Title`. Rollback: pin v0.3.0.

## Open Questions

- Whether `DefaultErrorRenderer` should later replace the adapter path (D2) — answered by parity data after this change; no effect on this change's tasks.

### D8 and D9 outcome (owner decisions 2026-11-20)
- **D8:** option A as a **value struct**: `func(ctx context.Context, err error, sent ErrorReport)` with `ErrorReport{Status int; Type, Title string}`, passed by value. A `*ErrorReport` would escape to the heap (the hook is an opaque function value); an interface wrapping `*prblm.Problem` would also avoid the allocation but lets a hook retain a live pointer, needs nil handling, is harder to fake in tests and breaks implementers when a method is added. The value is copied on the stack, its strings share the problem's text, and a hook may keep it. The zero report (`Status` 0) marks a note about an answer not sent. A benchmark row with an error hook installed proves zero added allocations.
- **D9:** `srvr.log.schema` stays `1`: zero-config and prblm records are unchanged; `problem.type`/`problem.title` are absent only for a consumer's own renderer, and `srvr.renderer.failure` is an added key.
