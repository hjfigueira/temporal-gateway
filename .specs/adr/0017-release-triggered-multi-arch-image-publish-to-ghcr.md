# ADR-017: Release-triggered, multi-arch image publish to GHCR

**Status:** Accepted

**Related features:** [`deployment-cicd.feature`](../features/deployment-cicd.feature)

## Context

Images need to reach users without manual publish steps, but
building+pushing on every commit to `main` would publish unreviewed,
unversioned images.

## Decision

`.github/workflows/docker-release.yml` triggers only on a published GitHub
Release, building `linux/amd64` + `linux/arm64` and pushing to
`ghcr.io/<owner>/temporal-gateway` tagged with the release's semantic
version, its `major.minor`, and `latest` — with the `latest` tag skipped
for prereleases so a prerelease can never clobber the last stable
`latest`. Uses the built-in `GITHUB_TOKEN`, requiring no extra registry
secrets.

## Consequences

- There is no image published for arbitrary commits/branches — only tagged
  Releases produce one, which also means the image's version tag is always
  traceable to a real Release note.
- Multi-arch builds mean a publish takes noticeably longer than a
  single-arch build; `cache-from`/`cache-to: type=gha` is relied on to
  keep that bounded across releases.
