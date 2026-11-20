# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (task group 1)
- **Status:** closed: done 2026-11-20
- **Target:** engine-internal-split
- **Context:** 1.1: `git fetch`; `main` and `origin/main` at `5ee3ee5` (since planning at `25fa456`: only the proposals of this change and `pluggable-error-renderer`, `53ff593`, `88b0437`, merged as #23). Nothing archived since v0.3.0; active: `static-files` (complete, not archived), this change, `pluggable-error-renderer` (3/48, its group 1 decisions only), `scrty-integration` (unapplied). Working branch `refactor/engine-internal-split`. 1.2: `pluggable-error-renderer` "engine could be internal/engine" set to adopted by this change. Deferred unchanged, since this change makes no behavioural change: `static-files`' open `unassigned` entries (byte ranges, compression, dotfile/ETag options, Accept-Ranges, ServeContent plain-text 500) and every other open `unassigned` entry of the archived changes and `scrty-integration`. This change's "test-quality leak-check list omits internal/benchgate": adopted (see that entry). 1.3: dependency map below; owner chose the package set. 1.4: proposal (What Changes, Scope, Version), design (D2 rewritten with the verified table and dropped candidates, D3 note, Slices, Open Questions), test-quality delta (leak rows for `errchain`, `static`, `benchgate`; guard order and rejected rows name `static` instead of `logging`), tasks (groups 6-8 for config/logging/lifecycle removed; comparison and docs renumbered 6; release replaced by hand-off group 7; 4.2 and 5.2 name the moved error values and `StaticFailure`). The engine-support delta needed no change. `openspec validate engine-internal-split --strict` passes. 1.5 and 1.6: answered by the owner before this consolidation (entry "Owner decisions 2026-11-20: change order and release").

### Dependency map and final package set (task 1.3)
- **Status:** decided (owner, 2026-11-20)
- **Target:** engine-internal-split
- **Context:** Map built with `golang.org/x/tools/go/packages` (type-checked uses of every package-level object, method and field of `engine`, non-test files, by declaring file; tool in the session scratchpad, not committed): 299 distinct cross-file edges. Findings: the chain/guard/redaction functions in `triage.go` (plus `IsNil` in `core.go`, `FuncPointer` in `lifecycle.go`) use nothing else in the package: a leaf. `static.go` uses only `NewConfigError`, `RouteErrorf`, `IsNil`, `ValueText`, `errorText`, `hasCycle` and `MarkRouterAnswered` (the last only in `StaticResult.Failure`). `config.go` reaches `access.go`, `core.go`, `debugbuffer.go`, `lifecycle.go`, `log.go`, `observer.go`, `route.go`, `trace.go`, `triage.go`, `unmatched.go`; `log.go`/`debugbuffer.go` read `RequestState` and `Core` fields; `lifecycle.go` reads `Core` fields and 22 log keys. Applying D3: `config` fails D3.1/D3.2 (six concerns' types; many unexported names would be exported), `logging` and `lifecycle` fail D3.3 (cycle with `engine` or invented accessors). Options put to the owner: (a) `engine`+`errchain`+`static` with srvr's error values in `errchain` and `StaticResult.Failure` moved to `engine.StaticFailure`; (b) `engine`+`errchain` only; (c) force 5-7 packages. Owner chose (a). Final order: `tracectx` < `errchain` < `static` < `engine`; no cycle. One-sentence responsibilities in design D2. This is fewer than the 5 to 7 aimed for by the "do not oversplit" decision; the owner accepted it.
- **Suggested action:** none; slices 3 to 5 implement it.

### Owner decision 2026-11-20: move engine to internal/engine, then split
- **Status:** decided
- **Target:** engine-internal-split
- **Context:** The owner approved moving `engine` to `internal/engine` (evidence: `pluggable-error-renderer/notes.md`, "engine could be internal/engine") and splitting it by responsibility, leaf first, behaviour-preserving.
- **Suggested action:** none; this change implements it.

### Owner decision 2026-11-20: do not oversplit
- **Status:** decided
- **Target:** engine-internal-split
- **Context:** Plan fewer, cohesive packages: split only where a package has one clear responsibility and a small API; merge thin ones into a neighbour (for example guard and routes folded into neighbours; problems and access kept together when the callback indirection adds friction). Aim for roughly 5 to 7 internal packages, not 12. Applied in design D2 (target: `errchain`, `static`, `config`, `logging`, `lifecycle`, `engine`) and D3 ("merge if thin" criterion), and in task 1.3.
- **Suggested action:** group 1 applies D3 to the gopls-verified graph and records every merge or drop here.

### Owner decisions pending
- **Status:** open: only the benchmark go-aheads (tasks 2.1, 6.1) remain; order and release decided (below), version decided at `pluggable-error-renderer`'s release
- **Target:** engine-internal-split
- **Context:** change order `engine-internal-split` -> `pluggable-error-renderer` -> `scrty-integration` (task 1.5; the owner edits AGENTS.md and `openspec/config.yaml`); release coordination with `pluggable-error-renderer`, which also proposes `v0.4.0` (task 1.6); release version at task 10.3; go-ahead before each benchmark run (tasks 2.1, 9.1).
- **Suggested action:** ask in group 1; benchmarks only after the owner confirms no other benchmark session runs on the machine.

### test-quality leak-check list omits internal/benchgate
- **Status:** adopted by engine-internal-split (1.4: the guard's directory walk already checks `internal/benchgate`, `TestEveryTestPackageChecksLeaks` in `internal/paritytest/leak_test.go`; the delta adds its row)
- **Target:** engine-internal-split
- **Context:** At planning, the main spec `test-quality` "Every test package runs the leak check" lists packages without `github.com/kartaladev/srvr/internal/benchgate`, though it has a `TestMain` with goleak. Not a defect claim about code; a spec-completeness observation, unverified against the guard's behaviour.
- **Suggested action:** group 1 checks whether the guard covers it and, if so, adds the row to this change's test-quality delta.

### Owner decisions 2026-11-20: change order and release
- **Status:** closed: recorded; tasks 1.5 and 1.6 ticked against it, release group refreshed to a hand-off (group 7)
- **Target:** engine-internal-split
- **Context:** change order confirmed: `static-files` -> `engine-internal-split` -> `pluggable-error-renderer` -> `scrty-integration` (AGENTS.md and openspec/config.yaml updated). `engine-internal-split` and `pluggable-error-renderer` are released together as v0.4.0: the first change's release group does not tag; the release runs once, at the end of `pluggable-error-renderer`, after the owner confirms tags.
- **Suggested action:** group 1 ticks the order and release-coordination tasks against this entry; refresh the release groups accordingly.

### Benchmark baseline (tasks 2.1, 2.2)
- **Status:** closed: recorded 2026-11-21
- **Target:** engine-internal-split
- **Context:** Go-ahead: the owner answered "continue" after being told the baseline waited for their confirmation that no other benchmark session was running (an earlier "not now" was lifted by it). Base commit `5ee3ee5` in a worktree (session scratchpad `base/`). Command, 10 times in a row per module (root including `engine`, `ginsrvr`, `fibersrvr`): `GOWORK=off go test -run '^$' -bench 'Pipeline|LogHandler|Correlation|RedactSecrets|DebugBuffer' -benchmem -count=1 ./...`. Machine: Apple M4 Pro (14 threads), darwin/arm64, Go 1.27.4; 16 min total. Raw output in the session scratchpad `bench-base/` (`r.txt` = root module, `ginsrvr.txt`, `fibersrvr.txt`); `benchstat` prints every row with 10 samples. Reference allocs/op: net/http success 20, handler_error 77, unmatched 103, panic 83, 32-link chain 340, static 53; gin success 20, handler_error 79, unmatched 83, static 40; fiber success 47, handler_error 105, unmatched 111, static 77; LogHandler 0 steps 0; Correlation ungrouped 752 B/op.
- **Suggested action:** task 6.1 compares against these files (re-run base and branch interleaved, per D7).

### Slice 4 (errchain): what moved and what stayed
- **Status:** closed: done
- **Target:** engine-internal-split
- **Context:** `internal/errchain` holds the five sentinels, `ConfigError`, `NewConfigError`, `RouteErrorf`, `IsNil`, `TypeName`, `ValueText`, `TryValueText`, `PanicFormatting`, `ErrorText`, `Guard`, `HasCycle`, `FindInChain`, `WalkChain`/`Chain` and `RedactSecrets` (20 exported names, over D3.2's guide of about 15; 8 of them are the error values the owner chose to put here). Kept in `internal/engine`, against the planned list: `GuardChain`, the `unresolvable` stand-in and `panicOf`, because the stand-in's type name is observable: otelsrvr's `error.type` metric attribute and the observer's `Err` carry `*engine.unresolvable`, and the move made it `*errchain.Unresolvable` (RED evidence: `otelsrvr` `TestAttributes_FaultyErrors/Unwrap_panics`: `error.type = "*errchain.Unresolvable", want "*engine.unresolvable"`; `internal/paritytest` `TestParity_UnwalkableError`). Also kept (the `/simplify` altitude review): `fingerprint`, `topFrame` (triage-only) and `FuncPointer` (route tables). `Guard` returns `error` instead of the unexported `*guardedError` (all callers wrap it with `%w`; no behaviour change, no extra allocation per `-gcflags=-m`). Cross-package inlining of `TypeName`, `ValueText`, `NewConfigError` is kept (`-gcflags=-m`).
- **Suggested action:** none.

### Coverage of unmatchedLimiter.admit's CAS retry depends on scheduling
- **Status:** open
- **Target:** unassigned
- **Context:** `internal/engine/unmatched.go` `admit`: the `continue` after a failed `CompareAndSwap` (another request started the window first) is covered only when two requests race; `make cover` shows `admit` at 100% on one run and 93.3% on the next with no code change (slice 4 run). Not a defect claim; a hot-path branch without a deterministic test.
- **Suggested action:** a test injecting a window swap between `Load` and `CompareAndSwap` (for example through the `now` hook) so the branch is covered on every run.

### Slice 5 (static): shape and names
- **Status:** closed: done
- **Target:** engine-internal-split
- **Context:** `internal/static` imports only `internal/errchain`. `StaticResult.Failure` became `engine.StaticFailure(ctx, res, allow)` (`internal/engine/staticfailure.go`; callers `static.go`, `ginsrvr/static.go`, `fibersrvr/static.go`). Names inside the package lost the `Static` stutter (`Mount`, `New`, `Config`, `Settings`, `Option`, `Prefixes`, `Check`, `Answer` with `Found`/`NotFound`/`MethodNotAllowed`/`Failed`, `FileContent`, `Result`, `ServeFile`); `srvr`'s public aliases (`srvr.StaticConfig`, `srvr.StaticOption`) and every error field string (`"StaticOption"`, `"Static.Prefix"`, ...) are unchanged. Two test assertions changed only in the `%T` of test-local fixture types (`engine.configurerA` -> `static.configurerA`, `engine.noSeekFile` -> `static.noSeekFile`), qualified names per D4. Not done (from `/simplify`, outside this change's scope): moving `Prefixes`/`Check` (configure-phase conflict checks) next to route registration in `internal/engine`; splitting `StaticFailure` into a pure `Result` method plus the router-answered mark.
- **Suggested action:** none.

### configFields test helper is copied in seven test packages
- **Status:** open
- **Target:** unassigned
- **Context:** `configFields(err) []string` (the `*ConfigError` fields of a joined error) is declared in `server_test.go`, `ginsrvr/static_test.go`, `fibersrvr/static_test.go`, `otelsrvr/config_test.go`, `otelsetup/setup_test.go`, `internal/paritytest/config_test.go` and `internal/static/static_internal_test.go` (found by the slice 5 `/simplify` reuse review; pre-existing, not created by this change).
- **Suggested action:** an exported helper in a test-support location every module can import (for example `errchain.ConfigFields`, if it earns a production use) or accept the copies.
