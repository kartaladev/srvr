# Proposal

## Why

srvr renders every error response through prblm, and consumers can only tune that rendering through prblm options. Teams that must answer in a house error format (a legacy JSON envelope, an API gateway's contract, `application/vnd.api+json` errors) cannot use srvr today without rewriting every answer in middleware, which loses srvr's safety (cycle guard, commit checks, safe 500) and its triage records. The owner chose on 2026-11-20 (Option A) to let consumers replace prblm with their own renderer, while prblm stays the built-in default. It is planned before `scrty-integration`, so security refusals render through the same pluggable path (assumption for the owner to confirm, tasks 1.6).

## What Changes

- **New interface in core**: `srvr.ErrorRenderer` with `Render(ctx context.Context, req srvr.ErrorRequest, err error) srvr.ErrorResponse`. `ErrorResponse` is an engine-neutral value (`Status`, `Header`, `Body`, plus `Type` and `Title` for logs). srvr writes it on each engine itself, keeping its commit checks and the `Cache-Control`/`Vary` handling of prblm v0.3.0.
- **New option** `srvr.WithErrorRenderer(r)`. A nil or typed-nil renderer, or a renderer combined with `WithProblemOptions`, fails construction with an error wrapping `srvr.ErrConfig` that names `ErrorRenderer`. It is an implementation, so it has no config-struct field (documented).
- **srvr's own answers become typed errors** the renderer can recognise: `srvr.ErrNotFound` (wraps `prblm.ErrNotFound`), `*srvr.MethodNotAllowedError{Allow}`, `srvr.ErrRequestTooLarge` (also matches `*http.MaxBytesError`), `srvr.ErrHeadersTooLarge` and `srvr.ErrRequestTimeout` (fiber rejections), `srvr.ErrClientClosed` (499, also matches `context.Canceled`), and `srvr.PanicError` (an alias of `prblm.PanicError`). The router's 404/405 and the static mounts' misses send them. The default renderer maps each to today's exact problem.
- **Safety stays in srvr**: the error is guarded against cyclic chains before any renderer sees it. A renderer that panics, returns a status outside `400`-`599`, a body over 1 MiB, a body without `Content-Type`, or returns after its context ended is answered with srvr's fixed safe `500` problem, and the failure is logged. A renderer is never called once the response started.
- **Engines**: with a custom renderer, net/http, gin and fiber route handler errors, recovered panics, router answers and fiber's pre-routing rejections through srvr's renderer path instead of prblm's adapters (`ginprblm`, `fiberprblm`). Without one, the adapters run exactly as today.
- **BREAKING (pre-v1)**: `srvr.ErrorHook` stops exposing `*prblm.Problem`. The new signature is an owner decision (design D8, recommended: `func(ctx, err error, sent ErrorReport)`).
- **Log schema**: `problem.type` and `problem.title` become optional, omitted when a custom renderer gives none; a new `srvr.renderer.failure` key names a renderer fault. Whether this bumps `srvr.log.schema` is an owner decision (design D9, recommended: stays `1`).
- **Benchmarks** (owner addition 2026-11-20): the error path and the happy path are benchmarked on every engine against the change's base commit, with a stop-for-owner threshold.

## Defaults and Customization

- Zero configuration: prblm renders every error exactly as in v0.3.0, byte for byte (status, headers, body), on every engine; the parity suite proves it. No internals in responses: no error text on 5xx, no stack, no paths.
- Opt-in: `WithErrorRenderer(r)` hands every error answer to `r`. srvr still guards the chain, enforces status range, body size and `Content-Type`, refuses to render after the response started, adds nothing to the body, and logs the full error chain with the unchanged redaction.
- Loosened safety: none by srvr. A custom renderer can itself put error text in a body; the godoc says the renderer then owns that leak and that srvr's guarantee covers only the default renderer and the safe 500.

## Capabilities

### New Capabilities
- `error-rendering`: the pluggable error renderer: the interface and option, configuration validation, the typed errors of srvr's own answers, the rules srvr enforces around a renderer (guard, status, size, content type, deadline, panic, committed response), and identical behaviour on every engine.

### Modified Capabilities
- `error-responses`: "Consumers extend error rendering" gains `WithErrorRenderer`, its conflict with `WithProblemOptions` and the new `ErrorHook` signature.
- `triage-error-records`: "Records include what the client saw": `problem.type` and `problem.title` are present only when the renderer gave them; a renderer fault is named.
- `test-quality`: a new requirement benchmarks the renderer path against the change's base commit.

## Scope by Gall's Law

The simplest version that works end to end: the `ErrorRenderer` types and a default prblm renderer behind srvr's answers on net/http, with no visible change (slice 2), then `WithErrorRenderer` on net/http (slice 3). Gin (slice 4) and fiber (slice 5) each follow on the shared `engine` path. The hook and log schema change after the owner decides (slice 6). Parity, documentation and end-to-end verification close it (slice 7), and the release waits for the owner (slice 8).

Left for later (recorded in `notes.md`): a configurable body limit or render deadline, content negotiation helpers for custom renderers, rendering net/http's own pre-routing 431 (net/http answers it before srvr runs), and adapters for other error libraries.

## Impact

- Compatibility decisions:
  - Public API: additive `ErrorRenderer`, `ErrorRequest`, `ErrorResponse`, `WithErrorRenderer`, `ErrNotFound`, `MethodNotAllowedError`, `ErrRequestTooLarge`, `ErrHeadersTooLarge`, `ErrRequestTimeout`, `ErrClientClosed`, `PanicError`, `ErrorReport`; in `engine` (unstable) the render path. **Breaking**: `ErrorHook`'s signature.
  - Defaults: unchanged.
  - Log schema: optional `problem.type`/`problem.title` for custom renderers, new `srvr.renderer.failure` key (owner decision on the version).
  - Dependencies: none added. The core stays standard library plus prblm; ginsrvr and fibersrvr keep their prblm adapters for the default path.
  - Version: suggested `v0.4.0` (breaking pre-1.0 change), confirmed by the owner at release.
- Code: `engine/core.go` (`Problems`, `onError`, `newProblems`), new `engine/render.go`, `engine/access.go` (`failure`, `recordFailure`, `RecordRejection`), `engine/clientclosed.go`, `engine/static.go` (`Failure`), `server.go` (`answerUnmatched`, `wrapHandler`, `stages`), `options.go`, `types.go`, `ginsrvr/server.go` (`newEngine`, `clearStatusOnly`), `fibersrvr/server.go` (`renderError`, `requestStage`), `otelsrvr` (`*prblm.PanicError` lookup keeps working through the alias), `internal/paritytest`, `LOGGING.md`, `README.md`, `CHANGELOG.md`, godoc, benchmarks.
- Builds on, for consolidation to check: `core-server` (prblm handler, `answerUnmatched`), `gin-and-fiber-engines` (ginprblm middleware and recovery, `clearStatusOnly`, fiberprblm error handler, `RecordRejection`), `triage-debug-buffer` and `followups-hardening` (`GuardChain`, `recordFailure` notes, 499 mapper, unmatched limit), `static-files` (static 404/405 through `StaticResult.Failure`, prblm v0.3.0 header handling), all released as v0.3.0.
- Affects `scrty-integration`, planned next: its refusals and its scrty-to-prblm mapping must render through `ErrorRenderer`, not prblm directly (notes.md entry addressed to it).
