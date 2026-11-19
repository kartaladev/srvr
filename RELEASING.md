# Releasing

srvr is a multi-module repository. Each released module is tagged on its own, with the module's
directory as the tag prefix (Go's convention for nested modules):

| Module | Tag |
|--------|-----|
| `github.com/kartaladev/srvr` | `vX.Y.Z` |
| `github.com/kartaladev/srvr/ginsrvr` | `ginsrvr/vX.Y.Z` |
| `github.com/kartaladev/srvr/fibersrvr` | `fibersrvr/vX.Y.Z` |
| `github.com/kartaladev/srvr/otelsrvr` | `otelsrvr/vX.Y.Z` |
| `github.com/kartaladev/srvr/otelsetup` | `otelsetup/vX.Y.Z` |

`internal/paritytest` is never released. Releases are cut by hand.

## Order

During development every module builds against the in-repository core through a `replace`
directive (`replace github.com/kartaladev/srvr => ../`). A released module must not keep it: `go`
ignores `replace` in a dependency, so consumers would build against whatever core version the
`require` names.

1. Move the `## [Unreleased]` entries of `CHANGELOG.md` under the new version and date.
2. Run the checks below from the root, then tag the core `vX.Y.Z` and push the tag.
3. For each module that requires only the core (`ginsrvr`, `fibersrvr`, `otelsrvr`): `go get github.com/kartaladev/srvr@vX.Y.Z`,
   drop the `replace` line, `go mod tidy`, commit, then tag `<module>/vX.Y.Z`.
4. For `otelsetup`, which also requires `otelsrvr`: require both released versions, drop both
   `replace` lines, tidy, commit and tag.
5. Restore the `replace` lines on `main` for development, in a separate commit. In the same
   commit, run `go mod tidy` in `internal/paritytest`: the modules it requires now require
   `vX.Y.Z`, so its own `require` of the core moves to `vX.Y.Z`.
   `make check` must pass after the restore.

While steps 3 and 4 are in progress, the `main` CI run is expected to fail with "updates to go.mod
needed" in the modules not yet released and in `internal/paritytest`: the released modules
already require `vX.Y.Z`, the others still require the core's pseudo-version. It is green again
after step 5.

## Gates

A tag is released only when

```sh
make check
```

passes from the root (the Makefile lists the checks it runs in every module with `GOWORK=off`).
The CI `release` job runs on every tag and fails when the tagged module's `go.mod` still has a
`replace` directive.
