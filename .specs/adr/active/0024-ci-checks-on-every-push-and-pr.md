# ADR-024: CI runs gofmt, vet, and race tests on every push and PR

**Status:** Accepted

**Related features:** [`deployment-cicd.feature`](../../features/deployment-cicd.feature)

## Context

The only workflow was the release image publish ([ADR-017](0017-release-triggered-multi-arch-image-publish-to-ghcr.md)).
Nothing ran the test suite automatically, so a broken change could reach
`main` and then a release.

## Decision

`.github/workflows/ci.yml` runs on every push to and pull request against
`main`. It uses the Go version from `go.mod` and runs
`gofmt -s -l` (it fails if anything is listed), `go vet ./...` and
`go test -race ./...`. These are the same commands CLAUDE.md lists for
local development. It also runs `govulncheck` (pinned version), which fails
on known vulnerabilities that our code can actually reach, in dependencies
or in the Go standard library. That is why `go.mod` pins a patched
`toolchain` line: `setup-go` builds with it, and a stdlib advisory is fixed
by bumping that line.

## Consequences

- PRs show a red check for formatting, vet, or test failures. Branch
  protection can make it required.
- Release publishing stays independent and doesn't wait on CI.
- A newly published advisory can turn `main` red with no code change. The
  fix is a dependency or `toolchain` bump; govulncheck names the fixed
  version.
