# Notes

Carry-over entries. Consolidation (task group 1) decides each open entry; entries are never deleted.

### Consolidation outcome (task group 1)
- **Status:** open: placeholder, replaced by task 1.7
- **Target:** engine-internal-split

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
- **Status:** open
- **Target:** engine-internal-split
- **Context:** change order `engine-internal-split` -> `pluggable-error-renderer` -> `scrty-integration` (task 1.5; the owner edits AGENTS.md and `openspec/config.yaml`); release coordination with `pluggable-error-renderer`, which also proposes `v0.4.0` (task 1.6); release version at task 10.3; go-ahead before each benchmark run (tasks 2.1, 9.1).
- **Suggested action:** ask in group 1; benchmarks only after the owner confirms no other benchmark session runs on the machine.

### test-quality leak-check list omits internal/benchgate
- **Status:** open
- **Target:** unassigned
- **Context:** At planning, the main spec `test-quality` "Every test package runs the leak check" lists packages without `github.com/kartaladev/srvr/internal/benchgate`, though it has a `TestMain` with goleak. Not a defect claim about code; a spec-completeness observation, unverified against the guard's behaviour.
- **Suggested action:** group 1 checks whether the guard covers it and, if so, adds the row to this change's test-quality delta.

### Owner decisions 2026-11-20: change order and release
- **Status:** closed: recorded
- **Target:** engine-internal-split
- **Context:** change order confirmed: `static-files` -> `engine-internal-split` -> `pluggable-error-renderer` -> `scrty-integration` (AGENTS.md and openspec/config.yaml updated). `engine-internal-split` and `pluggable-error-renderer` are released together as v0.4.0: the first change's release group does not tag; the release runs once, at the end of `pluggable-error-renderer`, after the owner confirms tags.
- **Suggested action:** group 1 ticks the order and release-coordination tasks against this entry; refresh the release groups accordingly.
