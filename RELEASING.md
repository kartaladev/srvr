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
5. Restore the `replace` lines on `main` for development, in a separate commit.

## Gates

A tag is released only when, in the directory of every module, these report nothing:

```sh
GOWORK=off go vet ./...
GOWORK=off golangci-lint run ./...
GOWORK=off go test -race ./...
GOWORK=off go mod tidy -diff
GOWORK=off govulncheck ./...
```

and the tagged module's `go.mod` has no `replace` directive left.
