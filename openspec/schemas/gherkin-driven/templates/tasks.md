# Tasks

## 1. Consolidation: refresh plan against merged changes

- [ ] 1.1 Sync with the main branch and list every change merged or archived since this change was planned; verify they are named in the completion note
- [ ] 1.2 Decide every open `notes.md` carry-over from earlier changes addressed to this change or `unassigned` (adopt, defer or close); verify none is left undecided
- [ ] 1.3 Refresh this change's proposal, specs, design and tasks against the current main specs and merged code; verify `openspec validate <change> --strict` passes
- [ ] 1.4 Record the consolidation outcome at the top of this change's `notes.md`; verify the file lists what was refreshed or states none was needed
- [ ] 1.5 Commit the consolidation as `docs(openspec): consolidate <change> against main`, or skip when 1.1-1.4 changed nothing; verify with `git log -1 --stat`

## 2. Slice: <!-- what works at the end of this slice -->

- [ ] 2.1 <!-- behavior (capability / Scenario: name) --> ; verify <!-- test or command -->
  - RED: <!-- failing test and the Examples rows it covers, including failure rows -->
  - GREEN: <!-- minimal production change -->
  - REFACTOR: <!-- smell and Fowler refactoring, then /simplify -->
- [ ] 2.2 <!-- verify the slice end to end and check hot-path coverage with go tool cover -func -->

## 3. Slice: <!-- next capability added to the working system -->

- [ ] 3.1 <!-- behavior (capability / Scenario: name) --> ; verify <!-- test or command -->
  - RED: <!-- ... -->
  - GREEN: <!-- ... -->
  - REFACTOR: <!-- ... -->
